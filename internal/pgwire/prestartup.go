package pgwire

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	paramReplication = "replication"
	paramOptions     = "options"
	protocolParamPfx = "_pq_."
)

var ErrClientConfig = errors.New("pgwire: bad client session configuration")

func (c ClientConfig) Validate() error {
	switch {
	case c.Random == nil:
		return fmt.Errorf("%w: no random source", ErrClientConfig)
	case c.Keys == nil:
		return fmt.Errorf("%w: no key issuer", ErrClientConfig)
	case c.Database == "":
		return fmt.Errorf("%w: no database name", ErrClientConfig)
	case c.TLS > ClientTLSRequire:
		return fmt.Errorf("%w: TLS mode %d", ErrClientConfig, c.TLS)
	case c.Auth == AuthSCRAM && len(c.MockSecret) == 0:
		return fmt.Errorf("%w: SCRAM needs a mock secret for unknown users", ErrClientConfig)
	}
	return nil
}

func isTooLarge(err error) bool { return errors.Is(err, ErrTooLarge) }

func (s *ClientSession) preStartup(acts []Action, body []byte) []Action {
	p := parseStartupPacket(body)
	switch p.kind {
	case startupSSL:
		return s.tlsRequest(acts)
	case startupGSS:
		if s.gssAsked {
			return s.violation(acts, ViolationSecondGSSRequest, false)
		}
		s.gssAsked = true
		s.st.Requests[RequestGSS][AnswerNo]++
		return s.write(acts, []byte{AnswerTLSNo}, 0)
	case startupCancel:
		s.st.Requests[RequestCancel][AnswerNone]++
		s.st.CancelRequests++
		if s.upgraded {
			s.st.CancelAfterTLS++
		}
		acts = append(acts, Action{Kind: ActCancel, Key: p.key})
		return s.close(acts, Action{Reason: ReasonCancelRequest})
	case startupNormal:
		return s.startup(acts, p)
	}
	return s.violation(acts, ViolationBadStartupPacket, false)
}

func (s *ClientSession) tlsRequest(acts []Action) []Action {
	if s.sslAsked {
		return s.violation(acts, ViolationSecondTLSRequest, false)
	}
	s.sslAsked = true
	if s.cfg.TLS == ClientTLSOff {
		s.st.Requests[RequestTLS][AnswerNo]++
		return s.write(acts, []byte{AnswerTLSNo}, 0)
	}
	s.st.Requests[RequestTLS][AnswerYes]++
	acts = s.write(acts, []byte{AnswerTLSYes}, 0)
	s.state = csTLSWait
	return append(acts, Action{Kind: ActStartTLS})
}

func (s *ClientSession) startup(acts []Action, p startupPacket) []Action {
	if s.cfg.TLS == ClientTLSRequire && !s.upgraded {
		s.st.PlaintextRefused++
		return s.fatal(acts, CodeInvalidAuthorization, "TLS is required for this connection: SSL off", ReasonPlaintextRefused)
	}
	major, minor := p.version>>16, p.version&0xffff
	text := "unsupported frontend protocol " + strconv.Itoa(int(major)) + "." + strconv.Itoa(int(minor)) + ": server supports 3.0 to 3.0"
	switch {
	case major < ProtocolVersion30>>16:
		acts = s.write(acts, AppendLegacyFatal(nil, text), 1)
		return s.close(acts, Action{Reason: ReasonProtocolVersion, Code: CodeFeatureNotSupported})
	case major > ProtocolVersion30>>16:
		return s.fatal(acts, CodeFeatureNotSupported, text, ReasonProtocolVersion)
	}
	var negotiate []byte
	p.params, negotiate = splitProtocolOptions(p.params, minor)
	if negotiate != nil {
		acts = s.write(acts, negotiate, 1)
	}
	user, _ := LookupParam(p.params, ParamUser)
	if user == "" {
		return s.fatal(acts, CodeInvalidAuthorization, "no PostgreSQL user name specified in startup packet", ReasonNoUser)
	}
	if name, ok := s.refusedParam(p.params); !ok {
		return s.fatal(acts, CodeFeatureNotSupported, `unsupported startup parameter "`+name+
			`": it differs from the pool's fixed session settings`, ReasonParamRefused)
	}
	s.user = user
	s.database = user
	if db, ok := LookupParam(p.params, ParamDatabase); ok && db != "" {
		s.database = db
	}
	s.fr = Framer{Max: maxAuthMessage}
	if s.cfg.Auth == AuthTrustForTestsOnly {
		if _, known := s.cfg.Tenants.Lookup(user); !known {
			return s.authFailed(acts, AuthCauseUnknownUser)
		}
		return s.authenticated(acts, nil, 0)
	}
	t, known := s.cfg.Tenants.Lookup(user)
	v := t.Verifier
	if !known {
		v = MockVerifier(s.cfg.MockSecret, user, s.cfg.MockIterations)
	}
	s.scram = NewScramServer(v, NewNonce(s.cfg.Random), !known)
	s.state = csAuthFirst
	return s.write(acts, AppendAuthenticationSASL(nil, ScramMechanism), 1)
}

func (s *ClientSession) refusedParam(params []Param) (string, bool) {
	for _, p := range params {
		switch {
		case p.Name == ParamUser || p.Name == ParamDatabase || p.Name == ParamApplicationName:
			continue
		case p.Name == paramReplication || p.Name == paramOptions:
			return p.Name, false
		}
		want, ok := LookupParam(s.cfg.SessionSettings, p.Name)
		if !ok || !strings.EqualFold(want, p.Value) {
			return p.Name, false
		}
	}
	return "", true
}

func splitProtocolOptions(params []Param, minor uint32) ([]Param, []byte) {
	var kept []Param
	var unknown []string
	for _, p := range params {
		if strings.HasPrefix(p.Name, protocolParamPfx) {
			unknown = append(unknown, p.Name)
			continue
		}
		kept = append(kept, p)
	}
	if minor == 0 && len(unknown) == 0 {
		return kept, nil
	}
	return kept, AppendNegotiateProtocolVersion(nil, ProtocolVersion30, unknown)
}
