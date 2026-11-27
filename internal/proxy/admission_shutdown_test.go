package proxy_test

import (
	"testing"

	"github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/proxy"
)

func shutdownAccounting(t *testing.T, r *rig) (proxy.Snapshot, admission.Snapshot) {
	t.Helper()
	a := r.e.Shutdown(r.now, nil)
	for _, x := range a {
		if x.Kind == proxy.ActWake && x.At > r.now {
			t.Fatalf("shutdown armed a wake at %d", x.At)
		}
	}
	if err := r.e.Invariants(); err != nil {
		t.Fatalf("invariants after shutdown: %v", err)
	}
	if err := r.e.Quiesce(); err != nil {
		t.Fatalf("quiesce after shutdown: %v", err)
	}
	s, as := r.e.Snapshot(), r.e.AdmissionSnapshot()
	if s.Shed != as.ShedTotal() {
		t.Fatalf("engine shed %d, admission shed %d (%v)", s.Shed, as.ShedTotal(), as.Shed)
	}
	if err := as.Check(); err != nil {
		t.Fatal(err)
	}
	return s, as
}

// TestAdmissionShutdownInTheQuietWindowCancelsTheTimer proves Shutdown removes empty tenant queues and cancels tAdmission so Quiesce passes.
func TestAdmissionShutdownInTheQuietWindowCancelsTheTimer(t *testing.T) {
	for _, rule := range []admission.RuleKind{admission.RuleTimeout, admission.RuleControlLaw} {
		r := admissionRig(t, 4, admission.Config{Rule: rule, LIFO: admission.LIFOOff})
		r.client(11)
		r.client(12)
		r.ack(r.query(11, "SELECT 1"))
		r.ack(r.query(12, "SELECT 2"))
		r.ack(r.ev(proxy.EvDBData, r.db, complete("SELECT 1", 'I'), 0))
		if r.e.AdmissionSnapshot().Tenants.Value == 0 {
			t.Fatalf("%v: the tenant queue is already gone; the test proves nothing", rule)
		}
		_, as := shutdownAccounting(t, r)
		if as.Tenants.Value != 0 || as.TenantsRemoved == 0 {
			t.Fatalf("%v: tenant queues after shutdown: %+v", rule, as.Tenants)
		}
	}
}

// TestAdmissionShutdownAtTheDeadlineShedsTheExpiredAndRemovesTheRest proves Shutdown sweeps due sheds before removing remaining queued clients so engine and admission counts agree.
func TestAdmissionShutdownAtTheDeadlineShedsTheExpiredAndRemovesTheRest(t *testing.T) {
	r := admissionRig(t, 4, admission.Config{Rule: admission.RuleTimeout, LIFO: admission.LIFOOff})
	r.client(11)
	r.client(12)
	r.client(13)
	r.ack(r.query(11, "SELECT 1"))
	queued := r.now
	r.ack(r.query(12, "SELECT 2"))
	r.at(queued + proxy.Instant(admission.DefaultInterval) - 1)
	r.ack(r.query(13, "SELECT 3"))
	r.at(queued + proxy.Instant(admission.DefaultInterval))
	s, as := shutdownAccounting(t, r)
	if as.Shed[admission.ReasonRule] != 1 || s.Shed != 1 || as.Removed != 1 {
		t.Fatalf("at the deadline: engine shed %d, admission shed %v removed %d", s.Shed, as.Shed, as.Removed)
	}
}
