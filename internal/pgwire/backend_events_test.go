package pgwire

import (
	"testing"
)

// TestHandshakeResultOutcomesAreClassifiedOnTheDatabaseSide proves every
// HandshakeOutcome and out-of-range enum value maps to the right Class and
// TLSVersion slot without panicking.
func TestHandshakeResultOutcomesAreClassifiedOnTheDatabaseSide(t *testing.T) {
	cases := []struct {
		outcome HandshakeOutcome
		class   Class
	}{
		{HandshakeFailed, ClassTLSFailed},
		{HandshakeFailedVerification, ClassTLSFailed},
		{HandshakeDeadline, ClassDeadline},
		{HandshakeViolation, ClassViolation},
		{HandshakeOutcome(200), ClassTLSFailed},
	}
	for _, c := range cases {
		t.Run(c.outcome.String(), func(t *testing.T) {
			steps := []clientStep{{in: []byte{AnswerTLSYes}}, {hs: &HandshakeResult{Outcome: c.outcome}}}
			r := requireBackendEveryChunking(t, BackendTLSRequire, steps)
			requireBackendClass(t, r, c.class)
			if sumInt64(r.stats.Database.TLSFailed[:]) != 1 {
				t.Fatalf("handshake failures %+v", r.stats.Database.TLSFailed)
			}
		})
	}
	steps := []clientStep{{in: []byte{AnswerTLSYes}}, {hs: &HandshakeResult{Outcome: HandshakeCompleted, Version: TLSVersion(250)}}}
	r := requireBackendEveryChunking(t, BackendTLSRequire, steps)
	if r.stats.Database.TLSCompleted[TLSVersionOther] != 1 || !r.session.OverTLS() {
		t.Fatalf("%+v", r.stats.Database.TLSCompleted)
	}
}

// TestReadyBackendSessionIgnoresStartupEvents proves a ready BackendSession is
// immutable to subsequent Receive, Deadline, PeerClosed, and HandshakeDone events.
func TestReadyBackendSessionIgnoresStartupEvents(t *testing.T) {
	steps := talkBackend(t, BackendTLSRequire, serverPlan{tlsAnswer: AnswerTLSYes, auth: AuthSASL})
	cfg := testBackendConfig(BackendTLSRequire, 1)
	r := replayBackend(t, cfg, steps, whole)
	s := r.session
	if !s.Ready() {
		t.Fatalf("not ready: %v", r.trace)
	}
	before := cfg.Stats.Snapshot()
	if acts, n := s.Receive(nil, AppendReadyForQuery(nil, StatusIdle)); n != 0 || len(acts) != 0 {
		t.Fatal("bytes after ready were taken")
	}
	if acts := s.Deadline(nil); len(acts) != 0 {
		t.Fatalf("deadline after ready: %v", traceOf(acts))
	}
	if acts := s.PeerClosed(nil); len(acts) != 0 {
		t.Fatalf("peer closed after ready: %v", traceOf(acts))
	}
	if acts := s.HandshakeDone(nil, HandshakeResult{Outcome: HandshakeFailed}); len(acts) != 0 {
		t.Fatalf("handshake after ready: %v", traceOf(acts))
	}
	if !s.Ready() || s.Closed() || s.Class() != ClassNone {
		t.Fatalf("ready=%v closed=%v class=%v", s.Ready(), s.Closed(), s.Class())
	}
	if got := cfg.Stats.Snapshot(); got != before {
		t.Fatalf("counters moved after ready: %+v", got.Database)
	}
}

// TestBackendSessionDropsSCRAMStateWhenItFails proves ScramClient (holding the
// plaintext password) is cleared on auth failure and deadline close.
func TestBackendSessionDropsSCRAMStateWhenItFails(t *testing.T) {
	steps := talkBackend(t, BackendTLSDisable, serverPlan{auth: AuthSASL, tamperFinal: true})
	r := replayBackend(t, testBackendConfig(BackendTLSDisable, 1), steps, whole)
	requireBackendClass(t, r, ClassAuthFailed)
	if r.session.scram != nil {
		t.Fatal("SCRAM state (with the password) kept after the failed login")
	}
	s := NewBackendSession(testBackendConfig(BackendTLSDisable, 1))
	s.Start(nil)
	s.Receive(nil, AppendAuthenticationSASL(nil, ScramMechanism))
	s.Deadline(nil)
	if !s.Closed() || s.scram != nil {
		t.Fatal("SCRAM state kept after the deadline")
	}
}
