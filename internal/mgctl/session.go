package mgctl

import (
	"errors"
	"fmt"
	"io"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/testclient"
)

// Wire read buffer size and retained row cap for a single query Reply.
const (
	readChunk = 32 << 10
	MaxRows   = 10_000
)

// Sentinel errors returned by Login, Session.Query, and Session.Receive.
var (
	ErrLogin    = errors.New("mgctl: login refused")
	ErrProtocol = errors.New("mgctl: protocol error")
	ErrClosed   = errors.New("mgctl: connection closed")
)

// Credentials holds the Postgres startup user, password, and database name.
type Credentials struct {
	User     string
	Password string
	Database string
}

// StartTLS upgrades rw to TLS after the server answers 'S' to an SSLRequest.
type StartTLS func(rw io.ReadWriter) (io.ReadWriter, error)

// ServerError holds the severity, SQLSTATE code, and message from a Postgres ErrorResponse.
type ServerError struct {
	Severity string
	Code     string
	Message  string
}

// Error formats the server error as "<Severity> <Code>: <Message>".
func (e *ServerError) Error() string { return e.Severity + " " + e.Code + ": " + e.Message }

// Reply holds the decoded columns, bounded rows, command tag, error, and
// transaction status from one simple-protocol query exchange.
type Reply struct {
	Columns   []string
	Rows      [][]string
	Truncated int64
	Tag       string
	Err       *ServerError
	Status    byte
}

// Session manages an authenticated Postgres simple-protocol connection over any
// io.ReadWriter, shared by mgctl query and loadgen's live runner.
type Session struct {
	rw   io.ReadWriter
	sess testclient.Session
	buf  []byte
	tls  bool
	PID  uint32
}

// Login performs optional TLS negotiation, sends the Postgres StartupMessage,
// completes trust or SCRAM-SHA-256 authentication, and waits for ReadyForQuery.
func Login(rw io.ReadWriter, c Credentials, startTLS StartTLS, nonce string) (*Session, error) {
	s := &Session{rw: rw, buf: make([]byte, readChunk)}
	if startTLS != nil {
		if err := s.upgrade(startTLS); err != nil {
			return nil, err
		}
	}
	params := []testclient.Param{{Name: "user", Value: c.User}, {Name: "database", Value: c.Database}}
	if err := s.Write(s.sess.Startup(nil, params)); err != nil {
		return nil, err
	}
	return s, s.authenticate(c, nonce)
}

func (s *Session) upgrade(startTLS StartTLS) error {
	if err := s.Write(testclient.EncodeSSLRequest(nil)); err != nil {
		return err
	}
	// Read exactly one byte unbuffered so no TLS handshake bytes are ever consumed into s.buf.
	var answer [1]byte
	if _, err := io.ReadFull(s.rw, answer[:]); err != nil {
		return err
	}
	if answer[0] != 'S' {
		return fmt.Errorf("%w: the server answered %q to the TLS request", ErrLogin, answer[0])
	}
	rw, err := startTLS(s.rw)
	if err != nil {
		return err
	}
	s.rw, s.tls = rw, true
	return nil
}

func (s *Session) authenticate(c Credentials, nonce string) error {
	var scram *pgwire.ScramClient
	for {
		m, err := s.next()
		if err != nil {
			return err
		}
		switch m.Type {
		case testclient.MsgError:
			return fmt.Errorf("%w: %w", ErrLogin, &ServerError{Severity: m.Severity, Code: m.Code, Message: m.Text})
		case testclient.MsgBackendKey:
			s.PID = m.ProcessID
		case testclient.MsgReady:
			return nil
		case testclient.MsgAuth:
			if err := s.authStep(m, &scram, c, nonce); err != nil {
				return err
			}
		}
	}
}

func (s *Session) authStep(m testclient.Msg, scram **pgwire.ScramClient, c Credentials, nonce string) error {
	switch m.Auth {
	case pgwire.AuthOK:
		return nil
	case pgwire.AuthSASL:
		*scram = pgwire.NewScramClient("", c.Password, nonce, pgwire.GS2Header(s.tls, false))
		return s.Write(pgwire.AppendSASLInitialResponse(nil, pgwire.ScramMechanism, (*scram).First()))
	case pgwire.AuthSASLContinue:
		if *scram == nil {
			return fmt.Errorf("%w: SASL continue before SASL start", ErrProtocol)
		}
		final, cause := (*scram).Final(m.Data)
		if cause != pgwire.AuthCauseNone {
			return fmt.Errorf("%w: SCRAM: %v", ErrLogin, cause)
		}
		return s.Write(pgwire.AppendSASLResponse(nil, final))
	case pgwire.AuthSASLFinal:
		if *scram == nil || (*scram).Verify(m.Data) != pgwire.AuthCauseNone {
			return fmt.Errorf("%w: the server signature did not verify", ErrLogin)
		}
		return nil
	}
	return fmt.Errorf("%w: authentication method %d is not supported (SCRAM-SHA-256 or trust only)", ErrLogin, m.Auth)
}

// Write writes encoded wire bytes directly to the underlying stream.
func (s *Session) Write(b []byte) error {
	_, err := s.rw.Write(b)
	return err
}

// Conn returns the active underlying stream (upgraded to TLS when StartTLS succeeded).
func (s *Session) Conn() io.ReadWriter { return s.rw }

func (s *Session) next() (testclient.Msg, error) {
	for {
		m, ok, err := s.sess.Next()
		if err != nil {
			if errors.Is(err, testclient.ErrClosed) {
				return m, ErrClosed
			}
			return m, fmt.Errorf("%w: %v", ErrProtocol, err)
		}
		if ok {
			return m, nil
		}
		n, err := s.rw.Read(s.buf)
		if n > 0 {
			s.sess.Feed(s.buf[:n])
			continue
		}
		if err != nil {
			return m, fmt.Errorf("%w: %v", ErrClosed, err)
		}
	}
}

// Query encodes sql as a Postgres simple Query message, sends it, and reads its reply.
func (s *Session) Query(sql string) (Reply, error) {
	if err := s.Write(testclient.EncodeQuery(nil, sql)); err != nil {
		return Reply{}, fmt.Errorf("%w: %v", ErrClosed, err)
	}
	return s.Receive()
}

// Receive reads wire messages until ReadyForQuery (or a FATAL/PANIC ErrorResponse,
// after which Postgres closes the connection without sending ReadyForQuery).
func (s *Session) Receive() (Reply, error) {
	var r Reply
	for {
		m, err := s.next()
		if err != nil {
			return r, err
		}
		switch m.Type {
		case testclient.MsgRowDesc:
			r.Columns = append([]string(nil), m.Fields...)
		case testclient.MsgDataRow:
			r.addRow(m.Values)
		case testclient.MsgCommand:
			r.Tag = m.Tag
		case testclient.MsgError:
			if r.Err == nil {
				r.Err = &ServerError{Severity: m.Severity, Code: m.Code, Message: m.Text}
			}
			if m.Severity == "FATAL" || m.Severity == "PANIC" {
				return r, nil
			}
		case testclient.MsgReady:
			r.Status = m.Status
			return r, nil
		}
	}
}

func (r *Reply) addRow(vals [][]byte) {
	if len(r.Rows) >= MaxRows {
		r.Truncated++
		return
	}
	row := make([]string, len(vals))
	for i, v := range vals {
		if v == nil {
			row[i] = "NULL"
			continue
		}
		row[i] = string(v)
	}
	r.Rows = append(r.Rows, row)
}

// Terminate sends a Postgres Terminate ('X') message to close the session cleanly.
func (s *Session) Terminate() error { return s.Write(testclient.EncodeTerminate(nil)) }
