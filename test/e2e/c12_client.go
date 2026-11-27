package e2e

import (
	"errors"
	"io"
	"time"

	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/proxy/proxysim"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
	"github.com/nickstrad/mini-gateway/internal/testclient"
)

const (
	clientBackoff     = time.Millisecond
	clientRedial      = 5 * time.Millisecond
	maxClientAttempts = 6
	maxClientOps      = 1 << 14
	maxClientErrors   = 1 << 16
)

type Phase struct{ From, Until time.Duration }

type RetryOp struct {
	Text     string
	Start    sim.Time
	Done     sim.Time
	Attempts int
	OK       bool
	Code     string
}

type ErrReply struct {
	Code    string
	Elapsed time.Duration
	At      sim.Time
}

type RetryClientResult struct {
	Ops      []RetryOp
	Errors   []ErrReply
	Sent     int64
	Redials  int64
	Overflow int64
}

type retryClient struct {
	s       *sim.Sim
	net     *simnet.Net
	addr    string
	opts    simnet.Options
	idx     int
	tenant  int
	phases  []Phase
	gap     time.Duration
	res     *RetryClientResult
	end     *simnet.End
	sess    testclient.Session
	out     []byte
	buf     [proxy.MaxRead]byte
	ready   bool
	stopped bool
	op      *RetryOp
	code    string
	sentAt  sim.Time
	waiting bool
	n       int
	timer   sim.Timer
}

func retryable(code string) bool { return code == "53M01" || code == "53M03" || code == "08006" }

func (c *retryClient) start() {
	if len(c.phases) == 0 {
		return
	}
	c.timer = c.s.At(sim.Time(c.phases[0].From), c.dial)
}

func (c *retryClient) dial() {
	c.ready = false
	c.sess = testclient.Session{}
	c.out = nil
	c.end = c.net.Dial(c.addr, c.opts, simnet.HandlerFunc(c.onEvent))
}

func (c *retryClient) onEvent(e *simnet.End, ev simnet.Event) {
	if c.stopped || e != c.end {
		return
	}
	switch ev {
	case simnet.EvConnected:
		params := []testclient.Param{{Name: "user", Value: proxysim.TenantName(c.tenant)}, {Name: "database", Value: proxysim.Database}}
		c.send(c.sess.Startup(nil, params))
	case simnet.EvRefused, simnet.EvReset:
		c.lost()
	case simnet.EvReadable:
		c.read()
	case simnet.EvWritable:
		c.flush()
	}
}

func (c *retryClient) send(b []byte) {
	c.out = append(c.out, b...)
	c.flush()
}

func (c *retryClient) flush() {
	if len(c.out) == 0 || c.end == nil {
		return
	}
	n, err := c.end.Write(c.out)
	if err != nil {
		c.lost()
		return
	}
	c.out = c.out[n:]
	if len(c.out) > 0 {
		c.end.WaitWritable()
	}
}

func (c *retryClient) read() {
	for !c.stopped && c.end != nil {
		n, err := c.end.Read(c.buf[:])
		if n > 0 {
			c.sess.Feed(c.buf[:n])
			if !c.drain() {
				return
			}
			continue
		}
		if err == io.EOF || (err != nil && !errors.Is(err, simnet.ErrNotConnected)) {
			c.lost()
		}
		return
	}
}

func (c *retryClient) drain() bool {
	for !c.stopped {
		m, ok, err := c.sess.Next()
		if err != nil {
			c.lost()
			return false
		}
		if !ok {
			return true
		}
		switch m.Type {
		case testclient.MsgError:
			if c.waiting {
				c.code = m.Code
			}
		case testclient.MsgReady:
			if !c.ready {
				c.ready = true
				c.next()
				continue
			}
			c.attemptDone()
		}
	}
	return false
}

func (c *retryClient) phaseAt(now time.Duration) (Phase, bool) {
	for _, p := range c.phases {
		if now < p.Until {
			return p, true
		}
	}
	return Phase{}, false
}

func (c *retryClient) next() {
	if c.op != nil {
		c.sendAttempt()
		return
	}
	now := time.Duration(c.s.Now())
	p, ok := c.phaseAt(now + c.gap)
	if !ok || len(c.res.Ops) >= maxClientOps {
		c.stop()
		return
	}
	at := max(now+c.gap, p.From)
	c.timer = c.s.At(sim.Time(at), c.newOp)
}

func (c *retryClient) newOp() {
	if c.stopped {
		return
	}
	c.n++
	c.op = &RetryOp{Text: update(c.idx, "a", "c"+itoa(c.idx)+"-"+itoa(c.n)), Start: c.s.Now()}
	c.sendAttempt()
}

func (c *retryClient) sendAttempt() {
	if !c.ready || c.stopped {
		return
	}
	c.op.Attempts++
	c.res.Sent++
	c.code = ""
	c.waiting = true
	c.sentAt = c.s.Now()
	c.send(c.sess.Query(nil, c.op.Text))
}

func (c *retryClient) recordError(code string) {
	if len(c.res.Errors) >= maxClientErrors {
		c.res.Overflow++
		return
	}
	c.res.Errors = append(c.res.Errors, ErrReply{Code: code, Elapsed: c.s.Now().Sub(c.sentAt), At: c.s.Now()})
}

func (c *retryClient) attemptDone() {
	c.waiting = false
	if c.code == "" {
		c.finishOp(true, "")
		c.next()
		return
	}
	c.recordError(c.code)
	if retryable(c.code) && c.op.Attempts < maxClientAttempts {
		c.timer = c.s.After(clientBackoff, c.sendAttempt)
		return
	}
	c.finishOp(false, c.code)
	c.next()
}

func (c *retryClient) finishOp(ok bool, code string) {
	c.op.OK, c.op.Code, c.op.Done = ok, code, c.s.Now()
	c.res.Ops = append(c.res.Ops, *c.op)
	c.op = nil
}

func (c *retryClient) lost() {
	if c.stopped || c.end == nil {
		return
	}
	_ = c.end.Close()
	c.end = nil
	if c.waiting {
		c.waiting = false
		c.recordError("closed")
		c.finishOp(false, "closed")
	}
	c.res.Redials++
	c.timer.Cancel()
	if _, ok := c.phaseAt(time.Duration(c.s.Now()) + clientRedial); !ok {
		c.stopped = true
		return
	}
	c.timer = c.s.After(clientRedial, c.dial)
}

func (c *retryClient) stop() {
	c.stopped = true
	c.timer.Cancel()
	if c.end != nil {
		c.send(testclient.EncodeTerminate(nil))
		_ = c.end.Close()
	}
}
