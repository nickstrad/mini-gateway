package e2e

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/testclient"
)

const (
	realOpTimeout = 60 * time.Second
	realReadChunk = 32 << 10
)

var ErrStartup = errors.New("e2e: startup refused")

type RealClient struct {
	conn       net.Conn
	sess       testclient.Session
	buf        []byte
	ByteByByte bool
	Startup    []string
	PID        uint32
	Secret     []byte
	TLS        bool
}

type DialOptions struct {
	User       string
	Password   string
	Database   string
	TLS        *tls.Config
	ByteByByte bool
}

func DialReal(addr string, o DialOptions) (*RealClient, error) {
	conn, err := net.DialTimeout("tcp", addr, realOpTimeout)
	if err != nil {
		return nil, err
	}
	c := &RealClient{conn: conn, buf: make([]byte, realReadChunk), ByteByByte: o.ByteByByte}
	_ = conn.SetDeadline(time.Now().Add(realOpTimeout))
	if o.TLS != nil {
		if err := c.upgrade(o.TLS); err != nil {
			_ = conn.Close()
			return nil, err
		}
	}
	if err := c.login(o); err != nil {
		_ = c.conn.Close()
		return c, err
	}
	return c, nil
}

func (c *RealClient) upgrade(cfg *tls.Config) error {
	if _, err := c.conn.Write(testclient.EncodeSSLRequest(nil)); err != nil {
		return err
	}
	var answer [1]byte
	if _, err := io.ReadFull(c.conn, answer[:]); err != nil {
		return err
	}
	if answer[0] != 'S' {
		return fmt.Errorf("%w: TLS answer %q", ErrStartup, answer[0])
	}
	tc := tls.Client(c.conn, cfg)
	if err := tc.Handshake(); err != nil {
		return err
	}
	c.conn, c.TLS = tc, true
	return nil
}

func nonce() string {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

func (c *RealClient) login(o DialOptions) error {
	params := []testclient.Param{{Name: "user", Value: o.User}, {Name: "database", Value: o.Database}}
	if err := c.write(c.sess.Startup(nil, params)); err != nil {
		return err
	}
	var scram *pgwire.ScramClient
	for {
		m, err := c.next()
		if err != nil {
			return err
		}
		switch m.Type {
		case testclient.MsgError:
			c.Startup = append(c.Startup, token(m))
			return fmt.Errorf("%w: %s %s", ErrStartup, m.Code, m.Text)
		case testclient.MsgBackendKey:
			c.PID, c.Secret = m.ProcessID, m.Secret
		case testclient.MsgReady:
			return nil
		case testclient.MsgAuth:
			switch m.Auth {
			case pgwire.AuthSASL:
				scram = pgwire.NewScramClient("", o.Password, nonce(), pgwire.GS2Header(c.TLS, false))
				if err := c.write(pgwire.AppendSASLInitialResponse(nil, pgwire.ScramMechanism, scram.First())); err != nil {
					return err
				}
			case pgwire.AuthSASLContinue:
				if scram == nil {
					return ErrStartup
				}
				final, cause := scram.Final(m.Data)
				if cause != pgwire.AuthCauseNone {
					return fmt.Errorf("%w: scram %v", ErrStartup, cause)
				}
				if err := c.write(pgwire.AppendSASLResponse(nil, final)); err != nil {
					return err
				}
			case pgwire.AuthSASLFinal:
				if scram == nil || scram.Verify(m.Data) != pgwire.AuthCauseNone {
					return fmt.Errorf("%w: server signature", ErrStartup)
				}
			}
		}
	}
}

func (c *RealClient) write(b []byte) error {
	_ = c.conn.SetDeadline(time.Now().Add(realOpTimeout))
	if !c.ByteByByte {
		_, err := c.conn.Write(b)
		return err
	}
	for i := range b {
		if _, err := c.conn.Write(b[i : i+1]); err != nil {
			return err
		}
	}
	return nil
}

func (c *RealClient) next() (testclient.Msg, error) {
	for {
		m, ok, err := c.sess.Next()
		if err != nil {
			return m, err
		}
		if ok {
			return m, nil
		}
		n, err := c.conn.Read(c.buf)
		if n > 0 {
			c.sess.Feed(c.buf[:n])
			continue
		}
		if err != nil {
			return m, err
		}
	}
}

func (c *RealClient) Query(sql string) []string { return c.Send(c.sess.Query(nil, sql)) }

func (c *RealClient) Send(b []byte) []string {
	if err := c.write(b); err != nil {
		return []string{"closed:write"}
	}
	return c.Reply()
}

func (c *RealClient) Reply() []string {
	var out []string
	for {
		m, err := c.next()
		if err != nil {
			return append(out, "closed:eof")
		}
		out = append(out, token(m))
		if m.Type == testclient.MsgReady {
			return out
		}
	}
}

func (c *RealClient) Next() ([]string, error) {
	m, err := c.next()
	if err != nil {
		return nil, err
	}
	return []string{token(m)}, nil
}

func (c *RealClient) Conn() net.Conn { return c.conn }

func (c *RealClient) Close() {
	_ = c.write(testclient.EncodeTerminate(nil))
	_ = c.conn.Close()
}

func (c *RealClient) Abort() {
	if tc, ok := c.conn.(*tls.Conn); ok {
		if tcp, ok := tc.NetConn().(*net.TCPConn); ok {
			_ = tcp.SetLinger(0)
		}
	} else if tcp, ok := c.conn.(*net.TCPConn); ok {
		_ = tcp.SetLinger(0)
	}
	_ = c.conn.Close()
}
