package simsrc_test

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/simsrc"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
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
	case reflect.String:
		f.n++
		v.SetString("s" + strconv.FormatInt(f.n, 10))
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

func emitFilled[T any](t *testing.T, component string, metrics []obs.MetricDesc, mapping func(T, *obs.Emitter)) (obs.InstanceSnapshot, map[int64]string) {
	t.Helper()
	var v T
	f := &filler{paths: map[int64]string{}}
	f.fill(reflect.ValueOf(&v).Elem(), "")
	c := obs.NewCollector(obs.Config{Instance: "i", Detail: true})
	if err := c.Register(obs.NewSource(component, metrics, func() T { return v }, mapping)); err != nil {
		t.Fatal(err)
	}
	s, err := c.Collect()
	if err != nil {
		t.Fatal(err)
	}
	return s, f.paths
}

func TestMappingsMoveEveryMetric(t *testing.T) {
	type result struct {
		comp    string
		metrics []obs.MetricDesc
		snap    obs.InstanceSnapshot
		paths   map[int64]string
		skip    []string
	}
	var rs []result
	add := func(comp string, metrics []obs.MetricDesc, snap obs.InstanceSnapshot, paths map[int64]string, skip ...string) {
		rs = append(rs, result{comp, metrics, snap, paths, skip})
	}
	s, p := emitFilled(t, obs.ComponentSim, simsrc.KernelMetrics(), simsrc.MapKernel)
	add(obs.ComponentSim, simsrc.KernelMetrics(), s, p)
	s, p = emitFilled(t, obs.ComponentSimnet, simsrc.NetMetrics(), simsrc.MapNet)
	add(obs.ComponentSimnet, simsrc.NetMetrics(), s, p)
	s, p = emitFilled(t, obs.ComponentFakepg, simsrc.FakeMetrics(), simsrc.MapFake)
	add(obs.ComponentFakepg, simsrc.FakeMetrics(), s, p)
	s, p = emitFilled(t, obs.ComponentWorkload, simsrc.WorkloadMetrics(), simsrc.MapWorkload)
	add(obs.ComponentWorkload, simsrc.WorkloadMetrics(), s, p, ".Total.", ".Lo", ".Hi", ".Count", ".Min", ".Max", "ClampedLow", "ClampedHigh")
	for _, r := range rs {
		values := map[int64]bool{}
		moved := map[string]bool{}
		for _, pt := range r.snap.Points {
			if pt.Component != r.comp {
				continue
			}
			values[pt.Value] = true
			moved[pt.Metric] = moved[pt.Metric] || pt.Value != 0
		}
		for _, h := range r.snap.Hists {
			for _, b := range h.Buckets {
				values[b.Count] = true
			}
			moved[h.Metric] = moved[h.Metric] || h.Count() > 0
		}
		for _, d := range r.metrics {
			if !moved[d.Name] {
				t.Errorf("%s.%s never moves even with every snapshot field non-zero", r.comp, d.Name)
			}
		}
		var lost []string
		for v, path := range r.paths {
			if values[v] || slices.ContainsFunc(r.skip, func(s string) bool { return strings.Contains(path, s) }) {
				continue
			}
			lost = append(lost, path)
		}
		slices.Sort(lost)
		if len(lost) > 0 {
			t.Errorf("%s: snapshot fields not exported: %v", r.comp, lost)
		}
	}
}

func TestDuplicateTenantNamesGetDistinctLabels(t *testing.T) {
	ts := []workload.TenantSnapshot{{Name: "app"}, {Name: "app"}, {Name: "web"}}
	if a, b, c := simsrc.TenantLabel(ts, 0), simsrc.TenantLabel(ts, 1), simsrc.TenantLabel(ts, 2); a != "app" || b != "app#1" || c != "web" {
		t.Fatalf("%s %s %s", a, b, c)
	}
}

func TestSourcesReadLiveComponents(t *testing.T) {
	s := sim.New(sim.Config{Seed: 1, Scenario: "simsrc"})
	n := simnet.New(s)
	srv, err := fakepg.New(n, fakepg.Config{})
	if err != nil {
		t.Fatal(err)
	}
	wl, err := workload.New(n, workload.Config{Addr: srv.Addr(), Tenants: []workload.Tenant{{Name: "a", Clients: 3, Requests: 2, Interval: 10_000_000, Statement: "SELECT 1"}}})
	if err != nil {
		t.Fatal(err)
	}
	wl.Start()
	c := obs.NewCollector(obs.Config{Instance: "i", Detail: true})
	if err := simsrc.Register(c, simsrc.Kernel(s), simsrc.Net(n), simsrc.Fake(srv), simsrc.Workload(wl)); err != nil {
		t.Fatal(err)
	}
	before, err := c.Publish()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RunFor(1_000_000_000); err != nil {
		t.Fatal(err)
	}
	after, err := c.Publish()
	if err != nil {
		t.Fatal(err)
	}
	if before.Sum(obs.ComponentWorkload, "requests") != 0 || after.Value(obs.ComponentWorkload, "requests", obs.Labels{Tenant: "a", Outcome: "ok"}) != 6 {
		t.Fatalf("requests ok: %d", after.Value(obs.ComponentWorkload, "requests", obs.Labels{Tenant: "a", Outcome: "ok"}))
	}
	if after.Value(obs.ComponentFakepg, "statements", obs.Labels{Mode: "const"}) != 6 || after.Value(obs.ComponentSim, "steps", obs.Labels{}) == 0 {
		t.Fatal("fakepg or kernel numbers did not move")
	}
	if after.Value(obs.ComponentSimnet, "conns_opened", obs.Labels{}) != 3 {
		t.Fatalf("simnet conns opened %d", after.Value(obs.ComponentSimnet, "conns_opened", obs.Labels{}))
	}
}
