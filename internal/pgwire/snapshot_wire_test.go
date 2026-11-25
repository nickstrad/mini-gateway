package pgwire

import (
	"encoding/binary"
	"reflect"
	"strings"
	"testing"
)

func feedClient(t *testing.T, st *WireStats, mode ClientTLSMode, steps []clientStep) {
	t.Helper()
	cfg := testClientConfig(t, mode, 1)
	cfg.Stats = st
	replayClient(t, cfg, steps, whole)
}

func feedBackend(t *testing.T, st *WireStats, mode BackendTLSMode, steps []clientStep) {
	t.Helper()
	cfg := testBackendConfig(mode, 1)
	cfg.Stats = st
	replayBackend(t, cfg, steps, whole)
}

func busyStats(t *testing.T) *WireStats {
	st := &WireStats{}
	ok := talk(t, cfgFor(t, ClientTLSOff)(), clientPlan{user: "alice", password: passwordOf("alice")})
	feedClient(t, st, ClientTLSOff, ok)
	tls := talk(t, cfgFor(t, ClientTLSRequire)(), clientPlan{gssFirst: true, sslFirst: true, user: "alice", password: passwordOf("alice")})
	feedClient(t, st, ClientTLSRequire, tls)
	tls12 := talk(t, cfgFor(t, ClientTLSAllow)(), clientPlan{sslFirst: true, user: "alice", password: passwordOf("alice"),
		handshake: &HandshakeResult{Outcome: HandshakeCompleted, Version: TLSVersion12}})
	feedClient(t, st, ClientTLSAllow, tls12)
	feedClient(t, st, ClientTLSAllow, []clientStep{{in: AppendSSLRequest(nil)}, {hs: &HandshakeResult{Outcome: HandshakeCompleted, Version: TLSVersionOther}}, {eof: true}})
	feedClient(t, st, ClientTLSOff, []clientStep{{in: AppendSSLRequest(nil)}, {eof: true}})
	feedClient(t, st, ClientTLSOff, []clientStep{{deadline: true}})
	feedClient(t, st, ClientTLSRequire, []clientStep{{in: append(AppendSSLRequest(nil), 1)}})
	feedClient(t, st, ClientTLSRequire, []clientStep{{in: AppendSSLRequest(nil)}, {hs: &HandshakeResult{Outcome: HandshakeFailed}}})
	feedClient(t, st, ClientTLSRequire, []clientStep{{in: AppendSSLRequest(nil)}, {hs: &HandshakeResult{Outcome: HandshakeFailedVerification}}})
	feedClient(t, st, ClientTLSRequire, []clientStep{{in: AppendSSLRequest(nil)}, {hs: &HandshakeResult{Outcome: HandshakeViolation}}})
	feedClient(t, st, ClientTLSRequire, []clientStep{{in: AppendSSLRequest(nil)}, {deadline: true}})
	feedClient(t, st, ClientTLSRequire, []clientStep{{in: startupBytes("alice", testDatabase, nil)}})
	bad := startupBytes("alice", testDatabase, nil)
	binary.BigEndian.PutUint32(bad[4:], 2<<16)
	feedClient(t, st, ClientTLSOff, []clientStep{{in: bad}})
	feedClient(t, st, ClientTLSOff, []clientStep{{in: startupBytes("alice", testDatabase, []Param{{"x", "y"}})}})
	feedClient(t, st, ClientTLSOff, []clientStep{{in: startupBytes("", testDatabase, nil)}})
	for _, p := range []clientPlan{{user: "alice", password: "bad"}, {user: "nobody", password: "bad"},
		{user: "alice", password: passwordOf("alice"), database: "nope"}} {
		feedClient(t, st, ClientTLSOff, talk(t, cfgFor(t, ClientTLSOff)(), p))
	}
	k := BackendKey{ProcessID: 1}
	feedClient(t, st, ClientTLSAllow, []clientStep{{in: AppendSSLRequest(nil)}, {hs: &HandshakeResult{Outcome: HandshakeCompleted, Version: TLSVersion13}}, {in: AppendCancelRequest(nil, k)}})
	feedClient(t, st, ClientTLSOff, []clientStep{{in: []byte{0, 1, 0, 0}}})
	feedClient(t, st, ClientTLSOff, []clientStep{{in: []byte{0, 0, 0, 2}}})
	feedClient(t, st, ClientTLSOff, []clientStep{{in: AppendGSSRequest(nil)}, {in: AppendGSSRequest(nil)}})
	feedClient(t, st, ClientTLSOff, []clientStep{{in: AppendSSLRequest(nil)}, {in: AppendSSLRequest(nil)}})
	feedClient(t, st, ClientTLSOff, []clientStep{{in: []byte{0, 0, 0, 12, 4, 210, 22, 47, 0, 0, 0, 0}}})
	steps := talk(t, cfgFor(t, ClientTLSOff)(), clientPlan{user: "alice", password: passwordOf("alice")})
	feedClient(t, st, ClientTLSOff, []clientStep{steps[0], {in: AppendQuery(nil, []byte("x"))}})
	feedClient(t, st, ClientTLSOff, []clientStep{steps[0], {in: cat(AppendMessageHeader(nil, 'p', 3), []byte("abc"))}})
	for _, f := range []func([]byte) []byte{
		func(b []byte) []byte {
			return AppendSASLInitialResponse(nil, ScramPlusMechanism, []byte("p=x,,n=,r=a"))
		},
		func(b []byte) []byte { return AppendSASLInitialResponse(nil, ScramMechanism, []byte("p=x,,n=,r=a")) },
		func(b []byte) []byte { return AppendSASLInitialResponse(nil, ScramMechanism, []byte("n,,r=a")) },
	} {
		feedClient(t, st, ClientTLSOff, []clientStep{steps[0], {in: f(nil)}})
	}
	nonce := talk(t, cfgFor(t, ClientTLSOff)(), clientPlan{user: "alice", password: passwordOf("alice")})
	nonce[2].in = AppendSASLResponse(nil, []byte(strings.Replace(string(nonce[2].in[5:]), ",r=", ",r=X", 1)))
	feedClient(t, st, ClientTLSOff, nonce)
	cb := talk(t, cfgFor(t, ClientTLSOff)(), clientPlan{user: "alice", password: passwordOf("alice"), gs2: GS2ClientSupports})
	cb[2].in = []byte(string(cb[2].in[:7]) + "biws" + string(cb[2].in[11:]))
	feedClient(t, st, ClientTLSOff, cb)
	trust := testClientConfig(t, ClientTLSOff, 1)
	trust.Auth, trust.Stats = AuthTrustForTestsOnly, st
	replayClient(t, trust, []clientStep{{in: startupBytes("alice", testDatabase, nil)}}, whole)

	for _, p := range []struct {
		mode BackendTLSMode
		plan serverPlan
	}{
		{BackendTLSDisable, serverPlan{auth: AuthSASL}},
		{BackendTLSRequire, serverPlan{tlsAnswer: 'S', auth: AuthSASL}},
		{BackendTLSRequire, serverPlan{tlsAnswer: 'N'}},
		{BackendTLSRequire, serverPlan{tlsAnswer: 'Q'}},
		{BackendTLSRequire, serverPlan{tlsAnswer: 'S', handshake: HandshakeFailed}},
		{BackendTLSVerifyFull, serverPlan{tlsAnswer: 'S', handshake: HandshakeFailedVerification}},
		{BackendTLSRequire, serverPlan{tlsAnswer: 'S', handshake: HandshakeViolation}},
		{BackendTLSDisable, serverPlan{auth: AuthSASL, password: "x"}},
		{BackendTLSDisable, serverPlan{auth: AuthSASL, tamperFinal: true}},
		{BackendTLSDisable, serverPlan{auth: AuthMD5Password}},
		{BackendTLSDisable, serverPlan{auth: AuthSASL, mechanisms: []string{"X"}}},
		{BackendTLSDisable, serverPlan{fatalCode: CodeTooManyConnections}},
		{BackendTLSDisable, serverPlan{fatalCode: "57P03"}},
	} {
		feedBackend(t, st, p.mode, talkBackend(t, p.mode, p.plan))
	}
	feedBackend(t, st, BackendTLSDisable, []clientStep{{in: []byte("HTTP/1.1")}})
	feedBackend(t, st, BackendTLSDisable, []clientStep{{in: []byte{'R', 0x7f, 0, 0, 0}}})
	feedBackend(t, st, BackendTLSDisable, []clientStep{{in: []byte{'R', 0, 0, 0, 1}}})
	feedBackend(t, st, BackendTLSDisable, []clientStep{{deadline: true}})
	feedBackend(t, st, BackendTLSRequire, []clientStep{{in: []byte{'S'}}, {deadline: true}})
	feedBackend(t, st, BackendTLSDisable, []clientStep{{eof: true}})
	feedBackend(t, st, BackendTLSRequire, []clientStep{{in: []byte{'S', 'x'}}})
	feedBackend(t, st, BackendTLSDisable, []clientStep{{in: AppendMessageHeader(nil, 'Q', 0)}})
	feedBackend(t, st, BackendTLSDisable, []clientStep{{in: cat(AppendMessageHeader(nil, 'R', 2), []byte{0, 0})}})
	for _, v := range []TLSVersion{TLSVersionOther, TLSVersion12} {
		feedBackend(t, st, BackendTLSRequire, []clientStep{{in: []byte{'S'}}, {hs: &HandshakeResult{Outcome: HandshakeCompleted, Version: v}}, {eof: true}})
	}
	ok2 := talkBackend(t, BackendTLSDisable, serverPlan{auth: AuthSASL})
	ok2[0].in = append(append([]byte(nil), ok2[0].in...), noticeMsg()...)
	feedBackend(t, st, BackendTLSDisable, ok2)

	tr := NewCycleTracker(0, st)
	tr.QuerySent()
	tr.Receive(nil, cat(noticeMsg(), AppendParameterStatus(nil, "a", "b"), AppendMessageHeader(nil, 'A', 0), AppendMessageHeader(nil, 'q', 0),
		AppendErrorResponse(nil, SeverityError, "1", "x"), AppendReadyForQuery(nil, 'E'), AppendReadyForQuery(nil, 'T'), AppendReadyForQuery(nil, 'I')))
	tr.QuerySent()
	tr.Receive(nil, AppendFatal(nil, "57P01", "x"))
	tr.PeerClosed(nil)
	NewCycleTracker(0, st).Receive(nil, AppendMessageHeader(nil, 'Z', 9))
	NewCycleTracker(0, st).Receive(nil, AppendMessageHeader(nil, 'D', MaxRelayedMessage+1))
	ft := NewFrontendTracker(0, st)
	ft.Receive(nil, cat(AppendQuery(nil, []byte("x")), AppendMessageHeader(nil, 'P', 0), AppendTerminate(nil)))
	NewFrontendTracker(0, st).Receive(nil, AppendMessageHeader(nil, '?', 0))
	return st
}

func TestWireCountersMoveAndConserve(t *testing.T) {
	snap := busyStats(t).Snapshot()
	if err := snap.Check(); err != nil {
		t.Fatal(err)
	}
	gauges := map[string]bool{"InStartup": true}
	impossible := map[string]map[int]bool{
		"StartupsFailed": {int(ReasonNone): true},
		"AuthFailures":   {int(AuthCauseNone): true},
		"Violations":     {int(ViolationNone): true},
		"TLSFailed":      {int(HandshakeCompleted): true},
	}
	clientOnly := map[string]map[int]bool{
		"AuthFailures": {int(AuthCauseServerSignature): true, int(AuthCauseUnsupportedMethod): true, int(AuthCauseServerRejected): true, int(AuthCauseNoScramOffered): true},
		"Violations":   {int(ViolationBadTLSAnswer): true},
		"TLSFailed":    {},
	}
	databaseOnly := map[string]map[int]bool{
		"AuthFailures": {int(AuthCauseWrongPassword): true, int(AuthCauseUnknownUser): true, int(AuthCauseBadMechanism): true,
			int(AuthCauseChannelBindingRefused): true, int(AuthCauseChannelBindingMismatch): true, int(AuthCauseNonceMismatch): true, int(AuthCauseMalformed): true},
		"Violations":     {int(ViolationSecondTLSRequest): true, int(ViolationSecondGSSRequest): true, int(ViolationBadStartupPacket): true, int(ViolationUnknownMessage): true},
		"TLSFailed":      {int(HandshakeViolation): true},
		"StartupsFailed": {int(ClassLost): false},
	}
	check := func(side string, v reflect.Value, skip map[string]map[int]bool) {
		var walk func(prefix string, v reflect.Value)
		walk = func(prefix string, v reflect.Value) {
			for i := 0; i < v.NumField(); i++ {
				f, name := v.Field(i), v.Type().Field(i).Name
				switch f.Kind() {
				case reflect.Struct:
					walk(prefix, f)
				case reflect.Int64:
					if !gauges[name] && f.Int() == 0 {
						t.Errorf("%s.%s did not move", side, name)
					}
				case reflect.Array:
					for j := 0; j < f.Len(); j++ {
						e := f.Index(j)
						if e.Kind() == reflect.Array {
							continue
						}
						if impossible[name][j] || skip[name][j] {
							continue
						}
						if e.Int() == 0 {
							t.Errorf("%s.%s[%d] did not move", side, name, j)
						}
					}
				}
			}
		}
		walk(side, v)
	}
	check("client", reflect.ValueOf(snap.Client), clientOnly)
	check("database", reflect.ValueOf(snap.Database), databaseOnly)
	if snap.Client.Requests[RequestTLS][AnswerYes] == 0 || snap.Client.Requests[RequestTLS][AnswerNo] == 0 ||
		snap.Client.Requests[RequestGSS][AnswerNo] == 0 || snap.Client.Requests[RequestCancel][AnswerNone] == 0 {
		t.Errorf("requests by kind and answer: %+v", snap.Client.Requests)
	}
}

func TestWireConservationCatchesAMissingOutcome(t *testing.T) {
	snap := busyStats(t).Snapshot()
	snap.Client.StartupsCompleted--
	if snap.Check() == nil {
		t.Fatal("client conservation not checked")
	}
	snap = busyStats(t).Snapshot()
	snap.Database.StartupsFailed[ClassDeadline]++
	if snap.Check() == nil {
		t.Fatal("database conservation not checked")
	}
	var w *WireStats
	if w.Snapshot() != (WireSnapshot{}) {
		t.Fatal("nil stats")
	}
}
