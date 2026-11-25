package workload

import (
	"errors"
	"io"
	"math/rand/v2"
	"slices"
	"strconv"
	"time"

	"github.com/nickstrad/mini-gateway/internal/randdist"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
	"github.com/nickstrad/mini-gateway/internal/testclient"
)

type cphase uint8

const (
	cIdle cphase = iota
	cConnecting
	cStartup
	cReady
	cDone
)

type request struct {
	id        int64
	intended  sim.Time
	sentAt    sim.Time
	stmts     []string
	orig      []string
	next      int
	attempts  int
	everSent  bool
	code      string
	fatal     bool
	inTx      bool
	leaveInTx bool
}

type conn struct {
	c   *client
	end *simnet.End
}

func (cc *conn) OnEvent(e *simnet.End, ev simnet.Event) {
	if cc.c.cc != cc {
		return
	}
	cc.c.onNet(ev)
}

type client struct {
	w        *Workload
	t        *tenantState
	id       int
	local    int
	arrivals *rand.Rand
	keys     *KeyChooser
	mix      *rand.Rand
	retryR   *rand.Rand
	cc       *conn
	sess     testclient.Session
	phase    cphase
	out      []byte
	backlog  []*request
	cur      *request
	made     int64
	waiting  int
	burst    bool
	wantConn bool
	connTry  int

	arrivalT  sim.Timer
	thinkT    sim.Timer
	deadlineT sim.Timer
	holdT     sim.Timer
	connT     sim.Timer
	stallT    sim.Timer
	resumeT   sim.Timer

	halfMessage bool
	stalled     bool
	stopReading bool
	leaveAfter  bool
	leaveOnSend bool
	sessions    int64
	leaveReset  bool
}

func newClient(w *Workload, t *tenantState, id, local int) *client {
	base := label(t.cfg.Name, itoa(local))
	c := &client{w: w, t: t, id: id, local: local,
		arrivals: w.s.Stream(base + "/arrivals"), keys: NewKeyChooser(w.s.Stream(base+"/keys"), t.cfg.Keys),
		mix: w.s.Stream(base + "/mix"), retryR: w.s.Stream(base + "/retry")}
	return c
}

func (c *client) finished() bool { return c.phase == cDone }

func (c *client) describe() string {
	return "phase " + strconv.Itoa(int(c.phase)) + ", backlog " + strconv.Itoa(len(c.backlog)) +
		", made " + strconv.FormatInt(c.made, 10) + ", current " + strconv.FormatBool(c.cur != nil)
}

func (c *client) cfg() *Tenant { return &c.t.cfg }

func (c *client) start() {
	t := c.cfg()
	at := sim.Later(0, t.Start)
	if t.Spread && t.Clients > 0 && t.Arrival != ArrivalClosed {
		at = sim.Later(at, time.Duration(int64(t.Interval)*int64(c.local)/int64(t.Clients)))
	}
	if t.ConnectFirst {
		c.wantConn = true
		c.w.s.At(sim.Later(0, t.Start), c.pump)
	}
	if t.Requests == 0 {
		if !t.ConnectFirst {
			c.w.s.At(sim.Later(0, t.Start), c.pump)
		}
		return
	}
	c.arrivalT = c.w.s.At(at, c.arrive)
}

func (c *client) moreArrivals() bool { return c.made < int64(c.cfg().Requests) }

func (c *client) arrive() {
	t := c.cfg()
	st := &c.t.st
	c.made++
	st.intended++
	r := &request{id: c.made, intended: c.w.s.Now(), stmts: c.statements()}
	r.orig = r.stmts
	c.w.s.Trace(kindArrive, int64(c.id), r.id, 0, 0)
	if len(c.backlog) >= t.Backlog {
		st.dropped++
		c.w.s.Trace(kindDrop, int64(c.id), r.id, 0, 0)
	} else {
		c.backlog = append(c.backlog, r)
		st.notYet.Add(1)
	}
	if c.moreArrivals() && t.Arrival != ArrivalClosed {
		gap := t.Interval
		if t.Arrival == ArrivalPoisson {
			gap = randdist.ExpGap(c.arrivals, t.Interval)
		}
		c.arrivalT = c.w.s.After(gap, c.arrive)
	}
	c.pump()
}

func (c *client) key() string { return c.keys.Next() }

func (c *client) pickOp() Op {
	mix := c.cfg().Mix
	total := 0
	for _, w := range mix {
		total += max(w.Weight, 0)
	}
	if total == 0 {
		return mix[0].Op
	}
	n := c.mix.IntN(total)
	for _, w := range mix {
		n -= max(w.Weight, 0)
		if n < 0 {
			return w.Op
		}
	}
	return mix[len(mix)-1].Op
}

func (c *client) tag(s string) string {
	if !c.cfg().Tag {
		return s
	}
	return "/*c" + strconv.Itoa(c.id) + "*/ " + s
}

func (c *client) read() string { return c.tag("SELECT v FROM kv WHERE k = '" + c.key() + "'") }

func (c *client) write(i int) string {
	v := "c" + strconv.Itoa(c.id) + "-" + strconv.FormatInt(c.made, 10) + "-" + strconv.Itoa(i)
	return c.tag("UPDATE kv SET v = '" + v + "' WHERE k = '" + c.key() + "'")
}

func (c *client) statements() []string {
	t := c.cfg()
	switch c.pickOp() {
	case OpWrite:
		return []string{c.write(0)}
	case OpTx:
		out := []string{c.tag("BEGIN")}
		for i := 0; i < t.TxStatements; i++ {
			if i < t.TxReads {
				out = append(out, c.read())
			} else {
				out = append(out, c.write(i))
			}
		}
		return append(out, c.tag("COMMIT"))
	case OpFixed:
		return []string{c.tag(t.Statement)}
	}
	return []string{c.read()}
}

func (c *client) idleAndEmpty() bool {
	return c.cur == nil && len(c.backlog) == 0 && c.waiting == 0 && !c.moreArrivals()
}

func (c *client) pump() {
	if c.phase == cDone || c.stalled {
		return
	}
	if c.cur != nil {
		return
	}
	if len(c.backlog) == 0 && !c.wantConn {
		if c.idleAndEmpty() && !c.arrivalT.Pending() && !c.thinkT.Pending() {
			c.finishLater()
		}
		return
	}
	switch c.phase {
	case cIdle:
		c.connect()
		return
	case cConnecting, cStartup:
		return
	}
	c.wantConn = false
	if len(c.backlog) == 0 {
		c.pump()
		return
	}
	c.holdT.Cancel()
	c.cur = c.backlog[0]
	c.backlog = c.backlog[1:]
	c.sendNext()
}

func (c *client) finishLater() {
	if c.holdT.Pending() || c.phase == cDone {
		return
	}
	hold := c.cfg().Hold
	if c.phase != cReady {
		hold = 0
	}
	c.holdT = c.w.s.After(hold, c.finish)
}

func (c *client) finish() {
	if !c.idleAndEmpty() || c.wantConn || c.burst {
		return
	}
	c.drop(false)
	c.phase = cDone
}

func (c *client) connect() {
	st := &c.t.st
	st.connects++
	c.connTry++
	c.phase = cConnecting
	c.sess = testclient.Session{}
	c.out = nil
	c.cc = &conn{c: c}
	c.cc.end = c.w.n.Dial(c.w.cfg.Addr, c.w.cfg.Options, c.cc)
	st.open.Add(1)
	c.w.s.Trace(kindConnect, int64(c.id), c.cc.end.ID(), 0, 0)
	if d := c.cfg().Deadline; d > 0 {
		c.connT = c.w.s.After(d, func() { c.connectFailed("", true) })
	}
}

func (c *client) onNet(ev simnet.Event) {
	switch ev {
	case simnet.EvConnected:
		if c.phase == cConnecting {
			c.phase = cStartup
			c.send(c.sess.Startup(nil, []testclient.Param{{Name: "user", Value: c.cfg().Name}, {Name: "database", Value: c.cfg().Database}}))
		}
	case simnet.EvRefused:
		c.connectFailed("", false)
	case simnet.EvReset:
		c.lost("")
	case simnet.EvWritable:
		c.flush()
	case simnet.EvReadable:
		c.readAll()
	}
}

func (c *client) send(b []byte) {
	c.out = append(c.out, b...)
	c.flush()
}

func (c *client) flush() {
	if c.cc == nil || len(c.out) == 0 {
		return
	}
	n, err := c.cc.end.Write(c.out)
	if err != nil {
		c.out = nil
		return
	}
	c.out = c.out[n:]
}

func (c *client) readAll() {
	for c.cc != nil && !c.stopReading {
		end := c.cc.end
		n, err := end.Read(c.w.readBuf[:])
		if n > 0 {
			c.sess.Feed(c.w.readBuf[:n])
			if !c.drainMessages() {
				return
			}
			continue
		}
		if err == io.EOF || (err != nil && !errors.Is(err, simnet.ErrClosed)) {
			c.lost("")
		}
		return
	}
}

func (c *client) drainMessages() bool {
	for c.cc != nil {
		m, ok, err := c.sess.Next()
		if ok {
			c.onMessage(m)
			continue
		}
		if err != nil {
			code := ""
			if errors.Is(err, testclient.ErrClosed) && c.cur != nil {
				code = c.cur.code
			}
			c.lost(code)
			return false
		}
		return true
	}
	return false
}

func (c *client) onMessage(m testclient.Msg) {
	switch m.Type {
	case testclient.MsgError:
		if c.phase == cStartup {
			c.connectFailed(m.Code, false)
			return
		}
		if c.cur != nil && c.cur.code == "" {
			c.cur.code = m.Code
		}
	case testclient.MsgReady:
		if c.phase == cStartup {
			c.ready()
			return
		}
		c.onReady(m.Status)
	}
}

func (c *client) ready() {
	st := &c.t.st
	c.connT.Cancel()
	c.phase = cReady
	st.connectsOK++
	if c.sessions > 0 {
		st.reconnects++
	}
	c.sessions++
	c.connTry = 0
	c.burst = false
	c.pump()
}

func (c *client) sendNext() {
	r := c.cur
	st := &c.t.st
	if !r.everSent {
		r.everSent = true
		st.sent++
		st.notYet.Add(-1)
		st.inFlight.Add(1)
	}
	if r.next == 0 {
		r.sentAt = c.w.s.Now()
		if d := c.cfg().Deadline; d > 0 {
			c.deadlineT.Cancel()
			c.deadlineT = c.w.s.After(d, c.timedOut)
		}
	}
	q := r.stmts[r.next]
	r.next++
	c.w.s.Trace(kindSend, int64(c.id), r.id, int64(r.next), int64(r.attempts))
	b := c.sess.Query(nil, q)
	if c.halfMessage {
		c.halfMessage = false
		c.stalled = true
		c.t.st.halfMessages++
		c.send(b[:len(b)/2])
		c.stallT = c.w.s.After(DefaultHalfHold, c.unstall)
		return
	}
	c.send(b)
	if d, ok := c.disconnectFor(r, DisconnectAfterSend); ok {
		c.leave(d.Reset)
		return
	}
	if c.leaveOnSend {
		c.leaveOnSend = false
		c.leave(c.leaveReset)
	}
}

func (c *client) disconnectFor(r *request, p DisconnectPoint) (Disconnect, bool) {
	for _, d := range c.cfg().Disconnect {
		if d.Point == p && int64(d.Request) == r.id-1 && r.attempts == 0 {
			return d, true
		}
	}
	return Disconnect{}, false
}

func (c *client) unstall() {
	c.stalled = false
	c.leave(false)
}

func (c *client) onReady(status byte) {
	r := c.cur
	if r == nil {
		return
	}
	if status == 'T' {
		if d, ok := c.disconnectFor(r, DisconnectInTx); ok {
			c.leave(d.Reset)
			return
		}
		if c.leaveInTx() {
			return
		}
	}
	if status == 'E' && r.next < len(r.stmts) {
		r.stmts = append(slices.Clip(r.stmts[:r.next]), c.tag("ROLLBACK"))
	}
	if r.next < len(r.stmts) && (r.code == "" || status == 'E') {
		if d := c.cfg().TxThink; d > 0 {
			c.thinkT = c.w.s.After(d, func() {
				if c.cur == r {
					c.sendNext()
				}
			})
			return
		}
		c.sendNext()
		return
	}
	if r.code != "" {
		c.cur = nil
		c.deadlineT.Cancel()
		c.fail(r, OutcomeError)
		c.afterRequest()
		return
	}
	c.complete(OutcomeOK)
}

func (c *client) leaveInTx() bool {
	if !c.leaveAfter || c.cur == nil {
		return false
	}
	c.leaveAfter = false
	c.leave(c.leaveReset)
	return true
}

func (c *client) complete(o Outcome) {
	r := c.cur
	c.cur = nil
	c.deadlineT.Cancel()
	c.record(r, o)
	c.afterRequest()
}

func (c *client) afterRequest() {
	t := c.cfg()
	if t.Arrival == ArrivalClosed && c.moreArrivals() && !c.arrivalT.Pending() {
		c.arrivalT = c.w.s.After(t.Think, c.arrive)
	}
	c.pump()
}

func (c *client) record(r *request, o Outcome) {
	st := &c.t.st
	now := c.w.s.Now()
	if r.everSent {
		st.inFlight.Add(-1)
	} else {
		st.notYet.Add(-1)
	}
	if o == OutcomeConnectFailed && r.everSent {
		o = OutcomeLost
		if r.code != "" {
			o = OutcomeError
		}
	}
	switch o {
	case OutcomeOK:
		st.ok++
	case OutcomeError:
		st.errs++
	case OutcomeLost:
		st.lost++
	case OutcomeTimedOut:
		st.timedOut++
	case OutcomeClientLeft:
		st.left++
	case OutcomeConnectFailed:
		st.unsentFailed++
	}
	st.code(r.code)
	if o == OutcomeOK || o == OutcomeError {
		c.t.latency.Record(int64(now - r.intended))
		c.t.fromSend.Record(int64(now - r.sentAt))
	}
	c.w.s.Trace(kindDone, int64(c.id), r.id, int64(o), int64(now-r.intended))
}

func (c *client) retryable(r *request, o Outcome) bool {
	p := &c.cfg().Retry
	if r.attempts >= p.Max {
		return false
	}
	switch o {
	case OutcomeConnectFailed:
		if p.OnConnect {
			return true
		}
	case OutcomeLost:
		if p.OnLost {
			return true
		}
	case OutcomeTimedOut:
		if p.OnTimeout {
			return true
		}
	}
	for _, code := range p.OnCodes {
		if code == r.code {
			return true
		}
	}
	return false
}

func (c *client) backoff(attempt int) time.Duration {
	p := &c.cfg().Retry
	d := sim.Scaled(int64(1)<<min(max(attempt, 0), 30), p.Backoff)
	if p.Cap > 0 && d > p.Cap {
		d = p.Cap
	}
	return d
}

func (c *client) fail(r *request, o Outcome) {
	if r == nil {
		return
	}
	if c.retryable(r, o) {
		r.attempts++
		r.next = 0
		r.code = ""
		r.stmts = r.orig
		c.t.st.retries++
		c.waiting++
		c.w.s.Trace(kindRetry, int64(c.id), r.id, int64(r.attempts), int64(o))
		c.w.s.After(c.backoff(r.attempts-1), func() {
			c.waiting--
			c.backlog = append([]*request{r}, c.backlog...)
			c.pump()
		})
		return
	}
	c.record(r, o)
}

func (c *client) drop(reset bool) {
	c.connT.Cancel()
	c.deadlineT.Cancel()
	c.thinkT.Cancel()
	if c.cc == nil {
		c.phase = cIdle
		return
	}
	end := c.cc.end
	c.cc = nil
	c.out = nil
	c.t.st.open.Add(-1)
	if reset {
		_ = end.Reset()
	} else {
		if c.phase == cReady {
			_, _ = end.Write(testclient.EncodeTerminate(nil))
		}
		_ = end.Close()
	}
	c.phase = cIdle
}

func (c *client) connectFailed(code string, timeout bool) {
	if c.phase != cConnecting && c.phase != cStartup {
		return
	}
	st := &c.t.st
	st.connectsFailed++
	c.drop(timeout)
	if len(c.backlog) > 0 {
		r := c.backlog[0]
		c.backlog = c.backlog[1:]
		r.code = code
		c.fail(r, OutcomeConnectFailed)
	} else if code != "" {
		st.code(code)
	}
	if c.wantConn || c.burst {
		p := &c.cfg().Retry
		if p.OnConnect && c.connTry <= p.Max {
			c.w.s.After(c.backoff(c.connTry-1), c.pump)
			return
		}
		c.wantConn, c.burst = false, false
	}
	c.afterRequest()
}

func (c *client) lost(code string) {
	if c.phase == cConnecting || c.phase == cStartup {
		c.connectFailed(code, false)
		return
	}
	if c.cc == nil {
		return
	}
	r := c.cur
	c.cur = nil
	c.stalled = false
	c.stallT.Cancel()
	c.drop(false)
	if r != nil {
		if code == "" {
			code = r.code
		}
		r.code = code
		o := OutcomeLost
		if code != "" {
			o = OutcomeError
		}
		c.fail(r, o)
	}
	c.afterRequest()
}

func (c *client) timedOut() {
	r := c.cur
	if r == nil {
		return
	}
	c.cur = nil
	c.stalled = false
	c.stallT.Cancel()
	c.drop(false)
	c.fail(r, OutcomeTimedOut)
	c.afterRequest()
}

func (c *client) leave(reset bool) {
	c.t.st.disconnects++
	r := c.cur
	c.cur = nil
	c.drop(reset)
	if r != nil {
		c.record(r, OutcomeClientLeft)
	}
	c.afterRequest()
}
