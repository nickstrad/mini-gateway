package pool

import "testing"

// TestArbitrationNoReplyCloseDoesNotSuspectNewConnections proves a delayed
// cleanup close from an earlier no-reply timeout does not mark newer replacements suspect.
func TestArbitrationNoReplyCloseDoesNotSuspectNewConnections(t *testing.T) {
	res := runOps(modelConfig(), decodeOps([]byte("700900700000Y01C20900100")))
	if res.violation != "" {
		t.Fatalf("%s\nREPLAY: %s", res.violation, res)
	}
}
