package pgwire

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"errors"
	"flag"
	"math/rand/v2"
	"os"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/golden.txt")

const goldenFile = "testdata/golden.txt"

type goldenMsg struct {
	name string
	data []byte
}

func producedMessages(t *testing.T) []goldenMsg {
	v := rfcVerifier(t)
	s := NewScramServer(v, rfcServerNonce, false)
	first, _ := s.First(ScramMechanism, []byte(rfcClientFirst))
	final, _ := s.Final([]byte(rfcClientFinal))
	key := BackendKey{ProcessID: 1234567, Secret: [4]byte{0xde, 0xad, 0xbe, 0xef}}
	var params []byte
	for _, p := range DefaultServerParams() {
		params = AppendParameterStatus(params, p.Name, p.Value)
	}
	nonce := NewNonce(rand.New(rand.NewPCG(1, 1)))
	c := NewScramClient("", "pencil", nonce, GS2NoBinding)
	return []goldenMsg{
		{"answer-tls-yes", []byte{AnswerTLSYes}},
		{"answer-tls-no", []byte{AnswerTLSNo}},
		{"authentication-sasl", AppendAuthenticationSASL(nil, ScramMechanism)},
		{"authentication-sasl-continue", AppendAuthentication(nil, AuthSASLContinue, first)},
		{"authentication-sasl-final", AppendAuthentication(nil, AuthSASLFinal, final)},
		{"authentication-ok", AppendAuthentication(nil, AuthOK, nil)},
		{"parameter-status-default-set", params},
		{"backend-key-data", AppendBackendKeyData(nil, key)},
		{"ready-for-query-idle", AppendReadyForQuery(nil, StatusIdle)},
		{"fatal-plaintext-refused", AppendFatal(nil, CodeInvalidAuthorization, "TLS is required for this connection: SSL off")},
		{"fatal-wrong-password", AppendFatal(nil, CodeInvalidPassword, `password authentication failed for user "alice"`)},
		{"fatal-unknown-database", AppendFatal(nil, CodeUnknownDatabase, `database "other" does not exist`)},
		{"fatal-protocol-version-4", AppendFatal(nil, CodeFeatureNotSupported, "unsupported frontend protocol 4.0: server supports 3.0 to 3.0")},
		{"fatal-protocol-version-2-legacy", AppendLegacyFatal(nil, "unsupported frontend protocol 2.0: server supports 3.0 to 3.0")},
		{"negotiate-protocol-version", AppendNegotiateProtocolVersion(nil, ProtocolVersion30, []string{"_pq_.mg_option"})},
		{"fatal-no-user", AppendFatal(nil, CodeInvalidAuthorization, "no PostgreSQL user name specified in startup packet")},
		{"fatal-parameter", AppendFatal(nil, CodeFeatureNotSupported, `unsupported startup parameter "search_path": it differs from the pool's fixed session settings`)},
		{"fatal-auth-timeout", AppendFatal(nil, CodeProtocolViolation, "canceling authentication due to timeout")},
		{"ssl-request", AppendSSLRequest(nil)},
		{"gss-request", AppendGSSRequest(nil)},
		{"cancel-request", AppendCancelRequest(nil, key)},
		{"startup-message", AppendStartupMessage(nil, []Param{{ParamUser, "pool"}, {ParamDatabase, "app"}, {ParamClientEncoding, "UTF8"}})},
		{"sasl-initial-response", AppendSASLInitialResponse(nil, ScramMechanism, c.First())},
		{"sasl-response-rfc7677", AppendSASLResponse(nil, []byte(rfcClientFinal))},
		{"query", AppendQuery(nil, []byte("SELECT 1"))},
		{"terminate", AppendTerminate(nil)},
	}
}

func TestGoldenBytes(t *testing.T) {
	msgs := producedMessages(t)
	var buf bytes.Buffer
	for _, m := range msgs {
		buf.WriteString(m.name + " " + hex.EncodeToString(m.data) + "\n")
	}
	if *updateGolden {
		if err := os.WriteFile(goldenFile, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(goldenFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, buf.Bytes()) {
		t.Fatalf("golden bytes changed; run with -update after checking\nwant:\n%s\ngot:\n%s", want, buf.Bytes())
	}
	sc := bufio.NewScanner(bytes.NewReader(want))
	n := 0
	for sc.Scan() {
		name, _, _ := strings.Cut(sc.Text(), " ")
		if name != msgs[n].name {
			t.Fatalf("line %d is %s", n, name)
		}
		n++
	}
}

func TestGoldenRepliesAreWhatTheMachinesWrite(t *testing.T) {
	steps := talk(t, cfgFor(t, ClientTLSRequire)(), clientPlan{sslFirst: true, user: "alice", password: passwordOf("alice")})
	r := replayClient(t, testClientConfig(t, ClientTLSRequire, 1), steps, whole)
	w := writesOf(r)
	golden := map[string][]byte{}
	for _, m := range producedMessages(t) {
		golden[m.name] = m.data
	}
	for _, name := range []string{"answer-tls-yes", "authentication-sasl", "authentication-ok", "parameter-status-default-set", "ready-for-query-idle"} {
		g := golden[name]
		if name == "parameter-status-default-set" {
			g = bytes.Replace(g, []byte("session_authorization\x00\x00"), []byte("session_authorization\x00alice\x00"), 1)
			g[bytes.Index(g, []byte("session_authorization"))-4+3] += 5
		}
		if !bytes.Contains(w, g) {
			t.Fatalf("the machine did not write %s", name)
		}
	}
}

func TestFramerTakesOnlyWhatItNeeds(t *testing.T) {
	two := append(AppendQuery(nil, []byte("a")), AppendQuery(nil, []byte("bb"))...)
	var f Framer
	n, done, err := f.Take(two)
	if err != nil || !done || n != 7 || string(f.Frame().Body) != "a\x00" {
		t.Fatalf("n=%d done=%v err=%v", n, done, err)
	}
	n, done, err = f.Take(two[7:])
	if err != nil || !done || n != 8 || string(f.Frame().Body) != "bb\x00" {
		t.Fatalf("n=%d done=%v err=%v", n, done, err)
	}
	u := Framer{Untyped: true}
	req := append(AppendSSLRequest(nil), 'x')
	if n, done, _ := u.Take(req); n != 8 || !done {
		t.Fatalf("untyped took %d", n)
	}
}

func TestFramerRefusesFromTheHeaderAlone(t *testing.T) {
	cases := []struct {
		f    Framer
		in   []byte
		want error
	}{
		{Framer{}, []byte{'Q', 0, 0, 0, 3}, ErrBadLength},
		{Framer{Max: 10}, []byte{'Q', 0, 0, 0, 15}, ErrTooLarge},
		{Framer{}, []byte{'Q', 0xff, 0xff, 0xff, 0xff}, ErrTooLarge},
		{Framer{Untyped: true}, []byte{0, 0, 0, 7}, ErrBadLength},
		{Framer{Untyped: true}, []byte{0, 1, 0, 0}, ErrTooLarge},
	}
	for i, tc := range cases {
		n, _, err := tc.f.Take(tc.in)
		if !errors.Is(err, tc.want) || n != len(tc.in) || tc.f.BufferCap() > 0 {
			t.Errorf("case %d: n=%d err=%v cap=%d", i, n, err, tc.f.BufferCap())
		}
		if n2, _, err2 := tc.f.Take([]byte{1}); n2 != 0 || err2 == nil {
			t.Errorf("case %d: framer continued after an error", i)
		}
	}
	var skip Framer
	skip.SkipBodies('D')
	if _, _, err := skip.Take([]byte{'D', 0x10, 0, 0, 0}); err != nil || skip.BufferCap() != 0 {
		t.Fatalf("a skipped 256 MiB body was refused or buffered: %v", err)
	}
}
