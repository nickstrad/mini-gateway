package pgwire

import (
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

const dbPassword = "db-secret"

func testBackendConfig(mode BackendTLSMode, seed uint64) BackendConfig {
	return BackendConfig{TLS: mode, User: "pool", Database: "app", Password: dbPassword,
		SessionSettings: DefaultSessionSettings(), Random: rand.New(rand.NewPCG(seed, 5)), Stats: &WireStats{}}
}

type backendRun struct {
	trace    []string
	consumed int
	session  *BackendSession
	stats    WireSnapshot
}

func replayBackend(t testing.TB, cfg BackendConfig, steps []clientStep, chunk func(int, []byte) [][]byte) backendRun {
	t.Helper()
	s := NewBackendSession(cfg)
	acts := s.Start(nil)
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
					t.Fatalf("step %d: consumed %d of %d and still in startup", i, n, len(piece))
				}
				if n < len(piece) {
					break
				}
			}
		}
	}
	return backendRun{trace: traceOf(acts), consumed: total, session: s, stats: cfg.Stats.Snapshot()}
}

func requireBackendEveryChunking(t *testing.T, mode BackendTLSMode, steps []clientStep) backendRun {
	t.Helper()
	base := replayBackend(t, testBackendConfig(mode, 1), steps, whole)
	check := func(label string, r backendRun) {
		t.Helper()
		if strings.Join(r.trace, "\n") != strings.Join(base.trace, "\n") || r.consumed != base.consumed {
			t.Fatalf("%s differs\nwhole:\n%s\n%s:\n%s", label, strings.Join(base.trace, "\n"), label, strings.Join(r.trace, "\n"))
		}
	}
	check("one-byte", replayBackend(t, testBackendConfig(mode, 1), steps, oneByte))
	for si, st := range steps {
		for k := 1; k < len(st.in); k++ {
			split := func(i int, b []byte) [][]byte {
				if i != si {
					return [][]byte{b}
				}
				return [][]byte{b[:k], b[k:]}
			}
			check(fmt.Sprintf("step %d split %d", si, k), replayBackend(t, testBackendConfig(mode, 1), steps, split))
		}
	}
	if err := base.stats.Check(); err != nil {
		t.Fatal(err)
	}
	return base
}

type serverPlan struct {
	tlsAnswer   byte
	handshake   HandshakeOutcome
	mechanisms  []string
	auth        uint32
	password    string
	tamperFinal bool
	skipFinal   bool
	fatalCode   string
	fatalAfter  bool
	readyStatus byte
	wantGS2     string
}

func pgParams() []byte {
	var out []byte
	for _, p := range DefaultServerParams() {
		out = AppendParameterStatus(out, p.Name, p.Value)
	}
	return out
}

func talkBackend(t *testing.T, mode BackendTLSMode, p serverPlan) []clientStep {
	t.Helper()
	s := NewBackendSession(testBackendConfig(mode, 1))
	acts := s.Start(nil)
	var steps []clientStep
	send := func(b []byte) []Action {
		steps = append(steps, clientStep{in: b})
		out, _ := s.Receive(nil, b)
		return out
	}
	if mode != BackendTLSDisable {
		if string(acts[0].Data) != string(AppendSSLRequest(nil)) {
			t.Fatalf("no SSLRequest: %x", acts[0].Data)
		}
		acts = send([]byte{p.tlsAnswer})
		if s.Closed() {
			return steps
		}
		hs := HandshakeResult{Outcome: p.handshake, Version: TLSVersion13}
		steps = append(steps, clientStep{hs: &hs})
		acts = s.HandshakeDone(nil, hs)
		if s.Closed() {
			return steps
		}
	}
	startup := acts[0].Data
	if binary.BigEndian.Uint32(startup[4:]) != ProtocolVersion30 || !strings.Contains(string(startup), "user\x00pool\x00database\x00app\x00client_encoding\x00UTF8\x00") {
		t.Fatalf("startup %q", startup)
	}
	if p.fatalCode != "" && !p.fatalAfter {
		send(AppendFatal(nil, p.fatalCode, "refused"))
		return steps
	}
	switch p.auth {
	case AuthOK:
	case AuthSASL:
		mechs := p.mechanisms
		if mechs == nil {
			mechs = []string{ScramMechanism}
		}
		acts = send(AppendAuthenticationSASL(nil, mechs...))
		if s.Closed() {
			return steps
		}
		msgs, _ := splitTyped(acts[0].Data)
		mech, data, ok := parseSASLInitial(msgs[0].body)
		if !ok || mech != ScramMechanism {
			t.Fatalf("initial response %q", msgs[0].body)
		}
		if p.wantGS2 != "" && !strings.HasPrefix(string(data), p.wantGS2) {
			t.Fatalf("GS2 header %q, want %q", data, p.wantGS2)
		}
		pw := p.password
		if pw == "" {
			pw = dbPassword
		}
		v, _ := NewVerifier(pw, []byte("server-salt-0123"), testIters)
		srv := NewScramServer(v, "servernonce", false)
		first, cause := srv.First(mech, data)
		if cause != AuthCauseNone {
			t.Fatalf("server first: %v", cause)
		}
		acts = send(AppendAuthentication(nil, AuthSASLContinue, first))
		if s.Closed() {
			return steps
		}
		msgs, _ = splitTyped(acts[0].Data)
		final, cause := srv.Final(msgs[0].body)
		if cause != AuthCauseNone {
			send(AppendFatal(nil, CodeInvalidPassword, `password authentication failed for user "pool"`))
			return steps
		}
		if p.tamperFinal {
			final[3] ^= 1
		}
		if !p.skipFinal {
			send(AppendAuthentication(nil, AuthSASLFinal, final))
			if s.Closed() {
				return steps
			}
		}
	default:
		send(AppendAuthentication(nil, p.auth, []byte{1, 2, 3, 4}))
		return steps
	}
	out := AppendAuthentication(nil, AuthOK, nil)
	if p.fatalCode != "" {
		send(append(out, AppendFatal(nil, p.fatalCode, "after auth")...))
		return steps
	}
	out = append(out, pgParams()...)
	out = AppendBackendKeyData(out, BackendKey{ProcessID: 777, Secret: [4]byte{9, 9, 9, 9}})
	status := p.readyStatus
	if status == 0 {
		status = StatusIdle
	}
	send(AppendReadyForQuery(out, status))
	return steps
}

func requireBackendClass(t *testing.T, r backendRun, c Class) {
	t.Helper()
	if !strings.Contains(lastLine(replayResult{trace: r.trace}), "class="+c.String()) || r.stats.Database.StartupsFailed[c] != 1 {
		t.Fatalf("want class %v, trace:\n%s", c, strings.Join(r.trace, "\n"))
	}
}

// TestBackendLogsInWithSCRAM proves BackendSession completes plaintext SCRAM
// login and collects all 14 server parameters and BackendKeyData across every chunking.
func TestBackendLogsInWithSCRAM(t *testing.T) {
	steps := talkBackend(t, BackendTLSDisable, serverPlan{auth: AuthSASL, wantGS2: GS2NoBinding})
	r := requireBackendEveryChunking(t, BackendTLSDisable, steps)
	if !r.session.Ready() || len(r.session.Params()) != 14 || r.session.Key().ProcessID != 777 {
		t.Fatalf("%v %v", r.trace, r.session.Params())
	}
	if !strings.HasPrefix(lastLine(replayResult{trace: r.trace}), "ready") || r.stats.Database.StartupsCompleted != 1 {
		t.Fatal(r.trace)
	}
}

// TestBackendChoosesSCRAMWhenPlusIsOfferedAndPicksTheGS2Header proves
// BackendSession selects SCRAM-SHA-256 even when -PLUS is offered and sends
// GS2 "n,," when -PLUS is offered or in plaintext, and "y,," over TLS without -PLUS.
func TestBackendChoosesSCRAMWhenPlusIsOfferedAndPicksTheGS2Header(t *testing.T) {
	both := []string{ScramPlusMechanism, ScramMechanism}
	steps := talkBackend(t, BackendTLSRequire, serverPlan{tlsAnswer: 'S', auth: AuthSASL, mechanisms: both, wantGS2: GS2NoBinding})
	r := requireBackendEveryChunking(t, BackendTLSRequire, steps)
	if !r.session.Ready() || !r.session.OverTLS() {
		t.Fatal(r.trace)
	}
	steps = talkBackend(t, BackendTLSRequire, serverPlan{tlsAnswer: 'S', auth: AuthSASL, wantGS2: GS2ClientSupports})
	r = requireBackendEveryChunking(t, BackendTLSRequire, steps)
	if !r.session.Ready() {
		t.Fatal(r.trace)
	}
	steps = talkBackend(t, BackendTLSDisable, serverPlan{auth: AuthSASL, mechanisms: both, wantGS2: GS2NoBinding})
	if r := requireBackendEveryChunking(t, BackendTLSDisable, steps); !r.session.Ready() {
		t.Fatal(r.trace)
	}
}

// TestBackendTrust proves BackendSession accepts direct AuthenticationOk (trust auth).
func TestBackendTrust(t *testing.T) {
	r := requireBackendEveryChunking(t, BackendTLSDisable, talkBackend(t, BackendTLSDisable, serverPlan{auth: AuthOK}))
	if !r.session.Ready() {
		t.Fatal(r.trace)
	}
}

// TestBackendFailureClasses proves all 17 database startup failures (TLS refusal,
// handshake errors, auth errors, unsupported methods, 53300, server FATALs, and
// protocol errors) map to their documented Class and ServerCode across all chunkings.
func TestBackendFailureClasses(t *testing.T) {
	cases := []struct {
		name  string
		mode  BackendTLSMode
		plan  serverPlan
		class Class
		code  string
	}{
		{"tls-required-answered-N", BackendTLSRequire, serverPlan{tlsAnswer: 'N'}, ClassTLSRefused, ""},
		{"tls-verify-answered-N", BackendTLSVerifyFull, serverPlan{tlsAnswer: 'N'}, ClassTLSRefused, ""},
		{"tls-answer-garbage", BackendTLSRequire, serverPlan{tlsAnswer: 'E'}, ClassViolation, ""},
		{"handshake-failed", BackendTLSRequire, serverPlan{tlsAnswer: 'S', handshake: HandshakeFailed}, ClassTLSFailed, ""},
		{"certificate-fails-verification", BackendTLSVerifyFull, serverPlan{tlsAnswer: 'S', handshake: HandshakeFailedVerification}, ClassTLSFailed, ""},
		{"wrong-password", BackendTLSDisable, serverPlan{auth: AuthSASL, password: "other"}, ClassAuthFailed, CodeInvalidPassword},
		{"tampered-server-signature", BackendTLSDisable, serverPlan{auth: AuthSASL, tamperFinal: true}, ClassAuthFailed, ""},
		{"server-skips-its-signature", BackendTLSDisable, serverPlan{auth: AuthSASL, skipFinal: true}, ClassAuthFailed, ""},
		{"no-scram-offered", BackendTLSDisable, serverPlan{auth: AuthSASL, mechanisms: []string{"OAUTHBEARER"}}, ClassAuthUnsupported, ""},
		{"md5", BackendTLSDisable, serverPlan{auth: AuthMD5Password}, ClassAuthUnsupported, ""},
		{"cleartext", BackendTLSDisable, serverPlan{auth: AuthCleartextPassword}, ClassAuthUnsupported, ""},
		{"gss", BackendTLSDisable, serverPlan{auth: AuthGSS}, ClassAuthUnsupported, ""},
		{"too-many-connections", BackendTLSDisable, serverPlan{fatalCode: CodeTooManyConnections}, ClassTooManyConnections, CodeTooManyConnections},
		{"invalid-password-fatal", BackendTLSDisable, serverPlan{fatalCode: CodeInvalidPassword}, ClassAuthFailed, CodeInvalidPassword},
		{"unknown-database-after-auth", BackendTLSDisable, serverPlan{auth: AuthOK, fatalCode: CodeUnknownDatabase, fatalAfter: true}, ClassServerFatal, CodeUnknownDatabase},
		{"starting-up", BackendTLSDisable, serverPlan{fatalCode: "57P03"}, ClassServerFatal, "57P03"},
		{"ready-in-transaction", BackendTLSDisable, serverPlan{auth: AuthOK, readyStatus: 'T'}, ClassViolation, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := requireBackendEveryChunking(t, tc.mode, talkBackend(t, tc.mode, tc.plan))
			requireBackendClass(t, r, tc.class)
			if r.session.ServerCode() != tc.code {
				t.Fatalf("code %q", r.session.ServerCode())
			}
		})
	}
}

// TestBackendGarbageSilenceAndEOF proves non-Postgres bytes, oversized headers,
// deadlines, premature EOF, and bytes before HandshakeDone classify cleanly.
func TestBackendGarbageSilenceAndEOF(t *testing.T) {
	r := requireBackendEveryChunking(t, BackendTLSDisable, []clientStep{{in: []byte("HTTP/1.1 400 Bad Request\r\n\r\n")}})
	requireBackendClass(t, r, ClassViolation)
	r = requireBackendEveryChunking(t, BackendTLSDisable, []clientStep{{in: []byte{'R', 0x7f, 0xff, 0xff, 0xff}}})
	requireBackendClass(t, r, ClassViolation)
	if r.stats.Database.Oversized != 1 {
		t.Fatal("oversized not counted")
	}
	r = requireBackendEveryChunking(t, BackendTLSDisable, []clientStep{{deadline: true}})
	requireBackendClass(t, r, ClassDeadline)
	r = requireBackendEveryChunking(t, BackendTLSRequire, []clientStep{{in: []byte{'S'}}, {deadline: true}})
	requireBackendClass(t, r, ClassDeadline)
	if r.stats.Database.TLSFailed[HandshakeDeadline] != 1 {
		t.Fatal("handshake deadline not counted")
	}
	r = requireBackendEveryChunking(t, BackendTLSDisable, []clientStep{{in: AppendAuthenticationSASL(nil, ScramMechanism)[:7]}, {eof: true}})
	requireBackendClass(t, r, ClassLost)
	r = requireBackendEveryChunking(t, BackendTLSRequire, []clientStep{{in: []byte{'S', 'x'}}})
	requireBackendClass(t, r, ClassViolation)
	if !strings.Contains(lastLine(replayResult{trace: r.trace}), "bytes-before-handshake") || r.consumed != 1 {
		t.Fatal(r.trace)
	}
}

// TestBackendConfigValidation proves BackendConfig.Validate rejects missing
// user or random source.
func TestBackendConfigValidation(t *testing.T) {
	if err := testBackendConfig(BackendTLSDisable, 1).Validate(); err != nil {
		t.Fatal(err)
	}
	c := testBackendConfig(BackendTLSDisable, 1)
	c.User = ""
	if c.Validate() == nil {
		t.Fatal("no user accepted")
	}
	c = testBackendConfig(BackendTLSDisable, 1)
	c.Random = nil
	if c.Validate() == nil {
		t.Fatal("no random accepted")
	}
}
