package pgwiretest

import (
	"bytes"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
	"github.com/nickstrad/mini-gateway/internal/sim/simtls"
)

func dialProxy(t *testing.T, d *SimDialer, addr string) *simConn {
	t.Helper()
	c := &simConn{d: d}
	c.end = d.Net.Dial(addr, simnet.DefaultOptions(), simnet.HandlerFunc(func(_ *simnet.End, ev simnet.Event) {
		switch ev {
		case simnet.EvConnected:
			c.connected = true
		case simnet.EvRefused:
			c.refused = true
		}
	}))
	for i := 0; !c.connected && !c.refused; i++ {
		if i >= maxSimSteps || !d.step() {
			t.Fatal("dial timed out")
		}
	}
	if c.refused {
		t.Fatal("refused")
	}
	return c
}

func readN(t *testing.T, c *simConn, n int) []byte {
	t.Helper()
	out := make([]byte, 0, n)
	buf := make([]byte, n)
	for len(out) < n {
		k, err := c.Read(buf[:n-len(out)])
		if err != nil {
			t.Fatalf("after %q: %v", out, err)
		}
		out = append(out, buf[:k]...)
	}
	return out
}

func TestProxyKeepsBytesCoalescedWithTheClientHello(t *testing.T) {
	d, err := NewSimFidelity(7)
	if err != nil {
		t.Fatal(err)
	}
	v, err := pgwire.NewVerifier("pw", []byte("proxy-test-salt0"), 16)
	if err != nil {
		t.Fatal(err)
	}
	table, err := pgwire.NewTenantTable([]pgwire.Tenant{{User: "alice", Verifier: v}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(d.Net, Config{Database: d.PG.Addr(),
		Client:  pgwire.ClientConfig{TLS: pgwire.ClientTLSRequire, Tenants: table, Database: FidelityDatabase, MockSecret: []byte("m")},
		Backend: pgwire.BackendConfig{User: FidelityUser, Database: FidelityDatabase, Password: FidelityPassword}})
	if err != nil {
		t.Fatal(err)
	}
	c := dialProxy(t, d, p.Addr())
	if err := c.Write(pgwire.AppendSSLRequest(nil)); err != nil {
		t.Fatal(err)
	}
	if got := readN(t, c, 1); got[0] != pgwire.AnswerTLSYes {
		t.Fatalf("answer %q", got)
	}
	startup := pgwire.AppendStartupMessage(nil, []pgwire.Param{{Name: "user", Value: "alice"}, {Name: "database", Value: FidelityDatabase}})
	if err := c.Write(append(simtls.AppendClientHello(nil), startup...)); err != nil {
		t.Fatal(err)
	}
	reply := readN(t, c, simtls.HelloLen)
	if r, _, ok := simtls.ParseServerReply(reply); !ok || r != simtls.ReplyOK {
		t.Fatalf("handshake reply %x", reply)
	}
	sasl := readN(t, c, 5)
	if sasl[0] != pgwire.MsgAuthentication {
		t.Fatalf("after the handshake %q, want AuthenticationSASL", sasl)
	}
	rest := readN(t, c, int(sasl[4])-4)
	if !bytes.Contains(rest, []byte(pgwire.ScramMechanism)) {
		t.Fatalf("%q", rest)
	}
	c.Close()
}
