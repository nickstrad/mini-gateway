package pool

import "testing"

func TestArbitrationNoReplyCloseDoesNotSuspectNewConnections(t *testing.T) {
	res := runOps(modelConfig(), decodeOps([]byte("700900700000Y01C20900100")))
	if res.violation != "" {
		t.Fatalf("%s\nREPLAY: %s", res.violation, res)
	}
}
