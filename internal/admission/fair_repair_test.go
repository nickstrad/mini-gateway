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
