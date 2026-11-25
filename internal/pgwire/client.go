package pgwire

import (
	"encoding/binary"
	"time"
)

const (
	// DefaultClientStartupTimeout is the single deadline covering pre-startup
	// negotiation, TLS handshake, and client authentication.
	DefaultClientStartupTimeout = 5 * time.Second
	// maxAuthMessage allows envelope overhead (mechanism name + 4-byte length)
	// above MaxScramMessage; the inner SCRAM payload is bounded separately.
	maxAuthMessage = MaxScramMessage + 64
)

// ClientTLSMode controls how ClientSession answers SSLRequest and whether
// plaintext startup packets are allowed.
type ClientTLSMode uint8

// Supported client-facing TLS modes.
const (
	// ClientTLSOff answers SSLRequest with 'N' and accepts plaintext startup.
	ClientTLSOff ClientTLSMode = iota
	// ClientTLSAllow answers SSLRequest with 'S' and also accepts plaintext startup.
	ClientTLSAllow
	// ClientTLSRequire answers SSLRequest with 'S' and refuses plaintext startup with FATAL 28000.
	ClientTLSRequire
)

// AuthMode selects the client authentication mechanism. Zero is SCRAM-SHA-256.
type AuthMode uint8

// Supported client authentication modes.
const (
	// AuthSCRAM authenticates clients with SCRAM-SHA-256 (default).
	AuthSCRAM AuthMode = iota
	// AuthTrustForTestsOnly skips password verification for known tenants; never a default.
	AuthTrustForTestsOnly
)

// ClientConfig configures a ClientSession state machine.
type ClientConfig struct {
	TLS             ClientTLSMode
	Auth            AuthMode
	Tenants         *TenantTable
	Database        string
	SessionSettings []Param
	ServerParams    []Param
	Random          Random
	Keys            *KeyIssuer
	MockSecret      []byte
	MockIterations  int
	MaxMessage      int
	Stats           *WireStats
	plantEarlyBytes bool
}

// ActionKind identifies an output action emitted by ClientSession or BackendSession.
type ActionKind uint8

// Session output action kinds.
const (
	ActWrite ActionKind = iota + 1
	ActStartTLS
	ActReady
	ActCancel
	ActClose
)

var actionNames = [...]string{"?", "write", "start-tls", "ready", "cancel", "close"}

// String returns the canonical hyphenated name of k.
func (k ActionKind) String() string {
	if int(k) < len(actionNames) {
		return actionNames[k]
	}
	return "?"
}

// Action is one side effect requested by a sans-I/O session machine.
// Only fields relevant to Kind are populated.
type Action struct {
	Kind      ActionKind
	Data      []byte
	Reason    Reason
	Class     Class
	Violation Violation
	Cause     AuthCause
	Code      string
	User      string
	Key       BackendKey
	Params    []Param
}

type clientState uint8

const (
	csPre clientState = iota
	csTLSWait
	csAuthFirst
	csAuthFinal
	csReady
	csClosed
)

// ClientSession is the sans-I/O state machine for a client-facing Postgres
// connection through pre-startup negotiation, optional TLS upgrade, and
// authentication up to ReadyForQuery('I').
type ClientSession struct {
	cfg      ClientConfig
	st       *ClientWireSnapshot
	state    clientState
	fr       Framer
	upgraded bool
	sslAsked bool
	gssAsked bool
	user     string
	database string
	scram    *ScramServer
	key      BackendKey
	reason   Reason
}

// NewClientSession starts a client startup session and increments StartupsBegun
// and InStartup immediately so conservation holds from connection accept.
func NewClientSession(cfg ClientConfig) *ClientSession {
	if cfg.Stats == nil {
		cfg.Stats = &WireStats{}
	}
	s := &ClientSession{cfg: cfg, st: &cfg.Stats.snap.Client}
	s.fr.Untyped = true
	s.st.StartupsBegun++
	s.st.InStartup++
	s.st.InStartupPeak = max(s.st.InStartupPeak, s.st.InStartup)
	return s
}

// Ready reports whether startup succeeded and the session is ready for queries.
func (s *ClientSession) Ready() bool { return s.state == csReady }

// Closed reports whether startup ended without reaching ReadyForQuery.
func (s *ClientSession) Closed() bool { return s.state == csClosed }

// Reason returns the close reason recorded when startup failed or ended.
func (s *ClientSession) Reason() Reason { return s.reason }

// User returns the authenticated startup user name once parsed.
func (s *ClientSession) User() string { return s.user }

// Key returns the proxy-issued BackendKey sent to the client on login success.
func (s *ClientSession) Key() BackendKey { return s.key }

// Upgraded reports whether the client completed a TLS upgrade.
func (s *ClientSession) Upgraded() bool { return s.upgraded }

// AwaitingTLS reports whether the session emitted ActStartTLS and is waiting
// for HandshakeDone.
func (s *ClientSession) AwaitingTLS() bool { return s.state == csTLSWait }

func (s *ClientSession) inStartup() bool { return s.state != csReady && s.state != csClosed }

// Receive feeds wire bytes into the startup machine, appending resulting
// actions to acts and returning how many bytes of b were consumed.
//
// Once ready or closed, Receive consumes nothing so trailing bytes remain
// available to the caller (either query-phase input or smuggled surplus bytes).
func (s *ClientSession) Receive(acts []Action, b []byte) ([]Action, int) {
	n := 0
	for n < len(b) && s.inStartup() {
		if s.state == csTLSWait {
			// Refuse any byte delivered between an accepted SSLRequest and
			// HandshakeDone without consuming it (CVE-2021-23214 class).
			if !s.cfg.plantEarlyBytes {
				acts = s.violation(acts, ViolationBytesBeforeHandshake, false)
				break
			}
			s.state, s.upgraded = csPre, true
		}
		c, done, err := s.fr.Take(b[n:])
		n += c
		if err != nil {
			acts = s.framingError(acts, err)
			break
		}
		if !done {
			break
		}
		s.st.MessagesIn++
		acts = s.handle(acts, s.fr.Frame())
	}
	s.st.BytesIn += int64(n)
	return acts, n
}

func (s *ClientSession) framingError(acts []Action, err error) []Action {
	v := ViolationBadLength
	if isTooLarge(err) {
		v = ViolationTooLarge
		s.st.Oversized++
	}
	return s.violation(acts, v, s.state == csAuthFirst || s.state == csAuthFinal)
}

func (s *ClientSession) handle(acts []Action, f Frame) []Action {
	switch s.state {
	case csPre:
		return s.preStartup(acts, f.Body)
	case csAuthFirst, csAuthFinal:
		return s.authMessage(acts, f)
	}
	return acts
}

func (s *ClientSession) write(acts []Action, data []byte, messages int64) []Action {
	s.st.BytesOut += int64(len(data))
	s.st.MessagesOut += messages
	return append(acts, Action{Kind: ActWrite, Data: data})
}

func (s *ClientSession) fatal(acts []Action, code, message string, r Reason) []Action {
	acts = s.write(acts, AppendFatal(nil, code, message), 1)
	return s.close(acts, Action{Reason: r, Code: code})
}

func (s *ClientSession) close(acts []Action, a Action) []Action {
	if s.state == csClosed {
		return acts
	}
	if s.inStartup() {
		s.st.StartupsFailed[a.Reason]++
		s.st.InStartup--
	}
	s.state = csClosed
	s.scram = nil
	s.reason = a.Reason
	a.Kind = ActClose
	return append(acts, a)
}

func (s *ClientSession) violation(acts []Action, v Violation, reply bool) []Action {
	s.st.Violations[v]++
	if reply {
		acts = s.write(acts, AppendFatal(nil, CodeProtocolViolation, "protocol violation: "+v.String()), 1)
	}
	return s.close(acts, Action{Reason: ReasonViolation, Violation: v})
}

// HandshakeDone delivers the outcome of a TLS handshake requested by
// ActStartTLS. Ignored unless the session is awaiting a handshake.
func (s *ClientSession) HandshakeDone(acts []Action, r HandshakeResult) []Action {
	if s.state != csTLSWait {
		return acts
	}
	r = r.normalized()
	if r.Outcome != HandshakeCompleted {
		s.st.TLSFailed[r.Outcome]++
		switch r.Outcome {
		case HandshakeDeadline:
			return s.close(acts, Action{Reason: ReasonDeadline})
		case HandshakeViolation:
			return s.violation(acts, ViolationBadHandshake, false)
		}
		return s.close(acts, Action{Reason: ReasonTLSFailed})
	}
	s.st.TLSCompleted[r.Version]++
	s.upgraded = true
	s.state = csPre
	return acts
}

// Deadline signals that the client startup timeout expired. Ready or already
// closed sessions ignore it; during SASL authentication a FATAL 08P01 reply is
// sent before closing.
func (s *ClientSession) Deadline(acts []Action) []Action {
	switch s.state {
	case csTLSWait:
		s.st.TLSFailed[HandshakeDeadline]++
	case csAuthFirst, csAuthFinal:
		acts = s.write(acts, AppendFatal(nil, CodeProtocolViolation, "canceling authentication due to timeout"), 1)
	}
	if !s.inStartup() {
		return acts
	}
	return s.close(acts, Action{Reason: ReasonDeadline})
}

// PeerClosed signals EOF or connection reset from the client during startup.
// Ready or already closed sessions ignore it.
func (s *ClientSession) PeerClosed(acts []Action) []Action {
	if !s.inStartup() {
		return acts
	}
	return s.close(acts, Action{Reason: ReasonClientClosed})
}

func (s *ClientSession) authMessage(acts []Action, f Frame) []Action {
	if f.Type == MsgTerminate {
		return s.close(acts, Action{Reason: ReasonClientClosed})
	}
	if f.Type != MsgPassword {
		return s.violation(acts, ViolationUnexpectedMessage, true)
	}
	if s.state == csAuthFirst {
		mech, data, ok := parseSASLInitial(f.Body)
		if !ok {
			return s.violation(acts, ViolationBadMessage, true)
		}
		reply, cause := s.scram.First(mech, data)
		if cause != AuthCauseNone {
			return s.authFailed(acts, cause)
		}
		s.state = csAuthFinal
		return s.write(acts, AppendAuthentication(nil, AuthSASLContinue, reply), 1)
	}
	reply, cause := s.scram.Final(f.Body)
	if cause != AuthCauseNone {
		return s.authFailed(acts, cause)
	}
	s.scram = nil
	return s.authenticated(acts, AppendAuthentication(nil, AuthSASLFinal, reply), 1)
}

func parseSASLInitial(body []byte) (string, []byte, bool) {
	mech, rest, ok := cutCString(body)
	if !ok || len(rest) < lengthFieldLen {
		return "", nil, false
	}
	n := int32(binary.BigEndian.Uint32(rest))
	rest = rest[lengthFieldLen:]
	if n == -1 && len(rest) == 0 {
		return mech, nil, true
	}
	if n < 0 || int(n) != len(rest) {
		return "", nil, false
	}
	return mech, rest, true
}

func (s *ClientSession) authFailed(acts []Action, cause AuthCause) []Action {
	s.st.AuthFailures[cause]++
	code, message := CodeProtocolViolation, "malformed SCRAM message"
	switch cause {
	case AuthCauseWrongPassword, AuthCauseUnknownUser:
		code, message = CodeInvalidPassword, `password authentication failed for user "`+s.user+`"`
	case AuthCauseBadMechanism:
		message = "client selected an invalid SASL authentication mechanism"
	case AuthCauseChannelBindingRefused:
		message = "SCRAM channel binding is not supported by this server"
	case AuthCauseChannelBindingMismatch:
		message = "SCRAM channel binding check failed"
	case AuthCauseNonceMismatch:
		message = "SCRAM nonce does not match"
	}
	acts = s.write(acts, AppendFatal(nil, code, message), 1)
	return s.close(acts, Action{Reason: ReasonAuthFailed, Cause: cause, Code: code})
}

func (s *ClientSession) authenticated(acts []Action, out []byte, messages int64) []Action {
	out = AppendAuthentication(out, AuthOK, nil)
	messages++
	if s.database != s.cfg.Database {
		out = AppendFatal(out, CodeUnknownDatabase, `database "`+s.database+`" does not exist`)
		acts = s.write(acts, out, messages+1)
		return s.close(acts, Action{Reason: ReasonUnknownDatabase, Code: CodeUnknownDatabase})
	}
	params := s.cfg.ServerParams
	if params == nil {
		params = DefaultServerParams()
	}
	for _, p := range params {
		switch p.Name {
		case ParamSessionAuthorization:
			p.Value = s.user
		case ParamIsSuperuser:
			p.Value = "off"
		}
		out = AppendParameterStatus(out, p.Name, p.Value)
		messages++
	}
	s.key = s.cfg.Keys.Issue()
	out = AppendBackendKeyData(out, s.key)
	out = AppendReadyForQuery(out, StatusIdle)
	acts = s.write(acts, out, messages+2)
	s.state = csReady
	s.st.StartupsCompleted++
	s.st.InStartup--
	return append(acts, Action{Kind: ActReady, User: s.user, Key: s.key})
}

// ParseSASLInitialResponse decodes a frontend SASLInitialResponse ('p') body
// into the selected mechanism name and initial client-first payload.
func ParseSASLInitialResponse(body []byte) (string, []byte, bool) { return parseSASLInitial(body) }
