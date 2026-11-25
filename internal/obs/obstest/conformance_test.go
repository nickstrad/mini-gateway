package obstest

import (
	"errors"
	"fmt"
	"runtime"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/obs"
)

type recorder struct {
	testing.TB
	failed bool
	msg    string
}

func (r *recorder) fail(m string) {
	if !r.failed {
		r.failed, r.msg = true, m
	}
}
func (r *recorder) Helper()                   {}
func (r *recorder) Error(a ...any)            { r.fail(fmt.Sprint(a...)) }
func (r *recorder) Errorf(f string, a ...any) { r.fail(fmt.Sprintf(f, a...)) }
func (r *recorder) Fatal(a ...any)            { r.fail(fmt.Sprint(a...)); runtime.Goexit() }
func (r *recorder) Fatalf(f string, a ...any) { r.fail(fmt.Sprintf(f, a...)); runtime.Goexit() }
func (r *recorder) FailNow()                  { r.fail("FailNow"); runtime.Goexit() }
func (r *recorder) Skip(...any)               { r.fail("skipped"); runtime.Goexit() }

type toy struct {
	keepRef  bool
	mutate   bool
	dropZero bool
	addLabel bool
	noise    bool
	err      error
	stored   obs.InstanceSnapshot
}

var noiseCounter int

func (a *toy) Name() string { return "toy" }

func (a *toy) Export(s obs.InstanceSnapshot) error {
	if a.mutate && len(s.Points) > 0 {
		s.Points[0].Value++
	}
	if a.keepRef {
		a.stored = s
	} else {
		a.stored = s.Clone()
	}
	return a.err
}

func (a *toy) output(testing.TB) []byte {
	out := fmt.Sprintf("%+v", a.stored)
	if a.noise {
		noiseCounter++
		out += fmt.Sprint(noiseCounter)
	}
	return []byte(out)
}

func (a *toy) decoded(testing.TB) obs.InstanceSnapshot {
	s := a.stored.Clone()
	if a.dropZero {
		var keep []obs.Point
		for _, p := range s.Points {
			if p.Metric != "zero" {
				keep = append(keep, p)
			}
		}
		s.Points = keep
	}
	if a.addLabel {
		for i, p := range s.Points {
			if p.Metric == "zero" {
				s.Points[i].Labels.Mode = "sneaky"
			}
		}
	}
	return s
}

func toySubject(proto toy, failingErr error) Subject {
	return Subject{
		Name:     "toy",
		Fidelity: Exact,
		New: func(testing.TB, *obs.Registry) Instance {
			a := proto
			return Instance{Adapter: &a, Output: a.output, Decoded: a.decoded}
		},
		NewFailing: func(testing.TB, *obs.Registry) obs.Adapter {
			a := proto
			a.err = failingErr
			return &a
		},
	}
}

func runCheck(t *testing.T, c Check, sub Subject) (bool, string) {
	snap, reg := FixtureSnapshot(t)
	r := &recorder{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Run(r, sub, snap, reg)
	}()
	<-done
	return r.failed, r.msg
}

func TestConformanceCatchesBrokenAdapters(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name  string
		sub   Subject
		fails []string
	}{
		{"correct", toySubject(toy{}, boom), nil},
		{"keeps a reference", toySubject(toy{keepRef: true}, boom), []string{"no-retained-reference"}},
		{"modifies its input", toySubject(toy{mutate: true}, boom), []string{"no-retained-reference"}},
		{"drops a declared metric", toySubject(toy{dropZero: true}, boom), []string{"values"}},
		{"adds an undeclared label", toySubject(toy{addLabel: true}, boom), []string{"values", "labels"}},
		{"not deterministic", toySubject(toy{noise: true}, boom), []string{"deterministic", "no-retained-reference"}},
		{"hides its errors", toySubject(toy{}, nil), []string{"error-counted-not-propagated"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, c := range Checks() {
				failed, msg := runCheck(t, c, tc.sub)
				want := false
				for _, f := range tc.fails {
					want = want || f == c.Name
				}
				if failed != want {
					t.Errorf("check %s: failed=%v want %v (%s)", c.Name, failed, want, msg)
				}
			}
		})
	}
}
