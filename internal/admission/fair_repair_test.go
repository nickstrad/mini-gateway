package admission

import (
	"fmt"
	"testing"
)

func floodTenants(t *testing.T, q *Queues, owner map[int64]string, tenants, each int) {
	t.Helper()
	id := int64(len(owner))
	for i := range tenants {
		name := tenantName(i)
		for range each {
			id++
			owner[id] = name
			mustPush(t, q, 1, name, id)
		}
	}
}

// TestRoundingRemaindersNeverIdleALeaseWhileEqualTenantsWait proves that when
// integer-floor shares sum below the limit and no active tenant is below share,
// every lease up to the limit is lent by round robin.
func TestRoundingRemaindersNeverIdleALeaseWhileEqualTenantsWait(t *testing.T) {
	for _, c := range []struct{ limit, tenants int }{{3, 2}, {4, 3}, {5, 2}, {7, 3}, {5, 3}} {
		t.Run(fmt.Sprintf("limit-%d-tenants-%d", c.limit, c.tenants), func(t *testing.T) {
			q, _ := fairCore(t, c.limit)
			owner := map[int64]string{}
			floodTenants(t, q, owner, c.tenants, c.limit+2)
			for range c.limit {
				if _, ok := lendNext(t, q, 1, owner); !ok {
					t.Fatalf("a lease idles with %d of %d in flight while %d requests wait (held at a share %d)",
						q.InFlight(), c.limit, q.Len(), q.Snapshot().Fair.HeldShare)
				}
			}
			if q.InFlight() != c.limit {
				t.Fatalf("in flight %d, limit %d", q.InFlight(), c.limit)
			}
			if _, ok := q.Pop(1); ok {
				t.Fatal("lent past the limit")
			}
			if got := q.Snapshot().Fair.HeldShare; got != 0 {
				t.Fatalf("held at a share %d times, want 0: every tenant is at its share, none below", got)
			}
		})
	}
}

// TestTheLastLeaseIsHeldOnlyForAnActiveTenantBelowItsShare proves the last free
// lease is held while an active non-waiting tenant is below share and released once that tenant leaves.
func TestTheLastLeaseIsHeldOnlyForAnActiveTenantBelowItsShare(t *testing.T) {
	q, _ := fairCore(t, 4)
	owner := map[int64]string{1: "a", 2: "a", 3: "a", 4: "b"}
	for i := int64(1); i <= 3; i++ {
		mustPush(t, q, 1, "a", i)
	}
	mustPush(t, q, 1, "b", 4)
	for range 3 {
		lendNext(t, q, 1, owner)
	}
	if q.TenantInFlight("a") != 2 || q.TenantInFlight("b") != 1 {
		t.Fatalf("premise: a 2, b 1, got %d %d", q.TenantInFlight("a"), q.TenantInFlight("b"))
	}
	if _, ok := q.Pop(1); ok {
		t.Fatal("b holds 1 of its share 2 and is active: the last lease is held for it")
	}
	if q.Releasable() != 0 {
		t.Fatalf("releasable %d, want 0 while the last lease is held", q.Releasable())
	}
	q.LeaseEnd(2, "b")
	if q.Releasable() != 1 {
		t.Fatalf("b left: releasable %d, want 1", q.Releasable())
	}
	if name, ok := lendNext(t, q, 2, owner); !ok || name != "a" {
		t.Fatalf("b is no longer active: a must take the lease, got %q %v", name, ok)
	}
}

// TestReleasableCountsOnlyWhatTheDispatcherWouldLend proves Releasable accounts
// for the fair limit, reserved last slots, and fallback to queue length when detached.
func TestReleasableCountsOnlyWhatTheDispatcherWouldLend(t *testing.T) {
	q, _ := fairCore(t, 4)
	owner := map[int64]string{}
	if q.Releasable() != 0 {
		t.Fatal("nothing waits")
	}
	floodTenants(t, q, owner, 1, 6)
	if got := q.Releasable(); got != 4 {
		t.Fatalf("one tenant, six waiting, limit 4: releasable %d", got)
	}
	for range 4 {
		lendNext(t, q, 1, owner)
	}
	if got := q.Releasable(); got != 0 {
		t.Fatalf("at the limit: releasable %d", got)
	}
	q.SetLimit(1, 2)
	if got := q.Releasable(); got != 0 {
		t.Fatalf("limit below in flight: releasable %d", got)
	}
	q.SetLimit(1, 6)
	if got := q.Releasable(); got != 2 {
		t.Fatalf("limit 6, 4 in flight, 2 waiting: releasable %d", got)
	}
	q2, _ := fairCore(t, 4)
	owner2 := map[int64]string{1: "a", 2: "a", 3: "a", 4: "b", 5: "a"}
	for i := int64(1); i <= 3; i++ {
		mustPush(t, q2, 1, "a", i)
	}
	mustPush(t, q2, 1, "b", 4)
	mustPush(t, q2, 1, "a", 5)
	if got := q2.Releasable(); got != 3 {
		t.Fatalf("a 4 waiting, b 1, limit 4, share 2 each: b takes 1 of its 2 and holds the last lease back, releasable %d", got)
	}
	n := 0
	for {
		if _, ok := lendNext(t, q2, 1, owner2); !ok {
			break
		}
		n++
	}
	if n != 3 || q2.Releasable() != 0 {
		t.Fatalf("lent %d (releasable said 3), releasable now %d", n, q2.Releasable())
	}
	q3, _ := fairCore(t, 0)
	if q3.SetDispatcher(nil); q3.Releasable() != 0 {
		t.Fatal("without the dispatcher releasable is the queue length")
	}
	mustPush(t, q3, 1, "a", 1)
	if q3.Releasable() != 1 {
		t.Fatal("without the dispatcher releasable is the queue length")
	}
}

// TestReleasableMatchesRepeatedPicksOnRandomStates exhaustively proves
// Releasable equals the exact count of successive Pick+LeaseStart pairs.
func TestReleasableMatchesRepeatedPicksOnRandomStates(t *testing.T) {
	for limit := 1; limit <= 6; limit++ {
		for held := 0; held < 1<<6; held++ {
			for waiting := 0; waiting < 1<<6; waiting += 3 {
				q, _ := fairCore(t, limit)
				owner := map[int64]string{}
				id := int64(0)
				for i := range 3 {
					name := tenantName(i)
					for range held >> (2 * i) & 3 {
						if q.InFlight() < 16 {
							q.LeaseStart(1, name)
						}
					}
					for range waiting >> (2 * i) & 3 {
						id++
						owner[id] = name
						mustPush(t, q, 1, name, id)
					}
				}
				want := q.Releasable()
				got := 0
				for {
					if _, ok := lendNext(t, q, 1, owner); !ok {
						break
					}
					got++
				}
				if got != want {
					t.Fatalf("limit %d held %b waiting %b: releasable %d, the dispatcher lent %d", limit, held, waiting, want, got)
				}
			}
		}
	}
}

// TestActiveTenantsPeakCountsPushesAndNeverFalls proves active_tenants_peak
// updates on Push and remains monotonically non-decreasing after removals.
func TestActiveTenantsPeakCountsPushesAndNeverFalls(t *testing.T) {
	q, _ := fairCore(t, 4)
	for i := range 5 {
		mustPush(t, q, 1, tenantName(i), int64(i+1))
	}
	s1 := q.Snapshot()
	for i := range 5 {
		if !q.Remove(2, int64(i+1)) {
			t.Fatal("remove")
		}
	}
	s2 := q.Snapshot()
	if s1.Fair.Active.Peak != 5 || s2.Fair.Active.Peak != 5 || s2.Fair.Active.Value != 0 {
		t.Fatalf("active peak %d then %d (value %d), want 5 and 5", s1.Fair.Active.Peak, s2.Fair.Active.Peak, s2.Fair.Active.Value)
	}
	q2, _ := fairCore(t, 4)
	for i := range 3 {
		mustPush(t, q2, 1, tenantName(i), int64(i+1))
	}
	for i := range 3 {
		q2.Remove(2, int64(i+1))
	}
	if got := q2.Snapshot().Fair.Active.Peak; got != 3 {
		t.Fatalf("three tenants pushed with no snapshot between: peak %d, want 3", got)
	}
}

// TestUnpopRestoresTheHeldFlag proves Unpop restores Fair.held to its pre-pop value.
func TestUnpopRestoresTheHeldFlag(t *testing.T) {
	q, _ := fairCore(t, 4)
	owner := map[int64]string{1: "a", 2: "a", 3: "a", 4: "b", 5: "b"}
	for i := int64(1); i <= 3; i++ {
		mustPush(t, q, 1, "a", i)
	}
	mustPush(t, q, 1, "b", 4)
	for range 3 {
		lendNext(t, q, 1, owner)
	}
	if _, ok := q.Pop(1); ok || !q.Held() {
		t.Fatal("premise: the last lease is held for b")
	}
	mustPush(t, q, 2, "b", 5)
	id, ok := q.Pop(2)
	if !ok || id != 5 || q.Held() {
		t.Fatalf("b waits below its share and is lent: %d %v held %v", id, ok, q.Held())
	}
	if !q.Unpop() {
		t.Fatal("unpop refused")
	}
	if !q.Held() {
		t.Fatal("unpop must restore the held flag of the pick before it")
	}
}

// TestATenantAtItsShareWaitsWhileBelowShareTenantsArriveUntilItsOwnLeaseEnds
// proves round-robin fairness is bounded within a group and an at-share tenant
// moves to the below-share group once one of its own leases ends.
func TestATenantAtItsShareWaitsWhileBelowShareTenantsArriveUntilItsOwnLeaseEnds(t *testing.T) {
	q, _ := fairCore(t, 4)
	owner := map[int64]string{1: "a", 2: "a", 3: "a", 4: "b"}
	for i := int64(1); i <= 3; i++ {
		mustPush(t, q, 1, "a", i)
	}
	mustPush(t, q, 1, "b", 4)
	for range 3 {
		lendNext(t, q, 1, owner)
	}
	id := int64(4)
	for round := range 20 {
		id++
		owner[id] = "b"
		mustPush(t, q, Time(2+round), "b", id)
		name, ok := lendNext(t, q, Time(2+round), owner)
		if !ok || name != "b" {
			t.Fatalf("round %d: b below its share must be lent before a at its share, got %q %v", round, name, ok)
		}
		q.LeaseEnd(Time(2+round), "b")
	}
	if q.Depth("a") != 1 || q.TenantInFlight("a") != 2 {
		t.Fatalf("a waited through 20 lends to b: depth %d, in flight %d", q.Depth("a"), q.TenantInFlight("a"))
	}
	q.LeaseEnd(30, "a")
	id++
	owner[id] = "b"
	mustPush(t, q, 30, "b", id)
	if name, _ := lendNext(t, q, 30, owner); name != "a" {
		t.Fatalf("one of a's leases ended: a is below its share and its turn is older than b's, got %q", name)
	}
}

// TestTheShareBoundsNewLendsAndPreemptsNothing proves share changes and limit
// reductions never revoke open leases and only constrain subsequent lends.
func TestTheShareBoundsNewLendsAndPreemptsNothing(t *testing.T) {
	q, f := fairCore(t, 6)
	owner := map[int64]string{}
	floodTenants(t, q, owner, 1, 8)
	for range 6 {
		lendNext(t, q, 1, owner)
	}
	owner[100] = "tb"
	mustPush(t, q, 2, "tb", 100)
	if f.Share("ta") != 3 || q.TenantInFlight("ta") != 6 {
		t.Fatalf("premise: ta holds 6 with share 3, got %d share %d", q.TenantInFlight("ta"), f.Share("ta"))
	}
	if _, ok := q.Pop(2); ok {
		t.Fatal("at the limit nothing is lent")
	}
	if q.TenantInFlight("ta") != 6 {
		t.Fatal("a newcomer took nothing from ta's leases")
	}
	q.LeaseEnd(3, "ta")
	if name, _ := lendNext(t, q, 3, owner); name != "tb" {
		t.Fatalf("the freed lease goes to the newcomer below its share, got %q", name)
	}
	q.LeaseEnd(4, "ta")
	if _, ok := q.Pop(4); ok {
		t.Fatalf("ta holds %d, above its share 3, and the free lease is the last: it is held for tb, below its share", q.TenantInFlight("ta"))
	}
	if !q.SetLimit(5, 2) || q.TenantInFlight("ta") != 4 || q.InFlight() != 5 {
		t.Fatalf("lowering the limit ends no lease: in flight %d", q.InFlight())
	}
	for range 3 {
		q.LeaseEnd(6, "ta")
		if _, ok := q.Pop(6); ok {
			t.Fatalf("%d in flight at limit 2: nothing is lent until the total falls below the limit", q.InFlight())
		}
	}
	q.LeaseEnd(7, "tb")
	if _, ok := q.Pop(7); !ok {
		t.Fatal("below the lowered limit lending resumes")
	}
}
