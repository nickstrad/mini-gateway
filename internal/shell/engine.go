package shell

import (
	"time"

	"github.com/nickstrad/mini-gateway/internal/proxy"
)

func (s *Shell) engineLoop() {
	ticker := time.NewTicker(s.cfg.PublishInterval)
	defer s.engineExit(ticker)
	s.out = s.core.Start(s.now(), s.out[:0])
	s.execute()
	s.drainDeferred()
	s.publish()
	for {
		select {
		case <-s.stop:
			return
		case env := <-s.mailbox:
			s.deliver(env)
		case <-s.wake.C:
			s.wakeSet = false
			s.step(proxy.Event{Kind: proxy.EvTimer})
		case <-ticker.C:
			s.step(proxy.Event{Kind: proxy.EvPublish})
		}
	}
}

type stopper interface {
	Shutdown(now proxy.Instant, out []proxy.Action) []proxy.Action
}

func (s *Shell) engineExit(ticker *time.Ticker) {
	ticker.Stop()
	s.wake.Stop()
	// Shut the engine down before the final publish so the final CSV flush
	// records zero in-flight requests and zero connections resetting.
	if st, ok := s.core.(stopper); ok {
		s.out = st.Shutdown(s.now(), s.out[:0])
		s.execute()
	}
	for id, c := range s.clients {
		c.abort()
		delete(s.clients, id)
	}
	for id, c := range s.dbs {
		c.abort()
		delete(s.dbs, id)
	}
	s.publish()
	s.closeErr = s.closeObs()
}

func (s *Shell) deliver(env envelope) {
	ev := env.ev
	// Release the per-connection read credit only after Engine.Step finishes
	// processing this chunk (and any resulting ActClientPause/ActDBPause).
	if (ev.Kind == proxy.EvClientData || ev.Kind == proxy.EvDBData) && env.conn != nil {
		defer env.conn.setInflight(false)
	}
	switch ev.Kind {
	case proxy.EvClientAccepted:
		if len(s.clients) >= maxClientConns {
			s.stats.refused.Add(1)
			env.conn.abort()
			return
		}
		s.clients[ev.Conn] = env.conn
	case proxy.EvClientData, proxy.EvClientClosed, proxy.EvClientWrote, proxy.EvClientTLS:
		if s.clients[ev.Conn] != env.conn {
			s.stats.dropped.Add(1)
			return
		}
		if ev.Kind == proxy.EvClientClosed {
			delete(s.clients, ev.Conn)
		}
	default:
		if s.dbs[ev.Conn] != env.conn {
			s.stats.dropped.Add(1)
			return
		}
		if ev.Kind == proxy.EvDBClosed || ev.Kind == proxy.EvDBDialFailed {
			delete(s.dbs, ev.Conn)
		}
	}
	s.step(ev)
}

func (s *Shell) step(ev proxy.Event) {
	s.stepOnce(ev)
	s.drainDeferred()
}

func (s *Shell) drainDeferred() {
	for len(s.deferred) > 0 {
		ev := s.deferred[0]
		s.deferred = s.deferred[1:]
		s.stepOnce(ev)
	}
}

func (s *Shell) stepOnce(ev proxy.Event) {
	s.out = s.core.Step(s.now(), ev, s.out[:0])
	s.execute()
}

func (s *Shell) execute() {
	for i := range s.out {
		s.do(s.out[i])
		s.out[i] = proxy.Action{}
	}
}

func (s *Shell) do(a proxy.Action) {
	switch a.Kind {
	case proxy.ActClientWrite:
		s.write(s.clients[a.Conn], itemWrite, a.Data)
	case proxy.ActClientClose:
		s.closeConn(s.clients[a.Conn])
		delete(s.clients, a.Conn)
	case proxy.ActClientStartTLS:
		s.write(s.clients[a.Conn], itemStartTLS, nil)
	case proxy.ActClientPause:
		setMode(s.clients[a.Conn], readWait)
	case proxy.ActClientResume:
		setMode(s.clients[a.Conn], readFree)
	case proxy.ActDBOpen:
		s.openDB(a.Conn, a.TLSFirst)
	case proxy.ActDBWrite:
		s.write(s.dbs[a.Conn], itemWrite, a.Data)
	case proxy.ActDBClose:
		s.closeConn(s.dbs[a.Conn])
		delete(s.dbs, a.Conn)
	case proxy.ActDBStartTLS:
		s.write(s.dbs[a.Conn], itemStartTLS, nil)
	case proxy.ActDBPause:
		setMode(s.dbs[a.Conn], readWait)
	case proxy.ActDBResume:
		setMode(s.dbs[a.Conn], readFree)
	case proxy.ActWake:
		s.arm(a.At)
	case proxy.ActPublish:
		s.publish()
	}
}

func (s *Shell) write(c *conn, kind itemKind, data []byte) {
	if c == nil {
		return
	}
	if !c.enqueue(writeItem{kind: kind, data: data}) {
		s.stats.queueOverflows.Add(1)
		c.abort()
	}
}

func (s *Shell) closeConn(c *conn) {
	if c != nil && !c.closeByEngine() {
		s.stats.queueOverflows.Add(1)
		c.abort()
	}
}

func setMode(c *conn, m readMode) {
	if c != nil {
		c.setMode(m)
	}
}

func (s *Shell) arm(at proxy.Instant) {
	if s.wakeSet {
		s.wake.Stop()
		s.wakeSet = false
	}
	if at == 0 {
		return
	}
	s.wake.Reset(max(time.Duration(at-s.now()), 0))
	s.wakeSet = true
}

func (s *Shell) openDB(id proxy.ConnID, tlsFirst bool) {
	if old := s.dbs[id]; old != nil {
		old.abort()
		delete(s.dbs, id)
	}
	if len(s.dbs) >= maxDBConns {
		s.refuseDial(id)
		return
	}
	first := 0
	if tlsFirst {
		first = proxy.FirstDBTLSRead
	}
	c := newConn(sideDB, id, time.Now(), first)
	if !s.register(c) {
		s.refuseDial(id)
		return
	}
	s.dbs[id] = c
	s.spawn(func() { s.dialLoop(c) }, &s.stats.dbReaders)
}

func (s *Shell) refuseDial(id proxy.ConnID) {
	if len(s.deferred) >= maxDeferred {
		s.stats.dropped.Add(1)
		return
	}
	s.deferred = append(s.deferred, proxy.Event{Kind: proxy.EvDBDialFailed, Conn: id})
}
