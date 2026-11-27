package coalsrc_test

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/coalesce"
	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/coalsrc"
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

var skipped = []string{".WaitersPerFlight", ".Mode", ".Join"}

func collect(t *testing.T, s coalesce.Snapshot, detail bool) obs.InstanceSnapshot {
	t.Helper()
	c := obs.NewCollector(obs.Config{Instance: "i", Detail: detail})
	if err := c.Register(coalsrc.Source(func() coalesce.Snapshot { return s })); err != nil {
		t.Fatal(err)
	}
	out, err := c.Collect()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMappingExportsEveryFieldOfTheCoalescerSnapshot(t *testing.T) {
	var s coalesce.Snapshot
	f := &filler{paths: map[int64]string{}}
	f.fill(reflect.ValueOf(&s).Elem(), "")
	s.Mode, s.Join, s.HotKeysMax = coalesce.ModeAuto, coalesce.JoinRelaxed, 2
	s.PerTenant[0].Tenant, s.PerTenant[1].Tenant = "a", "b"
	values := map[int64]bool{}
	for _, p := range collect(t, s, true).Points {
		if p.Component == obs.ComponentCoalescer {
			values[p.Value] = true
		}
	}
	for v, path := range f.paths {
		skip := strings.HasSuffix(path, ".HotKeysMax")
		for _, h := range skipped {
			skip = skip || strings.HasPrefix(path, h)
		}
		if !skip && !values[v] {
			t.Errorf("field %s is not exported", path)
		}
	}
}

func TestCountersMoveAndConserveThroughTheMapping(t *testing.T) {
	tb, err := coalesce.New(coalesce.Config{Mode: coalesce.ModeAuto})
	if err != nil {
		t.Fatal(err)
	}
	u := work.Unit{Tenant: "a", Class: work.ClassStartLease, SafeRead: true, Shareable: true, HasKey: true, Key: work.ShareKey{Text: []byte("SELECT 1")}}
	j := tb.Join(0, 1, &u)
	tb.Join(0, 2, &u)
	tb.Started(1, j.Flight)
	tb.Finish(2, j.Flight, coalesce.Result{})
	for {
		if _, ok := tb.TakeNotice(); !ok {
			break
		}
	}
	s := tb.Snapshot()
	if err := s.Check(); err != nil {
		t.Fatal(err)
	}
	snap := collect(t, s, true)
	for name, want := range map[string]int64{"offered": 2, "flights_started": 1, "hits": 1, "sent": 1, "waiters_in": 2} {
		if got := snap.Value(obs.ComponentCoalescer, name, obs.Labels{}); got != want {
			t.Errorf("%s = %d, want %d", name, got, want)
		}
	}
	if got := snap.Value(obs.ComponentCoalescer, "waiter_ends", obs.Labels{Outcome: "reply"}); got != 2 {
		t.Errorf("waiter_ends reply %d", got)
	}
	if got := snap.Value(obs.ComponentCoalescer, "tenant_hits", obs.Labels{Tenant: "a"}); got != 1 {
		t.Errorf("tenant hits %d", got)
	}
	if got := snap.Value(obs.ComponentCoalescer, "mode", obs.Labels{Mode: "auto"}); got != 1 {
		t.Errorf("mode auto %d", got)
	}
}

func TestDetailOffExportsNoTenantRows(t *testing.T) {
	s := coalesce.Snapshot{PerTenant: []coalesce.TenantHits{{Tenant: "a", Hits: 3}}, Hits: 3}
	for _, p := range collect(t, s, false).Points {
		if p.Labels.Tenant != "" {
			t.Fatalf("tenant row with detail off: %+v", p)
		}
	}
}

func TestHealthStatesTheCoalescerNeverDegradesTheInstance(t *testing.T) {
	snap := collect(t, coalesce.Snapshot{Waiting: obs.Gauge{Value: 7}}, false)
	st, why := coalsrc.HealthRules()[0].Check(snap)
	if st != obs.StateOK || !strings.Contains(why, "7 requests waiting") {
		t.Fatalf("%v %q", st, why)
	}
}
