package retrysrc_test

import (
	"reflect"
	"strconv"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/enginesrc"
	"github.com/nickstrad/mini-gateway/internal/obs/retrysrc"
	"github.com/nickstrad/mini-gateway/internal/retry"
	"github.com/nickstrad/mini-gateway/internal/work"
)

type filler struct {
	n     int64
	paths map[int64]string
}

func (f *filler) fill(v reflect.Value, path string) {
	switch v.Kind() {
	case reflect.Int64:
		f.n++
		v.SetInt(f.n * 1009)
		f.paths[f.n*1009] = path
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			f.fill(v.Field(i), path+"."+v.Type().Field(i).Name)
		}
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			f.fill(v.Index(i), path+"["+strconv.Itoa(i)+"]")
		}
	}
}

func collect(t *testing.T, s retry.Snapshot) obs.InstanceSnapshot {
	t.Helper()
	c := obs.NewCollector(obs.Config{Instance: "i"})
	if err := c.Register(retrysrc.Source(func() retry.Snapshot { return s })); err != nil {
		t.Fatal(err)
	}
	out, err := c.Collect()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMappingExportsEveryFieldOfTheRetrySnapshot(t *testing.T) {
	var s retry.Snapshot
	f := &filler{paths: map[int64]string{}}
	f.fill(reflect.ValueOf(&s).Elem(), "")
	values := map[int64]bool{}
	for _, p := range collect(t, s).Points {
		if p.Component == obs.ComponentRetry {
			values[p.Value] = true
		}
	}
	for v, path := range f.paths {
		if path != ".Refused[0]" && !values[v] {
			t.Errorf("field %s is not exported", path)
		}
	}
}

func TestRetryCountersMoveAndConserve(t *testing.T) {
	p, _ := retry.New(retry.Config{Kind: retry.KindPreForwardOnce, CapTokens: 2, FloorTokens: 1})
	for range 30 {
		p.Attempt(0)
	}
	p.Decide(0, retry.Failure{Reached: work.No, Deadline: 1})
	p.Decide(0, retry.Failure{Reached: work.Yes, Deadline: 1})
	p.Decide(0, retry.Failure{Reached: work.No, Deadline: 1})
	p.Decide(0, retry.Failure{Reached: work.No, Deadline: 1})
	s := p.Snapshot()
	if err := s.Check(); err != nil {
		t.Fatal(err)
	}
	snap := collect(t, s)
	for name, want := range map[string]int64{"attempts": 30, "offered": 4, "retries": 2, "exhaustions": 1, "budget_level": 0,
		"deposits": 40, "withdrawals": 20, "expired": 20, "budget_cap": 20, "budget_floor": 10} {
		if got := snap.Value(obs.ComponentRetry, name, obs.Labels{}); got != want {
			t.Errorf("%s = %d, want %d", name, got, want)
		}
	}
	if got := snap.Value(obs.ComponentRetry, "refused", obs.Labels{Reason: "reached"}); got != 1 {
		t.Errorf("refused reached %d", got)
	}
}

func TestHealthIsUnchangedByRetries(t *testing.T) {
	quiet, _ := retry.New(retry.Config{Kind: retry.KindPreForwardOnce})
	busy, _ := retry.New(retry.Config{Kind: retry.KindPreForwardOnce, CapTokens: 1, FloorTokens: 1})
	for range 100 {
		busy.Decide(0, retry.Failure{Reached: work.No, Deadline: 1})
	}
	for _, rule := range enginesrc.HealthRules() {
		a, _ := rule.Check(collect(t, quiet.Snapshot()))
		b, _ := rule.Check(collect(t, busy.Snapshot()))
		if a != b || a != obs.StateOK {
			t.Fatalf("rule %s: %v without retries, %v with exhausted retries", rule.Name, a, b)
		}
	}
}
