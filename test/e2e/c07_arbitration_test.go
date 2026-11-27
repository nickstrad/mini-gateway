package e2e

import (
	"testing"
	"time"
)

// TestSlotsHeldCheckRejectsFailedLateClient proves checkSlotsHeld fails if the
// post-reap verification client does not recover.
func TestSlotsHeldCheckRejectsFailedLateClient(t *testing.T) {
	r := mustPass(t, SlotsHeldSpec(), checkSlotsHeld)
	r.Spec.Clients[0].Start = pathDeath + r.Spec.Timeouts.Statement + r.Spec.Timeouts.Cleanup + time.Second
	late := len(r.Clients) - 1
	for i := range r.Clients[late].Steps {
		r.Clients[late].Steps[i] = errTokens("53M03")
	}
	if err := checkSlotsHeld(r); err == nil {
		t.Fatal("post-reap client failed every step but recovery check passed")
	}
}
