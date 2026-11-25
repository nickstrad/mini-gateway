// Package testclient implements a sans-I/O Postgres simple-query client state
// machine and incremental framer shared by simulated clients and real-socket
// tier R tests. It is one of the two packages in Chunk 2 allowed to import
// pgproto3 (confined to msg.go).
package testclient

import (
	"errors"
	"fmt"
)

// Phase identifies the lifecycle stage of a client Session.
type Phase uint8

// Session lifecycle phases from initial dial through close.
const (
	PhaseNew Phase = iota
	PhasePreStartup
	PhaseStartup
	PhaseReady
	PhaseBusy
	PhaseClosed
)

// MaxParams bounds the number of server ParameterStatus entries retained on a Session.
const MaxParams = 64

var (
	// ErrProtocol reports a framing or decode violation on bytes received from the server.
	ErrProtocol = errors.New("testclient: protocol violation")
	// ErrClosed reports that a FATAL or PANIC ErrorResponse has terminated the session.
	ErrClosed = errors.New("testclient: session closed")
)

// Session tracks the client side of one Postgres simple-query connection:
// pre-startup negotiation, startup parameters, BackendKeyData, pending queries,
// and ReadyForQuery transaction status.
type Session struct {
	// MaxMessage bounds backend frame body sizes; zero uses DefaultMaxMessage.
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

// Phase returns the current session lifecycle phase.
func (s *Session) Phase() Phase { return s.phase }

// Status returns the last ReadyForQuery transaction status byte ('I', 'T', or 'E').
func (s *Session) Status() byte { return s.status }

// ProcessID returns the backend process ID received in BackendKeyData.
func (s *Session) ProcessID() uint32 { return s.pid }

// Secret returns the backend cancel secret received in BackendKeyData.
func (s *Session) Secret() []byte { return s.secret }

// Params returns the server parameters received via ParameterStatus messages.
func (s *Session) Params() []Param { return s.params }

// Pending returns the number of sent Query messages still awaiting ReadyForQuery.
func (s *Session) Pending() int { return s.pending }

// Err returns the sticky fatal or protocol error that closed the session, if any.
func (s *Session) Err() error { return s.err }

// PreStartup appends optional GSSENCRequest and SSLRequest packets and switches
// the session to PhasePreStartup so Next reads their one-byte answers first.
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

// Startup appends a startup packet and advances a new session into PhaseStartup.
func (s *Session) Startup(dst []byte, params []Param) []byte {
	if s.phase == PhaseNew {
		s.phase = PhaseStartup
	}
	return EncodeStartup(dst, params)
}

// Query appends a Query message and increments the pending reply count.
func (s *Session) Query(dst []byte, q string) []byte {
	s.pending++
	if s.phase == PhaseReady {
		s.phase = PhaseBusy
	}
	return EncodeQuery(dst, q)
}

// Feed supplies received server bytes to the session's incremental framer.
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

// Next decodes and applies the next complete backend message (or pre-startup
// single-byte reply). A FATAL/PANIC ErrorResponse is returned once with ok=true
// and err=nil, then sets the sticky ErrClosed error for subsequent calls.
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
