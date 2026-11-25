package pgwire

import (
	"strings"
	"testing"
)

// TestClientRejectsAnOversizedSCRAMMessageInsideTheFrame proves ClientSession
// refuses a SCRAM payload larger than MaxScramMessage even when the outer SASL
// frame fits within maxAuthMessage.
func TestClientRejectsAnOversizedSCRAMMessageInsideTheFrame(t *testing.T) {
	cfg := testClientConfig(t, ClientTLSOff, 1)
	s := NewClientSession(cfg)
	s.Receive(nil, startupBytes("alice", testDatabase, nil))
	msg := []byte("n,,n=," + "r=" + strings.Repeat("a", MaxScramMessage))
	acts, _ := s.Receive(nil, AppendSASLInitialResponse(nil, ScramMechanism, msg))
	if !s.Closed() || s.Reason() != ReasonAuthFailed {
		t.Fatalf("a %d-byte SCRAM message was taken: %v", len(msg), traceOf(acts))
	}
}

// TestScramFunctionsRefuseMessagesAboveTheBound proves every pure SCRAM
// server and client method refuses inputs exceeding MaxScramMessage.
func TestScramFunctionsRefuseMessagesAboveTheBound(t *testing.T) {
	v := secretVerifier(t)
	atBound := "n,,n=,r=" + strings.Repeat("a", MaxScramMessage-len("n,,n=,r="))
	if _, cause := NewScramServer(v, "srv", false).First(ScramMechanism, []byte(atBound)); cause != AuthCauseNone {
		t.Fatalf("a %d-byte first message was refused: %v", len(atBound), cause)
	}
	over := []byte(atBound + "a")
	if _, cause := NewScramServer(v, "srv", false).First(ScramMechanism, over); cause != AuthCauseMalformed {
		t.Fatalf("server first: %v", cause)
	}
	srv := NewScramServer(v, "srv", false)
	if _, cause := srv.First(ScramMechanism, []byte("n,,n=,r=abc")); cause != AuthCauseNone {
		t.Fatal(cause)
	}
	if _, cause := srv.Final([]byte("c=biws,r=abcsrv," + strings.Repeat("x=y,", MaxScramMessage/4) + "p=AAAA")); cause != AuthCauseMalformed {
		t.Fatalf("server final: %v", cause)
	}
	cl := NewScramClient("", "pw", "abc", GS2NoBinding)
	cl.First()
	long := "r=abcsrv,s=" + strings.Repeat("A", MaxScramMessage) + ",i=4096"
	if _, cause := cl.Final([]byte(long)); cause != AuthCauseMalformed {
		t.Fatalf("client final: %v", cause)
	}
	if cause := runScramToVerify(t, v, "v="+strings.Repeat("A", MaxScramMessage)); cause != AuthCauseMalformed {
		t.Fatalf("client verify: %v", cause)
	}
}

func runScramToVerify(t *testing.T, v Verifier, serverFinal string) AuthCause {
	t.Helper()
	cl := NewScramClient("", "secret", "clientnonce", GS2NoBinding)
	srv := NewScramServer(v, "servernonce", false)
	sf, cause := srv.First(ScramMechanism, cl.First())
	if cause != AuthCauseNone {
		t.Fatal(cause)
	}
	if _, cause = cl.Final(sf); cause != AuthCauseNone {
		t.Fatal(cause)
	}
	return cl.Verify([]byte(serverFinal))
}
