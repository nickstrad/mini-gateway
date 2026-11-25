package world

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
	"github.com/nickstrad/mini-gateway/internal/testclient"
)

// Defaults and bounds for the Postgres 17 fidelity script runner.
const (
	FidelityUser = "postgres"
	ProbeKey     = "probe"
	MaxConns     = 4
	CollectLimit = 60 * time.Second
)

// Step is one action in a fidelity Script: opening a connection, sending
// synchronous or asynchronous queries, collecting replies, pausing, or terminating.
type Step struct {
	Name      string
	Conn      int
	Open      bool
	Queries   []string
	Async     bool
	Collect   int
	Terminate bool
	Pause     time.Duration
	Values    bool
}

// Script is a named sequence of protocol exchanges executed identically against
// fakepg and real Postgres 17.
type Script struct {
	Name  string
	Steps []Step
}

// Conn is one client connection opened by a fidelity Target.
type Conn interface {
	Send(qs ...string) error
	Collect(n int) ([]testclient.Msg, bool, error)
}

// Target abstracts the database under test (SimTarget or real Postgres over TCP)
// for RunScript.
type Target interface {
	Open(user, db string) (Conn, []testclient.Msg, error)
	Terminate(c Conn) error
	Pause(d time.Duration) error
}

// ProbeScript returns the fidelity Script covering the Phase 1 probe's simple-query
// exchanges (B0 through B10) with trust authentication.
func ProbeScript() Script {
	q := func(name string, qs ...string) Step { return Step{Name: name, Queries: qs} }
	return Script{Name: "probe-b0-b10", Steps: []Step{
		{Name: "B0 startup", Open: true},
		q("B1 begin and select in one message", "BEGIN; SELECT 1"),
		q("B2 error inside the transaction", "SELECT 1/0"),
		q("B2 rollback", "ROLLBACK"),
		q("B3 two result sets", "SELECT 1; SELECT 2"),
		q("B4 three pipelined, the middle fails", "SELECT 'a'", "SELECT 1/0", "SELECT 'c'"),
		q("B5 pipelined after begin", "BEGIN", "SELECT txid_current() IS NOT NULL"),
		q("B5 cleanup", "ROLLBACK"),
		q("B6 session state", "SET statement_timeout = 1234", "SET application_name = 'tenant-a'", "SHOW statement_timeout"),
		q("B6 discard all", "DISCARD ALL", "SHOW statement_timeout"),
		q("B7 empty query", ""),
		q("B8 large reply", "SELECT repeat('x', 100) FROM generate_series(1, 200000)"),
		{Name: "B10 writer connection", Conn: 1, Open: true},
		{Name: "B10 in-flight read", Queries: []string{"SELECT v, pg_sleep(1) IS NOT NULL FROM kv WHERE k = '" + ProbeKey + "'"}, Async: true},
		{Name: "B10 pause", Pause: 200 * time.Millisecond},
		{Name: "B10 write during the read", Conn: 1, Queries: []string{"UPDATE kv SET v = '2' WHERE k = '" + ProbeKey + "'"}},
		{Name: "B10 the in-flight read returns the old value", Collect: 1, Values: true},
		{Name: "B10 a read after the write", Conn: 1, Queries: []string{"SELECT v FROM kv WHERE k = '" + ProbeKey + "'"}, Values: true},
		{Name: "B9 long statement", Queries: []string{"SELECT pg_sleep(30)"}, Async: true},
		{Name: "B9 pause", Pause: 300 * time.Millisecond},
		{Name: "B9 terminate the backend", Terminate: true},
		{Name: "B9 the reply ends without its end marker", Collect: 1},
	}}
}

// Transcript holds the normalized token lines produced by RunScript, stripping
// values that legitimately differ between real Postgres and fakepg (PIDs, secrets,
// server version text, timings).
type Transcript struct {
	Lines []string
}

// String formats Transcript with one line per step plus a trailing newline.
func (t Transcript) String() string { return strings.Join(t.Lines, "\n") + "\n" }

// ParseTranscript parses a recorded transcript file, ignoring blank lines and # comments.
func ParseTranscript(s string) Transcript {
	var out Transcript
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			out.Lines = append(out.Lines, l)
		}
	}
	return out
}

// Compare returns an error detailing every line where got diverges from want.
func Compare(want, got Transcript) error {
	var errs []error
	for i := 0; i < max(len(want.Lines), len(got.Lines)); i++ {
		w, g := "(missing)", "(missing)"
		if i < len(want.Lines) {
			w = want.Lines[i]
		}
		if i < len(got.Lines) {
			g = got.Lines[i]
		}
		if w != g {
			errs = append(errs, fmt.Errorf("line %d:\n  want %s\n  got  %s", i+1, w, g))
		}
	}
	return errors.Join(errs...)
}

// RunScript executes sc step by step against t and records the normalized Transcript.
func RunScript(sc Script, t Target) (Transcript, error) {
	var tr Transcript
	conns := make([]Conn, MaxConns)
	pending := make([]int, MaxConns)
	for _, st := range sc.Steps {
		if st.Conn < 0 || st.Conn >= MaxConns {
			return tr, fmt.Errorf("step %q: connection %d", st.Name, st.Conn)
		}
		var err error
		switch {
		case st.Open:
			var msgs []testclient.Msg
			conns[st.Conn], msgs, err = t.Open(FidelityUser, FidelityUser)
			if err == nil {
				tr.Lines = append(tr.Lines, st.Name+": "+startupTokens(msgs), st.Name+" parameters: "+paramNames(msgs))
			}
		case st.Pause > 0:
			err = t.Pause(st.Pause)
		case st.Terminate:
			err = t.Terminate(conns[st.Conn])
		case len(st.Queries) > 0:
			c := conns[st.Conn]
			if c == nil {
				return tr, fmt.Errorf("step %q: connection %d is not open", st.Name, st.Conn)
			}
			if err = c.Send(st.Queries...); err != nil {
				break
			}
			if st.Async {
				pending[st.Conn] += len(st.Queries)
				break
			}
			err = collectLine(&tr, c, st, len(st.Queries))
		case st.Collect > 0:
			c := conns[st.Conn]
			if c == nil || pending[st.Conn] < st.Collect {
				return tr, fmt.Errorf("step %q: nothing pending", st.Name)
			}
			pending[st.Conn] -= st.Collect
			err = collectLine(&tr, c, st, st.Collect)
		}
		if err != nil {
			return tr, fmt.Errorf("step %q: %w", st.Name, err)
		}
	}
	return tr, nil
}

func collectLine(tr *Transcript, c Conn, st Step, n int) error {
	msgs, ended, err := c.Collect(n)
	if err != nil {
		return err
	}
	tr.Lines = append(tr.Lines, st.Name+": "+Tokens(msgs, ended, st.Values))
	return nil
}

func startupTokens(msgs []testclient.Msg) string {
	var toks []string
	params := 0
	for _, m := range msgs {
		if m.Type == testclient.MsgParamStatus {
			params++
			continue
		}
		if params > 0 {
			toks = append(toks, "S*"+strconv.Itoa(params))
			params = 0
		}
		toks = append(toks, token(m, false))
	}
	return strings.Join(toks, " ")
}

func paramNames(msgs []testclient.Msg) string {
	var names []string
	for _, m := range msgs {
		if m.Type == testclient.MsgParamStatus {
			names = append(names, m.Name)
		}
	}
	slices.Sort(names)
	return strings.Join(names, ",")
}

// Tokens formats a slice of backend messages (plus optional EOF) into compact
// transcript tokens; consecutive DataRows collapse to D*<n> unless values is true.
func Tokens(msgs []testclient.Msg, ended bool, values bool) string {
	var toks []string
	rows := 0
	flush := func() {
		if rows > 1 {
			toks = append(toks, "D*"+strconv.Itoa(rows))
		} else if rows == 1 {
			toks = append(toks, "D")
		}
		rows = 0
	}
	for _, m := range msgs {
		if m.Type == testclient.MsgDataRow && !values {
			rows++
			continue
		}
		flush()
		toks = append(toks, token(m, values))
	}
	flush()
	if ended {
		toks = append(toks, "EOF")
	}
	return strings.Join(toks, " ")
}

func token(m testclient.Msg, values bool) string {
	switch m.Type {
	case testclient.MsgAuth:
		return "R:" + strconv.FormatUint(uint64(m.Auth), 10)
	case testclient.MsgReady:
		return "Z:" + string(m.Status)
	case testclient.MsgRowDesc:
		return "T" + strconv.Itoa(len(m.Fields))
	case testclient.MsgDataRow:
		var vs []string
		for _, v := range m.Values {
			vs = append(vs, string(v))
		}
		return "D[" + strings.Join(vs, ",") + "]"
	case testclient.MsgCommand:
		return "C:" + strings.ReplaceAll(m.Tag, " ", "_")
	case testclient.MsgError, testclient.MsgNotice:
		return string(m.Type) + ":" + m.Severity + ":" + m.Code
	case testclient.MsgParamStatus:
		return "S:" + m.Name
	case testclient.MsgEmptyQuery:
		return "I"
	case testclient.MsgBackendKey:
		return "K"
	}
	return string(m.Type)
}

// SimTarget runs a fidelity Script against an in-process fakepg.Server over simnet.
type SimTarget struct {
	S    *sim.Sim
	Net  *simnet.Net
	Fake *fakepg.Server
}

// NewSimTarget constructs a seeded SimTarget preloaded with ProbeKey="1".
func NewSimTarget(seed int64) (*SimTarget, error) {
	s := sim.New(sim.Config{Seed: seed, Scenario: "fidelity"})
	n := simnet.New(s)
	srv, err := fakepg.New(n, fakepg.Config{Superusers: []string{FidelityUser}, Rows: []fakepg.Row{{Key: ProbeKey, Value: "1"}}})
	if err != nil {
		return nil, err
	}
	return &SimTarget{S: s, Net: n, Fake: srv}, nil
}

type simConn struct {
	t     *SimTarget
	p     *workload.Probe
	taken int
}

// Open dials the simulated server, sends Startup, and collects messages through ReadyForQuery.
func (t *SimTarget) Open(user, db string) (Conn, []testclient.Msg, error) {
	c := &simConn{t: t, p: workload.NewProbe(t.Net, t.Fake.Addr(), simnet.DefaultOptions())}
	c.p.Startup(user, db)
	msgs, ended, err := c.Collect(1)
	if err != nil || ended {
		return nil, msgs, fmt.Errorf("startup failed: %v %s", err, Tokens(msgs, ended, false))
	}
	return c, msgs, nil
}

// Terminate injects FaultTerminate against c's backend, matching pg_terminate_backend on Postgres.
func (t *SimTarget) Terminate(c Conn) error {
	sc, ok := c.(*simConn)
	if !ok {
		return errors.New("not a simulator connection")
	}
	t.S.ApplyFault(sim.Fault{Kind: fakepg.FaultTerminate, Target: sc.p.End().ID()})
	return t.S.Err()
}

// Pause advances virtual time by d.
func (t *SimTarget) Pause(d time.Duration) error { return t.S.RunFor(d) }

// Close cleanly terminates and closes conns, drains the loop, and checks quiesce.
func (t *SimTarget) Close(conns ...Conn) error {
	for _, c := range conns {
		if sc, ok := c.(*simConn); ok {
			sc.p.Terminate()
			sc.p.Close()
		}
	}
	if err := t.S.RunUntilQuiet(); err != nil {
		return err
	}
	return t.S.CheckQuiesce()
}

func (c *simConn) Send(qs ...string) error {
	c.p.Query(qs...)
	return nil
}

func (c *simConn) Collect(n int) ([]testclient.Msg, bool, error) {
	deadline := sim.Later(c.t.S.Now(), CollectLimit)
	for {
		if msgs, ended, ok := c.scan(n); ok {
			return msgs, ended, nil
		}
		if c.t.S.Now() >= deadline {
			return nil, false, errors.New("no reply within the collect limit")
		}
		if err := c.t.S.RunFor(time.Millisecond); err != nil {
			return nil, false, err
		}
	}
}

func (c *simConn) scan(n int) ([]testclient.Msg, bool, bool) {
	var msgs []testclient.Msg
	ready := 0
	all := c.p.Events()
	for i := c.taken; i < len(all); i++ {
		e := all[i]
		if e.Kind != workload.EventMessage {
			if e.Kind == workload.EventEOF || e.Kind == workload.EventReset || e.Kind == workload.EventProtocolError {
				c.taken = i + 1
				return msgs, true, true
			}
			continue
		}
		msgs = append(msgs, e.Msg)
		if e.Msg.Type == testclient.MsgReady {
			ready++
			if ready == n {
				c.taken = i + 1
				return msgs, false, true
			}
		}
	}
	return nil, false, false
}
