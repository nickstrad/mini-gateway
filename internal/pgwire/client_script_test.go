package pgwire

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

const (
	testDatabase = "app"
	testPassword = "secret"
	testIters    = 64
)

type wireMsg struct {
	typ  byte
	body []byte
}

func splitTyped(b []byte) ([]wireMsg, []byte) {
	var out []wireMsg
	for len(b) >= 5 {
		n := int(binary.BigEndian.Uint32(b[1:5]))
		if n < 4 || len(b) < 1+n {
			break
		}
		out = append(out, wireMsg{typ: b[0], body: b[5 : 1+n]})
		b = b[1+n:]
	}
	return out, b
}

func testTenants(t testing.TB) *TenantTable {
	t.Helper()
	var ts []Tenant
	for _, u := range []string{"alice", "bob"} {
		v, err := NewVerifier(testPassword+"-"+u, []byte("salt-of-"+u+"-0123"), testIters)
		if err != nil {
			t.Fatal(err)
		}
		ts = append(ts, Tenant{User: u, Verifier: v})
	}
	tt, err := NewTenantTable(ts)
	if err != nil {
		t.Fatal(err)
	}
	return tt
}

func passwordOf(user string) string { return testPassword + "-" + user }

func testClientConfig(t testing.TB, mode ClientTLSMode, seed uint64) ClientConfig {
	rnd := rand.New(rand.NewPCG(seed, 99))
	return ClientConfig{TLS: mode, Tenants: testTenants(t), Database: testDatabase, SessionSettings: DefaultSessionSettings(),
		Random: rnd, Keys: NewKeyIssuer(rnd), MockSecret: []byte("mock-secret"), MockIterations: testIters, Stats: &WireStats{}}
}

type clientStep struct {
	in       []byte
	hs       *HandshakeResult
	deadline bool
	eof      bool
}

func describeAction(a Action) string {
	switch a.Kind {
	case ActWrite:
		return "write " + hex.EncodeToString(a.Data)
	case ActReady:
		return fmt.Sprintf("ready user=%s pid=%d secret=%x", a.User, a.Key.ProcessID, a.Key.Secret)
	case ActCancel:
		return fmt.Sprintf("cancel pid=%d secret=%x", a.Key.ProcessID, a.Key.Secret)
	case ActClose:
		return fmt.Sprintf("close reason=%v violation=%v cause=%v code=%s class=%v", a.Reason, a.Violation, a.Cause, a.Code, a.Class)
	}
	return a.Kind.String()
}

func traceOf(acts []Action) []string {
	var out []string
	for _, a := range acts {
		d := describeAction(a)
		if a.Kind == ActWrite && len(out) > 0 && strings.HasPrefix(out[len(out)-1], "write ") {
			out[len(out)-1] += hex.EncodeToString(a.Data)
			continue
		}
		out = append(out, d)
	}
	return out
}

type replayResult struct {
	trace    []string
	consumed int
	session  *ClientSession
	stats    WireSnapshot
}

func replayClient(t testing.TB, cfg ClientConfig, steps []clientStep, chunk func(step int, b []byte) [][]byte) replayResult {
	t.Helper()
	s := NewClientSession(cfg)
	var acts []Action
	total := 0
	for i, st := range steps {
		switch {
		case st.hs != nil:
			acts = s.HandshakeDone(acts, *st.hs)
		case st.deadline:
			acts = s.Deadline(acts)
		case st.eof:
			acts = s.PeerClosed(acts)
		default:
			for _, piece := range chunk(i, st.in) {
				var n int
				acts, n = s.Receive(acts, piece)
				total += n
				if n < len(piece) && !s.Closed() && !s.Ready() {
					t.Fatalf("step %d: consumed %d of %d bytes and still in startup", i, n, len(piece))
				}
				if n < len(piece) {
					break
				}
			}
		}
	}
	return replayResult{trace: traceOf(acts), consumed: total, session: s, stats: cfg.Stats.Snapshot()}
}

func whole(_ int, b []byte) [][]byte { return [][]byte{b} }

func oneByte(_ int, b []byte) [][]byte {
	out := make([][]byte, len(b))
	for i := range b {
		out[i] = b[i : i+1]
	}
	return out
}

func requireEveryChunking(t *testing.T, mkCfg func() ClientConfig, steps []clientStep) replayResult {
	t.Helper()
	base := replayClient(t, mkCfg(), steps, whole)
	check := func(label string, r replayResult) {
		t.Helper()
		if strings.Join(r.trace, "\n") != strings.Join(base.trace, "\n") {
			t.Fatalf("%s: trace differs\nwhole:\n%s\n%s:\n%s", label, strings.Join(base.trace, "\n"), label, strings.Join(r.trace, "\n"))
		}
		if r.consumed != base.consumed {
			t.Fatalf("%s: consumed %d bytes, whole delivery consumed %d", label, r.consumed, base.consumed)
		}
	}
	check("one-byte", replayClient(t, mkCfg(), steps, oneByte))
	for si, st := range steps {
		for k := 1; k < len(st.in); k++ {
			split := func(i int, b []byte) [][]byte {
				if i != si {
					return [][]byte{b}
				}
				return [][]byte{b[:k], b[k:]}
			}
			check(fmt.Sprintf("step %d split at %d", si, k), replayClient(t, mkCfg(), steps, split))
		}
	}
	return base
}

type clientPlan struct {
	sslFirst     bool
	gssFirst     bool
	handshake    *HandshakeResult
	user         string
	password     string
	database     string
	extra        []Param
	gs2          string
	afterUpgrade []byte
}

func startupBytes(user, database string, extra []Param) []byte {
	ps := []Param{{ParamUser, user}}
	if database != "" {
		ps = append(ps, Param{ParamDatabase, database})
	}
	return AppendStartupMessage(nil, append(ps, extra...))
}

func talk(t testing.TB, cfg ClientConfig, p clientPlan) []clientStep {
	t.Helper()
	s := NewClientSession(cfg)
	var steps []clientStep
	send := func(b []byte) []Action {
		steps = append(steps, clientStep{in: b})
		acts, n := s.Receive(nil, b)
		if n != len(b) && !s.Closed() && !s.Ready() {
			t.Fatalf("consumed %d of %d", n, len(b))
		}
		return acts
	}
	if p.gssFirst {
		send(AppendGSSRequest(nil))
	}
	if p.sslFirst {
		acts := send(AppendSSLRequest(nil))
		if len(acts) > 1 && acts[1].Kind == ActStartTLS {
			hs := HandshakeResult{Outcome: HandshakeCompleted, Version: TLSVersion13}
			if p.handshake != nil {
				hs = *p.handshake
			}
			steps = append(steps, clientStep{hs: &hs})
			s.HandshakeDone(nil, hs)
			if s.Closed() {
				return steps
			}
		}
		if p.afterUpgrade != nil {
			send(p.afterUpgrade)
			return steps
		}
	}
	db := p.database
	if db == "" {
		db = testDatabase
	}
	acts := send(startupBytes(p.user, db, p.extra))
	if s.Closed() || s.Ready() {
		return steps
	}
	msgs, _ := splitTyped(acts[0].Data)
	if len(msgs) != 1 || msgs[0].typ != MsgAuthentication || binary.BigEndian.Uint32(msgs[0].body) != AuthSASL {
		t.Fatalf("expected AuthenticationSASL, got %x", acts[0].Data)
	}
	gs2 := p.gs2
	if gs2 == "" {
		gs2 = GS2NoBinding
	}
	c := NewScramClient("", p.password, "clientnonce0123456789", gs2)
	acts = send(AppendSASLInitialResponse(nil, ScramMechanism, c.First()))
	if s.Closed() {
		return steps
	}
	msgs, _ = splitTyped(acts[0].Data)
	final, cause := c.Final(msgs[0].body[4:])
	if cause != AuthCauseNone {
		t.Fatalf("client final: %v", cause)
	}
	send(AppendSASLResponse(nil, final))
	return steps
}
