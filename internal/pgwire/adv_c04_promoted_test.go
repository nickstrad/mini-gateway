// Property, regression, and Postgres 17 fidelity tests for wire framing,
// TLS negotiation, SCRAM-SHA-256 authentication, and session cycle tracking.
package pgwire_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	p "github.com/nickstrad/mini-gateway/internal/pgwire"
	pt "github.com/nickstrad/mini-gateway/internal/pgwire/pgwiretest"
)

const c04Seed int64 = 4042026

func c04Mark(t *testing.T) { t.Helper(); t.Logf("seed=%d", c04Seed) }

func c04Verifier(t *testing.T) p.Verifier {
	t.Helper()
	v, e := p.NewVerifier("correct-password", []byte("adversary-salt-16"), 32)
	if e != nil {
		t.Fatal(e)
	}
	return v
}

func c04Config(t *testing.T, mode p.ClientTLSMode) p.ClientConfig {
	t.Helper()
	r := rand.New(rand.NewSource(c04Seed))
	tab, e := p.NewTenantTable([]p.Tenant{{User: "alice", Verifier: c04Verifier(t)}})
	if e != nil {
		t.Fatal(e)
	}
	return p.ClientConfig{TLS: mode, Auth: p.AuthTrustForTestsOnly, Tenants: tab, Database: "db", Random: r, Keys: p.NewKeyIssuer(r), MockSecret: []byte("mock-key"), MockIterations: 32, Stats: &p.WireStats{}}
}

func c04Startup() []byte {
	return p.AppendStartupMessage(nil, []p.Param{{Name: "user", Value: "alice"}, {Name: "database", Value: "db"}})
}

func c04Frame(typ byte, body []byte) []byte {
	b := make([]byte, 5)
	b[0] = typ
	binary.BigEndian.PutUint32(b[1:], uint32(4+len(body)))
	return append(b, body...)
}

func c04Writes(a []p.Action) []byte {
	var out []byte
	for _, x := range a {
		if x.Kind == p.ActWrite {
			out = append(out, x.Data...)
		}
	}
	return out
}

func c04Parts(b []byte, n int, fn func([]byte)) {
	for len(b) > 0 {
		k := min(n, len(b))
		fn(b[:k])
		b = b[k:]
	}
}

func c04Backend(stats *p.WireStats) *p.BackendSession {
	b := p.NewBackendSession(p.BackendConfig{User: "alice", Database: "db", Password: "correct-password", Random: rand.New(rand.NewSource(c04Seed)), Stats: stats})
	b.Start(nil)
	return b
}

func c04AuthOK() []byte { return p.AppendAuthentication(nil, p.AuthOK, nil) }

func c04Exchange(t *testing.T, gs2 string) (*p.ScramClient, *p.ScramServer, []byte) {
	t.Helper()
	c := p.NewScramClient("alice", "correct-password", "client-fixed-nonce", gs2)
	s := p.NewScramServer(c04Verifier(t), "server-fixed-nonce", false)
	sf, e := s.First(p.ScramMechanism, c.First())
	if e != p.AuthCauseNone {
		t.Fatal(e)
	}
	cf, e := c.Final(sf)
	if e != p.AuthCauseNone {
		t.Fatal(e)
	}
	return c, s, cf
}

func TestAdv_1_ClientFragmentedDuplicateParameters(t *testing.T) {
	c04Mark(t)
	b := p.AppendStartupMessage(nil, []p.Param{{Name: "user", Value: "bad"}, {Name: "database", Value: "db"}, {Name: "user", Value: "alice"}, {Name: "application_name", Value: "\u03bb"}})
	var want []p.Action
	for n := 1; n <= len(b); n++ {
		s := p.NewClientSession(c04Config(t, p.ClientTLSOff))
		var got []p.Action
		c04Parts(b, n, func(x []byte) { got, _ = s.Receive(got, x) })
		if !s.Ready() {
			t.Fatalf("chunk=%d not ready: %v", n, got)
		}
		if n == 1 {
			want = got
		} else if !reflect.DeepEqual(want, got) {
			t.Fatalf("chunk=%d actions differ", n)
		}
	}
}

func TestAdv_1_BackendParameterReplacementPartitions(t *testing.T) {
	c04Mark(t)
	b := c04AuthOK()
	for i := 0; i < 17; i++ {
		b = p.AppendParameterStatus(b, "TimeZone", fmt.Sprint(i))
	}
	b = p.AppendReadyForQuery(b, 'I')
	var want []p.Action
	for n := 1; n <= len(b); n++ {
		s := c04Backend(nil)
		var got []p.Action
		c04Parts(b, n, func(x []byte) { got, _ = s.Receive(got, x) })
		if !s.Ready() {
			t.Fatalf("chunk=%d not ready", n)
		}
		if n == 1 {
			want = got
		} else if !reflect.DeepEqual(want, got) {
			t.Fatalf("chunk=%d actions differ: %v / %v", n, want, got)
		}
	}
}

func TestAdv_1_CycleRandomPartitions(t *testing.T) {
	c04Mark(t)
	var b []byte
	for i := 0; i < 80; i++ {
		b = append(b, c04Frame(byte(128+i), bytes.Repeat([]byte{byte(i)}, i))...)
		b = p.AppendParameterStatus(b, "x", fmt.Sprint(i))
		b = p.AppendReadyForQuery(b, "ITE"[i%3])
	}
	run := func(r *rand.Rand) []p.CycleEvent {
		s := p.NewReplyTracker(4096, nil)
		for i := 0; i < 80; i++ {
			s.QuerySent()
		}
		var ev []p.CycleEvent
		for off := 0; off < len(b); {
			n := min(1+r.Intn(97), len(b)-off)
			ev, _ = s.Receive(ev, b[off:off+n])
			off += n
		}
		return ev
	}
	want := run(rand.New(rand.NewSource(c04Seed)))
	for i := int64(1); i < 40; i++ {
		if got := run(rand.New(rand.NewSource(c04Seed + i))); !reflect.DeepEqual(got, want) {
			t.Fatalf("partition seed=%d differs", c04Seed+i)
		}
	}
}

func TestAdv_2_HeaderLengthExtremes(t *testing.T) {
	c04Mark(t)
	for _, u := range []bool{false, true} {
		for _, n := range []uint32{0, 1, 3, 0x7fffffff, 0x80000000, 0xffffffff} {
			b := make([]byte, 4)
			binary.BigEndian.PutUint32(b, n)
			if !u {
				b = append([]byte{'Q'}, b...)
			}
			f := p.Framer{Untyped: u, Max: 128}
			consumed, _, err := f.Take(b)
			if err == nil || consumed != len(b) || f.BufferCap() != 0 {
				t.Errorf("untyped=%v length=%d consumed=%d cap=%d err=%v", u, n, consumed, f.BufferCap(), err)
			}
		}
	}
}

func TestAdv_2_InvalidHandshakeEnums(t *testing.T) {
	c04Mark(t)
	for _, side := range []string{"client", "backend"} {
		for _, r := range []p.HandshakeResult{{Outcome: 255}, {Outcome: p.HandshakeCompleted, Version: 255}} {
			t.Run(fmt.Sprint(side, r), func(t *testing.T) {
				defer func() {
					if x := recover(); x != nil {
						t.Errorf("handshake event %+v panicked: %v", r, x)
					}
				}()
				if side == "client" {
					s := p.NewClientSession(c04Config(t, p.ClientTLSAllow))
					s.Receive(nil, p.AppendSSLRequest(nil))
					s.HandshakeDone(nil, r)
				} else {
					s := p.NewBackendSession(p.BackendConfig{TLS: p.BackendTLSRequire, Random: rand.New(rand.NewSource(c04Seed))})
					s.Start(nil)
					s.Receive(nil, []byte{'S'})
					s.HandshakeDone(nil, r)
				}
			})
		}
	}
}

func TestAdv_2_ScramMessageBound(t *testing.T) {
	c04Mark(t)
	s := p.NewScramServer(c04Verifier(t), "srv", false)
	msg := []byte("n,,n=" + strings.Repeat("a", p.MaxScramMessage*4) + ",r=nonce")
	out, cause := s.First(p.ScramMechanism, msg)
	if cause == p.AuthCauseNone {
		t.Fatalf("accepted %d-byte SCRAM input (limit %d), reply=%q", len(msg), p.MaxScramMessage, out)
	}
}

func TestAdv_3_EarlyByteAtEveryRequestCut(t *testing.T) {
	c04Mark(t)
	req := p.AppendSSLRequest(nil)
	for cut := 0; cut <= 8; cut++ {
		s := p.NewClientSession(c04Config(t, p.ClientTLSAllow))
		_, n := s.Receive(nil, req[:cut])
		a, m := s.Receive(nil, append(append([]byte{}, req[cut:]...), 0))
		if n+m != 8 || !s.Closed() || s.Reason() != p.ReasonViolation || bytes.Contains(c04Writes(a), []byte("SCRAM")) {
			t.Fatalf("cut=%d consumed=%d closed=%v acts=%v", cut, n+m, s.Closed(), a)
		}
		s.HandshakeDone(nil, p.HandshakeResult{})
		if s.Upgraded() {
			t.Fatal("late handshake resurrected session")
		}
	}
}

func TestAdv_3_HandshakeReplayCannotUpgradeTwice(t *testing.T) {
	c04Mark(t)
	s := p.NewClientSession(c04Config(t, p.ClientTLSAllow))
	s.Receive(nil, p.AppendSSLRequest(nil))
	s.HandshakeDone(nil, p.HandshakeResult{})
	s.HandshakeDone(nil, p.HandshakeResult{})
	s.Receive(nil, p.AppendGSSRequest(nil))
	a, _ := s.Receive(nil, p.AppendSSLRequest(nil))
	if !s.Closed() || s.Reason() != p.ReasonViolation {
		t.Fatalf("second upgrade not refused: %v", a)
	}
}

func TestAdv_3_BackendCoalescedTLSBytes(t *testing.T) {
	c04Mark(t)
	s := p.NewBackendSession(p.BackendConfig{TLS: p.BackendTLSRequire})
	s.Start(nil)
	a, n := s.Receive(nil, append([]byte{'S'}, c04AuthOK()...))
	if n != 1 || !s.Closed() || s.Class() != p.ClassViolation {
		t.Fatalf("consumed=%d closed=%v actions=%v", n, s.Closed(), a)
	}
	if len(s.HandshakeDone(nil, p.HandshakeResult{})) != 0 {
		t.Fatal("late result emitted actions")
	}
}

func TestAdv_4_RequireCannotBeBypassedByGSSTrain(t *testing.T) {
	c04Mark(t)
	s := p.NewClientSession(c04Config(t, p.ClientTLSRequire))
	a, _ := s.Receive(nil, append(p.AppendGSSRequest(nil), c04Startup()...))
	if s.Ready() || s.Reason() != p.ReasonPlaintextRefused || !bytes.Contains(c04Writes(a), []byte("28000")) {
		t.Fatalf("plaintext accepted or wrong refusal: %v", a)
	}
}

func TestAdv_4_OffCoalescedGSSSSLStartup(t *testing.T) {
	c04Mark(t)
	s := p.NewClientSession(c04Config(t, p.ClientTLSOff))
	a, _ := s.Receive(nil, append(p.AppendSSLRequest(p.AppendGSSRequest(nil)), c04Startup()...))
	if !s.Ready() || !bytes.HasPrefix(c04Writes(a), []byte("NN")) || s.Upgraded() {
		t.Fatalf("off conversation: ready=%v actions=%v", s.Ready(), a)
	}
}

func TestAdv_4_AllowIndependentModes(t *testing.T) {
	c04Mark(t)
	cfg := c04Config(t, p.ClientTLSAllow)
	for i := 0; i < 30; i++ {
		s := p.NewClientSession(cfg)
		if i%2 == 0 {
			s.Receive(nil, p.AppendSSLRequest(nil))
			s.HandshakeDone(nil, p.HandshakeResult{Version: p.TLSVersion13})
		}
		s.Receive(nil, c04Startup())
		if !s.Ready() || s.Upgraded() != (i%2 == 0) {
			t.Fatalf("session %d ready=%v upgraded=%v", i, s.Ready(), s.Upgraded())
		}
	}
}

func TestAdv_5_ProofReplayAcrossNonces(t *testing.T) {
	c04Mark(t)
	_, s, proof := c04Exchange(t, p.GS2NoBinding)
	if _, c := s.Final(proof); c != p.AuthCauseNone {
		t.Fatal(c)
	}
	for i := 0; i < 20; i++ {
		other := p.NewScramServer(c04Verifier(t), fmt.Sprint(i), false)
		other.First(p.ScramMechanism, []byte("n,,n=alice,r=client-fixed-nonce"))
		if _, c := other.Final(proof); c == p.AuthCauseNone {
			t.Fatalf("replayed proof accepted nonce=%d", i)
		}
	}
}

func TestAdv_5_ChannelBindingSubstitution(t *testing.T) {
	c04Mark(t)
	for _, gs := range []string{p.GS2NoBinding, p.GS2ClientSupports} {
		_, s, proof := c04Exchange(t, gs)
		bad := bytes.Replace(proof, []byte("c="+p.ChannelBindingValue(gs)), []byte("c="+p.ChannelBindingValue("p=tls-server-end-point,,")), 1)
		if _, c := s.Final(bad); c != p.AuthCauseChannelBindingMismatch {
			t.Fatalf("GS2=%q observed %v", gs, c)
		}
		_, s, proof = c04Exchange(t, gs)
		if _, c := s.Final(proof); c != p.AuthCauseNone {
			t.Fatalf("matching GS2=%q rejected %v", gs, c)
		}
	}
	for _, gs := range []string{"p=tls-unique,,", "p=tls-server-end-point,,"} {
		s := p.NewScramServer(c04Verifier(t), "srv", false)
		if _, c := s.First(p.ScramMechanism, []byte(gs+"n=alice,r=nonce")); c != p.AuthCauseChannelBindingRefused {
			t.Fatal(c)
		}
	}
}

func TestAdv_5_UnknownUserIterationOracle(t *testing.T) {
	c04Mark(t)
	cfg := c04Config(t, p.ClientTLSOff)
	cfg.Auth = p.AuthSCRAM
	cfg.MockIterations = 0
	first := func(user string) string {
		s := p.NewClientSession(cfg)
		a, _ := s.Receive(nil, p.AppendStartupMessage(nil, []p.Param{{Name: "user", Value: user}, {Name: "database", Value: "db"}}))
		if bytes.Contains(c04Writes(a), []byte(p.ScramPlusMechanism)) {
			t.Fatal("PLUS offered")
		}
		a, _ = s.Receive(nil, p.AppendSASLInitialResponse(nil, p.ScramMechanism, []byte("n,,n=,r=fixednonce")))
		b := c04Writes(a)
		i := bytes.LastIndex(b, []byte(",i="))
		if i < 0 {
			t.Fatalf("missing challenge %q", b)
		}
		return string(b[i:])
	}
	known, unknown := first("alice"), first("ghost")
	if known != unknown {
		t.Fatalf("user enumeration from iteration challenge: known=%s unknown=%s", known, unknown)
	}
}

func TestAdv_6_SignatureBitFlips(t *testing.T) {
	c04Mark(t)
	for i := 0; i < 32; i++ {
		c, s, proof := c04Exchange(t, p.GS2NoBinding)
		sig, cause := s.Final(proof)
		if cause != p.AuthCauseNone {
			t.Fatal(cause)
		}
		raw, err := base64.StdEncoding.DecodeString(string(sig[2:]))
		if err != nil {
			t.Fatal(err)
		}
		raw[i] ^= 1
		sig = []byte("v=" + base64.StdEncoding.EncodeToString(raw))
		if cause := c.Verify(sig); cause == p.AuthCauseNone {
			t.Fatalf("tampered signature accepted byte=%d", i)
		}
	}
}

func TestAdv_6_MechanismOrderAndDuplicates(t *testing.T) {
	c04Mark(t)
	for _, list := range [][]string{{p.ScramPlusMechanism, p.ScramMechanism}, {p.ScramMechanism, p.ScramPlusMechanism}, {"UNKNOWN", p.ScramPlusMechanism, p.ScramMechanism, p.ScramMechanism}} {
		s := c04Backend(nil)
		a, _ := s.Receive(nil, p.AppendAuthenticationSASL(nil, list...))
		b := c04Writes(a)
		if len(b) < 5 {
			t.Fatalf("no initial response: %v", a)
		}
		m, _, ok := p.ParseSASLInitialResponse(b[5:])
		if !ok || m != p.ScramMechanism {
			t.Fatalf("offers=%v selected %q", list, m)
		}
	}
}

func TestAdv_6_AuthOKBeforeServerProof(t *testing.T) {
	c04Mark(t)
	s := c04Backend(nil)
	s.Receive(nil, p.AppendAuthenticationSASL(nil, p.ScramMechanism))
	a, _ := s.Receive(nil, append(c04AuthOK(), p.AppendReadyForQuery(nil, 'I')...))
	if s.Class() != p.ClassAuthFailed || s.Ready() {
		t.Fatalf("skipped server proof class=%v ready=%v acts=%v", s.Class(), s.Ready(), a)
	}
}

func TestAdv_7_ClientFaultOrderConservation(t *testing.T) {
	c04Mark(t)
	for _, first := range []string{"deadline", "eof", "bad"} {
		cfg := c04Config(t, p.ClientTLSAllow)
		s := p.NewClientSession(cfg)
		s.Receive(nil, p.AppendSSLRequest(nil))
		switch first {
		case "deadline":
			s.Deadline(nil)
		case "eof":
			s.PeerClosed(nil)
		case "bad":
			s.Receive(nil, []byte{0})
		}
		before := cfg.Stats.Snapshot()
		s.Deadline(nil)
		s.PeerClosed(nil)
		s.HandshakeDone(nil, p.HandshakeResult{Outcome: p.HandshakeFailed})
		s.Receive(nil, c04Startup())
		after := cfg.Stats.Snapshot()
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("%s post-close counters changed", first)
		}
		if e := after.Check(); e != nil {
			t.Fatal(e)
		}
		want := map[string]p.Reason{"deadline": p.ReasonDeadline, "eof": p.ReasonClientClosed, "bad": p.ReasonViolation}[first]
		if after.Client.StartupsFailed[want] != 1 {
			t.Fatalf("%s snapshot=%+v", first, after.Client)
		}
	}
}

func TestAdv_7_BackendParameterFloodThenFaults(t *testing.T) {
	c04Mark(t)
	stats := &p.WireStats{}
	s := c04Backend(stats)
	s.Receive(nil, c04AuthOK())
	for i := 0; i <= p.MaxParams; i++ {
		s.Receive(nil, p.AppendParameterStatus(nil, fmt.Sprint(i), "v"))
	}
	if s.Class() != p.ClassViolation || len(s.Params()) != p.MaxParams {
		t.Fatalf("class=%v params=%d", s.Class(), len(s.Params()))
	}
	before := stats.Snapshot()
	s.Deadline(nil)
	s.PeerClosed(nil)
	s.Start(nil)
	if after := stats.Snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatal("fault replay changed counters")
	}
	if e := before.Check(); e != nil {
		t.Fatal(e)
	}
}

func TestAdv_7_HandshakeDeadlineClassification(t *testing.T) {
	c04Mark(t)
	cfg := c04Config(t, p.ClientTLSAllow)
	s := p.NewClientSession(cfg)
	s.Receive(nil, p.AppendSSLRequest(nil))
	s.HandshakeDone(nil, p.HandshakeResult{Outcome: p.HandshakeDeadline})
	if s.Reason() != p.ReasonDeadline {
		t.Errorf("client handshake deadline counted as %v, want deadline", s.Reason())
	}
	b := p.NewBackendSession(p.BackendConfig{TLS: p.BackendTLSRequire})
	b.Start(nil)
	b.Receive(nil, []byte{'S'})
	b.HandshakeDone(nil, p.HandshakeResult{Outcome: p.HandshakeDeadline})
	if b.Class() != p.ClassDeadline {
		t.Errorf("backend handshake deadline counted as %v, want deadline", b.Class())
	}
}

func TestAdv_8_AllUnknownTypesAndStatuses(t *testing.T) {
	c04Mark(t)
	s := p.NewReplyTracker(32, nil)
	var off int64
	for i := 128; i < 256; i++ {
		s.QuerySent()
		b := append(c04Frame(byte(i), bytes.Repeat([]byte{1}, 70)), p.AppendReadyForQuery(nil, "ITE"[i%3])...)
		ev, n := s.Receive(nil, b)
		off += int64(len(b))
		if n != len(b) || len(ev) != 2 || ev[0].Kind != p.CycleMessage || ev[0].Known || ev[1].Status != "ITE"[i%3] || ev[1].End != off || s.Outstanding() != 0 {
			t.Fatalf("type=%d events=%v consumed=%d", i, ev, n)
		}
	}
}

func TestAdv_8_LargePartialUnknownNoBuffer(t *testing.T) {
	c04Mark(t)
	s := p.NewReplyTracker(64, nil)
	s.QuerySent()
	b := p.AppendMessageHeader(nil, 0xfe, 16<<20)
	s.Receive(nil, b)
	block := make([]byte, 8192)
	for i := 0; i < 2047; i++ {
		ev, _ := s.Receive(nil, block)
		if len(ev) != 0 || s.BufferCap() != 0 {
			t.Fatalf("chunk=%d events=%v cap=%d", i, ev, s.BufferCap())
		}
	}
	ev := s.PeerClosed(nil)
	if len(ev) != 1 || !ev[0].InCycle || ev[0].Kind != p.CycleEndOfStream {
		t.Fatalf("truncated reply EOF=%v", ev)
	}
}

func TestAdv_8_UnsolicitedBetweenReadyAndNextQuery(t *testing.T) {
	c04Mark(t)
	s := p.NewReplyTracker(1024, nil)
	s.QuerySent()
	s.Receive(nil, p.AppendReadyForQuery(nil, 'T'))
	ev, _ := s.Receive(nil, p.AppendParameterStatus(nil, "search_path", "hostile_schema"))
	if len(ev) != 1 || ev[0].Kind != p.CycleParameterStatus || ev[0].InCycle || ev[0].Value != "hostile_schema" {
		t.Fatalf("unsolicited=%v", ev)
	}
	s.QuerySent()
	ev, _ = s.Receive(nil, append(p.AppendFatal(nil, "57P01", "shutdown"), p.AppendReadyForQuery(nil, 'E')...))
	if len(ev) != 2 || ev[0].Severity != "FATAL" || ev[0].Code != "57P01" || ev[1].Status != 'E' {
		t.Fatalf("fatal/status events=%v", ev)
	}
}

type c04ConstantRandom uint64

func (r c04ConstantRandom) Uint64() uint64 { return uint64(r) }

func TestAdv_9_ConstantRandomSingleIssuer(t *testing.T) {
	c04Mark(t)
	k := p.NewKeyIssuer(c04ConstantRandom(0x7ffffffe))
	seen := map[p.BackendKey]bool{}
	for i := 0; i < 10000; i++ {
		v := k.Issue()
		if seen[v] || v.ProcessID == 0 {
			t.Fatalf("duplicate/zero key at %d: %+v", i, v)
		}
		seen[v] = true
	}
}

func TestAdv_9_ClosedSessionsDoNotRecycleKeys(t *testing.T) {
	c04Mark(t)
	cfg := c04Config(t, p.ClientTLSOff)
	seen := map[p.BackendKey]bool{}
	for i := 0; i < 300; i++ {
		s := p.NewClientSession(cfg)
		s.Receive(nil, c04Startup())
		k := s.Key()
		if !s.Ready() || seen[k] {
			t.Fatalf("session=%d duplicate=%v ready=%v", i, seen[k], s.Ready())
		}
		seen[k] = true
		s.PeerClosed(nil)
	}
}

func c04Sources(t *testing.T) []*ast.File {
	t.Helper()
	names, e := filepath.Glob("*.go")
	if e != nil || len(names) == 0 {
		t.Fatal("missing source", e)
	}
	var out []*ast.File
	for _, n := range names {
		if strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, e := parser.ParseFile(token.NewFileSet(), n, nil, 0)
		if e != nil {
			t.Fatal(e)
		}
		out = append(out, f)
	}
	return out
}

func TestAdv_10_NoIOImports(t *testing.T) {
	c04Mark(t)
	for _, f := range c04Sources(t) {
		for _, im := range f.Imports {
			v := strings.Trim(im.Path.Value, "\"")
			if v == "net" || v == "crypto/tls" || v == "os" || strings.HasPrefix(v, "net/") || strings.HasPrefix(v, "os/") {
				t.Errorf("I/O import %s", v)
			}
		}
	}
}

func TestAdv_10_NoGoroutinesOrClockReads(t *testing.T) {
	c04Mark(t)
	for _, f := range c04Sources(t) {
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.GoStmt:
				t.Error("goroutine in pure package")
			case *ast.CallExpr:
				if s, ok := x.Fun.(*ast.SelectorExpr); ok {
					if id, ok := s.X.(*ast.Ident); ok && id.Name == "time" {
						switch s.Sel.Name {
						case "Now", "Sleep", "After", "NewTimer", "NewTicker":
							t.Errorf("wall clock call time.%s", s.Sel.Name)
						}
					}
				}
			}
			return true
		})
	}
}

func c04ReplayDigest(t *testing.T) string {
	cfg := c04Config(t, p.ClientTLSOff)
	var all []byte
	for i := 0; i < 50; i++ {
		s := p.NewClientSession(cfg)
		a, _ := s.Receive(nil, c04Startup())
		all = append(all, c04Writes(a)...)
		s.PeerClosed(nil)
	}
	return fmt.Sprintf("%x", sha256.Sum256(append(all, []byte(fmt.Sprint(cfg.Stats.Snapshot()))...)))
}

func TestAdv_10_ProcessReplay(t *testing.T) {
	c04Mark(t)
	if os.Getenv("ADV_REPLAY_CHILD") == "1" {
		fmt.Println("DIGEST=" + c04ReplayDigest(t))
		return
	}
	var want string
	for i := 0; i < 3; i++ {
		c := exec.Command(os.Args[0], "-test.run=^TestAdv_10_ProcessReplay$", "-test.v")
		c.Env = append(os.Environ(), "ADV_REPLAY_CHILD=1")
		b, e := c.CombinedOutput()
		if e != nil {
			t.Fatalf("child: %v %s", e, b)
		}
		var got string
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "DIGEST=") {
				got = line
			}
		}
		if got == "" {
			t.Fatal("no digest")
		}
		if i == 0 {
			want = got
		} else if got != want {
			t.Fatalf("process %d digest %s want %s", i, got, want)
		}
	}
}

func TestAdv_2_WireSCRAMBound(t *testing.T) {
	c04Mark(t)
	cfg := c04Config(t, p.ClientTLSOff)
	cfg.Auth = p.AuthSCRAM
	s := p.NewClientSession(cfg)
	s.Receive(nil, c04Startup())
	msg := []byte("n,,n=" + strings.Repeat("a", p.MaxScramMessage) + ",r=nonce")
	a, _ := s.Receive(nil, p.AppendSASLInitialResponse(nil, p.ScramMechanism, msg))
	if !s.Closed() {
		t.Fatalf("wire session accepted %d-byte SCRAM message above %d: reply=%q", len(msg), p.MaxScramMessage, c04Writes(a))
	}
}

func TestAdv_2_SeededHostileCampaign(t *testing.T) {
	c04Mark(t)
	count := 100
	if os.Getenv("ADV_LONG") == "1" {
		count = 50000
	}
	t.Logf("campaign seeds=%d..%d", c04Seed, c04Seed+int64(count)-1)
	for i := 0; i < count; i++ {
		r := rand.New(rand.NewSource(c04Seed + int64(i)))
		cfg := c04Config(t, p.ClientTLSMode(i%3))
		c := p.NewClientSession(cfg)
		bs := &p.WireStats{}
		b := c04Backend(bs)
		f := p.Framer{Max: 256}
		tr := p.NewReplyTracker(256, nil)
		for step := 0; step < 12; step++ {
			x := make([]byte, r.Intn(129))
			r.Read(x)
			if step%4 == 0 {
				x = c04Frame(byte(r.Intn(256)), x)
			}
			switch r.Intn(8) {
			case 0:
				c.Deadline(nil)
				b.Deadline(nil)
			case 1:
				c.PeerClosed(nil)
				b.PeerClosed(nil)
			default:
				c.Receive(nil, x)
				b.Receive(nil, x)
			}
			f.Take(x)
			tr.Receive(nil, x)
			if f.BufferCap() > 512 {
				t.Fatalf("seed=%d step=%d frame cap=%d", c04Seed+int64(i), step, f.BufferCap())
			}
		}
		c.Deadline(nil)
		b.Deadline(nil)
		if e := cfg.Stats.Snapshot().Check(); e != nil {
			t.Fatalf("seed=%d client conservation: %v", c04Seed+int64(i), e)
		}
		if e := bs.Snapshot().Check(); e != nil {
			t.Fatalf("seed=%d backend conservation: %v", c04Seed+int64(i), e)
		}
	}
}

func c04FirstReply(r io.Reader) (string, error) {
	var typ [1]byte
	if _, e := io.ReadFull(r, typ[:]); e != nil {
		return "EOF", nil
	}
	if typ[0] == 'N' || typ[0] == 'S' || typ[0] == 'G' {
		return string(typ[:]), nil
	}
	var h [4]byte
	if _, e := io.ReadFull(r, h[:]); e != nil {
		return "", e
	}
	n := binary.BigEndian.Uint32(h[:])
	if n < 4 || n > 65536 {
		return "", fmt.Errorf("bad response length %d", n)
	}
	b := make([]byte, n-4)
	if _, e := io.ReadFull(r, b); e != nil {
		return "", e
	}
	switch typ[0] {
	case 'E':
		f, ok := p.ParseErrorFields(b)
		if !ok {
			return "", fmt.Errorf("bad error %q", b)
		}
		return "E:" + f.Severity + ":" + f.Code, nil
	case 'R':
		if len(b) < 4 {
			return "", fmt.Errorf("short auth")
		}
		return fmt.Sprintf("R:%d", binary.BigEndian.Uint32(b)), nil
	default:
		return fmt.Sprintf("%c:%x", typ[0], b), nil
	}
}

type c04Rw interface {
	io.Reader
	Write([]byte) (int, error)
}

type c04SimRW struct{ pt.Transport }

func (s c04SimRW) Write(b []byte) (int, error) { e := s.Transport.Write(b); return len(b), e }

func c04Conversation(t *testing.T, c c04Rw, kind string) string {
	t.Helper()
	var b []byte
	var prefix string
	switch kind {
	case "repeat-ssl":
		b = p.AppendSSLRequest(nil)
		if _, e := c.Write(b); e != nil {
			t.Fatal(e)
		}
		v, e := c04FirstReply(c)
		if e != nil {
			t.Fatal(e)
		}
		prefix = v + " "
	case "ssl-with-body":
		b = append(p.AppendSSLRequest(nil), 0, 0, 0, 0)
		binary.BigEndian.PutUint32(b, 12)
	case "startup-missing-terminator":
		b = p.AppendStartupMessage(nil, []p.Param{{Name: "user", Value: "postgres"}, {Name: "database", Value: "postgres"}})
		b = b[:len(b)-1]
		binary.BigEndian.PutUint32(b, uint32(len(b)))
	default:
		t.Fatal(kind)
	}
	if _, e := c.Write(b); e != nil {
		t.Fatal(e)
	}
	v, e := c04FirstReply(c)
	if e != nil {
		t.Fatal(e)
	}
	return prefix + v
}

func c04FidelityAttack(t *testing.T, kind string, live bool) {
	t.Helper()
	c04Mark(t)
	path := "testdata/adv-c04-oracle-" + kind + ".json"
	var want string
	if live {
		addr := os.Getenv("MG_PG_ADDR")
		if addr == "" {
			t.Fatal("MG_PG_ADDR required")
		}
		c, e := net.DialTimeout("tcp", addr, 3*time.Second)
		if e != nil {
			t.Fatal(e)
		}
		defer c.Close()
		if e = c.SetDeadline(time.Now().Add(3 * time.Second)); e != nil {
			t.Fatal(e)
		}
		want = c04Conversation(t, c, kind)
		if os.Getenv("ADV_RECORD") == "1" {
			b, _ := json.MarshalIndent(map[string]string{"response": want, "server": os.Getenv("ADV_PG_VERSION"), "seed": fmt.Sprint(c04Seed)}, "", "  ")
			if e := os.WriteFile(path, append(b, '\n'), 0644); e != nil {
				t.Fatal(e)
			}
		}
	} else {
		b, e := os.ReadFile(path)
		if e != nil {
			t.Fatal("run tier-r.sh to establish independent oracle: ", e)
		}
		var v map[string]string
		if e = json.Unmarshal(b, &v); e != nil {
			t.Fatal(e)
		}
		if !strings.HasPrefix(v["server"], "17.") {
			t.Fatal("oracle is not Postgres 17")
		}
		want = v["response"]
	}
	d, e := pt.NewSimFidelity(c04Seed)
	if e != nil {
		t.Fatal(e)
	}
	c, e := d.Dial(pt.TargetSSLOff)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	got := c04Conversation(t, c04SimRW{c}, kind)
	if got != want {
		t.Fatalf("conversation=%s fake=%q Postgres17=%q", kind, got, want)
	}
}

func TestAdv_11_RepeatedSSLAfterRefusal(t *testing.T) { c04FidelityAttack(t, "repeat-ssl", false) }

func TestTierRAdvC04_11_RepeatedSSLAfterRefusal(t *testing.T) {
	if os.Getenv("MG_TIER_R") != "1" {
		t.Skip("tier R: set MG_TIER_R=1 (scripts/tier-r.sh)")
	}
	c04FidelityAttack(t, "repeat-ssl", true)
}

func TestAdv_11_SSLRequestWithBody(t *testing.T) { c04FidelityAttack(t, "ssl-with-body", false) }

func TestTierRAdvC04_11_SSLRequestWithBody(t *testing.T) {
	if os.Getenv("MG_TIER_R") != "1" {
		t.Skip("tier R: set MG_TIER_R=1 (scripts/tier-r.sh)")
	}
	c04FidelityAttack(t, "ssl-with-body", true)
}

func TestAdv_11_StartupMissingTerminator(t *testing.T) {
	c04FidelityAttack(t, "startup-missing-terminator", false)
}

func TestTierRAdvC04_11_StartupMissingTerminator(t *testing.T) {
	if os.Getenv("MG_TIER_R") != "1" {
		t.Skip("tier R: set MG_TIER_R=1 (scripts/tier-r.sh)")
	}
	c04FidelityAttack(t, "startup-missing-terminator", true)
}
