package admission

import "testing"

func TestRefuseCountsABreakerShedTheCallerAnswersAndKeepsTheQueueHealthy(t *testing.T) {
	q := mustNew(t, Config{Rule: RuleTimeout, Detail: true, TenantCapacity: 4, Capacity: 8})
	q.Push(0, "a", 1)
	q.Push(0, "a", 2)
	q.Push(0, "b", 3)
	if !q.Refuse(Millisecond, 2) || !q.Refuse(Millisecond, 3) {
		t.Fatal("a queued request was not refused")
	}
	if q.Refuse(Millisecond, 3) || q.Refuse(Millisecond, 99) {
		t.Fatal("a request that is not queued was refused")
	}
	if q.Pending() != 0 {
		t.Fatalf("%d refusals left for TakeShed; the caller answers a refusal itself", q.Pending())
	}
	if err := q.Check(); err != nil {
		t.Fatal(err)
	}
	s := q.Snapshot()
	if s.Shed[ReasonBreaker] != 2 || s.ShedTotal() != 2 || q.ShedCount() != 2 || s.Removed != 0 || s.Queued.Value != 1 {
		t.Fatalf("breaker %d, shed %d, removed %d, queued %d", s.Shed[ReasonBreaker], s.ShedTotal(), s.Removed, s.Queued.Value)
	}
	if s.Shedding != 0 {
		t.Fatalf("%d tenant queues shedding after breaker refusals; the queue itself shed nothing", s.Shedding)
	}
	if err := s.Check(); err != nil {
		t.Fatal(err)
	}
	if id, ok := q.Pop(Millisecond); !ok || id != 1 {
		t.Fatalf("pop after refusals: %d %v", id, ok)
	}
	if ReasonBreaker.String() != "breaker" {
		t.Fatalf("reason name %q", ReasonBreaker.String())
	}
}
