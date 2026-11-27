package live_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/loadgen"
	"github.com/nickstrad/mini-gateway/internal/loadgen/live"
	"github.com/nickstrad/mini-gateway/internal/loadgen/loadgentest"
	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/csvflush"
)

func options(f *loadgentest.Fake, p loadgen.Plan, conns, outstanding int) live.Options {
	return live.Options{Addr: f.Addr(), Database: "app", UserPrefix: "t", Password: "unused", Plan: p,
		Tracker: loadgen.TrackerConfig{Conns: conns, MaxOutstanding: outstanding, LateAfterNs: int64(250 * time.Millisecond)},
		Drain:   3 * time.Second}
}

func plan(seed, rate int64, d time.Duration, tenants int, prof loadgen.Profile) loadgen.Plan {
	return loadgen.Plan{Seed: seed, Rate: rate, DurationNs: int64(d), Tenants: tenants, Keys: 100, Profile: prof}
}

func run(t *testing.T, o live.Options) live.Result {
	t.Helper()
	res, err := live.Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if err := res.Snapshot.Check(); err != nil {
		t.Fatal(err)
	}
	return res
}

// TestAchievedEqualsIntendedAgainstAFakeThatKeepsUp proves achieved equals
// target with zero late or unsent requests when the target keeps up.
func TestAchievedEqualsIntendedAgainstAFakeThatKeepsUp(t *testing.T) {
	f := loadgentest.Start(t, loadgentest.Options{})
	p := plan(11, 200, 2*time.Second, 2, loadgen.ProfileSimple)
	s := run(t, options(f, p, 4, 64)).Snapshot
	if s.Intended != p.Count() || s.Sent != p.Count() || s.CouldNotSend != 0 || s.Late != 0 || s.Shortfall() {
		t.Fatalf("not on schedule: %+v", s)
	}
	if s.Completed[loadgen.OutcomeOK] != p.Count() || s.InFlight != 0 || s.Queued != 0 {
		t.Fatalf("completed %v in flight %d", s.Completed, s.InFlight)
	}
	if loadgen.AchievedTenths(s) != 2000 {
		t.Fatalf("achieved %s/s", loadgen.FormatTenths(loadgen.AchievedTenths(s)))
	}
}

// TestTheSameSeedSendsTheSameStatementsPerTenant proves two live runs with the
// same seed send identical per-tenant SQL sequences matching the plan.
func TestTheSameSeedSendsTheSameStatementsPerTenant(t *testing.T) {
	p := plan(99, 300, time.Second, 3, loadgen.ProfileErrors)
	var got [2]map[string][]string
	for i := range got {
		f := loadgentest.Start(t, loadgentest.Options{})
		run(t, options(f, p, 3, 64))
		got[i] = map[string][]string{}
		for ten := 0; ten < p.Tenants; ten++ {
			u := loadgen.TenantUser("t", ten)
			got[i][u] = f.Statements(u)
		}
	}
	want := map[string][]string{}
	for i := int64(0); i < p.Count(); i++ {
		op := p.Op(i)
		u := loadgen.TenantUser("t", op.Tenant)
		want[u] = append(want[u], op.SQL)
	}
	for u, w := range want {
		if !slices.Equal(got[0][u], w) || !slices.Equal(got[1][u], w) {
			t.Fatalf("tenant %s: run 1 sent %d, run 2 sent %d statements, the plan has %d; or the order differs", u,
				len(got[0][u]), len(got[1][u]), len(w))
		}
	}
}

// TestAThrottledFakeGivesShortfallAndCouldNotSend proves a slow target fills
// MaxOutstanding and produces SHORTFALL with conserved could-not-send counts.
func TestAThrottledFakeGivesShortfallAndCouldNotSend(t *testing.T) {
	f := loadgentest.Start(t, loadgentest.Options{Delay: 50 * time.Millisecond})
	p := plan(5, 500, time.Second, 2, loadgen.ProfileSimple)
	s := run(t, options(f, p, 2, 4)).Snapshot
	if !s.Shortfall() || s.CouldNotSend == 0 {
		t.Fatalf("no shortfall against a throttled server: %+v", s)
	}
	if s.Intended != p.Count() || s.Intended != s.Sent+s.CouldNotSend {
		t.Fatalf("intended %d sent %d could not send %d", s.Intended, s.Sent, s.CouldNotSend)
	}
}

// TestAFakeThatStopsReadingMakesSendsLateOrUnsent proves a peer that stops
// reading causes SHORTFALL and zero ok completions while conserving counts.
func TestAFakeThatStopsReadingMakesSendsLateOrUnsent(t *testing.T) {
	f := loadgentest.Start(t, loadgentest.Options{NoRead: true})
	p := plan(6, 2000, time.Second, 1, loadgen.ProfileSimple)
	o := options(f, p, 1, 1024)
	o.Drain = 200 * time.Millisecond
	s := run(t, o).Snapshot
	if !s.Shortfall() || s.Late+s.CouldNotSend == 0 {
		t.Fatalf("a server that reads nothing gave no shortfall: %+v", s)
	}
	if s.Completed[loadgen.OutcomeOK] != 0 {
		t.Fatalf("completions from a server that never answers: %+v", s.Completed)
	}
}

// TestAStalledFakeShowsInLatencyFromIntendedSend proves a 600 ms server stall
// shows in intended-time latency without slowing the open-loop schedule.
func TestAStalledFakeShowsInLatencyFromIntendedSend(t *testing.T) {
	f := loadgentest.Start(t, loadgentest.Options{StallFrom: 300 * time.Millisecond, StallUntil: 900 * time.Millisecond})
	p := plan(7, 100, 1500*time.Millisecond, 2, loadgen.ProfileSimple)
	s := run(t, options(f, p, 2, 256)).Snapshot
	if s.Intended != p.Count() || s.Sent != p.Count() || s.CouldNotSend != 0 {
		t.Fatalf("the stall slowed the schedule: %+v", s)
	}
	l := loadgen.SnapshotLatencies(s.Latency)
	if l.P99 < int64(300*time.Millisecond) || l.Max < int64(500*time.Millisecond) {
		t.Fatalf("a 600 ms stall does not show in latency from intended send: p99 %s max %s", loadgen.FormatNs(l.P99), loadgen.FormatNs(l.Max))
	}
}

// TestErrorsProfileShowsEveryOutcomeUnderItsSQLStateClass proves the errors
// profile yields every promised outcome and exact per-class counts (23, 42, 22, 0A).
func TestErrorsProfileShowsEveryOutcomeUnderItsSQLStateClass(t *testing.T) {
	f := loadgentest.Start(t, loadgentest.Options{})
	p := plan(8, 400, time.Second, 2, loadgen.ProfileErrors)
	s := run(t, options(f, p, 2, 64)).Snapshot
	want := map[string]int64{}
	var ok, dbErr, refused int64
	for i := int64(0); i < p.Count(); i++ {
		op := p.Op(i)
		switch op.Kind {
		case loadgen.KindRead, loadgen.KindWrite:
			ok++
		case loadgen.KindRefused:
			refused++
			want[op.Kind.ExpectedClass()]++
		default:
			dbErr++
			want[op.Kind.ExpectedClass()]++
		}
	}
	if s.Completed[loadgen.OutcomeOK] != ok || s.Completed[loadgen.OutcomeDBError] != dbErr || s.Completed[loadgen.OutcomeRefused] != refused {
		t.Fatalf("completed %v, want ok %d db-error %d refused %d", s.Completed, ok, dbErr, refused)
	}
	for _, class := range []string{"23", "42", "22", "0A"} {
		if want[class] == 0 {
			t.Fatalf("the plan drew no %s", class)
		}
	}
	got := map[string]int64{}
	for _, c := range s.ErrorsByClass {
		got[c.Class] = c.Count
	}
	for class, n := range want {
		if got[class] != n {
			t.Fatalf("class %s: counted %d, sent %d (all: %v)", class, got[class], n, s.ErrorsByClass)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("classes %v, want %v", got, want)
	}
}

// TestConservationHoldsWhenTheRunIsInterrupted proves cancelling ctx mid-run
// stops new intends, drains queued work to could-not-send, and keeps Check green.
func TestConservationHoldsWhenTheRunIsInterrupted(t *testing.T) {
	f := loadgentest.Start(t, loadgentest.Options{Delay: 5 * time.Millisecond})
	p := plan(9, 300, 10*time.Second, 2, loadgen.ProfileSimple)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(700*time.Millisecond, cancel)
	res, err := live.Run(ctx, options(f, p, 2, 8))
	if err != nil {
		t.Fatal(err)
	}
	s := res.Snapshot
	if !res.Stopped || s.Intended == 0 || s.Intended >= p.Count() {
		t.Fatalf("interrupt not seen: stopped %v intended %d of %d", res.Stopped, s.Intended, p.Count())
	}
	if err := s.Check(); err != nil || s.Queued != 0 {
		t.Fatalf("after an interrupt: %v queued %d", err, s.Queued)
	}
}

// TestTheLoadToolFlushesItsOwnSnapshotToCSV proves the CSV run directory's
// final flush matches the final in-memory loadgen.Snapshot value for value.
func TestTheLoadToolFlushesItsOwnSnapshotToCSV(t *testing.T) {
	f := loadgentest.Start(t, loadgentest.Options{})
	dir := t.TempDir()
	p := plan(10, 100, time.Second, 1, loadgen.ProfileErrors)
	o := options(f, p, 1, 64)
	o.CSVDir = dir
	o.StatusEvery = 200 * time.Millisecond
	res := run(t, o)
	if res.RunDir == "" {
		t.Fatal("no run directory")
	}
	r, err := csvflush.Read(res.RunDir)
	if err != nil {
		t.Fatal(err)
	}
	seqs := r.FlushSeqs("loadgen")
	if len(seqs) < 2 {
		t.Fatalf("flushes %v", seqs)
	}
	final := r.Snapshot("loadgen", seqs[len(seqs)-1])
	s := res.Snapshot
	checks := map[string]int64{"intended": s.Intended, "sent": s.Sent, "could_not_send": s.CouldNotSend, "late": s.Late,
		"queued": s.Queued, "in_flight": s.InFlight}
	for m, want := range checks {
		if got := final.Value(obs.ComponentLoadgen, m, obs.Labels{}); got != want {
			t.Errorf("%s: file %d, snapshot %d", m, got, want)
		}
	}
	for _, out := range loadgen.Outcomes() {
		if got := final.Value(obs.ComponentLoadgen, "completed", obs.Labels{Outcome: out.String()}); got != s.Completed[out] {
			t.Errorf("completed{%s}: file %d, snapshot %d", out, got, s.Completed[out])
		}
	}
	if r.Run.Info.Scenario != "load-errors" || r.Run.Info.Seed != 10 || r.Run.Info.Tier != csvflush.TierReal {
		t.Fatalf("run.csv %+v", r.Run.Info)
	}
}

// TestNoLoginIsASetupError proves zero open connections return a setup error
// and record ConnectFailures.
func TestNoLoginIsASetupError(t *testing.T) {
	p := plan(1, 10, time.Second, 1, loadgen.ProfileSimple)
	o := live.Options{Addr: "127.0.0.1:1", Plan: p, Tracker: loadgen.TrackerConfig{Conns: 1}, DialTimeout: time.Second}
	res, err := live.Run(context.Background(), o)
	if err == nil {
		t.Fatal("no error without a server")
	}
	if res.Snapshot.ConnectFailures != 1 {
		t.Fatalf("connect failures %d", res.Snapshot.ConnectFailures)
	}
}
