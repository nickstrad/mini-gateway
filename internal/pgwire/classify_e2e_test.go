package pgwire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
	"github.com/nickstrad/mini-gateway/internal/sim/simtest"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
	"github.com/nickstrad/mini-gateway/internal/testclient"
	"github.com/nickstrad/mini-gateway/internal/work"
)

const (
	clsGwAddr     = "gw"
	clsGwReadSize = 4096
	clsGwMaxBuf   = 1 << 20
	clsStep       = 5 * time.Millisecond
)

type clsGateway struct {
	n       *simnet.Net
	backend string
	cls     Classifier
	rpl     ReplyBuilder
	sync    SyncDiscardSnapshot
	conns   []*clsGwConn
	fwd     int64
	errs    []error
}

type clsGwConn struct {
	gw      *clsGateway
	client  *simnet.End
	backend *simnet.End
	fromCli []byte
	fromDB  []byte
	toCli   []byte
	toDB    []byte
	dbUp    bool
	started bool
	status  byte
	sd      SyncDiscard
	closed  bool
	buf     [clsGwReadSize]byte
	tenant  work.TenantID
}

func clsNewGateway(n *simnet.Net, backend string, plant bool) (*clsGateway, error) {
	gw := &clsGateway{n: n, backend: backend}
	gw.cls.Config.plantSetForwardable = plant
	_, err := n.Listen(clsGwAddr, func(e *simnet.End) {
		c := &clsGwConn{gw: gw, client: e, status: 'I', tenant: "t1"}
		gw.conns = append(gw.conns, c)
		e.SetHandler(simnet.HandlerFunc(c.onClient))
		c.backend = n.Dial(backend, simnet.DefaultOptions(), simnet.HandlerFunc(c.onBackend))
	})
	return gw, err
}

func (gw *clsGateway) fail(err error) { gw.errs = append(gw.errs, err) }

func (c *clsGwConn) onClient(_ *simnet.End, ev simnet.Event) {
	switch ev {
	case simnet.EvReadable:
		c.readClient()
	case simnet.EvWritable:
		c.flush()
	case simnet.EvReset:
		c.shut()
	}
}

func (c *clsGwConn) onBackend(_ *simnet.End, ev simnet.Event) {
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

func (c *clsGwConn) shut() {
	if c.closed {
		return
	}
	c.closed = true
	_ = c.client.Close()
	_ = c.backend.Close()
}

func (c *clsGwConn) readClient() {
	for !c.closed {
		k, err := c.client.Read(c.buf[:])
		if k > 0 {
			if len(c.fromCli)+k > clsGwMaxBuf {
				c.gw.fail(errors.New("client buffer over its cap"))
				c.shut()
				return
			}
			c.fromCli = append(c.fromCli, c.buf[:k]...)
			c.handleClient()
			continue
		}
		if errors.Is(err, io.EOF) {
			c.shut()
		}
		return
	}
}

func (c *clsGwConn) readBackend() {
	for !c.closed {
		k, err := c.backend.Read(c.buf[:])
		if k > 0 {
			c.toCli = append(c.toCli, c.buf[:k]...)
			c.fromDB = append(c.fromDB, c.buf[:k]...)
			c.trackStatus()
			c.flush()
			continue
		}
		if errors.Is(err, io.EOF) {
			c.shut()
		}
		return
	}
}

func (c *clsGwConn) trackStatus() {
	for len(c.fromDB) >= 5 {
		n := int(binary.BigEndian.Uint32(c.fromDB[1:5]))
		if len(c.fromDB) < 1+n {
			return
		}
		if c.fromDB[0] == 'Z' && n == 5 {
			c.status = c.fromDB[5]
		}
		c.fromDB = c.fromDB[1+n:]
	}
}

func (c *clsGwConn) handleClient() {
	if !c.started {
		if len(c.fromCli) < 4 {
			return
		}
		n := int(binary.BigEndian.Uint32(c.fromCli[:4]))
		if len(c.fromCli) < n {
			return
		}
		c.toDB = append(c.toDB, c.fromCli[:n]...)
		c.fromCli = c.fromCli[n:]
		c.started = true
	}
	for len(c.fromCli) >= 5 && !c.closed {
		n := int(binary.BigEndian.Uint32(c.fromCli[1:5]))
		if len(c.fromCli) < 1+n {
			break
		}
		msg := c.fromCli[:1+n]
		c.fromCli = c.fromCli[1+n:]
		c.message(msg)
	}
	c.flush()
}

func (c *clsGwConn) message(msg []byte) {
	typ := msg[0]
	switch c.sd.Step(typ) {
	case SyncDiscardDrop:
		return
	case SyncDiscardReady:
		c.toCli, _ = ReplyReady(c.toCli, c.status)
		return
	case SyncDiscardRefuse, SyncDiscardRefuseAndReady:
		c.reply(ErrRowUnsupportedExtended)
		if !c.sd.Active() {
			c.toCli, _ = ReplyReady(c.toCli, c.status)
		}
		return
	}
	if typ != 'Q' {
		c.toDB = append(c.toDB, msg...)
		return
	}
	body := msg[5:]
	if len(body) == 0 || body[len(body)-1] != 0 {
		c.gw.fail(errors.New("query without its terminator"))
		return
	}
	u := c.gw.cls.Classify(c.tenant, body[:len(body)-1], c.status != 'I')
	if u.Class == work.ClassReject {
		c.reply(ErrRowUnsupportedFeature)
		return
	}
	c.gw.fwd++
	c.toDB = append(c.toDB, msg...)
}

func (c *clsGwConn) reply(id ErrRowID) {
	out, act, err := c.gw.rpl.Build(c.toCli, id, c.status)
	if err != nil {
		c.gw.fail(err)
		return
	}
	c.toCli = out
	if act.Close {
		c.gw.fail(fmt.Errorf("row %s closed the connection", ErrorRows[id].ID))
	}
}

func (c *clsGwConn) flush() {
	if c.closed {
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

func (gw *clsGateway) syncSnapshot() SyncDiscardSnapshot {
	var s SyncDiscardSnapshot
	for _, c := range gw.conns {
		x := c.sd.Snapshot()
		s.Refused += x.Refused
		s.Discarded += x.Discarded
		s.Synced += x.Synced
	}
	return s
}

type clsScenario struct {
	plant       bool
	followUps   bool
	clients     int
	statements  []string
	sentRejects int64
	sentAllowed int64
	parses      int64
}

type clsOutcome struct {
	sc       clsScenario
	gw       *clsGateway
	srv      *fakepg.Server
	pg       fakepg.Snapshot
	cls      ClassifierSnapshot
	rpl      ReplySnapshot
	sd       SyncDiscardSnapshot
	traces   []string
	paths    []string
	errs     []error
	allowed  map[string]bool
	quiesce  error
	runError error
}

func clsRejectStatements(rng *rand.Rand) []string {
	var out []string
	for _, r := range RejectRules {
		if r.Reason == work.RejectExtendedProtocol {
			continue
		}
		out = append(out, r.Example)
		out = append(out, string(clsPad([]byte(r.Example), rng)))
	}
	for _, c := range []string{"SET search_path = app", "SELECT set_config('search_path', 'app', false)", "SET application_name = 'planted'"} {
		out = append(out, string(clsPad([]byte(c), rng)))
	}
	rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

func (sc clsScenario) name() string {
	if sc.followUps {
		return "classifier-reject-list"
	}
	return "classifier-reject-only"
}

func clsRunScenario(seed int64, sc clsScenario) (*sim.Sim, *clsOutcome) {
	s := sim.New(sim.Config{Seed: seed, Scenario: sc.name()})
	n := simnet.New(s)
	o := &clsOutcome{allowed: map[string]bool{"SELECT 1": true, "SHOW search_path": true}}
	srv, err := fakepg.New(n, fakepg.Config{})
	if err != nil {
		o.runError = err
		return s, o
	}
	o.srv = srv
	gw, err := clsNewGateway(n, srv.Addr(), sc.plant)
	if err != nil {
		o.runError = err
		return s, o
	}
	o.gw = gw
	_ = s.RegisterState("pgwire.harness", func(w sim.StateSink) {
		cs, rs := gw.cls.Snapshot(), gw.rpl.Snapshot()
		w.Int(cs.Classified, cs.ByClass[work.ClassReject], cs.Opaque, cs.ReadOnly, cs.Coalescable, rs.Total, gw.fwd)
		w.Int(cs.ByReason[:]...)
		w.Int(rs.ByRow[:]...)
	})
	rng := sim.NewStream(seed, "pgwire/e2e")
	sc.clients = 1 + rng.IntN(3)
	sc.statements = clsRejectStatements(rng)
	probes := make([]*workload.Probe, sc.clients)
	for i := range probes {
		probes[i] = workload.NewProbe(n, clsGwAddr, simnet.DefaultOptions())
		probes[i].Startup("app", "app")
	}
	run := func() {
		if err := s.RunFor(clsStep); err != nil && o.runError == nil {
			o.runError = err
		}
	}
	run()
	clsShowPaths(probes, &sc)
	run()
	for _, q := range sc.statements {
		for _, p := range probes {
			p.Query(q)
			sc.sentRejects++
		}
		run()
		if sc.followUps {
			for _, p := range probes {
				p.Query("SELECT 1")
				sc.sentAllowed++
			}
			run()
		}
	}
	for _, p := range probes {
		var b []byte
		b = append(b, 'P', 0, 0, 0, 8, 0, 0, 0, 0)
		b = append(b, 'S', 0, 0, 0, 4)
		p.Send(b)
		sc.parses++
	}
	run()
	clsShowPaths(probes, &sc)
	run()
	for _, p := range probes {
		p.Terminate()
	}
	run()
	for _, p := range probes {
		p.Close()
	}
	if err := s.RunUntilQuiet(); err != nil && o.runError == nil {
		o.runError = err
	}
	o.quiesce = s.CheckQuiesce()
	o.sc = sc
	o.pg = srv.Snapshot()
	o.cls = gw.cls.Snapshot()
	o.rpl = gw.rpl.Snapshot()
	o.sd = gw.syncSnapshot()
	o.errs = gw.errs
	for _, p := range probes {
		o.traces = append(o.traces, p.Trace())
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
		o.paths = append(o.paths, strings.Join(paths, "|"))
	}
	return s, o
}

func clsShowPaths(probes []*workload.Probe, sc *clsScenario) {
	if !sc.followUps {
		return
	}
	for _, p := range probes {
		p.Query("SHOW search_path")
		sc.sentAllowed++
	}
}

func (o *clsOutcome) check() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if o.runError != nil {
		add("run: %v", o.runError)
	}
	if o.quiesce != nil {
		add("quiesce: %v", o.quiesce)
	}
	for _, e := range o.errs {
		add("gateway: %v", e)
	}
	if o.srv == nil {
		return errors.Join(errs...)
	}
	sc := o.sc
	if o.cls.ByClass[work.ClassReject] != sc.sentRejects {
		add("classifier rejects %d, reject statements sent %d", o.cls.ByClass[work.ClassReject], sc.sentRejects)
	}
	if o.rpl.ByRow[ErrRowUnsupportedFeature] != sc.sentRejects || o.rpl.ByRow[ErrRowUnsupportedExtended] != sc.parses {
		add("reply counters: unsupported-feature %d (want %d), unsupported-extended %d (want %d)", o.rpl.ByRow[ErrRowUnsupportedFeature], sc.sentRejects, o.rpl.ByRow[ErrRowUnsupportedExtended], sc.parses)
	}
	if o.pg.StatementsStarted != sc.sentAllowed {
		add("the fake Postgres started %d statements, %d allowed statements were sent: a rejected statement reached the database", o.pg.StatementsStarted, sc.sentAllowed)
	}
	for _, r := range o.srv.StatementLog() {
		if !o.allowed[r.Text] {
			add("the fake Postgres ran %q", r.Text)
		}
	}
	if o.pg.WritesApplied != 0 || o.pg.ParamReports != 0 {
		add("database state changed: writes %d, parameter reports %d", o.pg.WritesApplied, o.pg.ParamReports)
	}
	if o.gw.fwd != sc.sentAllowed {
		add("forwarded %d, allowed sent %d", o.gw.fwd, sc.sentAllowed)
	}
	if o.sd.Refused != sc.parses || o.sd.Synced != sc.parses {
		add("sync discard: %+v for %d parse messages", o.sd, sc.parses)
	}
	var sum int64
	for _, v := range o.cls.ByClass {
		sum += v
	}
	if sum != o.cls.Classified || o.cls.Classified != sc.sentRejects+sc.sentAllowed {
		add("classifier conservation: classified %d, sum by class %d, sent %d", o.cls.Classified, sum, sc.sentRejects+sc.sentAllowed)
	}
	var reasons int64
	for _, v := range o.cls.ByReason {
		reasons += v
	}
	if reasons != o.cls.ByClass[work.ClassReject] {
		add("rejects by reason %d, rejects %d", reasons, o.cls.ByClass[work.ClassReject])
	}
	if sc.sentRejects == 0 || o.cls.ByClass[work.ClassReject] == 0 || o.rpl.Total == 0 || o.pg.StartupsCompleted == 0 || (sc.followUps && o.pg.StatementsStarted == 0) {
		add("counters did not move: %+v", o.cls)
	}
	for i, tr := range o.traces {
		want := clsWantTrace(sc)
		k := strings.Index(tr, " K ZI ")
		if k < 0 || tr[k+6:] != want {
			add("client %d saw\n %s\nwant after startup\n %s", i+1, tr, want)
		}
		if p := o.paths[i]; sc.followUps && p != `"$user", public|"$user", public` {
			add("client %d: session state changed: search_path readings %s", i+1, p)
		}
	}
	return errors.Join(errs...)
}

func clsWantTrace(sc clsScenario) string {
	var parts []string
	if sc.followUps {
		parts = append(parts, "T D C:SHOW ZI")
	}
	for range sc.statements {
		parts = append(parts, "E:0A000 ZI")
		if sc.followUps {
			parts = append(parts, "T D C:SELECT 1 ZI")
		}
	}
	parts = append(parts, "E:0A000 ZI")
	if sc.followUps {
		parts = append(parts, "T D C:SHOW ZI")
	}
	parts = append(parts, "eof")
	return strings.Join(parts, " ")
}

func clsScenarioTest(t *testing.T, sc clsScenario) {
	simtest.Sweep(t, func(t *testing.T, seed int64) {
		var last *clsOutcome
		s := simtest.RequireDeterministic(t, seed, func(seed int64) *sim.Sim {
			s, o := clsRunScenario(seed, sc)
			last = o
			return s
		})
		d := s.Digests()
		t.Logf("digests %s seed=%d trace=%s state=%s", t.Name(), seed, d.Trace, d.State)
		if err := last.check(); err != nil {
			t.Fatalf("%v\n%s", err, simtest.ReplayCommand(t, seed))
		}
	})
}

func TestScenarioClassifierRejectList(t *testing.T) {
	clsScenarioTest(t, clsScenario{followUps: true})
}

func TestScenarioClassifierRejectOnly(t *testing.T) {
	clsScenarioTest(t, clsScenario{})
}

func TestClassifierPlantedSetForwardableIsCaught(t *testing.T) {
	for _, seed := range []int64{1, 2, 3} {
		_, o := clsRunScenario(seed, clsScenario{followUps: true})
		if err := o.check(); err != nil {
			t.Fatalf("seed %d: the scenario fails without the planted bug: %v", seed, err)
		}
		_, o = clsRunScenario(seed, clsScenario{followUps: true, plant: true})
		err := o.check()
		if err == nil {
			t.Fatalf("seed %d: with SET treated as forwardable the scenario still passes", seed)
		}
		msg := err.Error()
		if !strings.Contains(msg, "a rejected statement reached the database") || !strings.Contains(msg, "session state changed") {
			t.Fatalf("seed %d: the planted bug was caught for the wrong reason: %v", seed, err)
		}
	}
}

func TestClassifierRejectOnlyFlushNumbers(t *testing.T) {
	_, o := clsRunScenario(1, clsScenario{})
	if err := o.check(); err != nil {
		t.Fatal(err)
	}
	rows := ClassifierMetrics(o.cls)
	get := func(metric, reason string) int64 {
		for _, r := range rows {
			if r.Metric == metric && r.Reason == reason {
				return r.Value
			}
		}
		t.Fatalf("no metric %s/%s", metric, reason)
		return 0
	}
	var rejects int64
	for x := work.RejectSessionSet; x < work.NumRejectReasons; x++ {
		rejects += get("rejects", x.String())
	}
	if rejects != o.sc.sentRejects || get("statements", "") != o.sc.sentRejects+o.sc.sentAllowed {
		t.Fatalf("flushed rejects %d, sent %d", rejects, o.sc.sentRejects)
	}
	if o.pg.StatementsStarted != 0 || o.sc.sentAllowed != 0 {
		t.Fatalf("the fake Postgres started %d statements, want 0", o.pg.StatementsStarted)
	}
}
