package proxysim

import (
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
)

func TestSmokeQueryThroughProxy(t *testing.T) {
	w, err := BuildWorld(WorldConfig{Seed: 1, Scenario: "smoke", Engine: proxy.Config{Connections: 2},
		Fake: fakepg.Config{Rows: []fakepg.Row{{Key: "k1", Value: "v1"}}}})
	if err != nil {
		t.Fatal(err)
	}
	p := workload.NewProbe(w.Net, w.Proxy.Addr(), NetOptions())
	p.Startup(TenantName(0), Database)
	if err := w.S.RunFor(50 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	mark := len(p.Events())
	p.Query("SELECT v FROM kv WHERE k = 'k1'")
	_ = w.S.RunFor(50 * time.Millisecond)
	got := p.TraceFrom(mark)
	if !strings.Contains(got, "C:SELECT 1 ZI") {
		t.Fatalf("reply %q", got)
	}
	mark = len(p.Events())
	p.Query("BEGIN")
	_ = w.S.RunFor(20 * time.Millisecond)
	p.Query("UPDATE kv SET v = 'x' WHERE k = 'k1'")
	_ = w.S.RunFor(20 * time.Millisecond)
	p.Query("COMMIT")
	_ = w.S.RunFor(20 * time.Millisecond)
	t.Logf("tx: %s", p.TraceFrom(mark))
	p.Terminate()
	if err := w.Proxy.Finish(); err != nil {
		t.Fatal(err)
	}
	s := w.Proxy.Engine.Snapshot()
	t.Logf("snap: completed %d leases %d resets %d/%d", s.Completed, s.LeasesStarted, s.ResetsStarted, s.ResetsCompleted)
	if s.ResetsCompleted == 0 || s.Completed != 4 {
		t.Fatalf("snapshot %+v", s)
	}
}
