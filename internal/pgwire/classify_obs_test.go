package pgwire

import (
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
	"github.com/nickstrad/mini-gateway/internal/work"
)

func TestClassifierSnapshotCountersMoveAndConserve(t *testing.T) {
	var c Classifier
	if err := c.Snapshot().Check(); err != nil {
		t.Fatal(err)
	}
	for _, x := range clsLoadCorpus(t) {
		c.Classify("t", []byte(x.text), false)
		c.Classify("t", []byte(x.text), true)
	}
	s := c.Snapshot()
	if err := s.Check(); err != nil {
		t.Fatalf("%v: %+v", err, s)
	}
	for cl := work.ClassReject; cl <= work.ClassContinueLease; cl++ {
		if s.ByClass[cl] == 0 {
			t.Errorf("class %s did not move", cl)
		}
	}
	for r := work.RejectSessionSet; r < work.RejectExtendedProtocol; r++ {
		if s.ByReason[r] == 0 {
			t.Errorf("reason %s did not move", r)
		}
	}
	for i, v := range s.MayLeaveOpen {
		if v == 0 {
			t.Errorf("may-leave-open %s did not move", work.Tri(i))
		}
	}
	if s.Unclassifiable == 0 || s.Opaque == 0 || s.ReadOnly == 0 || s.Coalescable == 0 || s.Hinted == 0 || s.Statements == 0 || s.Classified == 0 {
		t.Errorf("a counter did not move: %+v", s)
	}
	rows := ClassifierMetrics(s)
	if len(rows) == 0 || rows[0].Metric != "statements" || rows[0].Value != s.Classified {
		t.Errorf("metric rows: %+v", rows)
	}
	bad := s
	bad.ByClass[work.ClassReject]++
	if bad.Check() == nil {
		t.Error("Check accepts counters that do not add up")
	}
	bad = s
	bad.Coalescable = bad.ReadOnly + 1
	if bad.Check() == nil {
		t.Error("Check accepts more coalescable than read-only")
	}
}

func TestReplyMetricsCoverEveryRow(t *testing.T) {
	var b ReplyBuilder
	_, _, _ = b.Build(nil, ErrRowShed, 'I')
	rows := ReplyMetrics(b.Snapshot())
	if len(rows) != int(ErrRowCount)+2 || rows[0].Reason != "shed" || rows[0].Value != 1 || rows[len(rows)-2].Value != 1 {
		t.Fatalf("%+v", rows)
	}
}

func TestClassifierReadOnlyCorpusLeavesTheFakeUnchanged(t *testing.T) {
	s := sim.New(sim.Config{Seed: 1, Scenario: t.Name()})
	n := simnet.New(s)
	srv, err := fakepg.New(n, fakepg.Config{Rows: []fakepg.Row{{Key: "a", Value: "1"}, {Key: "b", Value: "2"}}})
	if err != nil {
		t.Fatal(err)
	}
	p := workload.NewProbe(n, srv.Addr(), simnet.DefaultOptions())
	p.Startup("app", "app")
	if err := s.RunFor(time.Millisecond); err != nil {
		t.Fatal(err)
	}
	ran := 0
	for _, c := range clsLoadCorpus(t) {
		if !ClassifyQuery([]byte(c.text), false).ReadOnly {
			continue
		}
		p.Query(c.text)
		ran++
		if err := s.RunFor(time.Second); err != nil {
			t.Fatal(err)
		}
	}
	p.Terminate()
	p.Close()
	if err := s.RunUntilQuiet(); err != nil {
		t.Fatal(err)
	}
	snap := srv.Snapshot()
	if ran < 100 || snap.StatementsStarted < int64(ran) {
		t.Fatalf("ran %d read-only statements, the fake started %d", ran, snap.StatementsStarted)
	}
	if snap.WritesApplied != 0 || len(srv.WriteLog()) != 0 {
		t.Fatalf("read-only statements changed the fake: writes %d, log %d", snap.WritesApplied, len(srv.WriteLog()))
	}
	rows := srv.Rows()
	if len(rows) != 2 || rows[0].Value != "1" || rows[1].Value != "2" {
		t.Fatalf("table changed: %+v", rows)
	}
}
