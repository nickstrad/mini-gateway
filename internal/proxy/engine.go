package proxy

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/work"
)

type Engine struct {
	cfg        Config
	seams      Seams
	classifier *pgwire.Classifier
	replies    pgwire.ReplyBuilder
	wire       pgwire.WireStats
	clients    map[ConnID]*client
	clientList []*client
	dbs        map[ConnID]*dbConn
	slots      []*dbConn
	waiting    map[RequestID]*client
	nextDB     ConnID
	nextReq    RequestID
	dialing    int
	dialAfter  Instant
	redialT    *timer
	lastFailed bool
	draining   bool
	stopping   bool
	now        Instant
	t          timers
	out        []Action
	snap       Snapshot
	stmtHist   *obs.Histogram
	queueHist  *obs.Histogram
	resetHist  *obs.Histogram
	syncSnap   pgwire.SyncDiscardSnapshot
}

var ErrEngine = errors.New("proxy: engine")

func New(cfg Config) (*Engine, error) {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	e := &Engine{cfg: cfg, clients: make(map[ConnID]*client), dbs: make(map[ConnID]*dbConn),
		waiting: make(map[RequestID]*client), slots: make([]*dbConn, cfg.Connections)}
	e.cfg.Client.Stats = &e.wire
	e.cfg.Backend.Stats = &e.wire
	if e.cfg.Client.Keys == nil && e.cfg.Client.Random != nil {
		e.cfg.Client.Keys = pgwire.NewKeyIssuer(e.cfg.Client.Random)
	}
	if err := errors.Join(e.cfg.Client.Validate(), e.cfg.Backend.Validate()); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrConfig, err)
	}
	e.seams = cfg.Seams
	if e.seams.Classifier == nil {
		e.classifier = &pgwire.Classifier{Config: pgwire.ClassifierConfig{KeyScope: cfg.KeyScope}}
		e.seams.Classifier = e.classifier
	}
	if e.seams.Breaker == nil {
		e.seams.Breaker = ClosedBreaker{}
	}
	if e.seams.Rate == nil {
		e.seams.Rate = OpenRate{}
	}
	if e.seams.Coalescer == nil {
		e.seams.Coalescer = NoCoalesce{}
	}
	if e.seams.Queue == nil {
		e.seams.Queue = NewFIFO(cfg.QueueCap)
	}
	if e.seams.Pool == nil {
		e.seams.Pool = NewFixedSet(cfg.Connections)
	}
	if e.seams.Release == nil {
		e.seams.Release = ResetAlwaysPolicy{}
	}
	if e.seams.Retry == nil {
		e.seams.Retry = RetryOff{}
	}
	e.stmtHist = obs.NewDurationHistogram()
	e.queueHist = obs.NewDurationHistogram()
	e.resetHist = obs.NewDurationHistogram()
	e.snap.QueueCap = int64(e.seams.Queue.Cap())
	e.snap.RelayCap = int64(cfg.ReplyBufferCap)
	e.snap.DBCap = int64(cfg.Connections)
	return e, nil
}

func (e *Engine) Config() Config { return e.cfg }

func (e *Engine) Start(now Instant, out []Action) []Action {
	e.begin(now, out)
	e.dialMore()
	return e.finish()
}

func (e *Engine) Shutdown(now Instant, out []Action) []Action {
	e.begin(now, out)
	e.stopping = true
	for len(e.clientList) > 0 {
		e.closeClient(e.clientList[len(e.clientList)-1], CloseClientShutdown)
	}
	for _, d := range e.slots {
		if d != nil {
			e.closeDB(d, CloseDBShutdown)
		}
	}
	e.t.cancel(&e.redialT)
	return e.finish()
}

func (e *Engine) Step(now Instant, ev Event, out []Action) []Action {
	e.begin(now, out)
	e.snap.Events++
	switch ev.Kind {
	case EvClientAccepted:
		e.accept(ev.Conn)
	case EvClientData:
		if c := e.clients[ev.Conn]; c != nil {
			e.clientData(c, ev.Data)
		}
	case EvClientClosed:
		if c := e.clients[ev.Conn]; c != nil {
			e.clientGone(c)
		}
	case EvClientWrote:
		if c := e.clients[ev.Conn]; c != nil {
			e.clientWrote(c, ev.N)
		}
	case EvClientTLS:
		if c := e.clients[ev.Conn]; c != nil {
			e.clientTLS(c, ev.TLS)
		}
	case EvDBOpened:
		if d := e.dbs[ev.Conn]; d != nil {
			e.dbOpened(d)
		}
	case EvDBDialFailed:
		if d := e.dbs[ev.Conn]; d != nil {
			e.dbConnectFailed(d, CloseDBConnectFailed, pgwire.ClassLost)
		}
	case EvDBData:
		if d := e.dbs[ev.Conn]; d != nil {
			e.dbData(d, ev.Data)
		} else {
			e.snap.IgnoredDBData++
		}
	case EvDBClosed:
		if d := e.dbs[ev.Conn]; d != nil {
			e.dbClosed(d)
		}
	case EvDBWrote:
		if d := e.dbs[ev.Conn]; d != nil {
			d.acked += int64(ev.N)
		}
	case EvDBTLS:
		if d := e.dbs[ev.Conn]; d != nil {
			e.dbTLS(d, ev.TLS)
		}
	case EvTimer:
		e.fireTimers()
	case EvPublish:
		e.snap.Publishes++
		e.emit(Action{Kind: ActPublish})
	case EvDraining:
		e.draining = ev.On
	}
	return e.finish()
}

func (e *Engine) begin(now Instant, out []Action) {
	if now > e.now {
		e.now = now
	}
	e.out = out
}

func (e *Engine) finish() []Action {
	e.fireTimers()
	if next := e.t.next(); next != e.t.woke {
		e.t.woke = next
		e.emit(Action{Kind: ActWake, At: next})
	}
	out := e.out
	e.out = nil
	return out
}

func (e *Engine) emit(a Action) { e.out = append(e.out, a) }

func (e *Engine) fireTimers() {
	for {
		t := e.t.due(e.now)
		if t == nil {
			return
		}
		e.fire(t)
	}
}

func (e *Engine) fire(t *timer) {
	switch t.kind {
	case tClientStartup, tClientIdle, tClientMid, tClientWrite, tQueueWait:
		e.clientTimer(t)
	case tDBConnect, tDBStatement, tDBReset:
		e.dbTimer(t)
	case tRedial:
		e.redialT = nil
		e.dialMore()
	}
}

func (e *Engine) after(d time.Duration) Instant {
	if d <= 0 {
		return e.now
	}
	if int64(e.now) > math.MaxInt64-int64(d) {
		return Instant(math.MaxInt64)
	}
	return e.now + Instant(d)
}

func (e *Engine) Snapshot() Snapshot {
	s := e.snap
	s.StatementTime = e.stmtHist.Snapshot()
	s.QueueWait = e.queueHist.Snapshot()
	s.ResetTime = e.resetHist.Snapshot()
	s.TimersPending.Set(int64(e.t.pending()))
	s.Draining = e.draining
	s.DBDown = e.dbDown()
	return s
}

func (e *Engine) dbDown() bool {
	for _, d := range e.slots {
		if d != nil && d.phase >= dpIdle && d.phase <= dpResetting {
			return false
		}
	}
	return e.lastFailed
}

func (e *Engine) WireSnapshot() pgwire.WireSnapshot { return e.wire.Snapshot() }

func (e *Engine) ClassifierSnapshot() pgwire.ClassifierSnapshot {
	if e.classifier == nil {
		return pgwire.ClassifierSnapshot{}
	}
	return e.classifier.Snapshot()
}

func (e *Engine) ReplySnapshot() pgwire.ReplySnapshot { return e.replies.Snapshot() }

func (e *Engine) SyncSnapshot() pgwire.SyncDiscardSnapshot { return e.syncSnap }

func (e *Engine) Quiesce() error {
	var errs []error
	if n := len(e.clients); n != 0 {
		errs = append(errs, fmt.Errorf("%d clients connected", n))
	}
	if n := e.seams.Queue.Len(); n != 0 {
		errs = append(errs, fmt.Errorf("%d requests queued", n))
	}
	if n := len(e.waiting); n != 0 {
		errs = append(errs, fmt.Errorf("%d requests waiting", n))
	}
	for _, d := range e.slots {
		if d != nil && d.phase != dpIdle && !(d.phase == dpDialing || d.phase == dpStartup) {
			errs = append(errs, fmt.Errorf("database connection %d is %s", d.id, d.phase))
		}
	}
	if e.snap.InFlight.Value != 0 || e.snap.Queued.Value != 0 {
		errs = append(errs, fmt.Errorf("%d in flight, %d queued", e.snap.InFlight.Value, e.snap.Queued.Value))
	}
	if e.snap.DBLent.Value != 0 || e.snap.Resetting.Value != 0 {
		errs = append(errs, fmt.Errorf("%d lent, %d resetting", e.snap.DBLent.Value, e.snap.Resetting.Value))
	}
	return errors.Join(errs...)
}

func (e *Engine) Idle() bool { return e.Quiesce() == nil }

func (e *Engine) Invariants() error {
	if err := e.Snapshot().Check(); err != nil {
		return err
	}
	lent := 0
	for _, d := range e.slots {
		if d == nil || d.phase != dpLent {
			continue
		}
		lent++
		if d.client == nil || d.client.db != d {
			return fmt.Errorf("database connection %d lent without a matching client", d.id)
		}
	}
	for _, c := range e.clientList {
		if c.db != nil && c.db.client != c {
			return fmt.Errorf("client %d holds database connection %d lent to another client", c.id, c.db.id)
		}
	}
	if int64(lent) != e.snap.DBLent.Value {
		return fmt.Errorf("%d connections lent, gauge says %d", lent, e.snap.DBLent.Value)
	}
	if e.wire.Snapshot().Check() != nil {
		return e.wire.Snapshot().Check()
	}
	return nil
}

func (e *Engine) Tenant(id ConnID) (work.TenantID, bool) {
	c := e.clients[id]
	if c == nil {
		return "", false
	}
	return c.tenant, true
}

func (e *Engine) LentTo(db ConnID) (ConnID, bool) {
	d := e.dbs[db]
	if d == nil || d.client == nil {
		return 0, false
	}
	return d.client.id, true
}
