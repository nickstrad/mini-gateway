package proxy_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/coalesce"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/pool"
	"github.com/nickstrad/mini-gateway/internal/proxy"
)

type refusalTiming uint8

const (
	refusedBeforeTheDeadline refusalTiming = iota
	refusedAtTheDeadline
	refusedAfterTheDeadline
)

var refusalTimings = []refusalTiming{refusedBeforeTheDeadline, refusedAtTheDeadline, refusedAfterTheDeadline}

func (w refusalTiming) String() string {
	return [...]string{"refused before the deadline", "refused at the deadline", "refused after the deadline"}[w]
}

type repair struct {
	*heldFlight
	opened map[proxy.ConnID]bool
}

func repairRig(t *testing.T, join coalesce.JoinMode, flight time.Duration, edit func(*proxy.Config)) *repair {
	t.Helper()
	r := fairRig(t, 2, func(c *proxy.Config) {
		coalesceOn(join)(c)
		retryOn(c)
		if flight != 0 {
			c.Coalesce.Deadline = coalesce.Instant(flight)
		}
		if edit != nil {
			edit(c)
		}
	})
	return &repair{heldFlight: &heldFlight{coreRig: r}, opened: map[proxy.ConnID]bool{}}
}

func (h *repair) openHolding(id proxy.ConnID) []proxy.Action {
	h.t.Helper()
	h.opened[id] = true
	a := h.runHoldingTheRead(h.ev(proxy.EvDBOpened, id, nil, 0))
	return append(a, h.runHoldingTheRead(h.ev(proxy.EvDBData, id, pgwire.AppendReadyForQuery(pgwire.AppendAuthentication(nil, 0, nil), 'I'), 0))...)
}

func (h *repair) dbHolding(id proxy.ConnID, b []byte) []proxy.Action {
	return h.runHoldingTheRead(h.ev(proxy.EvDBData, id, b, 0))
}

func (h *repair) qHolding(id proxy.ConnID, s string) []proxy.Action {
	return h.runHoldingTheRead(h.ev(proxy.EvClientData, id, query(s), 0))
}

func (h *repair) untilHeldRead(a []proxy.Action) (proxy.ConnID, []proxy.Action) {
	h.t.Helper()
	all := a
	for range 20 {
		if on := h.reads(a); len(on) == 1 {
			return on[0], all
		} else if len(on) > 1 {
			h.t.Fatalf("the read was sent on %v", on)
		}
		var next []proxy.Action
		for _, x := range a {
			if x.Kind == proxy.ActDBWrite && bytes.Equal(x.Data, query("")) {
				next = append(next, h.dbHolding(x.Conn, emptyReply())...)
			}
		}
		for _, id := range h.dbs {
			if !h.opened[id] {
				next = append(next, h.openHolding(id)...)
			}
		}
		if len(next) == 0 {
			h.now += proxy.Instant(2 * time.Millisecond)
			next = h.runHoldingTheRead(h.ev(proxy.EvTimer, 0, nil, 0))
		}
		all = append(all, next...)
		a = next
	}
	h.t.Fatal("the read was never sent")
	return 0, nil
}

func (h *repair) waiters() []proxy.ConnID {
	var ids []proxy.ConnID
	for _, c := range []proxy.ConnID{h.owner, h.follower, h.rider} {
		if c != 0 {
			ids = append(ids, c)
		}
	}
	return ids
}

func (h *repair) runningFlight() proxy.ConnID {
	h.t.Helper()
	h.owner, h.follower, h.rider = 10, 11, 12
	h.login(10, 0)
	h.login(11, 1)
	h.login(12, 2)
	h.arrived = h.now
	h.qHolding(10, sharedRead)
	h.qHolding(11, sharedRead)
	h.qHolding(12, sharedRead)
	if len(h.dbs) == 0 {
		h.t.Fatal("premise: no dial for the flight")
	}
	on := h.dbs[len(h.dbs)-1]
	if got := h.reads(h.openHolding(on)); len(got) != 1 || got[0] != on {
		h.t.Fatalf("premise: the flight's read was sent on %v", got)
	}
	if s := h.e.Snapshot(); s.CoalesceFollowed != 2 || s.Coalesced.Value != 2 {
		h.t.Fatalf("premise: followed %d coalesced %d", s.CoalesceFollowed, s.Coalesced.Value)
	}
	return on
}

func (h *repair) promotedFlight(hold time.Duration) proxy.ConnID {
	h.t.Helper()
	h.login(10, 0)
	h.login(11, 1)
	h.login(12, 2)
	h.qHolding(10, sharedRead)
	first := h.dbs[len(h.dbs)-1]
	if got := h.reads(h.openHolding(first)); len(got) != 1 {
		h.t.Fatalf("premise: the first flight's read was sent on %v", got)
	}
	h.now += proxy.Instant(10 * time.Millisecond)
	h.arrived = h.now
	h.owner, h.follower = 11, 12
	h.noRead(h.qHolding(11, sharedRead))
	h.noRead(h.qHolding(12, sharedRead))
	if cs := h.e.CoalesceSnapshot(); cs.Held != 1 || h.e.Snapshot().Coalesced.Value != 2 {
		h.t.Fatalf("premise: held %d coalesced %d", cs.Held, h.e.Snapshot().Coalesced.Value)
	}
	h.now = h.arrived + proxy.Instant(hold)
	a := h.dbHolding(first, readReply("x"))
	if got := written(a, proxy.ActClientWrite, 10); !bytes.Equal(got, readReply("x")) {
		h.t.Fatalf("premise: the first flight's owner got %q", got)
	}
	a = append(a, h.dbHolding(first, complete("DISCARD ALL", 'I'))...)
	on, all := h.untilHeldRead(a)
	h.silent(all)
	if s := h.e.Snapshot(); s.CoalescePromoted != 1 || s.Coalesced.Value != 1 {
		h.t.Fatalf("premise: promoted %d coalesced %d", s.CoalescePromoted, s.Coalesced.Value)
	}
	return on
}

func (h *repair) refuseAround(on proxy.ConnID, deadline proxy.Instant, w refusalTiming) []proxy.Action {
	h.t.Helper()
	var a []proxy.Action
	switch w {
	case refusedBeforeTheDeadline:
		h.now = deadline - 1
		a = h.run(h.ev(proxy.EvDBWriteRefused, on, nil, 0))
		h.now = deadline
		a = append(a, h.run(h.ev(proxy.EvDBClosed, on, nil, 0))...)
	case refusedAtTheDeadline:
		h.now = deadline
		a = h.refuseAndClose(on)
	default:
		h.now = deadline
		a = h.run(h.ev(proxy.EvTimer, 0, nil, 0))
		h.now = deadline + 1
		a = append(a, h.refuseAndClose(on)...)
	}
	return a
}

func (h *repair) oneSharedRow(a []proxy.Action) []byte {
	h.t.Helper()
	var first []byte
	for _, c := range h.waiters() {
		got := written(a, proxy.ActClientWrite, c)
		if first == nil {
			first = got
		}
		if len(got) == 0 || !bytes.Equal(got, first) || bytes.Count(got, readyIdle) != 1 {
			h.t.Fatalf("client %d got %q, the first waiter %q", c, got, first)
		}
	}
	return first
}

func (h *repair) detached() {
	h.t.Helper()
	s, _ := h.agree()
	cs := h.e.CoalesceSnapshot()
	if s.RetryAsked != 0 || s.RetryPending.Value != 0 || s.Coalesced.Value != 0 || cs.Flights.Value != 0 || cs.Waiting.Value != 0 || s.Queued.Value != 0 {
		t := h.t
		t.Fatalf("asked %d pending %d coalesced %d flights %d waiting %d queued %d", s.RetryAsked, s.RetryPending.Value, s.Coalesced.Value,
			cs.Flights.Value, cs.Waiting.Value, s.Queued.Value)
	}
	h.noRead(h.tick(10 * time.Millisecond))
}

func (h *repair) readAgain() {
	h.t.Helper()
	h.login(30, 0)
	h.owner, h.follower, h.rider = 30, 0, 0
	on, _ := h.untilHeldRead(h.qHolding(30, sharedRead))
	h.everyoneGot(h.db(on, readReply("again")), readReply("again"))
}

func TestRepairARunningFlightAtItsFlightDeadlineIsNeverOfferedARetry(t *testing.T) {
	for _, w := range refusalTimings {
		for _, join := range joinModes {
			h := repairRig(t, join, joinStatement/2, nil)
			on := h.runningFlight()
			deadline := h.arrived + proxy.Instant(joinStatement/2)
			a := h.refuseAround(on, deadline, w)
			h.oneSharedRow(a)
			if s := h.e.Snapshot(); s.RetryOffered != 0 || s.CoalesceExpired != 1 || s.CoalesceErrors != 2 {
				t.Fatalf("%v %v: offered %d expired %d errors %d", w, join, s.RetryOffered, s.CoalesceExpired, s.CoalesceErrors)
			}
			h.detached()
			h.readAgain()
		}
	}
}

func TestRepairAPromotedHeldFlightAtItsFlightDeadlineIsNeverOfferedARetry(t *testing.T) {
	for _, flight := range []time.Duration{joinStatement / 2, joinStatement} {
		for _, w := range refusalTimings {
			h := repairRig(t, coalesce.JoinStrict, flight, nil)
			on := h.promotedFlight(flight / 2)
			a := h.refuseAround(on, h.arrived+proxy.Instant(flight), w)
			h.oneSharedRow(a)
			if s := h.e.Snapshot(); s.RetryOffered != 0 || s.CoalesceExpired != 1 || s.CoalesceErrors != 1 {
				t.Fatalf("flight %v %v: offered %d expired %d errors %d", flight, w, s.RetryOffered, s.CoalesceExpired, s.CoalesceErrors)
			}
			h.detached()
			h.readAgain()
		}
	}
}

func TestRepairAPromotedHeldFlightIsNeverRetriedAtOrPastItsArrivalPlusTheStatementTimeout(t *testing.T) {
	for _, flight := range []time.Duration{joinStatement / 2, joinStatement, 2 * joinStatement} {
		for _, past := range []time.Duration{0, 50 * time.Millisecond} {
			h := repairRig(t, coalesce.JoinStrict, flight, nil)
			on := h.promotedFlight(min(flight, joinStatement) * 3 / 4)
			h.now = h.arrived + proxy.Instant(joinStatement+past)
			a := h.run(h.ev(proxy.EvTimer, 0, nil, 0))
			a = append(a, h.refuseAndClose(on)...)
			h.oneSharedRow(a)
			if s, _ := h.agree(); s.RetryAsked != 0 || s.RetryPending.Value != 0 {
				t.Fatalf("flight %v, %v past the request deadline: asked %d pending %d", flight, past, s.RetryAsked, s.RetryPending.Value)
			}
			h.noRead(h.tick(10 * time.Millisecond))
		}
	}
}

func TestRepairARetryOfAPromotedHeldFlightEndsAtItsArrivalPlusTheStatementTimeout(t *testing.T) {
	for _, flight := range []time.Duration{joinStatement, 2 * joinStatement} {
		h := repairRig(t, coalesce.JoinStrict, flight, nil)
		on := h.promotedFlight(joinStatement * 3 / 4)
		h.now = h.arrived + proxy.Instant(joinStatement*7/8)
		again, all := h.untilHeldRead(h.refuseAndClose(on))
		h.silent(all)
		if s, _ := h.agree(); s.RetryAsked != 1 || s.RetryPending.Value != 1 {
			t.Fatalf("flight %v: asked %d pending %d", flight, s.RetryAsked, s.RetryPending.Value)
		}
		end := h.arrived + proxy.Instant(joinStatement)
		h.now = end - 1
		h.silent(h.run(h.ev(proxy.EvTimer, 0, nil, 0)))
		h.now = end
		a := h.run(h.ev(proxy.EvTimer, 0, nil, 0))
		h.oneSharedRow(a)
		if s, _ := h.agree(); s.RetryPending.Value != 0 || s.RetryEnds[proxy.RetryEndFailed] != 1 || s.Coalesced.Value != 0 {
			t.Fatalf("flight %v: pending %d ends %v coalesced %d", flight, s.RetryPending.Value, s.RetryEnds, s.Coalesced.Value)
		}
		h.silent(h.db(again, readReply("late")))
	}
}

func TestRepairAfterAHandoffTheLeadersArrivalBoundsTheRetrySoNoFollowerGetsAStatementSentPastItsDeadline(t *testing.T) {
	const followerLater = 100 * time.Millisecond
	for _, opened := range []bool{false, true} {
		for _, join := range joinModes {
			h := repairRig(t, join, 2*joinStatement, nil)
			h.owner, h.follower = 10, 11
			h.login(10, 0)
			h.login(11, 1)
			h.arrived = h.now
			h.qHolding(10, sharedRead)
			h.now += proxy.Instant(followerLater)
			followerDeadline := h.now + proxy.Instant(joinStatement)
			h.qHolding(11, sharedRead)
			on := h.dbs[len(h.dbs)-1]
			if got := h.reads(h.openHolding(on)); len(got) != 1 {
				t.Fatalf("premise: the read was sent on %v", got)
			}
			h.run(h.ev(proxy.EvClientClosed, 10, nil, 0))
			h.owner = 0
			if s := h.e.Snapshot(); s.CoalesceHandoffs != 1 {
				t.Fatalf("premise: handoffs %d", s.CoalesceHandoffs)
			}
			leaderDeadline := h.arrived + proxy.Instant(joinStatement)
			h.now = h.arrived + proxy.Instant(joinStatement*3/4)
			a := h.refuseAndClose(on)
			h.noRead(a)
			h.silent(a)
			if s, _ := h.agree(); s.RetryAsked != 1 || s.RetryPending.Value != 1 {
				t.Fatalf("%v: asked %d pending %d", join, s.RetryAsked, s.RetryPending.Value)
			}
			dial := h.dbs[len(h.dbs)-1]
			if opened {
				again, all := h.untilHeldRead(h.openHolding(dial))
				if h.now >= leaderDeadline || h.now >= followerDeadline {
					t.Fatalf("%v: the retry was sent at %d, the leader's deadline %d", join, h.now, leaderDeadline)
				}
				h.silent(all)
				h.everyoneGot(h.db(again, readReply("y")), readReply("y"))
				continue
			}
			h.now = leaderDeadline - 1
			h.silent(h.run(h.ev(proxy.EvTimer, 0, nil, 0)))
			h.now = leaderDeadline
			a = h.run(h.ev(proxy.EvTimer, 0, nil, 0))
			h.everyoneShed(a)
			if s, _ := h.agree(); s.RetryEnds[proxy.RetryEndShed] != 1 || s.RetryPending.Value != 0 || s.Queued.Value != 0 {
				t.Fatalf("%v: ends %v pending %d queued %d", join, s.RetryEnds, s.RetryPending.Value, s.Queued.Value)
			}
			h.now = followerDeadline
			h.noRead(h.open(dial))
			h.silent(h.tick(10 * time.Millisecond))
		}
	}
}

func (h *heldFlight) heldBehindTheRetry() {
	h.t.Helper()
	h.login(18, 2)
	h.login(19, 1)
	h.noRead(h.coreRig.q(18, sharedRead))
	h.noRead(h.coreRig.q(19, sharedRead))
	h.owner, h.follower, h.rider = 14, 15, 16
	h.agree()
}

func (h *heldFlight) everyoneRefusedByTheBreaker(a []proxy.Action, clients ...proxy.ConnID) {
	h.t.Helper()
	for _, c := range clients {
		if got := written(a, proxy.ActClientWrite, c); !bytes.Contains(got, []byte("53M03")) || bytes.Count(got, readyIdle) != 1 {
			h.t.Fatalf("client %d got %q, want one 53M03", c, got)
		}
	}
	s, _ := h.agree()
	cs := h.e.CoalesceSnapshot()
	if s.Queued.Value != 0 || s.Coalesced.Value != 0 || cs.Flights.Value != 0 || s.RetryPending.Value != 0 {
		h.t.Fatalf("queued %d coalesced %d flights %d pending %d", s.Queued.Value, s.Coalesced.Value, cs.Flights.Value, s.RetryPending.Value)
	}
}

func TestRepairAHeldFlightPromotedBehindARetryTheCustomBreakerRefusesIsRefusedWithAllItsWaiters(t *testing.T) {
	for _, join := range joinModes {
		b := &switchBreaker{}
		h := heldRetriedFlight(t, join, func(c *proxy.Config) { c.Seams.Breaker = b })
		h.heldBehindTheRetry()
		if cs := h.e.CoalesceSnapshot(); join == coalesce.JoinStrict && cs.Held != 1 {
			t.Fatalf("strict: held %d", cs.Held)
		}
		b.open = true
		a := h.commit(17, h.other)
		h.noRead(a)
		h.everyoneRefusedByTheBreaker(a, 14, 15, 16, 18, 19)
		if s := h.e.Snapshot(); join == coalesce.JoinStrict && (s.CoalescePromotedRefused != 1 || s.CoalescePromoted != 1) {
			t.Fatalf("strict: promoted %d refused at promotion %d", s.CoalescePromoted, s.CoalescePromotedRefused)
		}
		h.noRead(h.tick(10 * time.Millisecond))
	}
}

func TestRepairAHeldFlightPromotedByThePoolBreakerFlushIsRefusedAndTheFlushLeavesNothingQueued(t *testing.T) {
	for _, join := range joinModes {
		h := heldRetriedFlight(t, join, func(c *proxy.Config) { c.Pool.BreakerFailures = 2 })
		h.heldBehindTheRetry()
		h.login(20, 2)
		h.coreRig.q(20, "SELECT 20")
		dials := h.dials()
		h.noRead(h.tick(2 * time.Millisecond))
		if h.dials() != dials+1 {
			t.Fatalf("%v: premise: %d dials for tenant 2's read", join, h.dials()-dials)
		}
		a := h.run(h.ev(proxy.EvDBDialFailed, h.dbs[len(h.dbs)-1], nil, 0))
		if h.pool().Breaker != int64(pool.BreakerOpen) {
			t.Fatalf("%v: premise: breaker %d", join, h.pool().Breaker)
		}
		h.noRead(a)
		h.everyoneRefusedByTheBreaker(a, 14, 15, 16, 18, 19, 20)
		h.noRead(h.tick(10 * time.Millisecond))
	}
}
