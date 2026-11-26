package e2e

import (
	"slices"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/sim/workload"
	"github.com/nickstrad/mini-gateway/internal/sim/world"
)

func TestScenarioChunk2BaselineThroughTheProxy(t *testing.T) {
	sc, ok := world.Lookup("direct-mixed-transactions")
	if !ok {
		t.Fatal("no chunk 2 baseline")
	}
	for _, seed := range []int64{1, 2, 3} {
		direct := world.Run(sc, seed)
		if err := sc.Verify(direct); err != nil {
			t.Fatalf("direct: %v", err)
		}
		d := &ProxyDriver{SimDriver: world.SimDriver{Seed: seed, Scenario: "proxied-mixed-transactions"}, Conns: 4}
		err := world.Drive(d, sc.Setup(seed), sc.Duration)
		proxied := d.Result(sc.Name, err)
		if err := sc.Verify(proxied); err != nil {
			t.Fatalf("seed %d through the proxy: %v", seed, err)
		}
		for i, a := range direct.Obs.Clients.Tenants {
			b := proxied.Obs.Clients.Tenants[i]
			if a.Intended != b.Intended || a.OK != b.OK || a.Errors != b.Errors || a.Lost != b.Lost ||
				!slices.EqualFunc(a.ErrorsByCode, b.ErrorsByCode, func(x, y workload.CodeCount) bool { return x == y }) {
				t.Fatalf("seed %d tenant %s: direct ok %d errors %d %v, proxied ok %d errors %d %v", seed, a.Name, a.OK, a.Errors,
					a.ErrorsByCode, b.OK, b.Errors, b.ErrorsByCode)
			}
		}
		if proxied.Obs.DBConnsPeak > 5 || direct.Obs.DBConnsPeak < 20 {
			t.Fatalf("database connections: direct peak %d, proxied peak %d", direct.Obs.DBConnsPeak, proxied.Obs.DBConnsPeak)
		}
		if s := d.Proxy.Engine.Snapshot(); s.LeasesStarted == 0 || s.ResetsCompleted != s.LeasesEndedReset {
			t.Fatalf("leases %d resets %d/%d", s.LeasesStarted, s.ResetsCompleted, s.LeasesEndedReset)
		}
	}
}
