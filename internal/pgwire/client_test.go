package pgwire

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"testing"
)

func cfgFor(t *testing.T, mode ClientTLSMode) func() ClientConfig {
	return func() ClientConfig { return testClientConfig(t, mode, 1) }
}

func lastLine(r replayResult) string { return r.trace[len(r.trace)-1] }

func writesOf(r replayResult) []byte {
	var out []byte
	for _, l := range r.trace {
		if h, ok := strings.CutPrefix(l, "write "); ok {
			b, _ := hex.DecodeString(h)
			out = append(out, b...)
		}
	}
	return out
}

func requireClose(t *testing.T, r replayResult, reason Reason) {
	t.Helper()
	if !strings.HasPrefix(lastLine(r), "close reason="+reason.String()+" ") {
		t.Fatalf("want close %v, trace:\n%s", reason, strings.Join(r.trace, "\n"))
	}
	if r.stats.Client.StartupsFailed[reason] != 1 || r.stats.Client.StartupsBegun != 1 {
		t.Fatalf("startups failed by %v = %d", reason, r.stats.Client.StartupsFailed[reason])
	}
	if err := r.stats.Check(); err != nil {
		t.Fatal(err)
	}
}

func requireReady(t *testing.T, r replayResult) {
	t.Helper()
	if !strings.HasPrefix(lastLine(r), "ready user=") {
		t.Fatalf("not ready, trace:\n%s", strings.Join(r.trace, "\n"))
	}
	if r.stats.Client.StartupsCompleted != 1 {
		t.Fatalf("completed %d", r.stats.Client.StartupsCompleted)
	}
	if err := r.stats.Check(); err != nil {
		t.Fatal(err)
	}
}

func typesOf(b []byte) string {
	var sb strings.Builder
	for len(b) > 0 && (b[0] == 'S' || b[0] == 'N') && (len(b) == 1 || b[1] != 0) {
		sb.WriteString("[" + string(b[0]) + "]")
		b = b[1:]
	}
	msgs, rest := splitTyped(b)
	for _, m := range msgs {
		sb.WriteByte(m.typ)
		if m.typ == MsgAuthentication {
			sb.WriteString(itoa(int(binary.BigEndian.Uint32(m.body))))
		}
		if m.typ == MsgErrorResponse {
			e, _ := ParseErrorFields(m.body)
			sb.WriteString(":" + e.Severity + ":" + e.Code)
		}
		sb.WriteByte(' ')
	}
	if len(rest) > 0 {
		sb.WriteString("+" + hex.EncodeToString(rest))
	}
	return strings.TrimSpace(sb.String())
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

const readyShape = "R10 R11 R12 R0 S S S S S S S S S S S S S S K Z"

func TestPlaintextClientLogsInWithSCRAM(t *testing.T) {
	for _, mode := range []ClientTLSMode{ClientTLSOff, ClientTLSAllow} {
		steps := talk(t, cfgFor(t, mode)(), clientPlan{user: "alice", password: passwordOf("alice")})
		r := requireEveryChunking(t, cfgFor(t, mode), steps)
		requireReady(t, r)
		if got := typesOf(writesOf(r)); got != readyShape {
			t.Fatalf("mode %d: reply shape %q", mode, got)
		}
		if r.stats.Client.MessagesIn != 3 || r.stats.Client.BytesIn != int64(r.consumed) || r.stats.Client.BytesOut != int64(len(writesOf(r))) {
			t.Fatalf("counters %+v", r.stats.Client)
		}
	}
}

func TestDefaultStyleClientUpgradesWhenAllowedOrRequired(t *testing.T) {
	for _, mode := range []ClientTLSMode{ClientTLSAllow, ClientTLSRequire} {
		steps := talk(t, cfgFor(t, mode)(), clientPlan{sslFirst: true, user: "bob", password: passwordOf("bob"), gs2: GS2ClientSupports})
		r := requireEveryChunking(t, cfgFor(t, mode), steps)
		requireReady(t, r)
		if r.trace[0] != "write 53" || r.trace[1] != "start-tls" {
			t.Fatalf("pre-startup: %v", r.trace[:2])
		}
		if r.stats.Client.Requests[RequestTLS][AnswerYes] != 1 || r.stats.Client.TLSCompleted[TLSVersion13] != 1 {
			t.Fatalf("counters %+v", r.stats.Client)
		}
		if !r.session.Upgraded() {
			t.Fatal("not upgraded")
		}
	}
}

func TestTLSRequestAnsweredNWhenOff(t *testing.T) {
	steps := talk(t, cfgFor(t, ClientTLSOff)(), clientPlan{sslFirst: true, user: "alice", password: passwordOf("alice")})
	r := requireEveryChunking(t, cfgFor(t, ClientTLSOff), steps)
	requireReady(t, r)
	if !strings.HasPrefix(r.trace[0], "write 4e52") || r.stats.Client.Requests[RequestTLS][AnswerNo] != 1 {
		t.Fatalf("trace %v", r.trace)
	}
}

func TestPlaintextStartupRefusedWhenRequired(t *testing.T) {
	steps := []clientStep{{in: startupBytes("alice", testDatabase, nil)}}
	r := requireEveryChunking(t, cfgFor(t, ClientTLSRequire), steps)
	requireClose(t, r, ReasonPlaintextRefused)
	if got := typesOf(writesOf(r)); got != "E:FATAL:28000" || r.stats.Client.PlaintextRefused != 1 {
		t.Fatalf("reply %q", got)
	}
	gss := []clientStep{{in: AppendGSSRequest(nil)}, {in: startupBytes("alice", testDatabase, nil)}}
	r = requireEveryChunking(t, cfgFor(t, ClientTLSRequire), gss)
	requireClose(t, r, ReasonPlaintextRefused)
}

func TestGSSThenTLSRequest(t *testing.T) {
	for _, mode := range []ClientTLSMode{ClientTLSOff, ClientTLSAllow, ClientTLSRequire} {
		steps := talk(t, cfgFor(t, mode)(), clientPlan{gssFirst: true, sslFirst: true, user: "alice", password: passwordOf("alice")})
		r := requireEveryChunking(t, cfgFor(t, mode), steps)
		requireReady(t, r)
		if !strings.HasPrefix(r.trace[0], "write 4e") || r.stats.Client.Requests[RequestGSS][AnswerNo] != 1 {
			t.Fatalf("mode %d: %v", mode, r.trace)
		}
	}
}

func TestSecondTLSRequestAfterTheUpgradeIsAViolation(t *testing.T) {
	steps := talk(t, cfgFor(t, ClientTLSRequire)(), clientPlan{sslFirst: true, afterUpgrade: AppendSSLRequest(nil)})
	r := requireEveryChunking(t, cfgFor(t, ClientTLSRequire), steps)
	requireClose(t, r, ReasonViolation)
	if !strings.Contains(lastLine(r), "violation=second-tls-request") {
		t.Fatal(lastLine(r))
	}
	off := []clientStep{{in: AppendSSLRequest(nil)}, {in: AppendSSLRequest(nil)}}
	r = requireEveryChunking(t, cfgFor(t, ClientTLSOff), off)
	requireClose(t, r, ReasonViolation)
	gss := []clientStep{{in: AppendGSSRequest(nil)}, {in: AppendGSSRequest(nil)}}
	r = requireEveryChunking(t, cfgFor(t, ClientTLSOff), gss)
	if !strings.Contains(lastLine(r), "violation=second-gss-request") {
		t.Fatal(lastLine(r))
	}
}

func TestBytesSentEarlyAfterAnAcceptedTLSRequest(t *testing.T) {
	smuggled := append(AppendSSLRequest(nil), startupBytes("alice", testDatabase, nil)...)
	for _, mode := range []ClientTLSMode{ClientTLSAllow, ClientTLSRequire} {
		r := requireEveryChunking(t, cfgFor(t, mode), []clientStep{{in: smuggled}})
		requireClose(t, r, ReasonViolation)
		if r.consumed != 8 {
			t.Fatalf("consumed %d bytes, want exactly 8", r.consumed)
		}
		if strings.Join(r.trace[:2], ",") != "write 53,start-tls" || !strings.Contains(lastLine(r), "violation=bytes-before-handshake") {
			t.Fatalf("trace %v", r.trace)
		}
		late := []clientStep{{in: AppendSSLRequest(nil)}, {in: []byte{0x16}}, {hs: &HandshakeResult{Outcome: HandshakeCompleted}}}
		r = requireEveryChunking(t, cfgFor(t, mode), late)
		requireClose(t, r, ReasonViolation)
	}
	r := requireEveryChunking(t, cfgFor(t, ClientTLSOff), []clientStep{{in: smuggled}})
	if !strings.HasPrefix(lastLine(r), "write ") || r.consumed != len(smuggled) {
		t.Fatalf("with TLS off the startup after an N must be read: %v", r.trace)
	}
}

func TestPlantedEarlyBytesBugInterpretsTheSmuggledStartup(t *testing.T) {
	smuggled := append(AppendSSLRequest(nil), startupBytes("alice", testDatabase, nil)...)
	cfg := testClientConfig(t, ClientTLSRequire, 1)
	cfg.plantEarlyBytes = true
	r := replayClient(t, cfg, []clientStep{{in: smuggled}}, whole)
	if r.consumed == 8 || strings.HasPrefix(lastLine(r), "close") {
		t.Fatalf("the planted bug did not interpret the bytes: %v", r.trace)
	}
}

func TestHandshakeFailsOrNeverFinishes(t *testing.T) {
	failed := []clientStep{{in: AppendSSLRequest(nil)}, {hs: &HandshakeResult{Outcome: HandshakeFailed}}}
	r := requireEveryChunking(t, cfgFor(t, ClientTLSRequire), failed)
	requireClose(t, r, ReasonTLSFailed)
	if r.stats.Client.TLSFailed[HandshakeFailed] != 1 || len(writesOf(r)) != 1 {
		t.Fatalf("%+v %v", r.stats.Client.TLSFailed, r.trace)
	}
	stalled := []clientStep{{in: AppendSSLRequest(nil)}, {deadline: true}}
	r = requireEveryChunking(t, cfgFor(t, ClientTLSAllow), stalled)
	requireClose(t, r, ReasonDeadline)
	if r.stats.Client.TLSFailed[HandshakeDeadline] != 1 || len(writesOf(r)) != 1 {
		t.Fatalf("%+v %v", r.stats.Client.TLSFailed, r.trace)
	}
}

func TestCancelRequestBeforeAndAfterTheUpgrade(t *testing.T) {
	k := BackendKey{ProcessID: 4242, Secret: [4]byte{1, 2, 3, 4}}
	r := requireEveryChunking(t, cfgFor(t, ClientTLSRequire), []clientStep{{in: AppendCancelRequest(nil, k)}})
	requireClose(t, r, ReasonCancelRequest)
	if r.trace[0] != "cancel pid=4242 secret=01020304" || len(writesOf(r)) != 0 || r.stats.Client.CancelRequests != 1 {
		t.Fatalf("%v", r.trace)
	}
	after := []clientStep{{in: AppendSSLRequest(nil)}, {hs: &HandshakeResult{Outcome: HandshakeCompleted, Version: TLSVersion12}},
		{in: AppendCancelRequest(nil, k)}}
	r = requireEveryChunking(t, cfgFor(t, ClientTLSAllow), after)
	requireClose(t, r, ReasonCancelRequest)
	if r.stats.Client.CancelAfterTLS != 1 || r.stats.Client.TLSCompleted[TLSVersion12] != 1 {
		t.Fatalf("%+v", r.stats.Client)
	}
}

func TestWrongPasswordAndUnknownUserLookAlike(t *testing.T) {
	wrong := talk(t, cfgFor(t, ClientTLSOff)(), clientPlan{user: "alice", password: "nope"})
	rw := requireEveryChunking(t, cfgFor(t, ClientTLSOff), wrong)
	requireClose(t, rw, ReasonAuthFailed)
	unknown := talk(t, cfgFor(t, ClientTLSOff)(), clientPlan{user: "carol", password: "nope"})
	ru := requireEveryChunking(t, cfgFor(t, ClientTLSOff), unknown)
	requireClose(t, ru, ReasonAuthFailed)
	if typesOf(writesOf(rw)) != "R10 R11 E:FATAL:28P01" || typesOf(writesOf(ru)) != typesOf(writesOf(rw)) {
		t.Fatalf("shapes %q %q", typesOf(writesOf(rw)), typesOf(writesOf(ru)))
	}
	mw, _ := splitTyped(writesOf(rw))
	mu, _ := splitTyped(writesOf(ru))
	for i := range mw {
		if len(mw[i].body) != len(mu[i].body) {
			t.Fatalf("message %d: lengths %d and %d differ", i, len(mw[i].body), len(mu[i].body))
		}
	}
	if !bytes.Equal(bytes.ReplaceAll(mw[2].body, []byte("alice"), []byte("USER")), bytes.ReplaceAll(mu[2].body, []byte("carol"), []byte("USER"))) {
		t.Fatalf("the two FATAL replies differ: %q %q", mw[2].body, mu[2].body)
	}
	if string(mw[2].body) == "" || !strings.Contains(string(mw[2].body), `password authentication failed for user "alice"`) {
		t.Fatalf("reply %q", mw[2].body)
	}
	if rw.stats.Client.AuthFailures[AuthCauseWrongPassword] != 1 || ru.stats.Client.AuthFailures[AuthCauseUnknownUser] != 1 {
		t.Fatal("causes not counted")
	}
}

func TestUnknownUserWithTheRightProofForAnotherVerifierFails(t *testing.T) {
	steps := talk(t, cfgFor(t, ClientTLSOff)(), clientPlan{user: "mallory", password: "anything"})
	r := requireEveryChunking(t, cfgFor(t, ClientTLSOff), steps)
	requireClose(t, r, ReasonAuthFailed)
}

func TestWrongDatabaseIsRefusedAfterAuthentication(t *testing.T) {
	steps := talk(t, cfgFor(t, ClientTLSOff)(), clientPlan{user: "alice", password: passwordOf("alice"), database: "other"})
	r := requireEveryChunking(t, cfgFor(t, ClientTLSOff), steps)
	requireClose(t, r, ReasonUnknownDatabase)
	if got := typesOf(writesOf(r)); got != "R10 R11 R12 R0 E:FATAL:3D000" {
		t.Fatalf("reply %q", got)
	}
}

func TestBadProtocolVersion(t *testing.T) {
	b := startupBytes("alice", testDatabase, nil)
	binary.BigEndian.PutUint32(b[4:], 2<<16)
	r := requireEveryChunking(t, cfgFor(t, ClientTLSOff), []clientStep{{in: b}})
	requireClose(t, r, ReasonProtocolVersion)
	msgs, _ := splitTyped(writesOf(r))
	if typesOf(writesOf(r)) != "E:FATAL:0A000" || !strings.Contains(string(msgs[0].body), "unsupported frontend protocol 2.0: server supports 3.0 to 3.0") {
		t.Fatalf("reply %q", msgs[0].body)
	}
	binary.BigEndian.PutUint32(b[4:], 3<<16|2)
	r = requireEveryChunking(t, cfgFor(t, ClientTLSOff), []clientStep{{in: b}})
	requireClose(t, r, ReasonProtocolVersion)
}

func TestStartupParameters(t *testing.T) {
	ok := talk(t, cfgFor(t, ClientTLSOff)(), clientPlan{user: "alice", password: passwordOf("alice"),
		extra: []Param{{"application_name", "psql"}, {"client_encoding", "utf8"}}})
	requireReady(t, requireEveryChunking(t, cfgFor(t, ClientTLSOff), ok))
	for _, p := range []Param{{"client_encoding", "LATIN1"}, {"search_path", "x"}, {"replication", "true"},
		{"options", "-c work_mem=1GB"}, {"_pq_.foo", "1"}} {
		steps := []clientStep{{in: startupBytes("alice", testDatabase, []Param{p})}}
		r := requireEveryChunking(t, cfgFor(t, ClientTLSOff), steps)
		requireClose(t, r, ReasonParamRefused)
		if typesOf(writesOf(r)) != "E:FATAL:0A000" || !strings.Contains(string(writesOf(r)), `"`+p.Name+`"`) {
			t.Fatalf("%v: %q", p, writesOf(r))
		}
	}
	r := requireEveryChunking(t, cfgFor(t, ClientTLSOff), []clientStep{{in: startupBytes("", testDatabase, nil)}})
	requireClose(t, r, ReasonNoUser)
	if typesOf(writesOf(r)) != "E:FATAL:28000" {
		t.Fatal(typesOf(writesOf(r)))
	}
}

func TestDatabaseDefaultsToTheUserName(t *testing.T) {
	cfg := testClientConfig(t, ClientTLSOff, 1)
	cfg.Database = "alice"
	steps := talk(t, cfg, clientPlan{user: "alice", password: passwordOf("alice"), database: "alice"})
	steps[0].in = startupBytes("alice", "", nil)
	cfg2 := func() ClientConfig { c := testClientConfig(t, ClientTLSOff, 1); c.Database = "alice"; return c }
	requireReady(t, requireEveryChunking(t, cfg2, steps))
}

func TestGS2HeadersThroughTheMachine(t *testing.T) {
	for _, gs2 := range []string{GS2NoBinding, GS2ClientSupports} {
		steps := talk(t, cfgFor(t, ClientTLSOff)(), clientPlan{user: "alice", password: passwordOf("alice"), gs2: gs2})
		requireReady(t, requireEveryChunking(t, cfgFor(t, ClientTLSOff), steps))
	}
	steps := talk(t, cfgFor(t, ClientTLSOff)(), clientPlan{user: "alice", password: passwordOf("alice")})
	steps[1].in = AppendSASLInitialResponse(nil, ScramMechanism, []byte("p=tls-server-end-point,,n=,r=clientnonce0123456789"))
	r := requireEveryChunking(t, cfgFor(t, ClientTLSOff), steps[:2])
	requireClose(t, r, ReasonAuthFailed)
	if r.stats.Client.AuthFailures[AuthCauseChannelBindingRefused] != 1 || typesOf(writesOf(r)) != "R10 E:FATAL:08P01" {
		t.Fatalf("%q", typesOf(writesOf(r)))
	}
	if strings.Contains(string(writesOf(r)), ScramPlusMechanism) {
		t.Fatal("-PLUS offered")
	}
	steps2 := talk(t, cfgFor(t, ClientTLSOff)(), clientPlan{user: "alice", password: passwordOf("alice")})
	steps2[2].in = bytes.Replace(steps2[2].in, []byte("c=biws"), []byte("c=eSws"), 1)
	r = requireEveryChunking(t, cfgFor(t, ClientTLSOff), steps2)
	requireClose(t, r, ReasonAuthFailed)
	if r.stats.Client.AuthFailures[AuthCauseChannelBindingMismatch] != 1 {
		t.Fatalf("%+v", r.stats.Client.AuthFailures)
	}
}

func TestOnlySCRAMSHA256IsOffered(t *testing.T) {
	for _, mode := range []ClientTLSMode{ClientTLSOff, ClientTLSRequire} {
		steps := talk(t, cfgFor(t, mode)(), clientPlan{sslFirst: mode != ClientTLSOff, user: "alice", password: passwordOf("alice")})
		r := replayClient(t, cfgFor(t, mode)(), steps, whole)
		w := writesOf(r)
		i := bytes.Index(w, []byte{MsgAuthentication})
		msgs, _ := splitTyped(w[i:])
		if string(msgs[0].body[4:]) != ScramMechanism+"\x00\x00" {
			t.Fatalf("mechanisms %q", msgs[0].body[4:])
		}
	}
}

func TestTerminationAndEOFDuringStartup(t *testing.T) {
	r := requireEveryChunking(t, cfgFor(t, ClientTLSOff), []clientStep{{in: AppendSSLRequest(nil)}, {eof: true}})
	requireClose(t, r, ReasonClientClosed)
	steps := talk(t, cfgFor(t, ClientTLSOff)(), clientPlan{user: "alice", password: passwordOf("alice")})
	steps = append(steps[:1], clientStep{in: AppendTerminate(nil)})
	r = requireEveryChunking(t, cfgFor(t, ClientTLSOff), steps)
	requireClose(t, r, ReasonClientClosed)
	steps = append(steps[:1], clientStep{deadline: true})
	r = requireEveryChunking(t, cfgFor(t, ClientTLSOff), steps)
	requireClose(t, r, ReasonDeadline)
	if !strings.Contains(typesOf(writesOf(r)), "E:FATAL:08P01") {
		t.Fatal(typesOf(writesOf(r)))
	}
	r = requireEveryChunking(t, cfgFor(t, ClientTLSOff), []clientStep{{deadline: true}})
	requireClose(t, r, ReasonDeadline)
	steps = talk(t, cfgFor(t, ClientTLSOff)(), clientPlan{user: "alice", password: passwordOf("alice")})
	steps = append(steps[:1], clientStep{in: AppendQuery(nil, []byte("SELECT 1"))})
	r = requireEveryChunking(t, cfgFor(t, ClientTLSOff), steps)
	requireClose(t, r, ReasonViolation)
}

func TestTrustIsANamedTestSettingOnly(t *testing.T) {
	var zero ClientConfig
	if zero.Auth != AuthSCRAM {
		t.Fatal("the zero configuration is not SCRAM")
	}
	mk := func() ClientConfig {
		c := testClientConfig(t, ClientTLSOff, 1)
		c.Auth = AuthTrustForTestsOnly
		return c
	}
	r := requireEveryChunking(t, mk, []clientStep{{in: startupBytes("alice", testDatabase, nil)}})
	requireReady(t, r)
	if !strings.HasPrefix(typesOf(writesOf(r)), "R0 S") {
		t.Fatal(typesOf(writesOf(r)))
	}
	r = requireEveryChunking(t, mk, []clientStep{{in: startupBytes("carol", testDatabase, nil)}})
	requireClose(t, r, ReasonAuthFailed)
}

func TestOversizedLengthIsRefusedFromTheHeader(t *testing.T) {
	hdr := []byte{0, 0, 0x4e, 0x30}
	s := NewClientSession(testClientConfig(t, ClientTLSOff, 1))
	acts, n := s.Receive(nil, hdr)
	if n != 4 || !s.Closed() || !strings.Contains(describeAction(acts[len(acts)-1]), "violation=too-large") {
		t.Fatalf("n=%d %v", n, traceOf(acts))
	}
	for _, bad := range [][]byte{{0, 0, 0, 7}, {0, 0, 0, 0}, {0xff, 0xff, 0xff, 0xff}} {
		s := NewClientSession(testClientConfig(t, ClientTLSOff, 1))
		acts, _ := s.Receive(nil, bad)
		if !s.Closed() || len(acts) != 1 {
			t.Fatalf("%x: %v", bad, traceOf(acts))
		}
	}
	steps := talk(t, cfgFor(t, ClientTLSOff)(), clientPlan{user: "alice", password: passwordOf("alice")})
	cfg := testClientConfig(t, ClientTLSOff, 1)
	s = NewClientSession(cfg)
	s.Receive(nil, steps[0].in)
	acts, n = s.Receive(nil, []byte{'p', 0, 0x10, 0, 0})
	if n != 5 || !s.Closed() || cfg.Stats.Snapshot().Client.Oversized != 1 {
		t.Fatalf("n=%d %v", n, traceOf(acts))
	}
}

func TestProxyKeysAreUniqueAndFromTheIssuer(t *testing.T) {
	cfg := testClientConfig(t, ClientTLSOff, 3)
	seen := map[uint32]bool{}
	for i := 0; i < 2000; i++ {
		k := cfg.Keys.Issue()
		if k.ProcessID == 0 || k.ProcessID > maxProcessID || seen[k.ProcessID] {
			t.Fatalf("issue %d: pid %d repeated or out of range", i, k.ProcessID)
		}
		seen[k.ProcessID] = true
	}
	stepsA := talk(t, testClientConfig(t, ClientTLSOff, 3), clientPlan{user: "alice", password: passwordOf("alice")})
	stepsB := talk(t, testClientConfig(t, ClientTLSOff, 4), clientPlan{user: "alice", password: passwordOf("alice")})
	a := replayClient(t, testClientConfig(t, ClientTLSOff, 3), stepsA, whole)
	b := replayClient(t, testClientConfig(t, ClientTLSOff, 4), stepsB, whole)
	if !a.session.Ready() || !b.session.Ready() || a.session.Key() == b.session.Key() {
		t.Fatal("two seeds gave the same key")
	}
}

func TestReceiveAfterReadyConsumesNothing(t *testing.T) {
	steps := talk(t, cfgFor(t, ClientTLSOff)(), clientPlan{user: "alice", password: passwordOf("alice")})
	cfg := testClientConfig(t, ClientTLSOff, 1)
	s := NewClientSession(cfg)
	for _, st := range steps[:len(steps)-1] {
		s.Receive(nil, st.in)
	}
	last := append(append([]byte(nil), steps[len(steps)-1].in...), AppendQuery(nil, []byte("SELECT 1"))...)
	_, n := s.Receive(nil, last)
	if !s.Ready() || n != len(steps[len(steps)-1].in) {
		t.Fatalf("ready=%v consumed %d", s.Ready(), n)
	}
	if acts, n := s.Receive(nil, []byte("x")); n != 0 || len(acts) != 0 {
		t.Fatal("bytes after ready were taken")
	}
	if acts := s.Deadline(nil); len(acts) != 0 || !s.Ready() {
		t.Fatal("a deadline after ready changed the session")
	}
}

func TestClientConfigValidation(t *testing.T) {
	good := testClientConfig(t, ClientTLSOff, 1)
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for i, edit := range []func(*ClientConfig){
		func(c *ClientConfig) { c.Random = nil }, func(c *ClientConfig) { c.Keys = nil },
		func(c *ClientConfig) { c.Database = "" }, func(c *ClientConfig) { c.TLS = 9 },
		func(c *ClientConfig) { c.MockSecret = nil },
	} {
		c := testClientConfig(t, ClientTLSOff, 1)
		edit(&c)
		if c.Validate() == nil {
			t.Errorf("edit %d accepted", i)
		}
	}
}
