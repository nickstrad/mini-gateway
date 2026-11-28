package admission

import (
	"strings"
	"testing"
)

func TestQuiesceRefusesALeaseStillInFlightAndPassesOnceItEnds(t *testing.T) {
	q, f := fairCore(t, 2)
	if err := q.Quiesce(); err != nil {
		t.Fatalf("premise: %v", err)
	}
	q.LeaseStart(1, "a")
	err := q.Quiesce()
	if err == nil || !strings.Contains(err.Error(), "in flight") {
		t.Fatalf("Quiesce passed with a lease in flight: %v", err)
	}
	q.LeaseEnd(2, "a")
	if err := q.Quiesce(); err != nil || f.States() != 0 {
		t.Fatalf("after the lease ended: %v, states %d", err, f.States())
	}
}
