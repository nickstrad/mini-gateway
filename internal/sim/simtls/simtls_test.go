package simtls

import "testing"

// TestHelloAndReplies proves simtls ClientHello validation and ServerReply
// encode/parse round-trips across every reply kind and version.
func TestHelloAndReplies(t *testing.T) {
	if !ValidClientHello(AppendClientHello(nil)) || ValidClientHello(AppendBadClientHello(nil)) || ValidClientHello([]byte("short")) {
		t.Fatal("client hello")
	}
	for _, c := range []struct {
		r Reply
		v byte
	}{{ReplyOK, Version13}, {ReplyOK, Version12}, {ReplyBadCertificate, Version13}, {ReplyAlert, 0}} {
		b := AppendServerReply(nil, c.r, c.v)
		r, v, ok := ParseServerReply(b)
		if len(b) != HelloLen || !ok || r != c.r || v != c.v {
			t.Fatalf("%v: %x -> %v %v %v", c.r, b, r, v, ok)
		}
	}
	if _, _, ok := ParseServerReply(AppendClientHello(nil)); ok {
		t.Fatal("a client hello parsed as a reply")
	}
	if _, _, ok := ParseServerReply([]byte("N")); ok {
		t.Fatal("short reply")
	}
}
