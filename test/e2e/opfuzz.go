package e2e

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/proxy/proxysim"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
	"github.com/nickstrad/mini-gateway/internal/testclient"
)

const (
	OpSeed       = 1
	OpClients    = 3
	OpConns      = 1
	OpQueueCap   = 2
	MaxOps       = 48
	opBytes      = 4
	maxGapMs     = 32
	opStep       = time.Millisecond
	maxAdvanceMs = 40
	opSettle     = 30 * time.Second
	slowKey      = "slow"
)

const (
	OpConnect sim.FaultKind = iota + 1
	OpRead
	OpWrite
	OpBegin
	OpCommit
	OpRollback
	OpReject
	OpExtended
	OpSlowRead
	OpBigRead
	OpDisconnect
	OpVanish
	OpStopReading
	OpResumeReading
	OpCancel
	OpAdvance
	OpDBFragment
	OpDBFirstByte
	OpDBReset
	OpBadReset
	opEnd
)

const NumOps = int(opEnd - OpConnect)

var opNames = [NumOps]string{"connect", "read", "write", "begin", "commit", "rollback", "reject", "extended",
	"slow-read", "big-read", "disconnect", "vanish", "stop-reading", "resume-reading", "cancel", "advance",
	"db-fragment", "db-first-byte", "db-reset", "bad-reset"}

func OpNames() []sim.KindName {
	out := make([]sim.KindName, NumOps)
	for i := range out {
		out[i] = sim.KindName{Kind: OpConnect + sim.FaultKind(i), Name: opNames[i]}
	}
	return out
}

func DecodeOps(b []byte) []sim.Fault {
	n := min(len(b)/opBytes, MaxOps)
	ops := make([]sim.Fault, 0, n)
	for i := 0; i < n; i++ {
		p := b[i*opBytes:]
		ops = append(ops, sim.Fault{
			Kind:   OpConnect + sim.FaultKind(int(p[0])%NumOps),
			Target: int64(p[1]) % OpClients,
			Arg1:   int64(p[2]),
			At:     sim.Time(time.Duration(int(p[3])%maxGapMs) * time.Millisecond),
		})
	}
	return ops
}

func EncodeOps(ops []sim.Fault) []byte {
	var b []byte
	for _, o := range ops {
		b = append(b, byte(int(o.Kind-OpConnect)), byte(o.Target), byte(o.Arg1), byte(time.Duration(o.At)/time.Millisecond))
	}
	return b
}

func OpTimeouts() proxy.Timeouts {
	return proxy.Timeouts{ClientStartup: 400 * time.Millisecond, ClientMidMessage: 400 * time.Millisecond,
		ClientIdle: 20 * time.Second, QueueWait: 500 * time.Millisecond, DBConnect: 300 * time.Millisecond,
		Statement: 400 * time.Millisecond, Reset: 150 * time.Millisecond, ClientWrite: 250 * time.Millisecond,
		RedialDelay: 20 * time.Millisecond}
}

type OpResult struct {
	Violations []string
	Err        error
	Engine     proxy.Snapshot
	Steps      int64
}

func (r OpResult) Failed() bool { return r.Err != nil || len(r.Violations) > 0 }

func (r OpResult) String() string {
	if r.Err != nil {
		return r.Err.Error()
	}
	return strings.Join(r.Violations, "; ")
}

type opWorld struct {
	w       *proxysim.World
	tamper  *tamperer
	clients [OpClients]*opClient
	viol    []string
	cancels []*simnet.End
}

const maxViolations = 16

func (ow *opWorld) violate(format string, args ...any) {
	if len(ow.viol) < maxViolations {
		ow.viol = append(ow.viol, fmt.Sprintf(format, args...))
	}
}

func RunOps(ops []sim.Fault, plant proxy.Plant) OpResult {
	tm := &tamperer{}
	rows := []fakepg.Row{{Key: slowKey, Value: "s"}}
	for c := 0; c < OpClients; c++ {
		rows = append(rows, fakepg.Row{Key: key(c, "a"), Value: "init"})
	}
	cfg := proxysim.WorldConfig{Seed: OpSeed, Scenario: "fuzz-engine-ops", Tenants: OpClients,
		Fake: fakepg.Config{Rows: rows, MaxConnections: 64, Service: fakepg.Service{PerKey: []fakepg.KeyTime{{Key: slowKey, Time: 300 * time.Millisecond}}}},
		Engine: proxy.Config{Connections: OpConns, QueueCap: OpQueueCap, Timeouts: OpTimeouts(), ReplyBufferCap: 2 * proxy.MaxRead,
			Plant: plant, MaxClients: 16, MaxStartup: 16},
		DBAddr: tamperAddr, BeforeProxy: tm.listen}
	tm.done = true
	w, err := proxysim.BuildWorld(cfg)
	if err != nil {
		return OpResult{Err: err}
	}
	ow := &opWorld{w: w, tamper: tm}
	for _, op := range ops {
		if err := w.S.RunFor(opStep + time.Duration(op.At)); err != nil {
			return ow.result(err)
		}
		ow.apply(op)
	}
	ow.end()
	err = w.S.RunFor(opSettle)
	if err == nil {
		err = w.Proxy.Finish()
	}
	return ow.result(err)
}

func (ow *opWorld) result(err error) OpResult {
	if err == nil {
		err = ow.w.S.Err()
	}
	for _, c := range ow.clients {
		if c != nil && !c.done && err == nil {
			ow.violate("client %d still open at the end", c.idx)
		}
	}
	return OpResult{Violations: ow.viol, Err: err, Engine: ow.w.Proxy.Engine.Snapshot(), Steps: ow.w.S.Steps()}
}

func (ow *opWorld) end() {
	for _, c := range ow.clients {
		if c != nil {
			c.closeNow()
		}
	}
	for _, k := range []sim.FaultKind{fakepg.FaultReset, fakepg.FaultFragment, fakepg.FaultFirstByte} {
		ow.w.S.ApplyFault(sim.Fault{Kind: fakepg.FaultClear, Arg1: int64(k)})
	}
	ow.tamper.done = true
}

func (ow *opWorld) apply(op sim.Fault) {
	c := ow.clients[op.Target]
	switch op.Kind {
	case OpConnect:
		if c == nil || c.done {
			ow.clients[op.Target] = newOpClient(ow, int(op.Target))
		}
	case OpRead, OpWrite, OpBegin, OpCommit, OpRollback, OpReject, OpExtended, OpSlowRead, OpBigRead:
		if c == nil || c.done {
			c = newOpClient(ow, int(op.Target))
			ow.clients[op.Target] = c
		}
		c.query(op.Kind)
	case OpDisconnect:
		if c != nil {
			c.closeNow()
		}
	case OpVanish:
		if c != nil {
			c.resetNow()
		}
	case OpStopReading:
		if c != nil {
			c.noRead = true
		}
	case OpResumeReading:
		if c != nil {
			c.noRead = false
			c.read()
		}
	case OpCancel:
		if c != nil {
			ow.cancel(c)
		}
	case OpAdvance:
		_ = ow.w.S.RunFor(time.Duration(op.Arg1%maxAdvanceMs) * 20 * time.Millisecond)
	case OpDBFragment:
		ow.w.S.ApplyFault(sim.Fault{Kind: fakepg.FaultFragment, Arg2: op.Arg1%4 + 1})
	case OpDBFirstByte:
		ow.w.S.ApplyFault(sim.Fault{Kind: fakepg.FaultFirstByte, Arg2: int64(time.Duration(op.Arg1%64) * time.Millisecond)})
	case OpDBReset:
		ow.w.S.ApplyFault(sim.Fault{Kind: fakepg.FaultReset, Arg1: op.Arg1%4 + 1})
		ow.w.S.After(2*time.Millisecond, func() {
			ow.w.S.ApplyFault(sim.Fault{Kind: fakepg.FaultClear, Arg1: int64(fakepg.FaultReset)})
		})
	case OpBadReset:
		ow.tamper.mode = TamperError + Tamper(op.Arg1%int64(NumTampers-TamperError))
		ow.tamper.done = false
	}
}

func (ow *opWorld) cancel(c *opClient) {
	if c.sess.ProcessID() == 0 {
		return
	}
	pkt := testclient.EncodeCancel(nil, c.sess.ProcessID(), c.sess.Secret())
	var e *simnet.End
	e = ow.w.Net.Dial(ow.w.Proxy.Addr(), ow.w.Cfg.Options, simnet.HandlerFunc(func(_ *simnet.End, ev simnet.Event) {
		switch ev {
		case simnet.EvConnected:
			_, _ = e.Write(pkt)
		case simnet.EvReadable:
			var b [64]byte
			if _, err := e.Read(b[:]); err != nil {
				_ = e.Close()
			}
		case simnet.EvReset, simnet.EvRefused:
			_ = e.Close()
		}
	}))
}

type opClient struct {
	ow      *opWorld
	idx     int
	end     *simnet.End
	sess    testclient.Session
	out     []byte
	ready   bool
	busy    bool
	kind    sim.FaultKind
	tokens  []string
	noRead  bool
	pending sim.FaultKind
	done    bool
	buf     [proxy.MaxRead]byte
}

func newOpClient(ow *opWorld, idx int) *opClient {
	c := &opClient{ow: ow, idx: idx}
	c.end = ow.w.Net.Dial(ow.w.Proxy.Addr(), ow.w.Cfg.Options, c)
	return c
}

func (c *opClient) OnEvent(_ *simnet.End, ev simnet.Event) {
	if c.done {
		return
	}
	switch ev {
	case simnet.EvConnected:
		params := []testclient.Param{{Name: "user", Value: proxysim.TenantName(c.idx)}, {Name: "database", Value: proxysim.Database}}
		c.send(c.sess.Startup(nil, params))
	case simnet.EvReadable:
		c.read()
	case simnet.EvWritable:
		c.flush()
	case simnet.EvReset, simnet.EvRefused:
		c.gone()
	}
}

func (c *opClient) send(b []byte) {
	c.out = append(c.out, b...)
	c.flush()
}

func (c *opClient) flush() {
	if c.done || len(c.out) == 0 {
		return
	}
	n, err := c.end.Write(c.out)
	if err != nil {
		c.gone()
		return
	}
	c.out = c.out[n:]
	if len(c.out) > 0 {
		c.end.WaitWritable()
	}
}

func (c *opClient) read() {
	for !c.done && !c.noRead {
		n, err := c.end.Read(c.buf[:])
		if n > 0 {
			c.sess.Feed(c.buf[:n])
			c.drain()
			continue
		}
		if err == io.EOF || (err != nil && !errors.Is(err, simnet.ErrNotConnected)) {
			c.gone()
		}
		return
	}
}

func (c *opClient) drain() {
	for !c.done {
		m, ok, err := c.sess.Next()
		if err != nil {
			if strings.Contains(err.Error(), "FATAL") {
				c.gone()
				return
			}
			c.ow.violate("client %d: undecodable reply: %v", c.idx, err)
			c.closeNow()
			return
		}
		if !ok {
			return
		}
		c.message(m)
	}
}

func (c *opClient) message(m testclient.Msg) {
	t := token(m)
	if !c.ready {
		if m.Type == testclient.MsgReady {
			c.ready = true
			if k := c.pending; k != 0 {
				c.pending = 0
				c.query(k)
			}
		}
		return
	}
	if strings.Contains(t, "DISCARD") {
		c.ow.violate("client %d saw the reset's reply: %s", c.idx, t)
	}
	if !c.busy {
		if m.Type != testclient.MsgError && m.Type != testclient.MsgNotice && m.Type != testclient.MsgParamStatus {
			c.ow.violate("client %d: unsolicited %s", c.idx, t)
		}
		return
	}
	c.tokens = append(c.tokens, t)
	if m.Type == testclient.MsgCommand && !tagFits(c.kind, m.Tag) {
		c.ow.violate("client %d: %s answered with tag %q", c.idx, opNames[c.kind-OpConnect], m.Tag)
	}
	if m.Type == testclient.MsgReady {
		c.busy = false
		c.tokens = c.tokens[:0]
	}
}

func tagFits(k sim.FaultKind, tag string) bool {
	switch k {
	case OpRead, OpSlowRead:
		return tag == "SELECT 1"
	case OpBigRead:
		return tag == "SELECT 200"
	case OpWrite:
		return tag == "UPDATE 1"
	case OpBegin:
		return tag == "BEGIN"
	case OpCommit:
		return tag == "COMMIT" || tag == "ROLLBACK"
	case OpRollback:
		return tag == "ROLLBACK"
	}
	return false
}

func (c *opClient) query(k sim.FaultKind) {
	if c.done || c.busy {
		return
	}
	if !c.ready {
		c.pending = k
		return
	}
	c.busy, c.kind = true, k
	switch k {
	case OpRead:
		c.send(c.sess.Query(nil, read(c.idx, "a")))
	case OpSlowRead:
		c.send(c.sess.Query(nil, "SELECT v FROM kv WHERE k = '"+slowKey+"'"))
	case OpBigRead:
		c.send(c.sess.Query(nil, rowsQuery(200, 100)))
	case OpWrite:
		c.send(c.sess.Query(nil, update(c.idx, "a", "w")))
	case OpBegin:
		c.send(c.sess.Query(nil, "BEGIN"))
	case OpCommit:
		c.send(c.sess.Query(nil, "COMMIT"))
	case OpRollback:
		c.send(c.sess.Query(nil, "ROLLBACK"))
	case OpReject:
		c.send(c.sess.Query(nil, "SET search_path = x"))
	case OpExtended:
		c.send(ExtendedMessages())
	}
}

func (c *opClient) closeNow() {
	if c.done {
		return
	}
	c.done = true
	_ = c.end.Close()
}

func (c *opClient) resetNow() {
	if c.done {
		return
	}
	c.done = true
	_ = c.end.Reset()
}

func (c *opClient) gone() {
	if c.done {
		return
	}
	c.done = true
	_ = c.end.Close()
}
