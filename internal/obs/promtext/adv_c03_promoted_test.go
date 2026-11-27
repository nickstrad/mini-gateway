package promtext_test

import (
	"math/rand"
	"strconv"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/promtext"
)

const c03Seed int64 = 730031

func c03Start(t *testing.T) {
	t.Helper()
	t.Logf("seed=%d REPLAY: go test -count=1 -run '^%s$' ./internal/obs/promtext", c03Seed, t.Name())
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

func TestAdv_8_HostileTenantCampaign(t *testing.T) {
	c03Start(t)
	rng := rand.New(rand.NewSource(c03Seed))
	r := c03Registry(t)
	a := promtext.New(r, nil, promtext.Config{TenantMax: 3, EvictAfter: 2})
	for round := 0; round < 12; round++ {
		s := c03Snapshot()
		s.Points = s.Points[:1]
		s.Hists = nil
		for i := 0; i < 127; i++ {
			raw := make([]byte, rng.Intn(90)+1)
			rng.Read(raw)
			s.Points = append(s.Points, obs.Point{Component: "pool", Metric: "requests", Kind: obs.KindCounter, Labels: obs.Labels{Tenant: string(raw)}, Value: int64(i + 1)})
		}
		s.Sort()
		c03Must(t, a.Export(s))
		c03Must(t, promtext.Check(a.Last()))
		if a.TenantsTracked() > 3 {
			t.Fatalf("round %d tracked %d > 3", round, a.TenantsTracked())
		}
		labels := map[string]bool{}
		for _, line := range strings.Split(string(a.Last()), "\n") {
			i := strings.Index(line, `tenant="`)
			if i < 0 {
				continue
			}
			q := line[i+len("tenant="):]
			for j := 1; j < len(q); j++ {
				if q[j] == '\\' {
					j++
					continue
				}
				if q[j] == '"' {
					v, e := strconv.Unquote(q[:j+1])
					c03Must(t, e)
					labels[v] = true
					break
				}
			}
		}
		if len(labels) > 4 {
			t.Fatalf("round %d has %d tenant labels", round, len(labels))
		}
	}
}
