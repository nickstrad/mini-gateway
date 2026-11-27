package proxysim

import (
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
)

func lostInsert(t *testing.T) proxy.Snapshot {
	t.Helper()
	w, err := BuildWorld(WorldConfig{Seed: 1, Scenario: t.Name(), Engine: proxy.Config{Connections: 1}})
	if err != nil {
		t.Fatal(err)
	}
	p := workload.NewProbe(w.Net, w.Proxy.Addr(), NetOptions())
	p.Startup(TenantName(0), Database)
	if err := w.S.RunFor(20 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if len(w.Proxy.dbs) != 1 {
		t.Fatalf("%d database connections", len(w.Proxy.dbs))
	}
	var db *conn
	for _, c := range w.Proxy.dbs {
		db = c
	}
	_ = db.end.Reset()
	p.Query("INSERT INTO kv VALUES ('k', 'v')")
	if err := w.S.RunFor(20 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	s := w.Proxy.Engine.Snapshot()
	if s.Failed != 1 {
		t.Fatalf("failed %d, trace %s", s.Failed, p.Trace())
	}
	return s
}

// TestWriteRefusedByAResetSocketIsReportedBeforeTheClose proves proxysim posts EvDBWriteRefused before EvDBClosed when a database write fails on a reset socket.
func TestWriteRefusedByAResetSocketIsReportedBeforeTheClose(t *testing.T) {
	s := lostInsert(t)
	if s.ReachedNo != 1 || s.ByRow[pgwire.ErrRowLostAfterRead] != 1 {
		t.Fatalf("reached no %d rows %v", s.ReachedNo, s.ByRow)
	}
}
