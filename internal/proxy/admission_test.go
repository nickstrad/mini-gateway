package proxy_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
)

var readyIdle = pgwire.AppendReadyForQuery(nil, 'I')

func admissionRig(t *testing.T, cap int, a admission.Config) *rig {
	return newRig(t, func(c *proxy.Config) {
		c.QueueCap = cap
		c.Admission = a
	})
}

func shedReply(b []byte) bool {
	return bytes.Contains(b, []byte("53M01")) && bytes.HasSuffix(b, readyIdle) && bytes.Count(b, []byte{'Z', 0, 0, 0, 5}) == 1
}

func wakeAt(a []proxy.Action) proxy.Instant {
	var at proxy.Instant
	for _, x := range a {
		if x.Kind == proxy.ActWake {
			at = x.At
		}
	}
	return at
}

// TestAdmissionShedByTheRuleIsAnsweredAtOnceWithItsRowAndEndMarker proves a rule
// shed replies immediately at its exact deadline with 53M01 and ReadyForQuery without touching the database.
func TestAdmissionShedByTheRuleIsAnsweredAtOnceWithItsRowAndEndMarker(t *testing.T) {
	r := admissionRig(t, 4, admission.Config{Rule: admission.RuleTimeout, LIFO: admission.LIFOOff})
	r.client(11)
	r.client(12)
	r.ack(r.query(11, "SELECT 1"))
	queued := r.now
	r.query(12, "SELECT 2")
	deadline := queued + proxy.Instant(admission.DefaultInterval)
	r.at(deadline - 1)
	if a := r.ev(proxy.EvTimer, 0, nil, 0); len(written(a, proxy.ActClientWrite, 12)) != 0 {
		t.Fatal("shed before the interval")
	}
	r.at(deadline)
	a := r.ack(r.ev(proxy.EvTimer, 0, nil, 0))
	if !shedReply(written(a, proxy.ActClientWrite, 12)) || did(a, proxy.ActDBWrite, r.db) {
		t.Fatalf("client 12 got %q at its allowed wait", written(a, proxy.ActClientWrite, 12))
	}
	s, as := r.e.Snapshot(), r.e.AdmissionSnapshot()
	if s.Shed != 1 || s.ByRow[pgwire.ErrRowShed] != 1 || as.Shed[admission.ReasonRule] != 1 || as.Pushes != 2 {
		t.Fatalf("engine shed %d row %d, admission %+v", s.Shed, s.ByRow[pgwire.ErrRowShed], as.Shed)
	}
	if err := as.Check(); err != nil {
		t.Fatal(err)
	}
}

// TestAdmissionArmsItsNextDeadline proves pushing a request into AdmissionQueue arms ActWake at its allowed wait.
func TestAdmissionArmsItsNextDeadline(t *testing.T) {
	r := admissionRig(t, 4, admission.Config{Rule: admission.RuleTimeout, LIFO: admission.LIFOOff})
	r.client(11)
	r.client(12)
	r.ack(r.query(11, "SELECT 1"))
	a := r.query(12, "SELECT 2")
	if at := wakeAt(a); at != r.now+proxy.Instant(admission.DefaultInterval) {
		t.Fatalf("wake at %d, want the head's allowed wait %d", at, r.now+proxy.Instant(admission.DefaultInterval))
	}
}

// TestAdmissionOverflowShedsTheQueuedHeadNotTheArrival proves full-queue overflow answers the oldest queued client with 53M01 and keeps the new arrival.
func TestAdmissionOverflowShedsTheQueuedHeadNotTheArrival(t *testing.T) {
	r := admissionRig(t, 2, admission.Config{Rule: admission.RuleControlLaw})
	for id := proxy.ConnID(11); id <= 14; id++ {
		r.client(id)
	}
	r.ack(r.query(11, "SELECT 1"))
	r.query(12, "SELECT 2")
	r.query(13, "SELECT 3")
	a := r.ack(r.query(14, "SELECT 4"))
	if !shedReply(written(a, proxy.ActClientWrite, 12)) || len(written(a, proxy.ActClientWrite, 14)) != 0 {
		t.Fatalf("head %q arrival %q", written(a, proxy.ActClientWrite, 12), written(a, proxy.ActClientWrite, 14))
	}
	if as := r.e.AdmissionSnapshot(); as.Shed[admission.ReasonOverflow] != 1 || as.Queued.Value != 2 {
		t.Fatalf("%+v", as)
	}
}

// TestAdmissionConservesOnEveryExitPath proves pushes equal served + removed + deadline sheds across mixed client exits.
func TestAdmissionConservesOnEveryExitPath(t *testing.T) {
	r := admissionRig(t, 8, admission.Config{Rule: admission.RuleFIFO})
	for id := proxy.ConnID(11); id <= 14; id++ {
		r.client(id)
	}
	r.ack(r.query(11, "SELECT 1"))
	r.query(12, "SELECT 2")
	r.query(13, "SELECT 3")
	r.query(14, "SELECT 4")
	r.ev(proxy.EvClientClosed, 13, nil, 0)
	r.ack(r.ev(proxy.EvDBData, r.db, complete("SELECT 1", 'I'), 0))
	r.ack(r.ev(proxy.EvDBData, r.db, complete("DISCARD ALL", 'I'), 0))
	r.at(r.now + proxy.Instant(r.e.Config().Timeouts.QueueWait))
	r.ack(r.ev(proxy.EvTimer, 0, nil, 0))
	as := r.e.AdmissionSnapshot()
	if as.Pushes != 4 || as.Served != 2 || as.Removed != 1 || as.Shed[admission.ReasonDeadline] != 1 || as.Queued.Value != 0 {
		t.Fatalf("pushes %d served %d removed %d shed %v queued %d", as.Pushes, as.Served, as.Removed, as.Shed, as.Queued.Value)
	}
	if err := as.Check(); err != nil {
		t.Fatal(err)
	}
}

// TestAdmissionComparisonArmsAreReachableOnlyThroughTheEngineConfig proves simulator arms construct via proxy.Config while bad admission settings fail validation.
func TestAdmissionComparisonArmsAreReachableOnlyThroughTheEngineConfig(t *testing.T) {
	r := admissionRig(t, 4, admission.Config{Rule: admission.RuleFolly})
	if r.e.AdmissionSnapshot().Rule != admission.RuleFolly {
		t.Fatal("arm not built")
	}
	c := r.e.Config()
	c.Admission.Target = time.Duration(admission.DefaultInterval).Nanoseconds()
	if _, err := proxy.New(c); err == nil {
		t.Fatal("bad admission config accepted")
	}
}
