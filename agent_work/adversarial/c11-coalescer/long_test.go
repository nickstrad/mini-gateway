package adv_test

import (
	c "github.com/nickstrad/mini-gateway/internal/coalesce"
	"github.com/nickstrad/mini-gateway/internal/pool"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/test/e2e"
	"os"
	"testing"
	"time"
)

// This campaign is called from the three existing claim-13 attacks, so its
// outcomes belong to those attacks rather than inflating the report's count.
func hotEngine(t *testing.T, mode c.Mode, join c.JoinMode) {
	t.Helper()
	if os.Getenv("ADV_LONG") != "1" {
		return
	}
	s := spec()
	s.Name = "adv-c11-thousand-" + mode.String() + "-" + join.String()
	s.Conns = 32
	s.QueueCap = 2048
	s.Duration = 5 * time.Second
	s.Pool = &pool.Config{RatePerSec: 10000, MaxConnecting: 32}
	s.Coalesce.Mode = mode
	s.Coalesce.Join = join
	s.Admission.Capacity = 2048
	s.Admission.TenantCapacity = 2048
	s.Admission.Interval = 2_000_000_000
	s.Clients = nil
	for i := 0; i < 1000; i++ {
		s.Clients = append(s.Clients, e2e.Script{Tenant: i % 3, Start: time.Duration(i) * time.Microsecond, Steps: []e2e.Step{{SQL: "SELECT v FROM kv WHERE k = 'edge'"}}})
	}
	r := simRun(t, s, e2e.SimOptions{})
	reads := 0
	for _, q := range r.World.Fake.StatementLog() {
		if q.Kind == fakepg.KindRead {
			reads++
		}
	}
	if mode == c.ModeOff {
		want(t, reads, 1000)
	} else if reads < 1 || reads > 2 {
		t.Fatalf("1000 reads generated %d database executions", reads)
	}
	for i, cl := range r.Clients {
		if !e2e.HasToken(cl.Last(), "D:") {
			t.Fatalf("client %d missing data: %s", i, cl)
		}
	}
	t.Logf("1000 clients: database reads=%d mode=%s join=%s", reads, mode, join)
}
