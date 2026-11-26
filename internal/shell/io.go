package shell

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
)

func (c *conn) kind(client, db proxy.EventKind) proxy.EventKind {
	if c.side == sideClient {
		return client
	}
	return db
}

func (s *Shell) readLoop(c *conn) {
	defer s.readerExit(c)
	buf := make([]byte, proxy.MaxRead)
	backstop := s.cfg.Timeouts.ClientIdle + s.cfg.Timeouts.Statement
	for {
		nc, exact, ok := c.awaitRead()
		if !ok || nc == nil {
			return
		}
		_ = nc.SetReadDeadline(time.Now().Add(backstop))
		var n int
		var err error
		if exact > 0 {
			n, err = io.ReadFull(nc, buf[:exact])
			if err != nil {
				n = 0
			} else {
				c.firstReadDone()
			}
		} else {
			n, err = nc.Read(buf)
		}
		if n > 0 {
			ev := proxy.Event{Kind: c.kind(proxy.EvClientData, proxy.EvDBData), Conn: c.id, Data: append([]byte(nil), buf[:n]...)}
			c.setInflight(true)
			if !s.post(envelope{ev: ev, conn: c}) {
				return
			}
		}
		if err != nil {
			if isTimeout(err) {
				s.stats.readDeadlines.Add(1)
			}
			return
		}
	}
}

func (s *Shell) readerExit(c *conn) {
	c.abort()
	if c.side == sideDB {
		<-c.writerDone
	}
	if c.claimClosed() {
		if c.side == sideDB && c.stoppedWriting() {
			s.post(envelope{ev: proxy.Event{Kind: proxy.EvDBWriteRefused, Conn: c.id}, conn: c})
		}
		s.post(envelope{ev: proxy.Event{Kind: c.kind(proxy.EvClientClosed, proxy.EvDBClosed), Conn: c.id}, conn: c})
	}
	s.unregister(c)
}

func (s *Shell) writeLoop(c *conn) {
	defer close(c.writerDone)
	defer c.abort()
	for {
		it, ok := c.next()
		if !ok {
			return
		}
		switch it.kind {
		case itemWrite:
			if !s.writeOut(c, it.data) {
				return
			}
		case itemStartTLS:
			if !s.handshake(c) {
				return
			}
		case itemClose:
			return
		}
	}
}

func (s *Shell) writeOut(c *conn, data []byte) bool {
	nc := c.current()
	if len(data) == 0 {
		return nc != nil
	}
	if nc == nil {
		c.writeStopped()
		return false
	}
	_ = nc.SetWriteDeadline(time.Now().Add(s.cfg.Timeouts.ClientWrite))
	n, err := nc.Write(data)
	if n < len(data) {
		c.writeStopped()
	}
	if n > 0 {
		ev := proxy.Event{Kind: c.kind(proxy.EvClientWrote, proxy.EvDBWrote), Conn: c.id, N: n}
		if !s.post(envelope{ev: ev, conn: c}) {
			return false
		}
	}
	if err != nil {
		if isTimeout(err) {
			s.stats.writeDeadlines.Add(1)
		}
		return false
	}
	return true
}

func (s *Shell) handshake(c *conn) bool {
	raw := c.current()
	if raw == nil {
		return false
	}
	var tc *tls.Conn
	deadline := c.start.Add(s.cfg.Timeouts.ClientStartup)
	switch {
	case c.side == sideClient && s.cfg.ClientTLS != nil:
		tc = tls.Server(raw, s.cfg.ClientTLS)
	case c.side == sideDB && s.cfg.BackendTLS != nil:
		tc = tls.Client(raw, s.cfg.BackendTLS)
		deadline = c.start.Add(s.cfg.Timeouts.DBConnect)
	}
	res := pgwire.HandshakeResult{Outcome: pgwire.HandshakeFailed}
	if tc != nil {
		_ = raw.SetDeadline(deadline)
		err := tc.Handshake()
		_ = raw.SetDeadline(time.Time{})
		res = handshakeResult(err, tc)
	}
	if res.Outcome == pgwire.HandshakeCompleted {
		if !c.attach(tc) {
			return false
		}
	} else {
		s.stats.handshakesFailed.Add(1)
	}
	ev := proxy.Event{Kind: c.kind(proxy.EvClientTLS, proxy.EvDBTLS), Conn: c.id, TLS: res}
	return s.post(envelope{ev: ev, conn: c})
}

func handshakeResult(err error, tc *tls.Conn) pgwire.HandshakeResult {
	if err == nil {
		v := pgwire.TLSVersionOther
		switch tc.ConnectionState().Version {
		case tls.VersionTLS13:
			v = pgwire.TLSVersion13
		case tls.VersionTLS12:
			v = pgwire.TLSVersion12
		}
		return pgwire.HandshakeResult{Outcome: pgwire.HandshakeCompleted, Version: v}
	}
	var rh tls.RecordHeaderError
	var cv *tls.CertificateVerificationError
	var ua x509.UnknownAuthorityError
	var he x509.HostnameError
	var ci x509.CertificateInvalidError
	switch {
	case isTimeout(err):
		return pgwire.HandshakeResult{Outcome: pgwire.HandshakeDeadline}
	case errors.As(err, &rh):
		return pgwire.HandshakeResult{Outcome: pgwire.HandshakeViolation}
	case errors.As(err, &cv), errors.As(err, &ua), errors.As(err, &he), errors.As(err, &ci):
		return pgwire.HandshakeResult{Outcome: pgwire.HandshakeFailedVerification}
	}
	return pgwire.HandshakeResult{Outcome: pgwire.HandshakeFailed}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.As(err, &ne) && ne.Timeout()
}

func (s *Shell) dialLoop(c *conn) {
	ctx, cancel := context.WithDeadline(context.Background(), c.start.Add(s.cfg.Timeouts.DBConnect))
	if !c.setCancel(cancel) {
		cancel()
		s.unregister(c)
		return
	}
	nc, err := s.cfg.Dial(ctx, s.cfg.DBAddress)
	cancel()
	if err != nil {
		c.abort()
		if c.claimClosed() {
			s.post(envelope{ev: proxy.Event{Kind: proxy.EvDBDialFailed, Conn: c.id}, conn: c})
		}
		s.unregister(c)
		return
	}
	if !c.attach(nc) {
		_ = nc.Close()
		s.unregister(c)
		return
	}
	s.spawn(func() { s.writeLoop(c) }, &s.stats.dbWriters)
	if !s.post(envelope{ev: proxy.Event{Kind: proxy.EvDBOpened, Conn: c.id}, conn: c}) {
		c.abort()
		s.unregister(c)
		return
	}
	s.readLoop(c)
}
