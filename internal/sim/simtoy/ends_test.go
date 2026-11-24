package simtoy

import (
	"testing"

	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
)

type endView struct {
	accepted  int64
	resets    int64
	serverEOF bool
	closed    simnet.ClosedSnapshot
	refused   int64
	stalled   int64
	discarded bool
}

func TestEachFaultAsSeenByBothEnds(t *testing.T) {
	want := map[string]endView{
		"echo-clean":         {accepted: 4, closed: simnet.ClosedSnapshot{Graceful: 4}},
		"echo-random-split":  {accepted: 4, closed: simnet.ClosedSnapshot{Graceful: 4}},
		"echo-chunk-1":       {accepted: 4, closed: simnet.ClosedSnapshot{Graceful: 4}},
		"echo-stall":         {accepted: 4, closed: simnet.ClosedSnapshot{Graceful: 4}},
		"echo-stall-timeout": {accepted: 4, closed: simnet.ClosedSnapshot{Graceful: 4}, discarded: true},
		"echo-reset":         {accepted: 4, resets: 1, closed: simnet.ClosedSnapshot{Graceful: 3, Reset: 1}, discarded: true},
		"echo-half-open":     {accepted: 4, closed: simnet.ClosedSnapshot{Graceful: 3, HalfOpen: 1}, discarded: true},
		"echo-half-close":    {accepted: 4, closed: simnet.ClosedSnapshot{Graceful: 4}, discarded: true},
		"echo-refuse":        {accepted: 1, closed: simnet.ClosedSnapshot{Graceful: 1}, refused: 3},
		"echo-dial-stall":    {accepted: 1, closed: simnet.ClosedSnapshot{Graceful: 1, Aborted: 3}, stalled: 3},
		"echo-silence":       {accepted: 1, closed: simnet.ClosedSnapshot{Graceful: 1, Silent: 3}, discarded: true},
	}
	for _, sc := range Scenarios() {
		w, ok := want[sc.Name]
		if !ok {
			t.Fatalf("scenario %s has no expected end view", sc.Name)
		}
		for seed := int64(1); seed <= 20; seed++ {
			res := sc.Run(seed)
			if err := sc.Check(res); err != nil {
				t.Fatalf("%s seed %d: %v", sc.Name, seed, err)
			}
			snap := res.Net.Snapshot()
			if res.Server.Accepted != w.accepted || res.Server.Resets != w.resets {
				t.Fatalf("%s seed %d: server accepted %d resets %d", sc.Name, seed, res.Server.Accepted, res.Server.Resets)
			}
			if snap.Closed != w.closed || snap.DialsRefused != w.refused || snap.DialsStalled != w.stalled {
				t.Fatalf("%s seed %d: closed %+v refused %d stalled %d", sc.Name, seed, snap.Closed, snap.DialsRefused, snap.DialsStalled)
			}
			if got := snap.Up.Discarded+snap.Down.Discarded > 0; got != w.discarded {
				t.Fatalf("%s seed %d: discarded up %d down %d", sc.Name, seed, snap.Up.Discarded, snap.Down.Discarded)
			}
			if snap.ConnsOpened != w.accepted+snap.Closed.Silent || snap.DialsAttempted != 4 {
				t.Fatalf("%s seed %d: opened %d dials %d", sc.Name, seed, snap.ConnsOpened, snap.DialsAttempted)
			}
		}
	}
}

func TestStallTimeoutServerSeesClientCloseOnlyAfterTheClear(t *testing.T) {
	sc, _ := Lookup("echo-stall-timeout")
	for seed := int64(1); seed <= 10; seed++ {
		cfg := sc.Config(seed)
		var closedAt, finAt sim.Time = -1, -1
		cfg.Tap = func(r sim.Record) {
			switch r.Kind.String() {
			case "net.cls":
				if r.A == 1 {
					closedAt = r.At
				}
			case "net.fin":
				if r.A == 1 && r.B == 0 && finAt < 0 {
					finAt = r.At
				}
			}
		}
		res := Run(cfg)
		if res.Outcomes[0] != OutcomeTimedOut {
			t.Fatalf("seed %d: %s", seed, res.Outcomes[0])
		}
		if finAt < longClear || closedAt < finAt {
			t.Fatalf("seed %d: the client's FIN reached the server at %d and the connection closed at %d; the clear was at %d", seed, finAt, closedAt, longClear)
		}
	}
}

func TestHalfOpenClientTimesOutOnItsOwnDeadline(t *testing.T) {
	for _, name := range []string{"echo-half-open", "echo-silence", "echo-dial-stall", "echo-stall-timeout"} {
		sc, _ := Lookup(name)
		for seed := int64(1); seed <= 10; seed++ {
			res := sc.Run(seed)
			start := sc.Config(seed).StartAt
			if int64(res.Finished[0]) != int64(start+DefaultDeadline) {
				t.Fatalf("%s seed %d: client 0 finished at %d, not at its deadline", name, seed, res.Finished[0])
			}
		}
	}
}

func TestOutcomeNames(t *testing.T) {
	for o := OutcomeNone; o <= OutcomeMismatch; o++ {
		if o.String() == "?" {
			t.Fatalf("outcome %d has no name", o)
		}
	}
	if Outcome(200).String() != "?" {
		t.Fatal("unknown outcome")
	}
	if _, ok := Lookup("nope"); ok {
		t.Fatal("lookup")
	}
}

func TestLinesLongerThanTheWindow(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		o := BaseOptions()
		o.Window = 64
		res := Run(Config{Seed: seed, Options: o, MaxLine: 300, Lines: 10})
		if res.Err != nil || res.Quiesce != nil {
			t.Fatalf("seed %d: %v %v", seed, res.Err, res.Quiesce)
		}
		for i, out := range res.Outcomes {
			if out != OutcomeCompleted {
				t.Fatalf("seed %d client %d: %s", seed, i, out)
			}
		}
		if res.Net.Snapshot().Up.WritesRefused == 0 && res.Net.Snapshot().Down.WritesRefused == 0 {
			t.Fatalf("seed %d: a 64-byte window never pushed back", seed)
		}
	}
}

func TestServerPendingEchoIsBounded(t *testing.T) {
	const seed, maxLine = 1, 4 * MaxPending
	first := 1 + sim.New(sim.Config{Seed: seed}).Stream("simtoy/client/0").IntN(maxLine)
	if first <= MaxPending {
		t.Fatalf("seed %d gives a first line of %d bytes, not above MaxPending", seed, first)
	}
	o := BaseOptions()
	o.Window = MaxPending
	o.Down.Latency.Base = 0
	res := Run(Config{Seed: seed, Options: o, Clients: 1, Lines: 1, MaxLine: maxLine, Deadline: 100 * DefaultDeadline})
	if res.Err != nil || res.Quiesce != nil {
		t.Fatal(res.Err, res.Quiesce)
	}
	if res.Outcomes[0] != OutcomeReset {
		t.Fatalf("outcome %s", res.Outcomes[0])
	}
	if r := res.Net.Snapshot().Closed.Reset; r != 1 {
		t.Fatalf("closed by reset %d", r)
	}
	if res.Server.Echoed != 0 || res.Server.Lines != 0 {
		t.Fatalf("echoed %d lines %d", res.Server.Echoed, res.Server.Lines)
	}
}
