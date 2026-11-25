package pgwiretest

import (
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
)

type Transport interface {
	Write(b []byte) error
	Read(buf []byte) (int, error)
	StartTLS() error
	Close()
}

type Dialer interface {
	Dial(target string) (Transport, error)
}

var ErrTimeout = errors.New("pgwiretest: no reply in time")

type Login struct {
	User     string
	Password string
	Database string
	GS2      string
}

type Step struct {
	Name    string
	Target  string
	Request uint32
	TLS     bool
	Version uint32
	Login   *Login
	Extra   []pgwire.Param
}

var KnownDifferences = map[string]string{
	"gss-request": "the postgres:17 image is built with GSSAPI and answers G; the fake answers N, as a server without GSSAPI does and as the proxy does",
}

const (
	TargetSSLOff = "ssl-off"
	TargetSSLOn  = "ssl-on"
	maxTokens    = 256
)

func StartupScript(user, password, database string) []Step {
	login := func(pw, db, gs2 string) *Login {
		return &Login{User: user, Password: pw, Database: db, GS2: gs2}
	}
	return []Step{
		{Name: "tls-request-ssl-off", Target: TargetSSLOff, Request: pgwire.CodeSSLRequest},
		{Name: "tls-request-ssl-on", Target: TargetSSLOn, Request: pgwire.CodeSSLRequest},
		{Name: "gss-request", Target: TargetSSLOn, Request: pgwire.CodeGSSRequest},
		{Name: "scram-login", Target: TargetSSLOn, Login: login(password, database, "")},
		{Name: "scram-login-over-tls", Target: TargetSSLOn, TLS: true, Login: login(password, database, "")},
		{Name: "y-header-over-tls-with-plus-offered", Target: TargetSSLOn, TLS: true, Login: login(password, database, pgwire.GS2ClientSupports)},
		{Name: "wrong-password", Target: TargetSSLOn, Login: login("wrong-"+password, database, "")},
		{Name: "unknown-user", Target: TargetSSLOn, Login: &Login{User: "no_such_user", Password: "x", Database: database}},
		{Name: "unknown-database", Target: TargetSSLOn, Login: login(password, "no_such_database", "")},
		{Name: "protocol-version-2", Target: TargetSSLOn, Version: 2 << 16, Login: login(password, database, "")},
		{Name: "protocol-version-4", Target: TargetSSLOn, Version: 4 << 16, Login: login(password, database, "")},
		{Name: "protocol-version-3.2-negotiated", Target: TargetSSLOn, Version: 3<<16 | 2, Login: login(password, database, ""),
			Extra: []pgwire.Param{{Name: "_pq_.mg_option", Value: "x"}}},
	}
}

type reader struct {
	t    Transport
	buf  []byte
	tmp  [4096]byte
	eof  bool
	rerr error
}

func (r *reader) fill() bool {
	if r.eof {
		return false
	}
	n, err := r.t.Read(r.tmp[:])
	r.buf = append(r.buf, r.tmp[:n]...)
	if err != nil {
		r.eof, r.rerr = true, err
	}
	return n > 0 || !r.eof
}

func (r *reader) byte1() (byte, bool) {
	for len(r.buf) == 0 {
		if !r.fill() {
			return 0, false
		}
	}
	b := r.buf[0]
	r.buf = r.buf[1:]
	return b, true
}

func (r *reader) message() (byte, []byte, bool) {
	for {
		if len(r.buf) >= 5 {
			n := int(binary.BigEndian.Uint32(r.buf[1:5]))
			if n < 4 || n > pgwire.DefaultMaxMessage {
				return 0, nil, false
			}
			if len(r.buf) >= 1+n {
				typ, body := r.buf[0], append([]byte(nil), r.buf[5:1+n]...)
				r.buf = r.buf[1+n:]
				return typ, body, true
			}
		}
		if !r.fill() {
			return 0, nil, false
		}
	}
}

func Run(d Dialer, steps []Step) ([]string, error) {
	var out []string
	for _, st := range steps {
		tokens, err := runStep(d, st)
		if err != nil {
			return out, fmt.Errorf("%s: %w", st.Name, err)
		}
		out = append(out, st.Name+": "+strings.Join(tokens, " "))
	}
	return out, nil
}

func runStep(d Dialer, st Step) ([]string, error) {
	t, err := d.Dial(st.Target)
	if err != nil {
		return nil, err
	}
	defer t.Close()
	r := &reader{t: t}
	if st.Request != 0 {
		b := make([]byte, 8)
		binary.BigEndian.PutUint32(b, 8)
		binary.BigEndian.PutUint32(b[4:], st.Request)
		if err := t.Write(b); err != nil {
			return nil, err
		}
		a, ok := r.byte1()
		if !ok {
			return []string{"EOF"}, nil
		}
		return []string{string(a)}, nil
	}
	overTLS := false
	if st.TLS {
		if err := t.Write(pgwire.AppendSSLRequest(nil)); err != nil {
			return nil, err
		}
		a, ok := r.byte1()
		if !ok || a != pgwire.AnswerTLSYes {
			return []string{"TLS-refused"}, nil
		}
		if err := t.StartTLS(); err != nil {
			return nil, err
		}
		overTLS = true
	}
	l := st.Login
	params := append([]pgwire.Param{{Name: pgwire.ParamUser, Value: l.User}, {Name: pgwire.ParamDatabase, Value: l.Database}}, st.Extra...)
	startup := pgwire.AppendStartupMessage(nil, params)
	if st.Version != 0 {
		binary.BigEndian.PutUint32(startup[4:], st.Version)
	}
	if err := t.Write(startup); err != nil {
		return nil, err
	}
	return converse(t, r, l, overTLS)
}

func converse(t Transport, r *reader, l *Login, overTLS bool) ([]string, error) {
	var tokens []string
	var names []string
	var scram *pgwire.ScramClient
	params := 0
	flush := func() {
		if params > 0 {
			tokens = append(tokens, "S*"+strconv.Itoa(params))
			params = 0
		}
	}
	for len(tokens) < maxTokens {
		typ, body, ok := r.message()
		if !ok {
			flush()
			if len(r.buf) > 0 && r.buf[0] == pgwire.MsgErrorResponse {
				text, _, _ := strings.Cut(string(r.buf[1:]), "\x00")
				return append(tokens, "E-legacy:"+strconv.Quote(text)), nil
			}
			if len(r.buf) > 0 {
				return append(tokens, "GARBAGE"), nil
			}
			return append(tokens, "EOF"), nil
		}
		if typ != pgwire.MsgParameterStatus {
			flush()
		}
		switch typ {
		case pgwire.MsgAuthentication:
			code := binary.BigEndian.Uint32(body)
			data := body[4:]
			tok := "R:" + strconv.Itoa(int(code))
			switch code {
			case pgwire.AuthSASL:
				mechs := cstrings(data)
				tok += "[" + strings.Join(mechs, ",") + "]"
				gs2 := l.GS2
				if gs2 == "" {
					gs2 = pgwire.GS2Header(overTLS, slices.Contains(mechs, pgwire.ScramPlusMechanism))
				}
				scram = pgwire.NewScramClient("", l.Password, "fidelityclientnonce0123", gs2)
				if err := t.Write(pgwire.AppendSASLInitialResponse(nil, pgwire.ScramMechanism, scram.First())); err != nil {
					return nil, err
				}
			case pgwire.AuthSASLContinue:
				final, cause := scram.Final(data)
				if cause != pgwire.AuthCauseNone {
					return nil, fmt.Errorf("SCRAM continue: %v", cause)
				}
				if err := t.Write(pgwire.AppendSASLResponse(nil, final)); err != nil {
					return nil, err
				}
			case pgwire.AuthSASLFinal:
				if cause := scram.Verify(data); cause != pgwire.AuthCauseNone {
					tok += "!" + cause.String()
				}
			}
			tokens = append(tokens, tok)
		case pgwire.MsgParameterStatus:
			params++
			name, _, _ := strings.Cut(string(body), "\x00")
			names = append(names, name)
		case pgwire.MsgBackendKeyData:
			tokens = append(tokens, "K")
		case pgwire.MsgReadyForQuery:
			tokens = append(tokens, "Z:"+string(body))
			slices.Sort(names)
			if len(names) > 0 {
				tokens = append(tokens, "params["+strings.Join(names, ",")+"]")
			}
			return tokens, nil
		case pgwire.MsgErrorResponse, pgwire.MsgNoticeResponse:
			e, _ := pgwire.ParseErrorFields(body)
			tokens = append(tokens, string(typ)+":"+e.Severity+":"+e.Code)
		case pgwire.MsgNegotiateProtocol:
			tokens = append(tokens, "v:"+strconv.Itoa(int(binary.BigEndian.Uint32(body)&0xffff))+"["+strings.Join(cstrings(body[8:]), ",")+"]")
		default:
			tokens = append(tokens, "?"+string(typ))
		}
	}
	return tokens, nil
}

func cstrings(b []byte) []string {
	var out []string
	for len(b) > 0 && b[0] != 0 {
		s, rest, ok := strings.Cut(string(b), "\x00")
		if !ok {
			break
		}
		out = append(out, s)
		b = []byte(rest)
	}
	return out
}
