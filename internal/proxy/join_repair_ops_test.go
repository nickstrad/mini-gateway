package proxy_test

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/coalesce"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/proxy/proxysim"
)

const (
	opsSequences      = 400
	opsSequencesShort = 60
	opsPerSequence    = 160
	opsClients        = 8
	opsFirstClient    = proxy.ConnID(1_000_000)
	opsDrainRounds    = 400
)

var opsRow = message(pgwire.MsgDataRow, []byte{0, 1, 0, 0, 0, 1, 'v'})

var opsKeys = []string{sharedRead, "SELECT v FROM kv WHERE k = 'b'"}

type opsKind uint8

const (
	opSend opsKind = iota
	opLeave
	opRefuse
	opLose
	opReply
	opServer
	opOpen
	opDialFail
	opTime
	opBreaker
	opCancelDone
	opClose
	numOps
)

var opsProfiles = [][numOps]int{
	{opSend: 6, opLeave: 1, opRefuse: 3, opLose: 1, opReply: 3, opServer: 3, opOpen: 2, opDialFail: 1, opTime: 3, opBreaker: 1, opCancelDone: 1, opClose: 2},
	{opSend: 6, opLeave: 1, opRefuse: 2, opReply: 1, opServer: 4, opOpen: 3, opTime: 4, opBreaker: 1, opCancelDone: 2, opClose: 2},
}

type opsRig struct {
	*coreRig
	rng        *rand.Rand
	weights    [numOps]int
	maxStep    int
	b          *switchBreaker
	clients    []proxy.ConnID
	busy       map[proxy.ConnID]bool
	next       proxy.ConnID
	reads      map[proxy.ConnID]int
	needs      map[proxy.ConnID][]byte
	connecting []proxy.ConnID
	pending    []proxy.ConnID
	refused    []proxy.ConnID
	arrived    map[proxy.ConnID]proxy.Instant
	retried    map[proxy.ConnID]bool
	sentNow    []proxy.ConnID
	gotReply   []proxy.ConnID
	trail      []string
}

type opsMoved struct {
	asked, expired, promoted, promotedRefused, breakerEnds, flushed, handoffs, held, failed int64
}

func newOpsRig(t *testing.T, seed uint64) *opsRig {
	t.Helper()
	rng := rand.New(rand.NewPCG(seed, 0x6a6f696e))
	join := coalesce.JoinStrict
	if rng.IntN(4) == 0 {
		join = coalesce.JoinRelaxed
	}
	flight := []time.Duration{joinStatement / 2, joinStatement, 2 * joinStatement}[rng.IntN(3)]
	b := &switchBreaker{}
	r := fairRig(t, 3, func(c *proxy.Config) {
		coalesceOn(join)(c)
		retryOn(c)
		c.Coalesce.Deadline = coalesce.Instant(flight)
		c.Pool.BreakerFailures = 3
		c.Seams.Breaker = b
	})
	profile := rng.IntN(len(opsProfiles))
	o := &opsRig{coreRig: r, rng: rng, b: b, weights: opsProfiles[profile], maxStep: 150 / (1 + profile), busy: map[proxy.ConnID]bool{}, next: opsFirstClient,
		reads: map[proxy.ConnID]int{}, needs: map[proxy.ConnID][]byte{}, arrived: map[proxy.ConnID]proxy.Instant{},
		retried: map[proxy.ConnID]bool{}}
	o.note("join %v flight %v profile %d", join, flight, profile)
	for range opsClients {
		o.newClient()
	}
	return o
}

func (o *opsRig) note(f string, args ...any) { o.trail = append(o.trail, fmt.Sprintf(f, args...)) }

func (o *opsRig) newClient() {
	id := o.next
	o.next++
	o.take(o.ev(proxy.EvClientAccepted, id, nil, 0))
	b := pgwire.AppendStartupMessage(nil, []pgwire.Param{{Name: "user", Value: proxysim.TenantName(int(id) % 3)}, {Name: "database", Value: proxysim.Database}})
	o.take(o.ev(proxy.EvClientData, id, b[:proxy.FirstClientRead], 0))
	o.take(o.ev(proxy.EvClientData, id, b[proxy.FirstClientRead:], 0))
	o.clients = append(o.clients, id)
}

func (o *opsRig) isRead(b []byte) bool {
	for _, k := range opsKeys {
		if bytes.Equal(b, query(k)) {
			return true
		}
	}
	return false
}

func (o *opsRig) take(a []proxy.Action) {
	for _, x := range a {
		switch x.Kind {
		case proxy.ActClientWrite:
			if n := bytes.Count(x.Data, readyIdle); n > 0 {
				o.busy[x.Conn] = false
			}
			if bytes.Contains(x.Data, opsRow) {
				o.gotReply = append(o.gotReply, x.Conn)
			}
			o.take(o.ev(proxy.EvClientWrote, x.Conn, nil, len(x.Data)))
		case proxy.ActClientClose:
			o.clients = slices.DeleteFunc(o.clients, func(c proxy.ConnID) bool { return c == x.Conn })
			delete(o.busy, x.Conn)
		case proxy.ActDBWrite:
			o.dbWrite(x)
		case proxy.ActDBOpen:
			o.connecting = append(o.connecting, x.Conn)
			o.dbs = append(o.dbs, x.Conn)
		case proxy.ActDBClose:
			o.forget(x.Conn)
		case proxy.ActDBCancel:
			o.pending = append(o.pending, x.Conn)
		}
	}
}

func (o *opsRig) dbWrite(x proxy.Action) {
	switch {
	case o.isRead(x.Data):
		o.reads[x.Conn] += len(x.Data)
		o.sentNow = append(o.sentNow, x.Conn)
		return
	case x.Data[0] != 'Q':
		o.needs[x.Conn] = pgwire.AppendReadyForQuery(pgwire.AppendAuthentication(nil, 0, nil), 'I')
	case bytes.Equal(x.Data, query("")):
		o.needs[x.Conn] = emptyReply()
	case bytes.Equal(x.Data, query("DISCARD ALL")):
		o.needs[x.Conn] = complete("DISCARD ALL", 'I')
	case bytes.Equal(x.Data, query("ROLLBACK")):
		o.needs[x.Conn] = complete("ROLLBACK", 'I')
	}
	o.take(o.ev(proxy.EvDBWrote, x.Conn, nil, len(x.Data)))
}

func (o *opsRig) forget(id proxy.ConnID) {
	o.refused = slices.DeleteFunc(o.refused, func(c proxy.ConnID) bool { return c == id })
	delete(o.retried, id)
	delete(o.reads, id)
	delete(o.needs, id)
	o.connecting = slices.DeleteFunc(o.connecting, func(c proxy.ConnID) bool { return c == id })
}

func pick[T any](o *opsRig, xs []T) (T, bool) {
	var zero T
	if len(xs) == 0 {
		return zero, false
	}
	return xs[o.rng.IntN(len(xs))], true
}

func sortedKeys[V any](m map[proxy.ConnID]V) []proxy.ConnID {
	ks := make([]proxy.ConnID, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	return ks
}

func (o *opsRig) idleClients() []proxy.ConnID {
	var ids []proxy.ConnID
	for _, c := range o.clients {
		if !o.busy[c] {
			ids = append(ids, c)
		}
	}
	return ids
}

func (o *opsRig) busyClients() []proxy.ConnID {
	var ids []proxy.ConnID
	for _, c := range o.clients {
		if o.busy[c] {
			ids = append(ids, c)
		}
	}
	return ids
}

func (o *opsRig) chooseOp() opsKind {
	total := 0
	for _, w := range o.weights {
		total += w
	}
	n := o.rng.IntN(total)
	for k, w := range o.weights {
		if n < w {
			return opsKind(k)
		}
		n -= w
	}
	return opTime
}

func (o *opsRig) step(k opsKind) {
	forwards, attempts := o.forwards()
	defer o.markRetries(forwards, attempts)
	o.act(k)
	o.agree()
}

func (o *opsRig) act(k opsKind) {
	switch k {
	case opSend:
		if c, ok := pick(o, o.idleClients()); ok {
			key := opsKeys[boolIndex(o.rng.IntN(5) == 0)]
			o.note("send %d %q", c, key)
			o.busy[c] = true
			o.arrived[c] = o.now
			o.take(o.ev(proxy.EvClientData, c, query(key), 0))
		}
	case opLeave:
		if c, ok := pick(o, o.busyClients()); ok {
			o.note("leave %d", c)
			o.clients = slices.DeleteFunc(o.clients, func(x proxy.ConnID) bool { return x == c })
			delete(o.busy, c)
			o.take(o.ev(proxy.EvClientClosed, c, nil, 0))
			o.newClient()
		}
	case opRefuse, opLose:
		if d, ok := pick(o, sortedKeys(o.reads)); ok {
			o.note("%v %d", map[opsKind]string{opRefuse: "refuse", opLose: "lose"}[k], d)
			if k == opRefuse {
				o.take(o.ev(proxy.EvDBWriteRefused, d, nil, 0))
				if o.rng.IntN(2) == 0 {
					delete(o.reads, d)
					o.refused = append(o.refused, d)
					break
				}
			}
			o.forget(d)
			o.take(o.ev(proxy.EvDBClosed, d, nil, 0))
		}
	case opClose:
		if d, ok := pick(o, o.refused); ok {
			o.note("close %d", d)
			o.forget(d)
			o.take(o.ev(proxy.EvDBClosed, d, nil, 0))
		}
	case opReply:
		if d, ok := pick(o, sortedKeys(o.reads)); ok {
			o.note("reply %d", d)
			n := o.reads[d]
			delete(o.reads, d)
			o.take(o.ev(proxy.EvDBWrote, d, nil, n))
			retried := o.retried[d]
			o.gotReply = o.gotReply[:0]
			o.take(o.ev(proxy.EvDBData, d, readReply("v"), 0))
			o.retryReplyInTime(retried)
		}
	case opServer:
		if d, ok := pick(o, sortedKeys(o.needs)); ok {
			o.note("server %d", d)
			b := o.needs[d]
			delete(o.needs, d)
			o.take(o.ev(proxy.EvDBData, d, b, 0))
		}
	case opOpen, opDialFail:
		if d, ok := pick(o, o.connecting); ok {
			o.connecting = slices.DeleteFunc(o.connecting, func(c proxy.ConnID) bool { return c == d })
			if k == opOpen {
				o.note("open %d", d)
				o.take(o.ev(proxy.EvDBOpened, d, nil, 0))
			} else {
				o.note("dial failed %d", d)
				o.take(o.ev(proxy.EvDBDialFailed, d, nil, 0))
			}
		}
	case opTime:
		d := proxy.Instant(1+o.rng.IntN(o.maxStep)) * proxy.Instant(time.Millisecond)
		o.note("time +%v", time.Duration(d))
		o.now += d
		o.take(o.ev(proxy.EvTimer, 0, nil, 0))
	case opBreaker:
		o.b.open = !o.b.open
		o.note("breaker open %v", o.b.open)
	case opCancelDone:
		if d, ok := pick(o, o.pending); ok {
			o.note("cancel done %d", d)
			o.pending = slices.DeleteFunc(o.pending, func(c proxy.ConnID) bool { return c == d })
			o.take(o.ev(proxy.EvDBCancelDone, d, nil, 0))
		}
	}
}

func (o *opsRig) retryReplyInTime(retried bool) {
	if !retried {
		return
	}
	for _, c := range o.gotReply {
		if deadline := o.arrived[c] + proxy.Instant(joinStatement); o.now >= deadline {
			o.t.Fatalf("client %d arrived at %d got the reply of a retried statement at %d, at or past its deadline %d", c, o.arrived[c], o.now, deadline)
		}
	}
}

func (o *opsRig) forwards() (int64, int64) {
	s := o.e.Snapshot()
	rs, _ := o.e.RetrySnapshot()
	return s.StagePassed[proxy.StageForward], rs.Attempts
}

func (o *opsRig) markRetries(forwards, attempts int64) {
	f, a := o.forwards()
	retries := (f - forwards) - (a - attempts)
	for _, d := range o.sentNow {
		o.retried[d] = retries > 0 && retries == int64(len(o.sentNow))
	}
	o.sentNow = o.sentNow[:0]
}

func boolIndex(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (o *opsRig) drain() {
	o.b.open = false
	o.note("drain")
	for range opsDrainRounds {
		if len(o.busyClients()) == 0 {
			break
		}
		o.service()
	}
	if busy := o.busyClients(); len(busy) != 0 {
		o.t.Fatalf("clients %v never answered", busy)
	}
	for _, c := range slices.Clone(o.clients) {
		o.take(o.ev(proxy.EvClientClosed, c, nil, 0))
	}
	o.clients = nil
	quiet := time.Duration(o.e.Config().AdmissionConfig().Quiet())
	for range opsDrainRounds {
		o.service()
		o.now += proxy.Instant(quiet)
		o.take(o.ev(proxy.EvTimer, 0, nil, 0))
		if o.e.Quiesce() == nil {
			return
		}
	}
	o.t.Fatalf("never quiet: %v", o.e.Quiesce())
}

func (o *opsRig) service() {
	for range sortedKeys(o.reads) {
		o.step(opReply)
	}
	for range sortedKeys(o.needs) {
		o.step(opServer)
	}
	for len(o.connecting) > 0 {
		o.step(opOpen)
	}
	for len(o.pending) > 0 {
		o.step(opCancelDone)
	}
	for len(o.refused) > 0 {
		o.step(opClose)
	}
	o.step(opTime)
}

func (o *opsRig) moved(m *opsMoved) {
	s := o.e.Snapshot()
	m.asked += s.RetryAsked
	m.expired += s.CoalesceExpired
	m.promoted += s.CoalescePromoted
	m.promotedRefused += s.CoalescePromotedRefused
	m.breakerEnds += s.RetryEnds[proxy.RetryEndBreaker]
	m.flushed += s.BreakerFlushed
	m.handoffs += s.CoalesceHandoffs
	m.held += o.e.CoalesceSnapshot().Held
	m.failed += s.Failed
}

func runOpsSequence(t *testing.T, seed uint64, m *opsMoved) {
	t.Helper()
	o := newOpsRig(t, seed)
	defer func() {
		if t.Failed() {
			t.Logf("seed %d trail:\n%s", seed, joinLines(o.trail))
		}
	}()
	for range opsPerSequence {
		o.step(o.chooseOp())
	}
	o.drain()
	o.moved(m)
}

func joinLines(lines []string) string {
	var b bytes.Buffer
	for _, l := range lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String()
}

func TestRepairRandomFlightsRetriesDeadlinesAndBreakerTransitionsKeepTheInvariants(t *testing.T) {
	n := opsSequences
	if testing.Short() {
		n = opsSequencesShort
	}
	var m opsMoved
	for seed := range n {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) { runOpsSequence(t, uint64(seed), &m) })
		if t.Failed() {
			return
		}
	}
	t.Logf("%d sequences: %+v", n, m)
	for name, v := range map[string]int64{"retries granted": m.asked, "flights expired": m.expired, "held flights promoted": m.promoted,
		"promotions refused at an open breaker": m.promotedRefused, "retries refused at the breaker re-check": m.breakerEnds,
		"pool breaker flushes": m.flushed, "handoffs": m.handoffs, "held flights": m.held} {
		if v == 0 {
			t.Errorf("%s never happened over %d sequences: the generator does not reach the combination", name, n)
		}
	}
}
