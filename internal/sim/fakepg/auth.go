package fakepg

import (
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/sim/simtls"
)

// AuthMode selects how fakepg authenticates incoming connections.
type AuthMode uint8

// Supported fake Postgres authentication modes (zero keeps chunk 2's trust default).
const (
	AuthTrust AuthMode = iota
	AuthSCRAM
)

// UserVerifier pairs a role name with its SCRAM-SHA-256 verifier for AuthSCRAM.
type UserVerifier struct {
	User     string
	Verifier pgwire.Verifier
}

// TLSOutcome configures how fakepg completes a simulated TLS handshake after
// answering 'S' to SSLRequest.
type TLSOutcome uint8

// Simulated database-side TLS handshake outcomes.
const (
	TLSCompleted TLSOutcome = iota
	TLSFailed
	TLSStall
	TLSBadCertificate
	NumTLSOutcomes
)

// TLSConfig configures fakepg's answer to SSLRequest ('N' or 'S') and its
// simulated handshake outcome, duration, and advertised version.
type TLSConfig struct {
	Answer    byte
	Handshake time.Duration
	Outcome   TLSOutcome
	Version   byte
}

func (c TLSConfig) normalized() TLSConfig {
	if c.Answer != pgwire.AnswerTLSYes {
		c.Answer = pgwire.AnswerTLSNo
	}
	if c.Handshake <= 0 {
		c.Handshake = simtls.DefaultHandshake
	}
	if c.Version == 0 {
		c.Version = simtls.Version13
	}
	return c
}

// AuthSnapshot is a plain value copy of fakepg's TLS and SCRAM startup counters.
type AuthSnapshot struct {
	TLSAnsweredYes     int64
	TLSAnsweredNo      int64
	GSSAnsweredNo      int64
	HandshakesStarted  int64
	HandshakesByResult [NumTLSOutcomes]int64
	BadHellos          int64
	SCRAMStarted       int64
	SCRAMOK            int64
	SCRAMFailed        int64
	ChannelBindingRefs int64
	TLSChanges         int64
}

type authState struct {
	stats      AuthSnapshot
	tls        TLSConfig
	mockSecret []byte
	nonces     *rand.Rand
}

type startStep uint8

const (
	stepPacket startStep = iota
	stepHello
	stepHandshake
	stepSASLFirst
	stepSASLFinal
)

const mockSecretLen = 16

func (srv *Server) initAuth() {
	srv.auth.tls = srv.cfg.TLS.normalized()
	srv.auth.nonces = srv.s.Stream("fakepg/scram")
	r := srv.s.Stream("fakepg/scram-mock")
	srv.auth.mockSecret = make([]byte, mockSecretLen)
	for i := range srv.auth.mockSecret {
		srv.auth.mockSecret[i] = byte(r.Uint32())
	}
}

// SetTLS replaces the server's TLS configuration for subsequent connections.
func (srv *Server) SetTLS(c TLSConfig) {
	srv.auth.tls = c.normalized()
	srv.auth.stats.TLSChanges++
}

// TLS returns the server's current normalized TLS configuration.
func (srv *Server) TLS() TLSConfig { return srv.auth.tls }

// AuthSnapshot returns a plain copy of the server's TLS and SCRAM counters.
func (srv *Server) AuthSnapshot() AuthSnapshot { return srv.auth.stats }

func (b *backend) answerSSL() {
	// Match Postgres 17's ProcessStartupPacket ssl_done/gss_done flags: a
	// repeated SSLRequest fails with FATAL 0A000 (protocol 1234.5679), while a
	// refused SSLRequest clears gssDone and an accepted one latches both.
	if b.sslDone {
		b.refuseProtocol(codeSSLRequest)
		return
	}
	st := &b.srv.auth.stats
	b.sslDone, b.gssDone = true, false
	if b.srv.auth.tls.Answer != pgwire.AnswerTLSYes {
		st.TLSAnsweredNo++
		b.emit([]byte{pgwire.AnswerTLSNo})
		return
	}
	b.gssDone = true
	st.TLSAnsweredYes++
	b.emit([]byte{pgwire.AnswerTLSYes})
	b.step = stepHello
}

func (b *backend) answerGSS() {
	if b.gssDone {
		b.refuseProtocol(codeGSSRequest)
		return
	}
	b.sslDone, b.gssDone = false, true
	b.srv.auth.stats.GSSAnsweredNo++
	b.emit([]byte{pgwire.AnswerTLSNo})
}

func (b *backend) readHello() bool {
	for {
		if raw, ok := b.fr.TakeRaw(simtls.HelloLen); ok {
			b.hello(raw)
			return true
		}
		if b.eof {
			b.after = afterClose
			return true
		}
		n, err := b.end.Read(b.srv.readBuf[:])
		if n > 0 {
			b.srv.stats.bytesIn += int64(n)
			b.fr.Feed(b.srv.readBuf[:n])
			continue
		}
		if err != nil {
			b.eof = true
			continue
		}
		return false
	}
}

func (b *backend) watchHandshake() bool {
	for !b.eof {
		n, err := b.end.Read(b.srv.readBuf[:])
		if n > 0 {
			b.srv.stats.bytesIn += int64(n)
			b.fr.Feed(b.srv.readBuf[:n])
			continue
		}
		if err == nil {
			return false
		}
		b.eof = true
	}
	b.after = afterClose
	return true
}

func (b *backend) hello(raw []byte) {
	st := &b.srv.auth.stats
	if !simtls.ValidClientHello(raw) {
		st.BadHellos++
		b.emit(simtls.AppendServerReply(nil, simtls.ReplyAlert, 0))
		b.after = afterClose
		return
	}
	st.HandshakesStarted++
	cfg := b.srv.auth.tls
	b.step = stepHandshake
	if cfg.Outcome == TLSStall {
		st.HandshakesByResult[TLSStall]++
		return
	}
	b.tlsTimer = b.srv.s.After(cfg.Handshake, func() { b.finishHandshake(cfg) })
}

func (b *backend) finishHandshake(cfg TLSConfig) {
	if b.phase == phaseClosed {
		return
	}
	b.srv.auth.stats.HandshakesByResult[cfg.Outcome]++
	switch cfg.Outcome {
	case TLSFailed:
		b.emit(simtls.AppendServerReply(nil, simtls.ReplyAlert, 0))
		b.after = afterClose
	case TLSBadCertificate:
		b.emit(simtls.AppendServerReply(nil, simtls.ReplyBadCertificate, cfg.Version))
		b.overTLS, b.step = true, stepPacket
	default:
		b.emit(simtls.AppendServerReply(nil, simtls.ReplyOK, cfg.Version))
		b.overTLS, b.step = true, stepPacket
	}
	b.advance()
}

func (srv *Server) verifier(user string) (pgwire.Verifier, bool) {
	if allowed(srv.cfg.Users, user) {
		for _, u := range srv.cfg.Verifiers {
			if u.User == user {
				return u.Verifier, true
			}
		}
	}
	return pgwire.MockVerifier(srv.auth.mockSecret, user, pgwire.DefaultScramIterations), false
}

func (b *backend) beginAuth() {
	v, known := b.srv.verifier(b.user)
	b.scram = pgwire.NewScramServer(v, pgwire.NewNonce(b.srv.auth.nonces), !known)
	b.srv.auth.stats.SCRAMStarted++
	b.fr.Startup = false
	b.step = stepSASLFirst
	if b.overTLS {
		b.emit(pgwire.AppendAuthenticationSASL(nil, pgwire.ScramPlusMechanism, pgwire.ScramMechanism))
		return
	}
	b.emit(pgwire.AppendAuthenticationSASL(nil, pgwire.ScramMechanism))
}

func (b *backend) saslFatal(code, msg string) {
	b.srv.auth.stats.SCRAMFailed++
	b.scram = nil
	b.emit(encError(nil, errorf("FATAL", code, "%s", msg)))
	b.after = afterClose
}

func (b *backend) authMessage(typ byte, body []byte) {
	if typ != 'p' {
		b.saslFatal("08P01", "expected SASL response, got message type "+strconv.Itoa(int(typ)))
		return
	}
	if b.step == stepSASLFirst {
		mech, data, ok := pgwire.ParseSASLInitialResponse(body)
		if !ok {
			b.saslFatal("08P01", "malformed SASL message")
			return
		}
		if mech == pgwire.ScramPlusMechanism && b.overTLS {
			b.srv.auth.stats.ChannelBindingRefs++
			b.saslFatal("08P01", "channel binding is not supported by fakepg")
			return
		}
		reply, cause := b.scram.First(mech, data)
		if cause == pgwire.AuthCauseNone && b.overTLS && b.scram.GS2() == pgwire.GS2ClientSupports {
			b.saslFatal("28000", "SCRAM channel binding negotiation error")
			return
		}
		if cause != pgwire.AuthCauseNone {
			b.saslFatal("08P01", scramFailureText(cause))
			return
		}
		b.step = stepSASLFinal
		b.emit(pgwire.AppendAuthentication(nil, pgwire.AuthSASLContinue, reply))
		return
	}
	reply, cause := b.scram.Final(body)
	switch cause {
	case pgwire.AuthCauseNone:
	case pgwire.AuthCauseWrongPassword, pgwire.AuthCauseUnknownUser:
		b.saslFatal("28P01", `password authentication failed for user "`+b.user+`"`)
		return
	default:
		b.saslFatal("08P01", scramFailureText(cause))
		return
	}
	b.scram = nil
	b.step = stepPacket
	b.srv.auth.stats.SCRAMOK++
	b.emit(pgwire.AppendAuthentication(nil, pgwire.AuthSASLFinal, reply))
	if !allowed(b.srv.cfg.Databases, b.db) {
		b.emit(encAuthOK(nil))
		b.refuseWith(RefuseUnknownDatabase, errorf("FATAL", "3D000", "database %q does not exist", b.db))
		return
	}
	b.ready(b.appName)
}

func scramFailureText(c pgwire.AuthCause) string {
	switch c {
	case pgwire.AuthCauseBadMechanism:
		return "client selected an invalid SASL authentication mechanism"
	case pgwire.AuthCauseChannelBindingMismatch:
		return "SCRAM channel binding check failed"
	}
	return "malformed SCRAM message"
}

func decodeNewerMinor(p startupPacket, body []byte) (startupPacket, error) {
	params, ok := pgwire.ParseStartupParams(body)
	if !ok {
		return p, errBadStartup
	}
	for _, kv := range params {
		switch {
		case strings.HasPrefix(kv.Name, "_pq_."):
			p.unknown = append(p.unknown, kv.Name)
		case kv.Name == "user":
			p.user = kv.Value
		case kv.Name == "database":
			p.db = kv.Value
		case kv.Name == "application_name":
			p.appName = kv.Value
		}
	}
	p.version = protocol30
	p.negotiate = true
	return p, nil
}

func (b *backend) refuseProtocol(version uint32) {
	text := "unsupported frontend protocol " + strconv.Itoa(int(version>>16)) + "." + strconv.Itoa(int(version&0xffff)) + ": server supports 3.0 to 3.0"
	if version>>16 < protocol30>>16 {
		b.srv.stats.refused[RefuseProtocol]++
		b.srv.s.Trace(kindRefuse, b.id, int64(RefuseProtocol), CodeValue("0A000"), 0)
		b.emit(pgwire.AppendLegacyFatal(nil, text))
		b.after = afterClose
		return
	}
	b.refuseWith(RefuseProtocol, errorf("FATAL", "0A000", "%s", text))
}
