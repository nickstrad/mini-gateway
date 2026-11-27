package loadgen_test

import (
	"errors"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/loadgen"
	"github.com/nickstrad/mini-gateway/internal/obs"
)

const ms = int64(1_000_000)

func tracker(t *testing.T, p loadgen.Plan, cfg loadgen.TrackerConfig) *loadgen.Tracker {
	t.Helper()
	tr, err := loadgen.NewTracker(p, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for c := 0; c < cfg.Conns; c++ {
		tr.ConnUp(c)
	}
	return tr
}

type served struct {
	conn int
	at   int64
}

func runServer(t *testing.T, tr *loadgen.Tracker, step, end int64, serviceAt func(sentAt int64) int64) loadgen.Snapshot {
	t.Helper()
	var pending []served
	var out []loadgen.Dispatch
	lastDone := map[int]int64{}
	for now := int64(0); now <= end; now += step {
		keep := pending[:0]
		for _, s := range pending {
			if s.at <= now {
				if !tr.Completed(s.conn, now, loadgen.Result{Outcome: loadgen.OutcomeOK}) {
					t.Fatalf("completion on conn %d had nothing in flight", s.conn)
				}
				continue
			}
			keep = append(keep, s)
		}
		pending = keep
		out = tr.Tick(now, out[:0])
		for _, d := range out {
			if !tr.Sent(d.Conn, now) {
				t.Fatal("send without a queued op")
			}
			done := max(serviceAt(now), lastDone[d.Conn])
			lastDone[d.Conn] = done
			pending = append(pending, served{conn: d.Conn, at: done})
		}
		if err := tr.Snapshot().Check(); err != nil {
			t.Fatalf("at %d: %v", now, err)
		}
	}
	tr.Finish()
	return tr.Snapshot()
}

// TestAchievedEqualsIntendedAtARateTheServerCanServe proves achieved rate
// equals target with zero shortfall when the server keeps up.
func TestAchievedEqualsIntendedAtARateTheServerCanServe(t *testing.T) {
	p := plan(200, 2000)
	tr := tracker(t, p, loadgen.TrackerConfig{Conns: 4, MaxOutstanding: 8})
	s := runServer(t, tr, ms, 3000*ms, func(at int64) int64 { return at + 2*ms })
	if s.Intended != p.Count() || s.Sent != p.Count() || s.CouldNotSend != 0 || s.Late != 0 || s.Shortfall() {
		t.Fatalf("snapshot %+v", s)
	}
	if s.Completed[loadgen.OutcomeOK] != p.Count() || s.InFlight != 0 || s.Queued != 0 {
		t.Fatalf("completed %v in flight %d", s.Completed, s.InFlight)
	}
	if loadgen.AchievedTenths(s) != 2000 {
		t.Fatalf("achieved %d tenths", loadgen.AchievedTenths(s))
	}
	for _, l := range loadgen.Summary(s, loadgen.SnapshotLatencies(s.Latency), loadgen.SnapshotLatencies(s.LatencyFromSend)) {
		if strings.HasPrefix(l, "SHORTFALL") {
			t.Fatalf("SHORTFALL printed for a served run: %s", l)
		}
	}
}

// TestAThrottledServerGivesShortfallAndExactCouldNotSend proves back pressure
// produces SHORTFALL and exact conserved could-not-send counts.
func TestAThrottledServerGivesShortfallAndExactCouldNotSend(t *testing.T) {
	p := plan(1000, 1000)
	tr := tracker(t, p, loadgen.TrackerConfig{Conns: 2, MaxOutstanding: 4})
	s := runServer(t, tr, ms, 1500*ms, func(at int64) int64 { return at + 20*ms })
	if s.CouldNotSend == 0 || !s.Shortfall() {
		t.Fatalf("no shortfall: %+v", s)
	}
	if s.Intended != s.Sent+s.CouldNotSend {
		t.Fatalf("intended %d != sent %d + could not send %d", s.Intended, s.Sent, s.CouldNotSend)
	}
	maxServed := int64(1000) / 20 * int64(2*4)
	if s.Sent > maxServed+8 {
		t.Fatalf("sent %d, more than the server can take (%d)", s.Sent, maxServed)
	}
	found := false
	for _, l := range loadgen.Summary(s, loadgen.SnapshotLatencies(s.Latency), loadgen.SnapshotLatencies(s.LatencyFromSend)) {
		if strings.HasPrefix(l, "SHORTFALL: ") && strings.Contains(l, " could not send") {
			found = true
		}
	}
	if !found {
		t.Fatal("no SHORTFALL line")
	}
}

// TestLateIsCountedExactlyAgainstTheThreshold proves sends at exactly
// LateAfterNs are on time while sends 1 ns past it increment Late.
func TestLateIsCountedExactlyAgainstTheThreshold(t *testing.T) {
	p := plan(10, 1000)
	tr := tracker(t, p, loadgen.TrackerConfig{Conns: 2, MaxOutstanding: 16, LateAfterNs: 5 * ms})
	out := tr.Tick(p.DurationNs, nil)
	if int64(len(out)) != p.Count() {
		t.Fatalf("dispatched %d", len(out))
	}
	late := int64(0)
	for _, d := range out {
		sendAt := d.Op.At + 5*ms
		if d.Op.Index%3 == 0 {
			sendAt++
			late++
		}
		_ = sendAt
	}
	sent := map[int][]loadgen.Dispatch{}
	for _, d := range out {
		sent[d.Conn] = append(sent[d.Conn], d)
	}
	for c, ds := range sent {
		for _, d := range ds {
			at := d.Op.At + 5*ms
			if d.Op.Index%3 == 0 {
				at++
			}
			if !tr.Sent(c, at) {
				t.Fatal("send refused")
			}
		}
	}
	if s := tr.Snapshot(); s.Late != late {
		t.Fatalf("late %d, want %d (a send exactly at the threshold is on time)", s.Late, late)
	}
}

// TestAStalledServerShowsInLatencyFromIntendedSendNotInTheRate proves a 1 s
// stall shows in latency without reducing the intended or sent schedule.
func TestAStalledServerShowsInLatencyFromIntendedSendNotInTheRate(t *testing.T) {
	stall := func(at int64) int64 {
		if at >= 500*ms && at < 1500*ms {
			return 1500 * ms
		}
		return at + ms
	}
	p := plan(100, 3000)
	tr := tracker(t, p, loadgen.TrackerConfig{Conns: 4, MaxOutstanding: 64})
	s := runServer(t, tr, ms, 4000*ms, stall)
	if s.Intended != p.Count() || s.Sent != p.Count() || s.CouldNotSend != 0 {
		t.Fatalf("the schedule slowed: %+v", s)
	}
	l := loadgen.SnapshotLatencies(s.Latency)
	if l.Max < 990*ms || l.P99 < 900*ms {
		t.Fatalf("a one-second stall does not show in latency from intended send: %+v", l)
	}
	fromSend := loadgen.SnapshotLatencies(s.LatencyFromSend)
	if fromSend.Max < 990*ms {
		t.Fatalf("latency from send max %d", fromSend.Max)
	}
}

// TestPlantedClosedLoopHidesTheStallSoTheOpenLoopCheckCanFail proves the
// closed-loop plant collapses p99 during a stall so the open-loop assertion can fail.
func TestPlantedClosedLoopHidesTheStallSoTheOpenLoopCheckCanFail(t *testing.T) {
	p := plan(100, 3000)
	p.Tenants = 1
	slowStart := func(at int64) int64 {
		if at < 10*ms {
			return 1000 * ms
		}
		return at + ms
	}
	var got [2]loadgen.Latencies
	for i, plant := range []loadgen.Plant{loadgen.PlantNone, loadgen.PlantSlowSchedule} {
		tr := tracker(t, p, loadgen.TrackerConfig{Conns: 1, MaxOutstanding: 64, Plant: plant})
		s := runServer(t, tr, ms, 5000*ms, slowStart)
		got[i] = loadgen.SnapshotLatencies(s.Latency)
		_ = s
	}
	if got[0].P99 < 500*ms {
		t.Fatalf("open loop: the requests intended during the stall must show it in p99: %+v", got[0])
	}
	if got[1].P99 >= 100*ms {
		t.Fatalf("closed-loop plant still shows the stall in p99: %+v (the canary cannot fail)", got[1])
	}
}

// TestConnectionLossCountsLostAndCouldNotSendAndConserves proves ConnDown
// marks in-flight ops lost, queued ops could-not-send, and preserves Check.
func TestConnectionLossCountsLostAndCouldNotSendAndConserves(t *testing.T) {
	p := plan(1000, 100)
	tr := tracker(t, p, loadgen.TrackerConfig{Conns: 2, MaxOutstanding: 8})
	out := tr.Tick(5*ms, nil)
	for _, d := range out[:2] {
		tr.Sent(d.Conn, 5*ms)
	}
	tr.ConnDown(0)
	tr.ConnDown(1)
	s := tr.Snapshot()
	if err := s.Check(); err != nil {
		t.Fatal(err)
	}
	if s.Completed[loadgen.OutcomeLost] != 2 || s.Queued != 0 || s.CouldNotSend != int64(len(out))-2 || s.ConnsOpen != 0 {
		t.Fatalf("after loss: %+v", s)
	}
	out = tr.Tick(p.DurationNs, out[:0])
	if len(out) != 0 {
		t.Fatal("dispatched to a dead connection")
	}
	tr.Finish()
	s = tr.Snapshot()
	if err := s.Check(); err != nil || s.Intended != p.Count() || s.CouldNotSend != p.Count()-2 {
		t.Fatalf("%v %+v", err, s)
	}
}

// TestConservationHoldsOnEveryExitPathUnderRandomEvents proves Check holds
// after every event and after Finish across 200 seeded random sequences.
func TestConservationHoldsOnEveryExitPathUnderRandomEvents(t *testing.T) {
	for seed := uint64(1); seed <= 200; seed++ {
		r := rand.New(rand.NewPCG(seed, 9))
		p := plan(1+r.Int64N(2000), 1+r.Int64N(2000))
		p.Tenants = 1 + r.IntN(4)
		cfg := loadgen.TrackerConfig{Conns: p.Tenants + r.IntN(4), MaxOutstanding: 1 + r.IntN(8), LateAfterNs: r.Int64N(10 * ms)}
		tr := tracker(t, p, cfg)
		var out []loadgen.Dispatch
		now := int64(0)
		for k := 0; k < 400; k++ {
			now += r.Int64N(10 * ms)
			switch r.IntN(6) {
			case 0, 1:
				out = tr.Tick(now, out[:0])
			case 2:
				tr.Sent(r.IntN(cfg.Conns), now)
			case 3:
				o := loadgen.Outcomes()[r.IntN(4)]
				tr.Completed(r.IntN(cfg.Conns), now, loadgen.Result{Outcome: o, Code: []string{"", "23505", "42P01", "0A000", "x"}[r.IntN(5)]})
			case 4:
				if r.IntN(10) == 0 {
					tr.ConnDown(r.IntN(cfg.Conns))
				}
			case 5:
				if r.IntN(50) == 0 {
					tr.Stop()
				}
			}
			if err := tr.Snapshot().Check(); err != nil {
				t.Fatalf("seed %d step %d: %v", seed, k, err)
			}
		}
		tr.Finish()
		s := tr.Snapshot()
		if err := s.Check(); err != nil || s.Queued != 0 {
			t.Fatalf("seed %d after finish: %v queued %d", seed, err, s.Queued)
		}
	}
}

// TestErrorsAreCountedUnderTheirSQLStateClass proves error codes map to sorted
// two-character SQLSTATE classes and short codes fall back to "other".
func TestErrorsAreCountedUnderTheirSQLStateClass(t *testing.T) {
	p := plan(1000, 10)
	tr := tracker(t, p, loadgen.TrackerConfig{Conns: 2, MaxOutstanding: 16})
	out := tr.Tick(p.DurationNs, nil)
	codes := []string{"23505", "42P01", "22012", "0A000", "23505", "", "57014", "x"}
	for i, d := range out[:len(codes)] {
		tr.Sent(d.Conn, p.DurationNs)
		o := loadgen.OutcomeDBError
		if codes[i] == "" {
			o = loadgen.OutcomeOK
		}
		if codes[i] == "0A000" {
			o = loadgen.OutcomeRefused
		}
		_ = i
		tr.Completed(d.Conn, p.DurationNs, loadgen.Result{Outcome: o, Code: codes[i]})
	}
	s := tr.Snapshot()
	want := []loadgen.ClassCount{{"0A", 1}, {"22", 1}, {"23", 2}, {"42", 1}, {"57", 1}, {"other", 1}}
	if len(s.ErrorsByClass) != len(want) {
		t.Fatalf("classes %v", s.ErrorsByClass)
	}
	for i := range want {
		if s.ErrorsByClass[i] != want[i] {
			t.Fatalf("classes %v, want %v", s.ErrorsByClass, want)
		}
	}
	if s.Completed[loadgen.OutcomeRefused] != 1 || s.Completed[loadgen.OutcomeDBError] != 6 || s.Completed[loadgen.OutcomeOK] != 1 {
		t.Fatalf("completed %v", s.Completed)
	}
}

// TestErrorClassesAreBounded proves ErrorsByClass never exceeds MaxClasses
// while conserving the total error count through ClassOther.
func TestErrorClassesAreBounded(t *testing.T) {
	p := plan(1000, 1000)
	tr := tracker(t, p, loadgen.TrackerConfig{Conns: 2, MaxOutstanding: 1024})
	out := tr.Tick(p.DurationNs, nil)
	for i, d := range out[:500] {
		tr.Sent(d.Conn, p.DurationNs)
		code := string(rune('A'+i%26)) + string(rune('A'+i/26%26)) + "000"
		tr.Completed(d.Conn, p.DurationNs, loadgen.Result{Outcome: loadgen.OutcomeDBError, Code: code})
	}
	s := tr.Snapshot()
	if len(s.ErrorsByClass) > loadgen.MaxClasses {
		t.Fatalf("%d classes", len(s.ErrorsByClass))
	}
	total := int64(0)
	for _, c := range s.ErrorsByClass {
		total += c.Count
	}
	if total != 500 {
		t.Fatalf("errors counted %d", total)
	}
}

// TestTrackerConfigIsValidated proves NewTracker rejects invalid bounds and
// fills defaults for zero MaxOutstanding and LateAfterNs.
func TestTrackerConfigIsValidated(t *testing.T) {
	p := plan(10, 1000)
	for _, c := range []loadgen.TrackerConfig{{Conns: 1}, {Conns: loadgen.MaxConns + 1}, {Conns: 2, MaxOutstanding: -1},
		{Conns: 2, MaxOutstanding: loadgen.MaxOutstandingCap + 1}, {Conns: 2, LateAfterNs: -1}} {
		if _, err := loadgen.NewTracker(p, c); !errors.Is(err, loadgen.ErrTracker) {
			t.Errorf("%+v: %v", c, err)
		}
	}
	tr, err := loadgen.NewTracker(p, loadgen.TrackerConfig{Conns: 2})
	if err != nil || tr.Config().MaxOutstanding != loadgen.DefaultMaxOutstanding || tr.Config().LateAfterNs != loadgen.DefaultLateAfterNs {
		t.Fatalf("zero means the default: %v %+v", err, tr.Config())
	}
}

// TestSnapshotCountersMoveAndTheMappingExportsEveryMetric proves every loadgen
// metric moves, conserves through obs.Collector, and publishes from a zero snapshot.
func TestSnapshotCountersMoveAndTheMappingExportsEveryMetric(t *testing.T) {
	p := plan(100, 1000)
	tr := tracker(t, p, loadgen.TrackerConfig{Conns: 2, MaxOutstanding: 2, LateAfterNs: 1})
	tr.ConnectFailed(0)
	out := tr.Tick(0, nil)
	tr.Sent(out[0].Conn, 5*ms)
	tr.Completed(out[0].Conn, 6*ms, loadgen.Result{Outcome: loadgen.OutcomeDBError, Code: "23505"})
	s := runServer(t, tr, ms, 1500*ms, func(at int64) int64 { return at + 30*ms })
	c := obs.NewCollector(obs.Config{Instance: "loadgen", Detail: true})
	if err := c.Register(loadgen.Source(func() loadgen.Snapshot { return s })); err != nil {
		t.Fatal(err)
	}
	snap, err := c.Publish()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range loadgen.Metrics() {
		if d.Kind == obs.KindHistogram {
			if h, ok := snap.Hist(obs.ComponentLoadgen, d.Name, obs.Labels{}); !ok || h.Count() == 0 {
				t.Errorf("histogram %s did not move", d.Name)
			}
			continue
		}
		if d.Name == "queued" || d.Name == "in_flight" {
			continue
		}
		if snap.Sum(obs.ComponentLoadgen, d.Name) == 0 {
			t.Errorf("metric %s did not move", d.Name)
		}
	}
	if snap.Value(obs.ComponentLoadgen, "intended", obs.Labels{}) != snap.Value(obs.ComponentLoadgen, "sent", obs.Labels{})+
		snap.Value(obs.ComponentLoadgen, "could_not_send", obs.Labels{})+snap.Value(obs.ComponentLoadgen, "queued", obs.Labels{}) {
		t.Fatal("exported numbers do not conserve")
	}
	for _, o := range loadgen.Outcomes() {
		if _, ok := snap.Point(obs.ComponentLoadgen, "completed", obs.Labels{Outcome: o.String()}); !ok {
			t.Errorf("outcome %s not emitted", o)
		}
	}
	var zero loadgen.Snapshot
	c2 := obs.NewCollector(obs.Config{Instance: "loadgen", Detail: true})
	if err := c2.Register(loadgen.Source(func() loadgen.Snapshot { return zero })); err != nil {
		t.Fatal(err)
	}
	if _, err := c2.Publish(); err != nil {
		t.Fatalf("a zero snapshot must publish every unlabelled metric: %v", err)
	}
}

// TestSnapshotCheckCatchesBrokenConservation proves Snapshot.Check rejects
// unbalanced intended and sent equations with ErrConservation.
func TestSnapshotCheckCatchesBrokenConservation(t *testing.T) {
	s := loadgen.Snapshot{Planned: 10, Intended: 3, Sent: 2, CouldNotSend: 0}
	if err := s.Check(); !errors.Is(err, loadgen.ErrConservation) {
		t.Fatal("intended != sent + could not send not caught")
	}
	s = loadgen.Snapshot{Planned: 10, Intended: 2, Sent: 2, InFlight: 1}
	if err := s.Check(); !errors.Is(err, loadgen.ErrConservation) {
		t.Fatal("sent != completed + in flight not caught")
	}
}

// TestStatusAndSummaryLines proves StatusLine and Summary render counts,
// SQLSTATE classes, achieved rate, quantiles, and SHORTFALL text.
func TestStatusAndSummaryLines(t *testing.T) {
	s := loadgen.Snapshot{Planned: 100, TargetRate: 50, Intended: 100, Sent: 90, CouldNotSend: 10, Late: 5,
		ErrorsByClass: []loadgen.ClassCount{{"23", 4}}}
	s.Completed[loadgen.OutcomeOK] = 86
	s.Completed[loadgen.OutcomeDBError] = 4
	line := loadgen.StatusLine(2_500*ms, s, loadgen.Latencies{P50: 1_500_000, P99: 2_000_000_000})
	for _, want := range []string{"t=2.5s", "sent 90/100", "completed 90", "errors 23:4", "rate 42.5/50/s", "p50 1.5ms", "p99 2.0s"} {
		if !strings.Contains(line, want) {
			t.Errorf("status %q lacks %q", line, want)
		}
	}
	sum := strings.Join(loadgen.Summary(s, loadgen.Latencies{}, loadgen.Latencies{}), "\n")
	if !strings.Contains(sum, "SHORTFALL: achieved 42.5/s of target 50/s: 5 late, 10 could not send") {
		t.Fatalf("summary:\n%s", sum)
	}
}

func lateSendsQuickReplies(t *testing.T, lag int64) (loadgen.Plan, loadgen.Snapshot) {
	t.Helper()
	p := plan(10, 1000)
	p.Tenants = 1
	tr := tracker(t, p, loadgen.TrackerConfig{Conns: 1, MaxOutstanding: 16, LateAfterNs: 5 * ms})
	for _, d := range tr.Tick(p.DurationNs, nil) {
		sentAt := d.Op.At + lag
		if !tr.Sent(d.Conn, sentAt) || !tr.Completed(d.Conn, sentAt+ms, loadgen.Result{Outcome: loadgen.OutcomeOK}) {
			t.Fatal("send or completion refused")
		}
	}
	tr.Finish()
	s := tr.Snapshot()
	if err := s.Check(); err != nil {
		t.Fatal(err)
	}
	return p, s
}

// TestLatencyIsMeasuredFromTheIntendedSendTimeNotTheActualSend proves send lag
// appears in Snapshot.Latency while Snapshot.LatencyFromSend measures only reply time.
func TestLatencyIsMeasuredFromTheIntendedSendTimeNotTheActualSend(t *testing.T) {
	_, s := lateSendsQuickReplies(t, 400*ms)
	fromIntended := loadgen.SnapshotLatencies(s.Latency)
	fromSend := loadgen.SnapshotLatencies(s.LatencyFromSend)
	if s.Latency.Count != 10 || s.LatencyFromSend.Count != 10 {
		t.Fatalf("latency counts %d and %d, want 10", s.Latency.Count, s.LatencyFromSend.Count)
	}
	if fromIntended.P50 < 400*ms || fromIntended.Max < 401*ms {
		t.Fatalf("latency from intended send ignores the 400ms send lag: %+v", fromIntended)
	}
	if fromSend.Max > 2*ms {
		t.Fatalf("latency from actual send includes the send lag: %+v", fromSend)
	}
}

// TestLateSendsAloneAreAShortfall proves late sends with zero drops trigger
// Shortfall, lower achieved rate, and print SHORTFALL, while boundary sends do not.
func TestLateSendsAloneAreAShortfall(t *testing.T) {
	p, s := lateSendsQuickReplies(t, 6*ms)
	if s.CouldNotSend != 0 || s.Late != p.Count() {
		t.Fatalf("want every send late and none dropped: %+v", s)
	}
	if !s.Shortfall() {
		t.Fatal("late sends with nothing dropped are not a shortfall")
	}
	if loadgen.AchievedTenths(s) >= s.TargetRate*10 {
		t.Fatalf("achieved %d tenths of target %d/s", loadgen.AchievedTenths(s), s.TargetRate)
	}
	lines := strings.Join(loadgen.Summary(s, loadgen.Latencies{}, loadgen.Latencies{}), "\n")
	if !strings.Contains(lines, "SHORTFALL: achieved 0.0/s of target 10/s: 10 late, 0 could not send") {
		t.Fatalf("summary:\n%s", lines)
	}
	_, onTime := lateSendsQuickReplies(t, 5*ms)
	if onTime.Shortfall() || onTime.Late != 0 {
		t.Fatalf("sends exactly at the threshold are on time: %+v", onTime)
	}
}
