package proxy_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/coalesce"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/proxy/proxysim"
)

const (
	joinStatement   = 400 * time.Millisecond
	commitAndFlight = 2
)

var joinModes = []coalesce.JoinMode{coalesce.JoinStrict, coalesce.JoinRelaxed}

type heldFlight struct {
	*coreRig
	q                      *admission.Queues
	first, second, other   proxy.ConnID
	spare                  proxy.ConnID
	owner, follower, rider proxy.ConnID
	arrived                proxy.Instant
	dialsAtPremise         int
}

func coalesceOn(join coalesce.JoinMode) func(*proxy.Config) {
	return func(c *proxy.Config) {
		c.Coalesce = coalesce.Config{Mode: coalesce.ModeAuto, Join: join}
		c.Timeouts.Statement = joinStatement
		c.Timeouts.QueueWait = 3 * time.Second
	}
}

func heldFlightPremise(t *testing.T, join coalesce.JoinMode, edit func(*proxy.Config)) *heldFlight {
	t.Helper()
	h := &heldFlight{owner: 14, follower: 15, rider: 16}
	h.coreRig = fairRig(t, 4, func(c *proxy.Config) {
		coalesceOn(join)(c)
		if edit != nil {
			edit(c)
		}
		h.q = seamFair(t, c)
	})
	r := h.coreRig
	h.first = r.heldTx(10, 0)
	h.second = r.heldTx(11, 0)
	h.other = r.heldTx(12, 1)
	h.spare = r.idleConns(1, 13)[0]
	if tenantInFlight(r, 0) != 2 || tenantInFlight(r, 1) != 1 || r.pool().Idle.Value != 1 {
		t.Fatalf("premise: in flight %d / %d, idle %d", tenantInFlight(r, 0), tenantInFlight(r, 1), r.pool().Idle.Value)
	}
	r.login(h.owner, 0)
	r.login(h.follower, 1)
	r.login(h.rider, 2)
	h.arrived = r.now
	a := r.q(h.owner, sharedRead)
	a = append(a, r.q(h.follower, sharedRead)...)
	a = append(a, r.q(h.rider, sharedRead)...)
	h.noRead(a)
	s, as := r.agree()
	if s.CoalesceLed != 2 || s.CoalesceFollowed != 2 || s.Coalesced.Value != 2 || as.Fair.HeldShare == 0 {
		t.Fatalf("premise: led %d followed %d coalesced %d held at a share %d", s.CoalesceLed, s.CoalesceFollowed, s.Coalesced.Value, as.Fair.HeldShare)
	}
	if r.queuedOf(0) != 1 || r.queuedOf(1) != 0 || r.queuedOf(2) != 0 || h.q.Releasable() != 0 {
		t.Fatalf("premise: queued %d / %d / %d, releasable %d", r.queuedOf(0), r.queuedOf(1), r.queuedOf(2), h.q.Releasable())
	}
	if tenantInFlight(r, 0) != 2 || tenantInFlight(r, 1) != 1 || tenantInFlight(r, 2) != 0 {
		t.Fatalf("followers took a lease: in flight %d / %d / %d", tenantInFlight(r, 0), tenantInFlight(r, 1), tenantInFlight(r, 2))
	}
	h.dialsAtPremise = r.dials()
	return h
}

func (h *heldFlight) reads(a []proxy.Action) []proxy.ConnID {
	var on []proxy.ConnID
	for _, x := range a {
		if x.Kind == proxy.ActDBWrite && bytes.Contains(x.Data, query(sharedRead)) {
			on = append(on, x.Conn)
		}
	}
	return on
}

func (h *heldFlight) noRead(a []proxy.Action) {
	h.t.Helper()
	if on := h.reads(a); len(on) != 0 {
		h.t.Fatalf("the held flight's read was sent on %v", on)
	}
}

func (h *heldFlight) runHoldingTheRead(a []proxy.Action) []proxy.Action {
	h.t.Helper()
	all := a
	for _, x := range a {
		switch {
		case x.Kind == proxy.ActDBWrite && bytes.Contains(x.Data, query(sharedRead)):
		case x.Kind == proxy.ActClientWrite:
			all = append(all, h.runHoldingTheRead(h.ev(proxy.EvClientWrote, x.Conn, nil, len(x.Data)))...)
		case x.Kind == proxy.ActDBWrite:
			all = append(all, h.runHoldingTheRead(h.ev(proxy.EvDBWrote, x.Conn, nil, len(x.Data)))...)
		case x.Kind == proxy.ActDBOpen:
			h.dbs = append(h.dbs, x.Conn)
		case x.Kind == proxy.ActDBCancel:
			h.cancels = append(h.cancels, x.Conn)
		}
	}
	return all
}

func (h *heldFlight) release() (proxy.ConnID, []proxy.Action) {
	h.t.Helper()
	a := h.runHoldingTheRead(h.ev(proxy.EvClientData, 12, query("COMMIT"), 0))
	a = append(a, h.runHoldingTheRead(h.ev(proxy.EvDBData, h.other, complete("COMMIT", 'I'), 0))...)
	a = append(a, h.runHoldingTheRead(h.ev(proxy.EvDBData, h.other, complete("DISCARD ALL", 'I'), 0))...)
	if bytes.HasSuffix(written(a, proxy.ActDBWrite, h.other), query("")) {
		a = append(a, h.runHoldingTheRead(h.ev(proxy.EvDBData, h.other, emptyReply(), 0))...)
	}
	on := h.reads(a)
	if len(on) != 1 {
		h.t.Fatalf("tenant 1's lease ended: the flight must be sent once, sent on %v", on)
	}
	return on[0], a
}

func (h *heldFlight) everyoneGot(a []proxy.Action, want []byte) {
	h.t.Helper()
	for _, c := range []proxy.ConnID{h.owner, h.follower, h.rider} {
		if c == 0 {
			continue
		}
		if got := written(a, proxy.ActClientWrite, c); !bytes.Equal(got, want) {
			h.t.Fatalf("client %d got %q, want %q", c, got, want)
		}
	}
}

func (h *heldFlight) everyoneShed(a []proxy.Action) {
	h.t.Helper()
	for _, c := range []proxy.ConnID{h.owner, h.follower, h.rider} {
		if c != 0 && !shedReply(written(a, proxy.ActClientWrite, c)) {
			h.t.Fatalf("client %d got %q, want one 53M01", c, written(a, proxy.ActClientWrite, c))
		}
	}
}

func TestJoinFollowersOfOtherTenantsRideAFlightWithNoQueuePlaceAndNoLease(t *testing.T) {
	for _, join := range joinModes {
		h := heldFlightPremise(t, join, nil)
		for range 5 {
			h.noRead(h.tick(10 * time.Millisecond))
		}
		if h.dials() != h.dialsAtPremise || h.q.Releasable() != 0 {
			t.Fatalf("%v: a flight held at its owner's share was demand: %d dials, releasable %d", join, h.dials()-h.dialsAtPremise, h.q.Releasable())
		}
		on, _ := h.release()
		if tenantInFlight(h.coreRig, 0) != 3 || tenantInFlight(h.coreRig, 1) != 0 || tenantInFlight(h.coreRig, 2) != 0 {
			t.Fatalf("%v: the flight's lease is not counted once, for its owner's tenant: %d / %d / %d", join,
				tenantInFlight(h.coreRig, 0), tenantInFlight(h.coreRig, 1), tenantInFlight(h.coreRig, 2))
		}
		if s, _ := h.agree(); s.Coalesced.Value != 2 || h.q.Releasable() != 0 {
			t.Fatalf("%v: coalesced %d releasable %d", join, s.Coalesced.Value, h.q.Releasable())
		}
		a := h.db(on, readReply("x"))
		h.everyoneGot(a, readReply("x"))
		h.finishReset(on)
		s, as := h.agree()
		if tenantInFlight(h.coreRig, 0) != 2 || as.Fair.LeaseStarts != s.LeasesStarted || as.Fair.LeaseEnds != s.LeasesEnded {
			t.Fatalf("%v: in flight %d, fair starts %d ends %d, engine %d %d", join, tenantInFlight(h.coreRig, 0),
				as.Fair.LeaseStarts, as.Fair.LeaseEnds, s.LeasesStarted, s.LeasesEnded)
		}
		for _, row := range as.PerTenant {
			if row.Tenant == proxysim.TenantName(2) && row.Pushes != 0 {
				t.Fatalf("%v: tenant 2 only rode the flight but pushed %d", join, row.Pushes)
			}
		}
	}
}

func TestJoinAQueuedOwnerHandoffAcrossTenantsUnderABindingShareKeepsTheChargeOnTheQueuedTenant(t *testing.T) {
	for _, join := range joinModes {
		h := heldFlightPremise(t, join, nil)
		h.ev(proxy.EvClientClosed, h.owner, nil, 0)
		h.owner = 0
		s, _ := h.agree()
		if s.CoalesceHandoffs != 1 || s.Coalesced.Value != 1 || h.queuedOf(0) != 1 || h.queuedOf(2) != 0 || h.q.Releasable() != 0 {
			t.Fatalf("%v: handoffs %d coalesced %d queued %d / %d releasable %d", join, s.CoalesceHandoffs, s.Coalesced.Value,
				h.queuedOf(0), h.queuedOf(2), h.q.Releasable())
		}
		h.noRead(h.tick(10 * time.Millisecond))
		on, _ := h.release()
		if tenantInFlight(h.coreRig, 0) != 3 || tenantInFlight(h.coreRig, 2) != 0 {
			t.Fatalf("%v: the handed-off flight's lease went to %d / %d (tenant 0 / tenant 2), want it on tenant 0 whose queue it left",
				join, tenantInFlight(h.coreRig, 0), tenantInFlight(h.coreRig, 2))
		}
		h.agree()
		a := h.db(on, readReply("x"))
		h.everyoneGot(a, readReply("x"))
		h.finishReset(on)
		if s, as := h.agree(); tenantInFlight(h.coreRig, 0) != 2 || as.Fair.InFlight.Value != 2 || s.Coalesced.Value != 0 {
			t.Fatalf("%v: in flight %d total %d coalesced %d", join, tenantInFlight(h.coreRig, 0), as.Fair.InFlight.Value, s.Coalesced.Value)
		}
	}
}

func TestJoinARunningOwnerHandoffAcrossTenantsEndsTheLeaseOnTheTenantItWasLentTo(t *testing.T) {
	for _, join := range joinModes {
		h := heldFlightPremise(t, join, nil)
		on, _ := h.release()
		if a := h.ev(proxy.EvClientClosed, h.owner, nil, 0); did(a, proxy.ActDBClose, on) || did(a, proxy.ActDBCancel, on) {
			t.Fatalf("%v: the shared read was abandoned while waiters remained", join)
		}
		h.owner = 0
		if lent, ok := h.e.LentTo(on); !ok || lent != h.rider {
			t.Fatalf("%v: lent to %d, want the newest waiter %d", join, lent, h.rider)
		}
		if tenantInFlight(h.coreRig, 0) != 3 || tenantInFlight(h.coreRig, 2) != 0 {
			t.Fatalf("%v: in flight %d / %d after the handoff", join, tenantInFlight(h.coreRig, 0), tenantInFlight(h.coreRig, 2))
		}
		h.agree()
		a := h.db(on, readReply("x"))
		h.everyoneGot(a, readReply("x"))
		h.finishReset(on)
		s, as := h.agree()
		if tenantInFlight(h.coreRig, 0) != 2 || tenantInFlight(h.coreRig, 2) != 0 || as.Fair.LeaseEnds != s.LeasesEnded {
			t.Fatalf("%v: after the lease ended: in flight %d / %d, fair ends %d engine %d", join,
				tenantInFlight(h.coreRig, 0), tenantInFlight(h.coreRig, 2), as.Fair.LeaseEnds, s.LeasesEnded)
		}
	}
}

func TestJoinAFlightHeldBackByTheDispatcherEndsAtItsDeadlineWithOneSharedError(t *testing.T) {
	for _, join := range joinModes {
		h := heldFlightPremise(t, join, nil)
		deadline := h.arrived + proxy.Instant(joinStatement)
		h.now = deadline - 1
		h.noRead(h.run(h.ev(proxy.EvTimer, 0, nil, 0)))
		if s, _ := h.agree(); s.Coalesced.Value != 2 || h.queuedOf(0) != 1 {
			t.Fatalf("%v: before the deadline: coalesced %d queued %d", join, s.Coalesced.Value, h.queuedOf(0))
		}
		h.now = deadline
		a := h.run(h.ev(proxy.EvTimer, 0, nil, 0))
		h.noRead(a)
		h.everyoneShed(a)
		s, as := h.agree()
		if s.CoalesceExpired != 1 || s.Shed != 1 || s.Coalesced.Value != 0 || h.queuedOf(0) != 0 || h.q.Releasable() != 0 {
			t.Fatalf("%v: expired %d shed %d coalesced %d queued %d releasable %d", join, s.CoalesceExpired, s.Shed, s.Coalesced.Value,
				h.queuedOf(0), h.q.Releasable())
		}
		if as.Shed[admission.ReasonDeadline] != 1 {
			t.Fatalf("%v: admission sheds %v, want one deadline shed", join, as.Shed)
		}
		h.noRead(h.commit(12, h.other))
		if h.dials() != h.dialsAtPremise {
			t.Fatalf("%v: %d dials for an expired flight", join, h.dials()-h.dialsAtPremise)
		}
	}
}

func (h *heldFlight) untilRead(a []proxy.Action) (proxy.ConnID, []proxy.Action) {
	h.t.Helper()
	all := a
	opened := map[proxy.ConnID]bool{}
	for range 20 {
		if on := h.reads(a); len(on) == 1 {
			return on[0], all
		} else if len(on) > 1 {
			h.t.Fatalf("the read was sent on %v", on)
		}
		var next []proxy.Action
		for _, x := range a {
			switch {
			case x.Kind == proxy.ActDBWrite && bytes.Equal(x.Data, query("")):
				next = append(next, h.db(x.Conn, emptyReply())...)
			case x.Kind == proxy.ActDBOpen && !opened[x.Conn]:
				opened[x.Conn] = true
				next = append(next, h.open(x.Conn)...)
			}
		}
		if len(next) == 0 {
			next = h.tick(2 * time.Millisecond)
		}
		all = append(all, next...)
		a = next
	}
	h.t.Fatal("the read was never sent")
	return 0, nil
}

func (h *heldFlight) silent(a []proxy.Action) {
	h.t.Helper()
	for _, c := range []proxy.ConnID{h.owner, h.follower, h.rider} {
		if got := written(a, proxy.ActClientWrite, c); c != 0 && len(got) != 0 {
			h.t.Fatalf("client %d was answered before the flight ended: %q", c, got)
		}
	}
}

func TestJoinALeaderRefusedWithNothingReachedIsRetriedOnceForTheWholeFlight(t *testing.T) {
	for _, join := range joinModes {
		h := heldFlightPremise(t, join, retryOn)
		attempts, sent := h.retrySnap().Attempts, h.e.CoalesceSnapshot().Sent
		on, _ := h.release()
		if got := h.retrySnap().Attempts - attempts; got != commitAndFlight {
			t.Fatalf("%v: the COMMIT and a flight with three waiters deposited %d first attempts, want %d", join, got, commitAndFlight)
		}
		attempts += commitAndFlight
		a := h.refuseAndClose(on)
		h.silent(a)
		s, _ := h.agree()
		if s.RetryOffered != 1 || s.RetryAsked != 1 || s.RetryPending.Value != 1 || s.Coalesced.Value != 2 {
			t.Fatalf("%v: offered %d asked %d pending %d coalesced %d", join, s.RetryOffered, s.RetryAsked, s.RetryPending.Value, s.Coalesced.Value)
		}
		again, b := h.untilRead(a)
		h.silent(b)
		if got := h.retrySnap(); got.Attempts != attempts || got.Retries != 1 || got.Offered != 1 {
			t.Fatalf("%v: after the retry was sent: attempts %d retries %d offered %d", join, got.Attempts-attempts, got.Retries, got.Offered)
		}
		if tenantInFlight(h.coreRig, 0) != 3 {
			t.Fatalf("%v: the retried flight's lease: tenant 0 in flight %d", join, tenantInFlight(h.coreRig, 0))
		}
		h.agree()
		a = h.db(again, readReply("y"))
		h.everyoneGot(a, readReply("y"))
		h.finishReset(again)
		s, _ = h.agree()
		cs := h.e.CoalesceSnapshot()
		if s.RetryEnds[proxy.RetryEndCompleted] != 1 || s.RetryPending.Value != 0 || s.CoalesceReplies != 2 || s.CoalesceErrors != 0 || cs.Sent-sent != 1 {
			t.Fatalf("%v: ends %v pending %d replies %d errors %d sent %d", join, s.RetryEnds, s.RetryPending.Value, s.CoalesceReplies, s.CoalesceErrors, cs.Sent-sent)
		}
	}
}

func TestJoinARetriedFlightLostAgainIsAnsweredOnceWithOneSharedError(t *testing.T) {
	for _, join := range joinModes {
		h := heldFlightPremise(t, join, retryOn)
		on, _ := h.release()
		again, _ := h.untilRead(h.refuseAndClose(on))
		a := h.refuseAndClose(again)
		var first []byte
		for _, c := range []proxy.ConnID{h.owner, h.follower, h.rider} {
			got := written(a, proxy.ActClientWrite, c)
			if first == nil {
				first = got
			}
			if !bytes.Equal(got, first) || bytes.Count(got, readyIdle) != 1 || len(got) == 0 {
				t.Fatalf("%v: client %d got %q, the owner %q", join, c, got, first)
			}
		}
		s, _ := h.agree()
		if s.RetryOffered != 2 || s.RetryAsked != 1 || s.RetryEnds[proxy.RetryEndFailed] != 1 || s.CoalesceErrors != 2 || s.Coalesced.Value != 0 {
			t.Fatalf("%v: offered %d asked %d ends %v errors %d coalesced %d", join, s.RetryOffered, s.RetryAsked, s.RetryEnds, s.CoalesceErrors, s.Coalesced.Value)
		}
		h.noRead(h.tick(10 * time.Millisecond))
	}
}

func TestJoinWithTheRetryPolicyOffALeaderRefusedSharesItsErrorAtOnce(t *testing.T) {
	for _, join := range joinModes {
		h := heldFlightPremise(t, join, nil)
		on, _ := h.release()
		a := h.refuseAndClose(on)
		owner := written(a, proxy.ActClientWrite, h.owner)
		if len(owner) == 0 {
			t.Fatalf("%v: the owner was not answered", join)
		}
		h.everyoneGot(a, owner)
		if s, _ := h.agree(); s.RetryAsked != 0 || s.RetryOffered != 1 || s.CoalesceErrors != 2 {
			t.Fatalf("%v: asked %d offered %d errors %d", join, s.RetryAsked, s.RetryOffered, s.CoalesceErrors)
		}
	}
}

func heldRetriedFlight(t *testing.T, join coalesce.JoinMode, edit func(*proxy.Config)) *heldFlight {
	t.Helper()
	h := heldFlightPremise(t, join, func(c *proxy.Config) {
		retryOn(c)
		if edit != nil {
			edit(c)
		}
	})
	on, _ := h.release()
	h.other = h.heldTx(17, 1)
	a := h.refuseAndClose(on)
	h.noRead(a)
	h.silent(a)
	s, as := h.agree()
	if s.RetryPending.Value != 1 || h.queuedOf(0) != 1 || s.Coalesced.Value != 2 || h.q.Releasable() != 0 || as.Fair.HeldShare == 0 {
		t.Fatalf("premise: pending %d queued %d coalesced %d releasable %d held %d", s.RetryPending.Value, h.queuedOf(0), s.Coalesced.Value,
			h.q.Releasable(), as.Fair.HeldShare)
	}
	if tenantInFlight(h.coreRig, 0) != 2 || tenantInFlight(h.coreRig, 1) != 1 {
		t.Fatalf("premise: in flight %d / %d", tenantInFlight(h.coreRig, 0), tenantInFlight(h.coreRig, 1))
	}
	return h
}

func TestJoinARetriedFlightOwnerHeldInTheQueueEndsAtTheEarlierOfTheFlightAndRequestDeadlines(t *testing.T) {
	for _, arm := range []struct {
		name   string
		flight time.Duration
		ends   time.Duration
	}{
		{"flight deadline first", joinStatement / 2, joinStatement / 2},
		{"request deadline first", 2 * joinStatement, joinStatement},
	} {
		for _, join := range joinModes {
			h := heldRetriedFlight(t, join, func(c *proxy.Config) { c.Coalesce.Deadline = coalesce.Instant(arm.flight) })
			end := h.arrived + proxy.Instant(arm.ends)
			if h.now >= end {
				t.Fatalf("%s %v: premise ran past the deadline", arm.name, join)
			}
			h.now = end - 1
			a := h.run(h.ev(proxy.EvTimer, 0, nil, 0))
			h.noRead(a)
			h.silent(a)
			h.now = end
			a = h.run(h.ev(proxy.EvTimer, 0, nil, 0))
			h.noRead(a)
			h.everyoneShed(a)
			s, as := h.agree()
			if s.RetryEnds[proxy.RetryEndShed] != 1 || s.RetryPending.Value != 0 || s.Coalesced.Value != 0 || h.queuedOf(0) != 0 {
				t.Fatalf("%s %v: ends %v pending %d coalesced %d queued %d", arm.name, join, s.RetryEnds, s.RetryPending.Value, s.Coalesced.Value, h.queuedOf(0))
			}
			if as.Shed[admission.ReasonDeadline] != 1 || s.Shed != 1 {
				t.Fatalf("%s %v: admission sheds %v engine %d", arm.name, join, as.Shed, s.Shed)
			}
			h.silent(h.noReadAfter(h.commit(17, h.other)))
		}
	}
}

func (h *heldFlight) noReadAfter(a []proxy.Action) []proxy.Action {
	h.t.Helper()
	h.noRead(a)
	return a
}

func TestJoinTheBreakerReCheckRunsForARetriedFlightOwnerAtTheLendThatWouldTakeIt(t *testing.T) {
	for _, open := range []bool{false, true} {
		for _, join := range joinModes {
			b := &switchBreaker{}
			h := heldRetriedFlight(t, join, func(c *proxy.Config) { c.Seams.Breaker = b })
			b.open = open
			a := h.commit(17, h.other)
			if !open {
				on, all := h.untilRead(a)
				h.silent(all)
				a = h.db(on, readReply("z"))
				h.everyoneGot(a, readReply("z"))
				if s, _ := h.agree(); s.RetryEnds[proxy.RetryEndCompleted] != 1 || s.RetryPending.Value != 0 {
					t.Fatalf("closed %v: ends %v pending %d", join, s.RetryEnds, s.RetryPending.Value)
				}
				continue
			}
			h.noRead(a)
			for _, c := range []proxy.ConnID{h.owner, h.follower, h.rider} {
				if got := written(a, proxy.ActClientWrite, c); !bytes.Contains(got, []byte("53M03")) || bytes.Count(got, readyIdle) != 1 {
					t.Fatalf("open %v: client %d got %q, want one 53M03", join, c, got)
				}
			}
			s, _ := h.agree()
			if s.RetryEnds[proxy.RetryEndBreaker] != 1 || s.RetryPending.Value != 0 || s.Coalesced.Value != 0 || s.CoalesceErrors != 2 {
				t.Fatalf("open %v: ends %v pending %d coalesced %d errors %d", join, s.RetryEnds, s.RetryPending.Value, s.Coalesced.Value, s.CoalesceErrors)
			}
			h.noRead(h.tick(10 * time.Millisecond))
		}
	}
}

func TestJoinShutdownWithARetryPendingAndFlightsHeldSendsNothingNewToTheDatabase(t *testing.T) {
	for _, join := range joinModes {
		h := heldRetriedFlight(t, join, nil)
		h.login(18, 2)
		h.noRead(h.coreRig.q(18, sharedRead))
		cs := h.e.CoalesceSnapshot()
		if join == coalesce.JoinStrict && cs.Held != 1 {
			t.Fatalf("strict: a read arriving behind the retried flight was not held: held %d", cs.Held)
		}
		h.agree()
		for _, x := range h.e.Shutdown(h.now, nil) {
			if x.Kind == proxy.ActDBWrite && !bytes.Equal(x.Data, query("ROLLBACK")) {
				t.Fatalf("%v: shutdown sent a statement to database %d: %q", join, x.Conn, x.Data)
			}
		}
		s, as := h.e.Snapshot(), h.e.AdmissionSnapshot()
		if s.RetryEnds[proxy.RetryEndGone] != 1 || s.RetryPending.Value != 0 || s.Coalesced.Value != 0 || s.Queued.Value != 0 || s.InFlight.Value != 0 {
			t.Fatalf("%v: after shutdown: ends %v pending %d coalesced %d queued %d in flight %d", join, s.RetryEnds, s.RetryPending.Value,
				s.Coalesced.Value, s.Queued.Value, s.InFlight.Value)
		}
		if as.Fair.InFlight.Value != 0 || as.Fair.LeaseStarts != s.LeasesStarted || as.Fair.LeaseEnds != s.LeasesEnded {
			t.Fatalf("%v: fair in flight %d starts %d/%d ends %d/%d", join, as.Fair.InFlight.Value, as.Fair.LeaseStarts, s.LeasesStarted,
				as.Fair.LeaseEnds, s.LeasesEnded)
		}
		if err := h.e.Invariants(); err != nil {
			t.Fatalf("%v: %v", join, err)
		}
		if err := as.Check(); err != nil {
			t.Fatalf("%v: %v", join, err)
		}
		if err := h.e.Quiesce(); err != nil {
			t.Fatalf("%v: %v", join, err)
		}
	}
}
