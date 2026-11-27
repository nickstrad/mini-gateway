package proxy_test

import (
	"bytes"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/proxy/proxysim"
)

type rig struct {
	t   *testing.T
	e   *proxy.Engine
	now proxy.Instant
	db  proxy.ConnID
}

func newRig(t *testing.T, edit func(*proxy.Config)) *rig {
	t.Helper()
	w, err := proxysim.BuildWorld(proxysim.WorldConfig{Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	c := w.Proxy.Engine.Config()
	c.Connections = 1
	c.QueueCap = 2
	c.ReplyBufferCap = proxy.MinReplyBufferCap
	if edit != nil {
		edit(&c)
	}
	e, err := proxy.New(c)
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, e: e, now: 1}
	for _, a := range e.Start(r.now, nil) {
		if a.Kind == proxy.ActDBOpen {
			r.db = a.Conn
		}
	}
	if r.db == 0 {
		t.Fatal("no initial dial")
	}
	r.ack(r.ev(proxy.EvDBOpened, r.db, nil, 0))
	r.ev(proxy.EvDBData, r.db, pgwire.AppendReadyForQuery(pgwire.AppendAuthentication(nil, 0, nil), 'I'), 0)
	if e.Snapshot().DBIdle.Value != 1 {
		t.Fatal("database did not become idle")
	}
	return r
}

func (r *rig) ev(k proxy.EventKind, id proxy.ConnID, b []byte, n int) []proxy.Action {
	r.t.Helper()
	a := r.e.Step(r.now, proxy.Event{Kind: k, Conn: id, Data: b, N: n}, nil)
	if err := r.e.Invariants(); err != nil {
		r.t.Fatalf("after %s: %v", k, err)
	}
	return a
}

func (r *rig) at(now proxy.Instant) { r.now = now }

func (r *rig) ack(a []proxy.Action) []proxy.Action {
	for _, x := range a {
		switch x.Kind {
		case proxy.ActClientWrite:
			r.ev(proxy.EvClientWrote, x.Conn, nil, len(x.Data))
		case proxy.ActDBWrite:
			r.ev(proxy.EvDBWrote, x.Conn, nil, len(x.Data))
		}
	}
	return a
}

func (r *rig) client(id proxy.ConnID) {
	r.t.Helper()
	r.ev(proxy.EvClientAccepted, id, nil, 0)
	b := pgwire.AppendStartupMessage(nil, []pgwire.Param{{Name: "user", Value: proxysim.TenantName(0)}, {Name: "database", Value: proxysim.Database}})
	r.ack(r.ev(proxy.EvClientData, id, b[:proxy.FirstClientRead], 0))
	r.ack(r.ev(proxy.EvClientData, id, b[proxy.FirstClientRead:], 0))
	if _, ok := r.e.Tenant(id); !ok {
		r.t.Fatal("client did not authenticate")
	}
}

func (r *rig) query(id proxy.ConnID, s string) []proxy.Action {
	return r.ev(proxy.EvClientData, id, pgwire.AppendQuery(nil, []byte(s)), 0)
}

func message(typ byte, body []byte) []byte {
	return append(pgwire.AppendMessageHeader(nil, typ, len(body)), body...)
}

func complete(tag string, status byte) []byte {
	return pgwire.AppendReadyForQuery(message(pgwire.MsgCommandComplete, append([]byte(tag), 0)), status)
}

func written(a []proxy.Action, k proxy.ActionKind, id proxy.ConnID) []byte {
	var b []byte
	for _, x := range a {
		if x.Kind == k && x.Conn == id {
			b = append(b, x.Data...)
		}
	}
	return b
}

func did(a []proxy.Action, k proxy.ActionKind, id proxy.ConnID) bool {
	for _, x := range a {
		if x.Kind == k && x.Conn == id {
			return true
		}
	}
	return false
}

func (r *rig) leaseThenReset(c1, c2 proxy.ConnID) {
	r.t.Helper()
	r.client(c1)
	r.client(c2)
	r.ack(r.query(c1, "SELECT 81"))
	r.query(c2, "SELECT 82")
	a := r.ack(r.ev(proxy.EvDBData, r.db, complete("SELECT 1", 'I'), 0))
	if !bytes.Equal(written(a, proxy.ActDBWrite, r.db), pgwire.AppendQuery(nil, []byte("DISCARD ALL"))) {
		r.t.Fatal("no reset written at release")
	}
}
