package fakepg

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
)

var (
	kindAccept = sim.K("pg.acc")
	kindReady  = sim.K("pg.start")
	kindRefuse = sim.K("pg.ref")
	kindStmt   = sim.K("pg.stmt")
	kindDone   = sim.K("pg.done")
	kindWrite  = sim.K("pg.write")
	kindClose  = sim.K("pg.close")
	kindCancel = sim.K("pg.cancel")
	kindFire   = sim.K("pg.fire")
)

type Refusal uint8

const (
	RefuseTooMany Refusal = iota
	RefuseUnknownUser
	RefuseUnknownDatabase
	RefuseNoUser
	RefuseBadStartup
	RefuseStartingUp
	RefuseProtocol
	NumRefusals
)

type row struct {
	key   string
	value string
}

type stats struct {
	accepted        int64
	closed          int64
	open            obs.Gauge
	sessions        obs.Gauge
	startups        int64
	refused         [NumRefusals]int64
	started         int64
	finished        int64
	aborted         int64
	errors          int64
	byKind          [NumKinds]int64
	inService       obs.Gauge
	waiting         obs.Gauge
	writes          int64
	txBegun         int64
	txCommitted     int64
	txRolledBack    int64
	txLost          int64
	txOpen          int64
	cancelsReceived int64
	cancelsApplied  int64
	cancelsIdle     int64
	cancelsNoMatch  int64
	bytesIn         int64
	bytesOut        int64
	paramReports    int64
}

type Server struct {
	s          *sim.Sim
	net        *simnet.Net
	cfg        Config
	l          *simnet.Listener
	backends   []*backend
	nextPID    uint32
	keys       *rand.Rand
	table      []row
	waitQ      []*backend
	slots      int
	stmtSeq    int64
	writeSeq   int64
	txSeq      int64
	applyCalls int64
	stats      stats
	writeLog   ring[WriteRecord]
	stmtLog    ring[StatementRecord]
	txLog      ring[TxRecord]
	faults     faultState
	readBuf    [readChunk]byte
}

func New(net *simnet.Net, cfg Config) (*Server, error) {
	cfg = cfg.normalized()
	srv := &Server{s: net.Sim(), net: net, cfg: cfg, nextPID: firstPID}
	srv.keys = srv.s.Stream("fakepg/keys")
	srv.writeLog.cfg = cfg.WriteLog
	srv.stmtLog.cfg = cfg.StatementLog
	srv.txLog.cfg = cfg.StatementLog
	for _, r := range cfg.Rows {
		if err := srv.put(r.Key, r.Value); err != nil {
			return nil, err
		}
	}
	l, err := net.Listen(cfg.Addr, srv.accept)
	if err != nil {
		return nil, err
	}
	srv.l = l
	srv.faults.stalls = slices.Clone(cfg.Service.Stalls)
	if err := srv.registerFaults(); err != nil {
		return nil, err
	}
	_ = srv.s.RegisterQuiesce("fakepg", srv.quiesce)
	_ = srv.s.RegisterInvariant("fakepg.conservation", srv.conservation)
	_ = srv.s.RegisterState("fakepg", srv.writeState)
	return srv, nil
}

func (srv *Server) Addr() string               { return srv.cfg.Addr }
func (srv *Server) Listener() *simnet.Listener { return srv.l }
func (srv *Server) Config() Config             { return srv.cfg }

func (srv *Server) limit() int {
	if srv.faults.maxConnOn {
		return int(srv.faults.maxConn)
	}
	return srv.cfg.MaxConnections
}

func (srv *Server) accept(e *simnet.End) {
	srv.stats.accepted++
	srv.stats.open.Add(1)
	srv.nextPID++
	b := &backend{srv: srv, end: e, id: e.ID(), pid: srv.nextPID, phase: phaseStartup}
	b.fr.Startup = true
	b.fr.Max = srv.cfg.MaxMessage
	b.stream = sim.NewStream(srv.s.Seed(), "fakepg/service/"+strconv.FormatInt(b.id, 10))
	b.secret = make([]byte, 4)
	v := srv.keys.Uint32()
	b.secret[0], b.secret[1], b.secret[2], b.secret[3] = byte(v>>24), byte(v>>16), byte(v>>8), byte(v)
	switch {
	case srv.faults.restarting:
		b.refuseAs = RefuseStartingUp
	case int(srv.stats.sessions.Value) >= srv.limit():
		b.refuseAs = RefuseTooMany
	default:
		b.counted = true
		srv.stats.sessions.Add(1)
	}
	b.refuse = !b.counted
	b.muteStartup = srv.faults.silentStartup
	srv.backends = append(srv.backends, b)
	srv.s.Trace(kindAccept, b.id, int64(b.pid), boolInt(b.counted), srv.stats.sessions.Value)
	e.SetHandler(b)
	srv.applyNewConnFaults(b)
	b.advance()
}

func (srv *Server) remove(b *backend) {
	i := slices.Index(srv.backends, b)
	if i >= 0 {
		srv.backends = slices.Delete(srv.backends, i, i+1)
	}
}

func (srv *Server) backend(id int64) *backend {
	for _, b := range srv.backends {
		if b.id == id {
			return b
		}
	}
	return nil
}

func (srv *Server) byKey(pid uint32) *backend {
	for _, b := range srv.backends {
		if b.pid == pid && b.phase == phaseReady {
			return b
		}
	}
	return nil
}

func (srv *Server) find(key string) (int, bool) {
	return slices.BinarySearchFunc(srv.table, key, func(r row, k string) int { return strings.Compare(r.key, k) })
}

func (srv *Server) get(key string) (string, bool) {
	i, ok := srv.find(key)
	if !ok {
		return "", false
	}
	return srv.table[i].value, true
}

var ErrTableFull = errors.New("fakepg: table full")

func (srv *Server) put(key, value string) error {
	i, ok := srv.find(key)
	if ok {
		srv.table[i].value = value
		return nil
	}
	if len(srv.table) >= srv.cfg.MaxRows {
		return ErrTableFull
	}
	srv.table = slices.Insert(srv.table, i, row{key: key, value: value})
	return nil
}

func (srv *Server) del(key string) bool {
	i, ok := srv.find(key)
	if ok {
		srv.table = slices.Delete(srv.table, i, i+1)
	}
	return ok
}

func (srv *Server) Get(key string) (string, bool) { return srv.get(key) }

func (srv *Server) Rows() []Row {
	out := make([]Row, 0, len(srv.table))
	for _, r := range srv.table {
		out = append(out, Row{Key: r.key, Value: r.value})
	}
	return out
}

func (srv *Server) WriteLog() []WriteRecord         { return srv.writeLog.all() }
func (srv *Server) StatementLog() []StatementRecord { return srv.stmtLog.all() }
func (srv *Server) TransactionLog() []TxRecord      { return srv.txLog.all() }

func (srv *Server) logTx(b *backend, tx *txn, o TxOutcome) {
	srv.txLog.add(TxRecord{TxID: tx.id, Backend: b.id, Outcome: o, Writes: int64(len(tx.writes)), At: srv.s.Now()})
}

func (srv *Server) quiesce() error {
	st := &srv.stats
	var errs []error
	if st.open.Value != 0 || len(srv.backends) != 0 {
		errs = append(errs, fmt.Errorf("%d connections open (%d backends)", st.open.Value, len(srv.backends)))
	}
	if st.inService.Value != 0 || st.waiting.Value != 0 {
		errs = append(errs, fmt.Errorf("statements in service %d, waiting %d", st.inService.Value, st.waiting.Value))
	}
	if st.txOpen != 0 {
		errs = append(errs, fmt.Errorf("%d transactions open", st.txOpen))
	}
	return errors.Join(errs...)
}

func (srv *Server) conservation() error {
	st := &srv.stats
	return errors.Join(
		obs.Conserve(obs.T("accepted", st.accepted), obs.T("open", st.open.Value), obs.T("closed", st.closed)),
		obs.Conserve(obs.T("statements started", st.started), obs.T("finished", st.finished),
			obs.T("in service", st.inService.Value), obs.T("waiting", st.waiting.Value), obs.T("aborted", st.aborted)),
		obs.Conserve(obs.T("transactions begun", st.txBegun), obs.T("committed", st.txCommitted),
			obs.T("rolled back", st.txRolledBack), obs.T("lost", st.txLost), obs.T("open", st.txOpen)),
		obs.Conserve(obs.T("cancels received", st.cancelsReceived), obs.T("applied", st.cancelsApplied),
			obs.T("idle", st.cancelsIdle), obs.T("no match", st.cancelsNoMatch)),
		srv.sessionBound(),
	)
}

func (srv *Server) sessionBound() error {
	if srv.stats.sessions.Value > int64(srv.cfg.MaxConnections) {
		return fmt.Errorf("sessions %d above max_connections %d", srv.stats.sessions.Value, srv.cfg.MaxConnections)
	}
	return nil
}

func (srv *Server) writeState(w sim.StateSink) {
	st := &srv.stats
	w.Int(st.accepted, st.closed, st.open.Value, st.open.Peak, st.sessions.Value, st.sessions.Peak, st.startups)
	w.Int(st.refused[:]...)
	w.Int(st.started, st.finished, st.aborted, st.errors, st.inService.Value, st.inService.Peak, st.waiting.Value)
	w.Int(st.byKind[:]...)
	w.Int(st.writes, st.txBegun, st.txCommitted, st.txRolledBack, st.txLost, st.txOpen)
	w.Int(st.cancelsReceived, st.cancelsApplied, st.cancelsIdle, st.cancelsNoMatch, st.bytesIn, st.bytesOut)
	w.Int(int64(len(srv.table)))
	for _, r := range srv.table {
		w.String(r.key)
		w.String(r.value)
	}
	for _, b := range srv.backends {
		w.Int(b.id, int64(b.pid), int64(b.phase), int64(b.status()), int64(len(b.out)), int64(len(b.outBuf)))
	}
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
