package clsworld

import (
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
	"github.com/nickstrad/mini-gateway/internal/testclient"
	"github.com/nickstrad/mini-gateway/internal/work"
)

const (
	Addr           = "gw"
	ReadSize       = 4096
	MaxBuffer      = 1 << 20
	MaxMessage     = 1 << 20
	Step           = 5 * time.Millisecond
	Tenant         = "t1"
	MaxClients     = 3
	NameRejectList = "classifier-reject-list"
	NameRejectOnly = "classifier-reject-only"
	FollowUp       = "SELECT 1"
	ShowPath       = "SHOW search_path"
	CleanPath      = `"$user", public`
)

type Scenario struct {
	Name       string
	FollowUps  bool
	Classifier func(*pgwire.ClassifierConfig)
}

func RejectList() Scenario { return Scenario{Name: NameRejectList, FollowUps: true} }

func RejectOnly() Scenario { return Scenario{Name: NameRejectOnly} }

func Scenarios() []Scenario { return []Scenario{RejectList(), RejectOnly()} }

type Gateway struct {
	n          *simnet.Net
	backend    string
	Classifier pgwire.Classifier
	Replies    pgwire.ReplyBuilder
	Wire       pgwire.WireStats
	conns      []*conn
	Forwarded  int64
	Errs       []error
}

type conn struct {
	gw      *Gateway
	client  *simnet.End
	backend *simnet.End
	startup pgwire.Framer
	front   *pgwire.FrontendTracker
	back    pgwire.Framer
	events  []pgwire.FrontEvent
	toCli   []byte
	toDB    []byte
	dbUp    bool
	started bool
	status  byte
	sd      pgwire.SyncDiscard
	closed  bool
	buf     [ReadSize]byte
	tenant  work.TenantID
}

func NewGateway(n *simnet.Net, backend string, cfg pgwire.ClassifierConfig) (*Gateway, error) {
	gw := &Gateway{n: n, backend: backend}
	gw.Classifier.Config = cfg
	_, err := n.Listen(Addr, func(e *simnet.End) {
		c := &conn{gw: gw, client: e, status: 'I', tenant: Tenant}
		c.startup = pgwire.Framer{Untyped: true, Max: pgwire.MaxStartupPacket}
		c.front = pgwire.NewFrontendTracker(MaxMessage, &gw.Wire)
		c.back.Max = MaxMessage
		gw.conns = append(gw.conns, c)
		e.SetHandler(simnet.HandlerFunc(c.onClient))
		c.backend = n.Dial(backend, simnet.DefaultOptions(), simnet.HandlerFunc(c.onBackend))
	})
	return gw, err
}

func (gw *Gateway) fail(err error) { gw.Errs = append(gw.Errs, err) }

func (gw *Gateway) SyncSnapshot() pgwire.SyncDiscardSnapshot {
	var s pgwire.SyncDiscardSnapshot
	for _, c := range gw.conns {
		x := c.sd.Snapshot()
		s.Refused += x.Refused
		s.Discarded += x.Discarded
		s.Synced += x.Synced
	}
	return s
}

func (c *conn) onClient(_ *simnet.End, ev simnet.Event) {
	switch ev {
	case simnet.EvReadable:
		c.readClient()
	case simnet.EvWritable:
		c.flush()
	case simnet.EvReset:
		c.shut()
	}
}

func (c *conn) onBackend(_ *simnet.End, ev simnet.Event) {
	switch ev {
	case simnet.EvConnected:
		c.dbUp = true
		c.flush()
	case simnet.EvReadable:
		c.readBackend()
	case simnet.EvWritable:
		c.flush()
	case simnet.EvRefused, simnet.EvReset:
		c.shut()
	}
}

func (c *conn) shut() {
	if c.closed {
		return
	}
	c.closed = true
	_ = c.client.Close()
	_ = c.backend.Close()
}

func (c *conn) readClient() {
	for !c.closed {
		k, err := c.client.Read(c.buf[:])
		if k > 0 {
			c.fromClient(c.buf[:k])
			c.flush()
			continue
		}
		if errors.Is(err, io.EOF) {
			c.shut()
		}
		return
	}
}

func (c *conn) fromClient(b []byte) {
	if !c.started {
		k, done, err := c.startup.Take(b)
		if err != nil {
			c.gw.fail(fmt.Errorf("startup packet: %w", err))
			c.shut()
			return
		}
		if !done {
			return
		}
		f := c.startup.Frame()
		c.toDB = appendUntyped(c.toDB, f.Body)
		c.started = true
		b = b[k:]
	}
	c.events, _ = c.front.Receive(c.events[:0], b)
	for _, ev := range c.events {
		if c.closed {
			return
		}
		c.message(ev)
	}
}

func appendUntyped(dst, body []byte) []byte {
	n := len(body) + 4
	dst = append(dst, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	return append(dst, body...)
}

func (c *conn) readBackend() {
	for !c.closed {
		k, err := c.backend.Read(c.buf[:])
		if k > 0 {
			c.toCli = append(c.toCli, c.buf[:k]...)
			c.trackStatus(c.buf[:k])
			c.flush()
			continue
		}
		if errors.Is(err, io.EOF) {
			c.shut()
		}
		return
	}
}

func (c *conn) trackStatus(b []byte) {
	for len(b) > 0 {
		k, done, err := c.back.Take(b)
		if err != nil {
			c.gw.fail(fmt.Errorf("database framing: %w", err))
			c.shut()
			return
		}
		b = b[k:]
		if !done {
			return
		}
		if f := c.back.Frame(); f.Type == pgwire.MsgReadyForQuery && len(f.Body) == 1 {
			c.status = f.Body[0]
		}
	}
}

func (c *conn) message(ev pgwire.FrontEvent) {
	if ev.Kind == pgwire.FrontViolation {
		c.gw.fail(fmt.Errorf("client violation %v", ev.Violation))
		c.shut()
		return
	}
	switch c.sd.Step(ev.Type) {
	case pgwire.SyncDiscardDrop:
		return
	case pgwire.SyncDiscardReady:
		c.toCli, _ = pgwire.ReplyReady(c.toCli, c.status)
		return
	case pgwire.SyncDiscardRefuse, pgwire.SyncDiscardRefuseAndReady:
		c.reply(pgwire.ErrRowUnsupportedExtended)
		if !c.sd.Active() {
			c.toCli, _ = pgwire.ReplyReady(c.toCli, c.status)
		}
		return
	}
	if ev.Kind == pgwire.FrontTerminate {
		c.toDB = pgwire.AppendTerminate(c.toDB)
		return
	}
	text := ev.Body[:len(ev.Body)-1]
	u := c.gw.Classifier.Classify(c.tenant, text, c.status != 'I')
	if u.Class == work.ClassReject {
		c.reply(pgwire.ErrRowUnsupportedFeature)
		return
	}
	c.gw.Forwarded++
	c.toDB = pgwire.AppendQuery(c.toDB, text)
}

func (c *conn) reply(id pgwire.ErrRowID) {
	out, act, err := c.gw.Replies.Build(c.toCli, id, c.status)
	if err != nil {
		c.gw.fail(err)
		return
	}
	c.toCli = out
	if act.Close {
		c.gw.fail(fmt.Errorf("row %s closed the connection", pgwire.ErrorRows[id].ID))
	}
}

func (c *conn) flush() {
	if c.closed {
		return
	}
	if len(c.toCli) > MaxBuffer || len(c.toDB) > MaxBuffer {
		c.gw.fail(errors.New("a gateway buffer is over its cap"))
		c.shut()
		return
	}
	if len(c.toCli) > 0 {
		k, err := c.client.Write(c.toCli)
		if err == nil {
			c.toCli = c.toCli[k:]
		}
	}
	if c.dbUp && len(c.toDB) > 0 {
		k, err := c.backend.Write(c.toDB)
		if err == nil {
			c.toDB = c.toDB[k:]
		}
	}
}

type World struct {
	Sim      *sim.Sim
	Net      *simnet.Net
	PG       *fakepg.Server
	Gateway  *Gateway
	Probes   []*workload.Probe
	Scenario Scenario
	Plan     Plan
}

type Plan struct {
	Clients     int
	Statements  []string
	SentRejects int64
	SentAllowed int64
	Parses      int64
}

func RejectStatements(rng *rand.Rand) []string {
	var out []string
	for _, r := range pgwire.RejectRules {
		if r.Reason == work.RejectExtendedProtocol {
			continue
		}
		out = append(out, r.Example)
		out = append(out, Pad(r.Example, rng))
	}
	for _, c := range []string{"SET search_path = app", "SELECT set_config('search_path', 'app', false)", "SET application_name = 'planted'"} {
		out = append(out, Pad(c, rng))
	}
	rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

func padding(rng *rand.Rand) string {
	return []string{" ", "\n\t ", " /* pad */ ", " -- pad ; SET x = 1\n", " /* nested /* pad */ ; */ ", "\r\n\f\v", " /**/"}[rng.IntN(7)]
}

func mutateCase(rng *rand.Rand, b []byte) {
	for i, c := range b {
		switch {
		case c >= 'a' && c <= 'z' && rng.IntN(2) == 0:
			b[i] = c - 'a' + 'A'
		case c >= 'A' && c <= 'Z' && rng.IntN(2) == 0:
			b[i] = c - 'A' + 'a'
		}
	}
}

func Pad(src string, rng *rand.Rand) string {
	toks, _ := pgwire.LexAll([]byte(src))
	var out []byte
	for i, t := range toks {
		if rng.IntN(2) == 0 {
			out = append(out, padding(rng)...)
		}
		text := append([]byte(nil), t.Text([]byte(src))...)
		if t.Kind == pgwire.TokenWord {
			mutateCase(rng, text)
		}
		out = append(out, text...)
		if t.Kind == pgwire.TokenBad && t.End == len(src) && i == len(toks)-1 {
			return string(out)
		}
	}
	if rng.IntN(2) == 0 {
		out = append(out, padding(rng)...)
	}
	return string(out)
}

func Build(seed int64, sc Scenario, replay string) (*World, error) {
	s := sim.New(sim.Config{Seed: seed, Scenario: sc.Name})
	s.SetReplay(replay)
	n := simnet.New(s)
	srv, err := fakepg.New(n, fakepg.Config{})
	if err != nil {
		return nil, err
	}
	var cfg pgwire.ClassifierConfig
	if sc.Classifier != nil {
		sc.Classifier(&cfg)
	}
	gw, err := NewGateway(n, srv.Addr(), cfg)
	if err != nil {
		return nil, err
	}
	w := &World{Sim: s, Net: n, PG: srv, Gateway: gw, Scenario: sc}
	if err := s.RegisterState("pgwire.classifier.harness", func(st sim.StateSink) {
		cs, rs := gw.Classifier.Snapshot(), gw.Replies.Snapshot()
		st.Int(cs.Classified, cs.ByClass[work.ClassReject], cs.Opaque, cs.SafeRead, cs.Shareable, rs.Total, gw.Forwarded)
		st.Int(cs.ByReason[:]...)
		st.Int(rs.ByRow[:]...)
	}); err != nil {
		return nil, err
	}
	rng := sim.NewStream(seed, "pgwire/e2e")
	w.Plan.Clients = 1 + rng.IntN(MaxClients)
	w.Plan.Statements = RejectStatements(rng)
	w.Probes = make([]*workload.Probe, w.Plan.Clients)
	for i := range w.Probes {
		w.Probes[i] = workload.NewProbe(n, Addr, simnet.DefaultOptions())
		w.Probes[i].Startup("app", "app")
	}
	w.schedule()
	return w, nil
}

func (w *World) schedule() {
	at := Step
	step := func(f func()) {
		w.Sim.At(sim.Time(at), f)
		at += Step
	}
	step(w.showPaths)
	for _, q := range w.Plan.Statements {
		step(func() {
			for _, p := range w.Probes {
				p.Query(q)
				w.Plan.SentRejects++
			}
		})
		if w.Scenario.FollowUps {
			step(func() {
				for _, p := range w.Probes {
					p.Query(FollowUp)
					w.Plan.SentAllowed++
				}
			})
		}
	}
	step(func() {
		for _, p := range w.Probes {
			p.Send([]byte{'P', 0, 0, 0, 8, 0, 0, 0, 0, 'S', 0, 0, 0, 4})
			w.Plan.Parses++
		}
	})
	step(w.showPaths)
	step(func() {
		for _, p := range w.Probes {
			p.Terminate()
		}
	})
	step(func() {
		for _, p := range w.Probes {
			p.Close()
		}
	})
}

func (w *World) showPaths() {
	if !w.Scenario.FollowUps {
		return
	}
	for _, p := range w.Probes {
		p.Query(ShowPath)
		w.Plan.SentAllowed++
	}
}

type Result struct {
	World      *World
	Plan       Plan
	PG         fakepg.Snapshot
	Classifier pgwire.ClassifierSnapshot
	Replies    pgwire.ReplySnapshot
	Sync       pgwire.SyncDiscardSnapshot
	Wire       pgwire.WireSnapshot
	Traces     []string
	Paths      []string
	Errs       []error
	Quiesce    error
	Err        error
}

func (w *World) Run() Result {
	r := Result{World: w}
	r.Err = w.Sim.RunUntilQuiet()
	r.Quiesce = w.Sim.CheckQuiesce()
	gw := w.Gateway
	r.Plan, r.PG, r.Classifier, r.Replies, r.Sync, r.Wire, r.Errs = w.Plan, w.PG.Snapshot(), gw.Classifier.Snapshot(), gw.Replies.Snapshot(), gw.SyncSnapshot(), gw.Wire.Snapshot(), gw.Errs
	for _, p := range w.Probes {
		r.Traces = append(r.Traces, p.Trace())
		var paths []string
		last := ""
		for _, m := range p.Messages() {
			switch {
			case m.Type == testclient.MsgDataRow && len(m.Values) == 1:
				last = string(m.Values[0])
			case m.Type == testclient.MsgCommand && m.Tag == "SHOW":
				paths = append(paths, last)
			}
		}
		r.Paths = append(r.Paths, strings.Join(paths, "|"))
	}
	return r
}

func Run(seed int64, sc Scenario, replay string) Result {
	w, err := Build(seed, sc, replay)
	if err != nil {
		return Result{Err: err}
	}
	return w.Run()
}

func (r Result) Verify() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if r.Err != nil {
		add("run: %v", r.Err)
	}
	if r.Quiesce != nil {
		add("quiesce: %v", r.Quiesce)
	}
	for _, e := range r.Errs {
		add("gateway: %v", e)
	}
	if r.World == nil {
		return errors.Join(errs...)
	}
	p, sc := r.Plan, r.World.Scenario
	if r.Classifier.ByClass[work.ClassReject] != p.SentRejects {
		add("classifier rejects %d, reject statements sent %d", r.Classifier.ByClass[work.ClassReject], p.SentRejects)
	}
	if r.Wire.Client.QueriesSeen != p.SentRejects+p.SentAllowed {
		add("the gateway's framer saw %d queries, %d were sent", r.Wire.Client.QueriesSeen, p.SentRejects+p.SentAllowed)
	}
	if r.Replies.ByRow[pgwire.ErrRowUnsupportedFeature] != p.SentRejects || r.Replies.ByRow[pgwire.ErrRowUnsupportedExtended] != p.Parses {
		add("reply counters: unsupported-feature %d (want %d), unsupported-extended %d (want %d)", r.Replies.ByRow[pgwire.ErrRowUnsupportedFeature], p.SentRejects, r.Replies.ByRow[pgwire.ErrRowUnsupportedExtended], p.Parses)
	}
	if r.PG.StatementsStarted != p.SentAllowed {
		add("the fake Postgres started %d statements, %d allowed statements were sent: a rejected statement reached the database", r.PG.StatementsStarted, p.SentAllowed)
	}
	for _, rec := range r.World.PG.StatementLog() {
		if rec.Text != FollowUp && rec.Text != ShowPath {
			add("the fake Postgres ran %q", rec.Text)
		}
	}
	if r.PG.WritesApplied != 0 || r.PG.ParamReports != 0 {
		add("database state changed: writes %d, parameter reports %d", r.PG.WritesApplied, r.PG.ParamReports)
	}
	if r.World.Gateway.Forwarded != p.SentAllowed {
		add("forwarded %d, allowed sent %d", r.World.Gateway.Forwarded, p.SentAllowed)
	}
	if r.Sync.Refused != p.Parses || r.Sync.Synced != p.Parses {
		add("sync discard: %+v for %d parse messages", r.Sync, p.Parses)
	}
	if err := r.Classifier.Check(); err != nil || r.Classifier.Classified != p.SentRejects+p.SentAllowed {
		add("classifier conservation: %v, classified %d, sent %d", err, r.Classifier.Classified, p.SentRejects+p.SentAllowed)
	}
	if p.SentRejects == 0 || r.Classifier.ByClass[work.ClassReject] == 0 || r.Replies.Total == 0 || r.PG.StartupsCompleted == 0 || (sc.FollowUps && r.PG.StatementsStarted == 0) {
		add("counters did not move: %+v", r.Classifier)
	}
	want := WantTrace(sc, p)
	for i, tr := range r.Traces {
		k := strings.Index(tr, " K ZI ")
		if k < 0 || tr[k+6:] != want {
			add("client %d saw\n %s\nwant after startup\n %s", i+1, tr, want)
		}
		if sc.FollowUps && r.Paths[i] != CleanPath+"|"+CleanPath {
			add("client %d: session state changed: search_path readings %s", i+1, r.Paths[i])
		}
	}
	return errors.Join(errs...)
}

func WantTrace(sc Scenario, p Plan) string {
	var parts []string
	if sc.FollowUps {
		parts = append(parts, "T D C:SHOW ZI")
	}
	for range p.Statements {
		parts = append(parts, "E:0A000 ZI")
		if sc.FollowUps {
			parts = append(parts, "T D C:SELECT 1 ZI")
		}
	}
	parts = append(parts, "E:0A000 ZI")
	if sc.FollowUps {
		parts = append(parts, "T D C:SHOW ZI")
	}
	parts = append(parts, "eof")
	return strings.Join(parts, " ")
}
