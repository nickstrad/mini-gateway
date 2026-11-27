package jsonstats_test

import (
	"bytes"
	"fmt"
	"math"
	"math/rand"
	"slices"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/jsonstats"
	"github.com/nickstrad/mini-gateway/internal/obs/logline"
)

const c03Seed int64 = 730031

func c03Start(t *testing.T) {
	t.Helper()
	t.Logf("seed=%d REPLAY: go test -count=1 -run '^%s$' ./internal/obs/jsonstats", c03Seed, t.Name())
}

func c03Must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func c03Registry(t *testing.T) *obs.Registry {
	t.Helper()
	r := &obs.Registry{}
	c03Must(t, r.Declare("pool", obs.CounterDesc("requests", obs.LabelTenant, "Requests."), obs.HighWaterDesc("peak", 0, "Peak."), obs.HistogramDesc("wait", obs.LabelTenant, obs.UnitNs, "Wait.")))
	return r
}

func c03Snapshot() obs.InstanceSnapshot {
	return obs.InstanceSnapshot{Instance: "a", Build: "build", ConfigFingerprint: "cfg", Points: []obs.Point{{Component: "pool", Metric: "peak", Kind: obs.KindGauge, Value: 0}, {Component: "pool", Metric: "requests", Kind: obs.KindCounter, Labels: obs.Labels{Tenant: "tenant"}, Value: 7}}, Hists: []obs.HistPoint{{Component: "pool", Metric: "wait", Labels: obs.Labels{Tenant: "tenant"}, Unit: obs.UnitNs, Buckets: []obs.Bucket{{Lo: 1, Hi: 1, Count: 3}, {Lo: 8, Hi: 9, Count: 4}}}}}
}

func TestAdv_2_EqualInputsAfterHostileNoise(t *testing.T) {
	c03Start(t)
	r := c03Registry(t)
	s := c03Snapshot()
	s.Instance = "\x00\n\\\"雪"
	a := jsonstats.New(nil)
	b := logline.New(r, nil)
	for _, x := range []interface {
		Export(obs.InstanceSnapshot) error
		Last() []byte
	}{a, b} {
		c03Must(t, x.Export(s))
		want := slices.Clone(x.Last())
		noise := s.Clone()
		noise.Points[0].Value = math.MaxInt64
		c03Must(t, x.Export(noise))
		c03Must(t, x.Export(s))
		if !bytes.Equal(want, x.Last()) {
			t.Errorf("%T depends on preceding snapshot", x)
		}
	}
}

func TestAdv_2_CollectorEmissionPermutation(t *testing.T) {
	c03Start(t)
	rng := rand.New(rand.NewSource(c03Seed))
	var first []byte
	for n := 0; n < 40; n++ {
		order := rng.Perm(19)
		c := obs.NewCollector(obs.Config{Instance: "x", Detail: true})
		c03Must(t, c.Register(obs.Source{Component: "pool", Metrics: []obs.MetricDesc{obs.CounterDesc("requests", obs.LabelTenant, "Requests.")}, Collect: func(e *obs.Emitter) {
			for _, i := range order {
				e.Counter("requests", obs.Labels{Tenant: fmt.Sprintf("t%02d", i)}, int64(i))
			}
		}}))
		s, e := c.Collect()
		c03Must(t, e)
		b, e := jsonstats.Render(s)
		c03Must(t, e)
		if n == 0 {
			first = b
		} else if !bytes.Equal(first, b) {
			t.Fatalf("seed=%d permutation=%v changed snapshot bytes", c03Seed, order)
		}
	}
}

func TestAdv_12_UndeclaredSingletonMerge(t *testing.T) {
	c03Start(t)
	r := c03Registry(t)
	s := c03Snapshot()
	s.Hists = nil
	s.Points = s.Points[1:]
	s.Points[0].Metric = "not_declared"
	got, e := obs.Merge(r, "fleet", s)
	if e == nil {
		b, e := jsonstats.Render(got)
		c03Must(t, e)
		t.Fatalf("undeclared singleton survived registry-aware merge and was exported: %s", b)
	}
}
