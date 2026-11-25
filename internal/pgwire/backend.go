package pgwire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DefaultConnectTimeout is the deadline covering database connect, optional
// TLS handshake, and authentication to ReadyForQuery('I').
const DefaultConnectTimeout = 2 * time.Second

// BackendTLSMode selects TLS behavior on the database-facing leg, using
// libpq's mode names.
type BackendTLSMode uint8

// Supported database-facing TLS modes.
const (
	// BackendTLSDisable connects in plaintext without sending SSLRequest.
	BackendTLSDisable BackendTLSMode = iota
	// BackendTLSRequire sends SSLRequest and requires encryption without CA verification.
	BackendTLSRequire
	// BackendTLSVerifyFull requires encryption and full certificate/hostname verification.
	BackendTLSVerifyFull
)

// BackendConfig configures a BackendSession state machine.
type BackendConfig struct {
	TLS             BackendTLSMode
	User            string
	Database        string
	Password        string
	SessionSettings []Param
	Random          Random
	MaxMessage      int
	Stats           *WireStats
}

// ErrBackendConfig is wrapped by BackendConfig.Validate on missing or invalid fields.
var ErrBackendConfig = errors.New("pgwire: bad database session configuration")

// Validate checks that BackendConfig has the fields required to run a database login.
func (c BackendConfig) Validate() error {
	switch {
	case c.User == "":
		return fmt.Errorf("%w: no user", ErrBackendConfig)
	case c.Random == nil:
		return fmt.Errorf("%w: no random source", ErrBackendConfig)
	case c.TLS > BackendTLSVerifyFull:
		return fmt.Errorf("%w: TLS mode %d", ErrBackendConfig, c.TLS)
	}
	return nil
}

type backendState uint8

const (
	bsNew backendState = iota
	bsTLSAnswer
	bsTLSWait
	bsAuth
	bsSASLContinue
	bsSASLFinal
	bsAuthOK
	bsParams
	bsReady
	bsClosed
)

// BackendSession is the sans-I/O state machine for logging a pooled database
// connection in to Postgres, optionally upgrading to TLS, running SCRAM-SHA-256
// or trust auth, and collecting server parameters and BackendKeyData.
type BackendSession struct {
	cfg        BackendConfig
	st         *DatabaseWireSnapshot
	state      backendState
	fr         Framer
	overTLS    bool
	scram      *ScramClient
	params     []Param
	key        BackendKey
	gotKey     bool
	class      Class
	serverCode string
}

// NewBackendSession creates a database startup machine and counts the startup
// as begun immediately. Call Start once the underlying transport connects.
func NewBackendSession(cfg BackendConfig) *BackendSession {
	if cfg.Stats == nil {
		cfg.Stats = &WireStats{}
	}
	s := &BackendSession{cfg: cfg, st: &cfg.Stats.snap.Database}
	s.fr.Max = cfg.MaxMessage
	s.st.StartupsBegun++
	s.st.InStartup++
	s.st.InStartupPeak = max(s.st.InStartupPeak, s.st.InStartup)
	return s
}

// Ready reports whether the database reached ReadyForQuery('I').
func (s *BackendSession) Ready() bool { return s.state == bsReady }

// Closed reports whether the database startup failed or closed.
func (s *BackendSession) Closed() bool { return s.state == bsClosed }

// Class returns the failure class recorded when startup failed.
func (s *BackendSession) Class() Class { return s.class }

// ServerCode returns the SQLSTATE from a server ErrorResponse during startup, if any.
func (s *BackendSession) ServerCode() string { return s.serverCode }

// Params returns the ParameterStatus pairs collected from the server during startup.
func (s *BackendSession) Params() []Param { return s.params }

// Key returns the BackendKeyData sent by the database server.
func (s *BackendSession) Key() BackendKey { return s.key }

// OverTLS reports whether the database connection completed a TLS upgrade.
func (s *BackendSession) OverTLS() bool { return s.overTLS }

// Authenticating reports whether a SASL exchange is currently in flight.
func (s *BackendSession) Authenticating() bool {
	return s.state >= bsSASLContinue && s.state <= bsAuthOK
}

// AwaitingTLS reports whether the server answered 'S' and the session is
// waiting for HandshakeDone.
func (s *BackendSession) AwaitingTLS() bool { return s.state == bsTLSWait }
func (s *BackendSession) inStartup() bool   { return s.state != bsReady && s.state != bsClosed }

func (s *BackendSession) write(acts []Action, data []byte, messages int64) []Action {
	s.st.BytesOut += int64(len(data))
	s.st.MessagesOut += messages
	return append(acts, Action{Kind: ActWrite, Data: data})
}

// Start emits the first outbound message once the transport is connected:
// SSLRequest when TLS is configured, or StartupMessage directly otherwise.
func (s *BackendSession) Start(acts []Action) []Action {
	if s.state != bsNew {
		return acts
	}
	if s.cfg.TLS != BackendTLSDisable {
		s.st.TLSRequests++
		s.state = bsTLSAnswer
		return s.write(acts, AppendSSLRequest(nil), 1)
	}
	return s.sendStartup(acts)
}

func (s *BackendSession) sendStartup(acts []Action) []Action {
	params := []Param{{ParamUser, s.cfg.User}}
	if s.cfg.Database != "" {
		params = append(params, Param{ParamDatabase, s.cfg.Database})
	}
	params = append(params, s.cfg.SessionSettings...)
	s.state = bsAuth
	return s.write(acts, AppendStartupMessage(nil, params), 1)
}

func (s *BackendSession) fail(acts []Action, a Action) []Action {
	if !s.inStartup() {
		return acts
	}
	// Record AnswerNone when startup fails before the server replies to SSLRequest
	// so TLSRequests - sum(TLSAnswers) stays conserved within [0, InStartup].
	if s.state == bsTLSAnswer {
		s.st.TLSAnswers[AnswerNone]++
	}
	s.st.StartupsFailed[a.Class]++
	s.st.InStartup--
	s.state = bsClosed
	// Release SCRAM state (including the plaintext database password) on every exit.
	s.scram = nil
	s.class = a.Class
	a.Kind = ActClose
	return append(acts, a)
}

func (s *BackendSession) violation(acts []Action, v Violation) []Action {
	s.st.Violations[v]++
	return s.fail(acts, Action{Class: ClassViolation, Violation: v})
}

// Receive feeds database bytes into the startup machine and returns resulting
// actions and the number of bytes consumed. Bytes arriving after 'S' and before
// HandshakeDone are rejected without being consumed (CVE-2021-23222 class).
func (s *BackendSession) Receive(acts []Action, b []byte) ([]Action, int) {
	n := 0
	for n < len(b) && s.inStartup() {
		switch s.state {
		case bsNew:
			acts = s.violation(acts, ViolationUnexpectedMessage)
			continue
		case bsTLSWait:
			acts = s.violation(acts, ViolationBytesBeforeHandshake)
			continue
		case bsTLSAnswer:
			acts = s.tlsAnswer(acts, b[n])
			n++
			continue
		}
		c, done, err := s.fr.Take(b[n:])
		n += c
		if err != nil {
			v := ViolationBadLength
			if isTooLarge(err) {
				v = ViolationTooLarge
				s.st.Oversized++
			}
			acts = s.violation(acts, v)
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

func (s *BackendSession) tlsAnswer(acts []Action, answer byte) []Action {
	s.state = bsTLSWait
	switch answer {
	case AnswerTLSYes:
		s.st.TLSAnswers[AnswerYes]++
		return append(acts, Action{Kind: ActStartTLS})
	case AnswerTLSNo:
		s.st.TLSAnswers[AnswerNo]++
		return s.fail(acts, Action{Class: ClassTLSRefused})
	}
	s.st.TLSAnswers[AnswerNone]++
	return s.violation(acts, ViolationBadTLSAnswer)
}

// HandshakeDone delivers the database TLS handshake result after ActStartTLS.
// Ignored unless the session is awaiting a handshake.
func (s *BackendSession) HandshakeDone(acts []Action, r HandshakeResult) []Action {
	if s.state != bsTLSWait {
		return acts
	}
	r = r.normalized()
	if r.Outcome != HandshakeCompleted {
		s.st.TLSFailed[r.Outcome]++
		switch r.Outcome {
		case HandshakeDeadline:
			return s.fail(acts, Action{Class: ClassDeadline})
		case HandshakeViolation:
			return s.violation(acts, ViolationBadHandshake)
		}
		return s.fail(acts, Action{Class: ClassTLSFailed})
	}
	s.st.TLSCompleted[r.Version]++
	s.overTLS = true
	return s.sendStartup(acts)
}

// Deadline signals that the connect/startup deadline expired. Ready or closed
// sessions ignore it.
func (s *BackendSession) Deadline(acts []Action) []Action {
	if s.state == bsTLSWait {
		s.st.TLSFailed[HandshakeDeadline]++
	}
	return s.fail(acts, Action{Class: ClassDeadline})
}

// PeerClosed signals EOF or connection reset from the database during startup.
// Ready or closed sessions ignore it.
func (s *BackendSession) PeerClosed(acts []Action) []Action {
	return s.fail(acts, Action{Class: ClassLost})
}

func (s *BackendSession) handle(acts []Action, f Frame) []Action {
	switch f.Type {
	case MsgErrorResponse:
		return s.serverError(acts, f.Body)
	case MsgNoticeResponse:
		return acts
	case MsgAuthentication:
		if s.state > bsAuthOK || s.state < bsAuth {
			return s.violation(acts, ViolationUnexpectedMessage)
		}
		return s.authRequest(acts, f.Body)
	case MsgParameterStatus, MsgBackendKeyData, MsgReadyForQuery:
		if s.state != bsParams {
			return s.violation(acts, ViolationUnexpectedMessage)
		}
		return s.startupReply(acts, f)
	}
	return s.violation(acts, ViolationUnexpectedMessage)
}

func (s *BackendSession) serverError(acts []Action, body []byte) []Action {
	e, ok := ParseErrorFields(body)
	if !ok {
		return s.violation(acts, ViolationBadMessage)
	}
	s.st.ServerFatals++
	s.serverCode = e.Code
	class := ClassServerFatal
	switch {
	case e.Code == CodeTooManyConnections:
		class = ClassTooManyConnections
	case strings.HasPrefix(e.Code, "28"):
		class = ClassAuthFailed
		s.st.AuthFailures[AuthCauseServerRejected]++
	}
	return s.fail(acts, Action{Class: class, Code: e.Code, Cause: causeIf(class == ClassAuthFailed, AuthCauseServerRejected)})
}

func causeIf(ok bool, c AuthCause) AuthCause {
	if ok {
		return c
	}
	return AuthCauseNone
}

func (s *BackendSession) authFailed(acts []Action, class Class, cause AuthCause) []Action {
	s.st.AuthFailures[cause]++
	return s.fail(acts, Action{Class: class, Cause: cause})
}

func (s *BackendSession) authRequest(acts []Action, body []byte) []Action {
	if len(body) < lengthFieldLen {
		return s.violation(acts, ViolationBadMessage)
	}
	code := binary.BigEndian.Uint32(body)
	data := body[lengthFieldLen:]
	switch {
	case code == AuthOK && (s.state == bsAuth || s.state == bsAuthOK):
		s.state = bsParams
		return acts
	case code == AuthOK && s.state > bsAuth:
		// A server that sends AuthenticationOk without first completing
		// AuthenticationSASLFinal skipped proving it holds ServerKey.
		return s.authFailed(acts, ClassAuthFailed, AuthCauseServerSignature)
	case code == AuthSASL && s.state == bsAuth:
		return s.saslStart(acts, data)
	case code == AuthSASLContinue && s.state == bsSASLContinue:
		reply, cause := s.scram.Final(data)
		if cause != AuthCauseNone {
			return s.authFailed(acts, ClassAuthFailed, cause)
		}
		s.state = bsSASLFinal
		return s.write(acts, AppendSASLResponse(nil, reply), 1)
	case code == AuthSASLFinal && s.state == bsSASLFinal:
		if cause := s.scram.Verify(data); cause != AuthCauseNone {
			return s.authFailed(acts, ClassAuthFailed, cause)
		}
		s.scram = nil
		s.state = bsAuthOK
		return acts
	case code == AuthCleartextPassword || code == AuthMD5Password || code == AuthGSS || code == AuthSSPI ||
		code == AuthKerberosV5 || code == AuthSCMCredential || code == AuthGSSContinue:
		return s.authFailed(acts, ClassAuthUnsupported, AuthCauseUnsupportedMethod)
	}
	return s.violation(acts, ViolationUnexpectedMessage)
}

func (s *BackendSession) saslStart(acts []Action, data []byte) []Action {
	scram, plus := false, false
	for len(data) > 0 && data[0] != 0 {
		m, rest, ok := cutCString(data)
		if !ok {
			return s.violation(acts, ViolationBadMessage)
		}
		scram = scram || m == ScramMechanism
		plus = plus || m == ScramPlusMechanism
		data = rest
	}
	if len(data) != 1 {
		return s.violation(acts, ViolationBadMessage)
	}
	if !scram {
		return s.authFailed(acts, ClassAuthUnsupported, AuthCauseNoScramOffered)
	}
	s.scram = NewScramClient("", s.cfg.Password, NewNonce(s.cfg.Random), GS2Header(s.overTLS, plus))
	s.state = bsSASLContinue
	return s.write(acts, AppendSASLInitialResponse(nil, ScramMechanism, s.scram.First()), 1)
}

func (s *BackendSession) startupReply(acts []Action, f Frame) []Action {
	switch f.Type {
	case MsgParameterStatus:
		name, rest, ok1 := cutCString(f.Body)
		value, rest, ok2 := cutCString(rest)
		if !ok1 || !ok2 || len(rest) != 0 {
			return s.violation(acts, ViolationBadMessage)
		}
		var ok bool
		if s.params, ok = setParam(s.params, name, value); !ok {
			return s.violation(acts, ViolationTooLarge)
		}
		return acts
	case MsgBackendKeyData:
		if len(f.Body) != lengthFieldLen+keySecretLen {
			return s.violation(acts, ViolationBadMessage)
		}
		s.key.ProcessID = binary.BigEndian.Uint32(f.Body)
		copy(s.key.Secret[:], f.Body[lengthFieldLen:])
		s.gotKey = true
		return acts
	}
	if len(f.Body) != 1 || f.Body[0] != StatusIdle {
		return s.violation(acts, ViolationBadMessage)
	}
	s.state = bsReady
	s.st.StartupsCompleted++
	s.st.InStartup--
	return append(acts, Action{Kind: ActReady, Params: s.params, Key: s.key})
}
