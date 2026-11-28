package proxy_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/coalesce"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/proxy/proxysim"
)

const repairStatement = 400 * time.Millisecond

func TestAQueuedFlightAtItsDeadlineIsShedThroughTheAdmissionQueue(t *testing.T) {
	r := newRig(t, func(c *proxy.Config) {
		c.Admission = admission.Config{Rule: admission.RuleTimeout}
		c.Coalesce = coalesce.Config{Mode: coalesce.ModeAuto, Join: coalesce.JoinStrict}
		c.Timeouts.Statement = repairStatement
		c.Timeouts.QueueWait = 3 * time.Second
	})
	r.client(11)
	r.client(12)
	r.ack(r.query(11, sharedRead))
	r.at(2)
	r.query(12, sharedRead)
	if s := r.e.CoalesceSnapshot(); s.Held != 1 {
		t.Fatalf("held %d", s.Held)
	}
	r.at(2 + proxy.Instant(repairStatement) - 1)
	r.ack(r.ev(proxy.EvDBData, r.db, readReply("x"), 0))
	if s := r.e.Snapshot(); s.Queued.Value != 1 || s.CoalescePromoted != 1 {
		t.Fatalf("queued %d promoted %d", s.Queued.Value, s.CoalescePromoted)
	}
	r.at(2 + proxy.Instant(repairStatement))
	a := r.ev(proxy.EvTimer, 0, nil, 0)
	if !shedReply(written(a, proxy.ActClientWrite, 12)) {
		t.Fatalf("the queued flight's owner was not shed at its deadline: %q", written(a, proxy.ActClientWrite, 12))
	}
	s, as := r.e.Snapshot(), r.e.AdmissionSnapshot()
	coreShed := int64(0)
	for _, n := range as.Shed {
		coreShed += n
	}
	if s.Shed != 1 || coreShed != 1 || s.CoalesceExpired != 1 {
		t.Fatalf("engine shed %d, admission shed %d, expired %d", s.Shed, coreShed, s.CoalesceExpired)
	}
}

type twoDB struct {
	*rig
	dbs []proxy.ConnID
}

func newTwoDBRig(t *testing.T, edit func(*proxy.Config)) *twoDB {
	t.Helper()
	w, err := proxysim.BuildWorld(proxysim.WorldConfig{Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	c := w.Proxy.Engine.Config()
	c.Connections = 2
	c.QueueCap = 4
	c.ReplyBufferCap = proxy.MinReplyBufferCap
	if edit != nil {
		edit(&c)
	}
	e, err := proxy.New(c)
	if err != nil {
		t.Fatal(err)
	}
	r := &twoDB{rig: &rig{t: t, e: e, now: 1}}
	for _, a := range e.Start(r.now, nil) {
		if a.Kind == proxy.ActDBOpen {
			r.dbs = append(r.dbs, a.Conn)
		}
	}
	for i := 0; i < len(r.dbs) && i < 2; i++ {
		id := r.dbs[i]
		r.ack(r.ev(proxy.EvDBOpened, id, nil, 0))
		for _, a := range r.ev(proxy.EvDBData, id, pgwire.AppendReadyForQuery(pgwire.AppendAuthentication(nil, 0, nil), 'I'), 0) {
			if a.Kind == proxy.ActDBOpen {
				r.dbs = append(r.dbs, a.Conn)
			}
		}
	}
	if e.Snapshot().DBIdle.Value != 2 {
		t.Fatal("databases did not become idle")
	}
	return r
}

func TestShutdownSendsNothingNewToTheDatabase(t *testing.T) {
	r := newTwoDBRig(t, func(c *proxy.Config) {
		c.Coalesce = coalesce.Config{Mode: coalesce.ModeAuto, Join: coalesce.JoinStrict}
	})
	r.client(11)
	r.client(12)
	a := r.ack(r.query(12, sharedRead))
	ran := 0
	for _, id := range r.dbs {
		if bytes.Equal(written(a, proxy.ActDBWrite, id), pgwire.AppendQuery(nil, []byte(sharedRead))) {
			ran++
		}
	}
	if ran != 1 {
		t.Fatalf("the first read ran on %d connections", ran)
	}
	r.query(11, sharedRead)
	if s := r.e.CoalesceSnapshot(); s.Held != 1 {
		t.Fatalf("held %d", s.Held)
	}
	out := r.e.Shutdown(r.now, nil)
	for _, x := range out {
		if x.Kind == proxy.ActDBWrite {
			t.Fatalf("shutdown wrote to database %d: %q", x.Conn, x.Data)
		}
	}
	s := r.e.Snapshot()
	if s.Coalesced.Value != 0 || s.InFlight.Value != 0 || s.Queued.Value != 0 {
		t.Fatalf("after shutdown: coalesced %d in flight %d queued %d", s.Coalesced.Value, s.InFlight.Value, s.Queued.Value)
	}
	if err := r.e.Quiesce(); err != nil {
		t.Fatal(err)
	}
	if err := r.e.Invariants(); err != nil {
		t.Fatal(err)
	}
}
