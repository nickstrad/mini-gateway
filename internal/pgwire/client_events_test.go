package pgwire

import (
	"testing"
)

// TestHandshakeResultOutcomesAreClassifiedOnTheClientSide proves every
// HandshakeOutcome (including out-of-range values) maps to its expected Reason.
func TestHandshakeResultOutcomesAreClassifiedOnTheClientSide(t *testing.T) {
	cases := []struct {
		outcome HandshakeOutcome
		reason  Reason
	}{
		{HandshakeFailed, ReasonTLSFailed},
		{HandshakeFailedVerification, ReasonTLSFailed},
		{HandshakeDeadline, ReasonDeadline},
		{HandshakeViolation, ReasonViolation},
		{HandshakeOutcome(200), ReasonTLSFailed},
	}
	for _, c := range cases {
		t.Run(c.outcome.String(), func(t *testing.T) {
			steps := []clientStep{{in: AppendSSLRequest(nil)}, {hs: &HandshakeResult{Outcome: c.outcome}}}
			r := requireEveryChunking(t, cfgFor(t, ClientTLSAllow), steps)
			requireClose(t, r, c.reason)
			if sumInt64(r.stats.Client.TLSFailed[:]) != 1 {
				t.Fatalf("handshake failures %+v", r.stats.Client.TLSFailed)
			}
		})
	}
}

// TestHandshakeResultWithAnUnknownVersionCountsAsOther proves an out-of-range
// TLSVersion clamps safely into TLSVersionOther.
func TestHandshakeResultWithAnUnknownVersionCountsAsOther(t *testing.T) {
	steps := []clientStep{{in: AppendSSLRequest(nil)}, {hs: &HandshakeResult{Outcome: HandshakeCompleted, Version: TLSVersion(250)}},
		{in: startupBytes("alice", testDatabase, nil)}}
	r := requireEveryChunking(t, cfgFor(t, ClientTLSRequire), steps)
	if r.stats.Client.TLSCompleted[TLSVersionOther] != 1 || !r.session.Upgraded() {
		t.Fatalf("%+v", r.stats.Client.TLSCompleted)
	}
}

// TestClientSessionDropsSCRAMStateWhenItCloses proves SCRAM state is released
// on auth failure and deadline close, not only on success.
func TestClientSessionDropsSCRAMStateWhenItCloses(t *testing.T) {
	steps := talk(t, cfgFor(t, ClientTLSOff)(), clientPlan{user: "alice", password: "nope"})
	r := replayClient(t, testClientConfig(t, ClientTLSOff, 1), steps, whole)
	requireClose(t, r, ReasonAuthFailed)
	if r.session.scram != nil {
		t.Fatal("SCRAM state kept after the failed login")
	}
	cfg := testClientConfig(t, ClientTLSOff, 1)
	s := NewClientSession(cfg)
	s.Receive(nil, startupBytes("alice", testDatabase, nil))
	s.Deadline(nil)
	if !s.Closed() || s.scram != nil {
		t.Fatal("SCRAM state kept after the deadline")
	}
}
