package e2e

import (
	"errors"
	"io"
	"strings"
	"time"

	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/proxy/proxysim"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
	"github.com/nickstrad/mini-gateway/internal/testclient"
)

const (
	maxTokens   = 1 << 16
	vanishDelay = 10 * time.Millisecond
)

type simClient struct {
	s        *sim.Sim
	net      *simnet.Net
	addr     string
	opts     simnet.Options
	idx      int
	script   Script
	end      *simnet.End
	sess     testclient.Session
	out      []byte
	step     int
	inStep   bool
	ready    bool
	done     bool
	noRead   bool
	res      *ClientResult
	cur      []string
	buf      [proxy.MaxRead]byte
	dropped  int64
	timer    sim.Timer
	sentAt   sim.Time
	finished func()
}

func newSimClient(s *sim.Sim, net *simnet.Net, addr string, opts simnet.Options, idx int, sc Script, res *ClientResult) *simClient {
	return &simClient{s: s, net: net, addr: addr, opts: opts, idx: idx, script: sc, res: res}
}

func (c *simClient) start() {
	c.timer = c.s.After(c.script.Start, c.dial)
}

func (c *simClient) dial() {
	c.end = c.net.Dial(c.addr, c.opts, c)
}

func (c *simClient) OnEvent(_ *simnet.End, ev simnet.Event) {
	if c.done {
		return
	}
	switch ev {
	case simnet.EvConnected:
		params := []testclient.Param{{Name: "user", Value: proxysim.TenantName(c.script.Tenant)}, {Name: "database", Value: proxysim.Database}}
		c.send(c.sess.Startup(nil, params))
	case simnet.EvRefused, simnet.EvReset:
		c.gone("reset")
	case simnet.EvReadable:
		c.read()
	case simnet.EvWritable:
		c.flush()
	}
}

func (c *simClient) send(b []byte) {
	c.out = append(c.out, b...)
	c.flush()
}

func (c *simClient) flush() {
	if len(c.out) == 0 || c.done {
		return
	}
	n, err := c.end.Write(c.out)
	if err != nil {
		c.gone("write-error")
		return
	}
	c.out = c.out[n:]
	if len(c.out) > 0 {
		c.end.WaitWritable()
	}
}

func (c *simClient) read() {
	for !c.done && !c.noRead {
		n, err := c.end.Read(c.buf[:])
		if n > 0 {
			c.sess.Feed(c.buf[:n])
			c.drain()
			continue
		}
		if err == io.EOF || (err != nil && !errors.Is(err, simnet.ErrNotConnected)) {
			c.gone("eof")
		}
		return
	}
}

func (c *simClient) drain() {
	for !c.done {
		m, ok, err := c.sess.Next()
		if err != nil {
			c.gone("protocol")
			return
		}
		if !ok {
			return
		}
		c.message(m)
	}
}

func token(m testclient.Msg) string {
	switch m.Type {
	case testclient.MsgReady:
		return "Z" + string(m.Status)
	case testclient.MsgError:
		return "E:" + m.Severity + ":" + m.Code
	case testclient.MsgNotice:
		return "N:" + m.Code
	case testclient.MsgCommand:
		return "C:" + m.Tag
	case testclient.MsgParamStatus:
		return "S:" + m.Name + "=" + m.Value
	case testclient.MsgDataRow:
		vals := make([]string, len(m.Values))
		for i, v := range m.Values {
			vals[i] = string(v)
		}
		return "D:" + strings.Join(vals, "|")
	case testclient.MsgRowDesc:
		return "T"
	case testclient.MsgEmptyQuery:
		return "I"
	case testclient.MsgSSLAnswer:
		return "ssl:" + string(m.Status)
	}
	return string(m.Type)
}

func (c *simClient) record(dst *[]string, t string) {
	if len(*dst) < maxTokens {
		*dst = append(*dst, t)
		return
	}
	c.dropped++
}

func (c *simClient) message(m testclient.Msg) {
	if !c.ready {
		switch m.Type {
		case testclient.MsgReady:
			c.ready = true
			c.next()
		case testclient.MsgError:
			c.record(&c.res.Startup, token(m))
		}
		return
	}
	if !c.inStep {
		c.record(&c.res.Startup, "unsolicited:"+token(m))
		return
	}
	c.record(&c.cur, token(m))
	if m.Type == testclient.MsgReady {
		c.endStep()
	}
}

func (c *simClient) endStep() {
	c.res.Steps = append(c.res.Steps, c.cur)
	c.res.Elapsed = append(c.res.Elapsed, c.s.Now().Sub(c.sentAt))
	c.cur = nil
	c.inStep = false
	st := c.script.Steps[c.step]
	c.step++
	if st.Vanish == VanishAfterReply {
		c.vanish()
		return
	}
	c.next()
}

func (c *simClient) next() {
	if c.step >= len(c.script.Steps) {
		c.send(testclient.EncodeTerminate(nil))
		c.finish()
		return
	}
	st := c.script.Steps[c.step]
	c.timer = c.s.After(st.Delay, c.sendStep)
}

func (c *simClient) sendStep() {
	if c.done {
		return
	}
	st := c.script.Steps[c.step]
	c.inStep = true
	c.sentAt = c.s.Now()
	if st.Raw != nil {
		c.send(st.Raw)
	} else {
		c.send(c.sess.Query(nil, st.SQL))
	}
	if st.StopReading {
		c.noRead = true
		c.s.After(st.StallFor, c.unstall)
	}
	if st.Vanish == VanishAfterSend {
		c.res.Steps = append(c.res.Steps, append(c.cur, "vanished"))
		c.done = true
		c.res.Closed = true
		c.timer = c.s.After(vanishDelay, func() {
			_ = c.end.Reset()
			c.complete()
		})
	}
}

func (c *simClient) vanish() {
	c.done = true
	c.res.Closed = true
	_ = c.end.Close()
	c.complete()
}

func (c *simClient) finish() {
	c.done = true
	c.flush()
	_ = c.end.Close()
	c.complete()
}

func (c *simClient) gone(why string) {
	if c.done {
		return
	}
	if c.inStep {
		c.record(&c.cur, "closed:"+why)
		c.res.Steps = append(c.res.Steps, c.cur)
		c.res.Elapsed = append(c.res.Elapsed, c.s.Now().Sub(c.sentAt))
		c.cur = nil
	} else if !c.ready {
		c.record(&c.res.Startup, "closed:"+why)
	}
	c.res.Closed = true
	c.done = true
	if c.end != nil {
		_ = c.end.Close()
	}
	c.complete()
}

func (c *simClient) complete() {
	c.timer.Cancel()
	if c.finished != nil {
		c.finished()
	}
}

func (c *simClient) unstall() {
	if c.noRead && !c.done {
		c.noRead = false
		c.read()
	}
}
