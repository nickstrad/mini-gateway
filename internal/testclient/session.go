package testclient

import (
	"errors"
	"fmt"
)

type Phase uint8

const (
	PhaseNew Phase = iota
	PhasePreStartup
	PhaseStartup
	PhaseReady
	PhaseBusy
	PhaseClosed
)

const MaxParams = 64

var (
	ErrProtocol = errors.New("testclient: protocol violation")
	ErrClosed   = errors.New("testclient: session closed")
)

type Session struct {
	MaxMessage int
	fr         Framer
	phase      Phase
	status     byte
	preAnswers int
	pid        uint32
	secret     []byte
	params     []Param
	pending    int
	err        error
}

func (s *Session) Phase() Phase      { return s.phase }
func (s *Session) Status() byte      { return s.status }
func (s *Session) ProcessID() uint32 { return s.pid }
func (s *Session) Secret() []byte    { return s.secret }
func (s *Session) Params() []Param   { return s.params }
func (s *Session) Pending() int      { return s.pending }
func (s *Session) Err() error        { return s.err }

func (s *Session) PreStartup(dst []byte, ssl, gss bool) []byte {
	if gss {
		dst = EncodeGSSRequest(dst)
		s.preAnswers++
	}
	if ssl {
		dst = EncodeSSLRequest(dst)
		s.preAnswers++
	}
	if s.preAnswers > 0 {
		s.phase = PhasePreStartup
	}
	return dst
}

func (s *Session) Startup(dst []byte, params []Param) []byte {
	if s.phase == PhaseNew {
		s.phase = PhaseStartup
	}
	return EncodeStartup(dst, params)
}

func (s *Session) Query(dst []byte, q string) []byte {
	s.pending++
	if s.phase == PhaseReady {
		s.phase = PhaseBusy
	}
	return EncodeQuery(dst, q)
}

func (s *Session) Feed(b []byte) {
	s.fr.Max = s.MaxMessage
	s.fr.Feed(b)
}

func (s *Session) fail(err error) error {
	if s.err == nil {
		s.err = err
	}
	s.phase = PhaseClosed
	return s.err
}

func (s *Session) Next() (Msg, bool, error) {
	if s.err != nil {
		return Msg{}, false, s.err
	}
	if s.phase == PhasePreStartup && s.preAnswers > 0 {
		b, ok := s.fr.TakeByte()
		if !ok {
			return Msg{}, false, nil
		}
		if b != 'N' && b != 'S' {
			return Msg{}, false, s.fail(fmt.Errorf("%w: answer %q to an encryption request", ErrProtocol, b))
		}
		s.preAnswers--
		if s.preAnswers == 0 {
			s.phase = PhaseStartup
		}
		return Msg{Type: MsgSSLAnswer, Status: b}, true, nil
	}
	typ, body, ok, err := s.fr.Next()
	if err != nil {
		return Msg{}, false, s.fail(err)
	}
	if !ok {
		return Msg{}, false, nil
	}
	m, err := Decode(typ, body)
	if err != nil {
		return m, false, s.fail(fmt.Errorf("%w: %v", ErrProtocol, err))
	}
	s.apply(m)
	return m, true, nil
}

func (s *Session) apply(m Msg) {
	switch m.Type {
	case MsgBackendKey:
		s.pid, s.secret = m.ProcessID, m.Secret
	case MsgParamStatus:
		s.setParam(m.Name, m.Value)
	case MsgReady:
		s.status = m.Status
		if s.phase == PhaseBusy || s.phase == PhaseReady {
			s.pending = max(0, s.pending-1)
		}
		s.phase = PhaseReady
		if s.pending > 0 {
			s.phase = PhaseBusy
		}
	case MsgError:
		if m.Severity == "FATAL" || m.Severity == "PANIC" {
			s.fail(fmt.Errorf("%w: %s %s: %s", ErrClosed, m.Severity, m.Code, m.Text))
		}
	}
}

func (s *Session) setParam(name, value string) {
	for i := range s.params {
		if s.params[i].Name == name {
			s.params[i].Value = value
			return
		}
	}
	if len(s.params) < MaxParams {
		s.params = append(s.params, Param{Name: name, Value: value})
	}
}
