package poolsrc_test

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/poolsrc"
	"github.com/nickstrad/mini-gateway/internal/pool"
	"github.com/nickstrad/mini-gateway/internal/work"
)

type filler struct {
	n     int64
	paths map[int64]string
}

func (f *filler) fill(v reflect.Value, path string) {
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
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
	case reflect.Slice:
		s := reflect.MakeSlice(v.Type(), 2, 2)
		for i := 0; i < 2; i++ {
			f.fill(s.Index(i), path+"["+strconv.Itoa(i)+"]")
		}
		v.Set(s)
	}
}

var histogramParts = []string{".Lo", ".Hi", ".Count", ".Min", ".Max", "ClampedLow", "ClampedHigh", ".Buckets"}

func collect(t *testing.T, s pool.Snapshot, detail bool) obs.InstanceSnapshot {
	t.Helper()
	c := obs.NewCollector(obs.Config{Instance: "i", Detail: detail})
	if err := c.Register(poolsrc.Source(func() pool.Snapshot { return s })); err != nil {
		t.Fatal(err)
	}
	out, err := c.Collect()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMappingExportsEveryFieldOfThePoolSnapshot(t *testing.T) {
	var s pool.Snapshot
	f := &filler{paths: map[int64]string{}}
	f.fill(reflect.ValueOf(&s).Elem(), "")
	values := map[int64]bool{}
	for _, p := range collect(t, s, true).Points {
		if p.Component == obs.ComponentPool {
			values[p.Value] = true
		}
	}
	for v, path := range f.paths {
		hist := false
		for _, h := range histogramParts {
			hist = hist || strings.Contains(path, h)
		}
		if !hist && !values[v] {
			t.Errorf("field %s is not exported", path)
		}
	}
}

func TestPoolCountersMoveAndConserveInARealRun(t *testing.T) {
	p, err := pool.New(pool.Config{Cap: 2, RatePerSec: 1000})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range p.Demand(0, 1, nil) {
		p.Opened(0, a.Conn, nil)
	}
	id, ok, _ := p.Checkout(1, work.TenantID("a"), nil)
	if !ok {
		t.Fatal("no lend")
	}
	p.Release(2, id, pool.End{Kind: pool.EndClean, Tenant: "a"}, nil)
	p.StepDone(3, id, 'I', nil)
	s := p.Snapshot()
	if err := s.Check(); err != nil {
		t.Fatal(err)
	}
	snap := collect(t, s, false)
	for name, want := range map[string]int64{"dials": 1, "dials_opened": 1, "open": 1, "idle": 1} {
		if got := snap.Value(obs.ComponentPool, name, obs.Labels{}); got != want {
			t.Errorf("%s = %d, want %d", name, got, want)
		}
	}
	if got := snap.Value(obs.ComponentPool, "checkouts", obs.Labels{Outcome: "lent"}); got != 1 {
		t.Errorf("checkouts lent %d", got)
	}
	if got := snap.Value(obs.ComponentPool, "resets_ok", obs.Labels{Mode: "always"}); got != 1 {
		t.Errorf("resets ok %d", got)
	}
}

func TestHealthIsDegradedWhileTheBreakerIsOpen(t *testing.T) {
	p, err := pool.New(pool.Config{Cap: 2, RatePerSec: 1000, BreakerFailures: 1, BreakerOpenFor: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	rule := poolsrc.HealthRules()[0]
	if st, _ := rule.Check(collect(t, p.Snapshot(), false)); st != obs.StateOK {
		t.Fatalf("closed breaker: %v", st)
	}
	a := p.Demand(0, 1, nil)
	p.DialFailed(0, a[0].Conn, nil)
	st, why := rule.Check(collect(t, p.Snapshot(), false))
	if st != obs.StateDegraded {
		t.Fatalf("open breaker: %v %s", st, why)
	}
}
