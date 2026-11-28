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

const joinStatement = 400 * time.Millisecond

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

func (h *heldFlight) release() (proxy.ConnID, []proxy.Action) {
	h.t.Helper()
	a := h.commit(12, h.other)
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
