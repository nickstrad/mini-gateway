package coalesce

import (
	"fmt"
	"slices"

	"github.com/nickstrad/mini-gateway/internal/work"
)

type opKind uint8

const (
	opJoin opKind = iota
	opLeave
	opStart
	opStore
	opFinish
	opAdvance
	opSweep
	opCancelAtResult
	numOpKinds
)

var opNames = [numOpKinds]string{"join", "leave", "start", "store", "finish", "advance", "sweep", "cancel-at-result"}

type op struct {
	kind opKind
	a, b byte
}

func (o op) String() string { return fmt.Sprintf("%s(%d,%d)", opNames[o.kind], o.a, o.b) }

const (
	fuzzMaxOps   = 96
	opBytes      = 3
	maxAdvanceMs = 255
)

func decodeOps(b []byte) []op {
	var ops []op
	for i := 0; i+opBytes <= len(b) && len(ops) < fuzzMaxOps; i += opBytes {
		ops = append(ops, op{kind: opKind(b[i] % byte(numOpKinds)), a: b[i+1], b: b[i+2]})
	}
	return ops
}

var testKeys = [...]string{"SELECT v FROM kv WHERE k = 'a'", "SELECT v FROM kv WHERE k = 'b'", "SELECT 1", "SELECT 2"}

func unitFor(a byte, scope work.KeyScope) work.Unit {
	text := testKeys[a%4]
	tenant := work.TenantID("t" + string(rune('0'+(a>>2)%2)))
	u := work.Unit{Tenant: tenant, Class: work.ClassStartLease}
	switch (a >> 3) % 4 {
	case 0, 1:
		u.SafeRead, u.Shareable = true, true
	case 2:
		u.Hinted = true
	case 3:
		u.SafeRead = true
	}
	if u.Shareable || u.Hinted || u.SafeRead {
		u.HasKey = true
		u.Key.Text = []byte(text)
		if scope == work.ScopeTenant {
			u.Key.Tenant = tenant
		}
	}
	return u
}

func smallConfig(mode Mode, join JoinMode) Config {
	return Config{Mode: mode, Join: join, MaxFlights: 3, MaxWaiters: 4, MaxReplyBytes: 4096,
		NoShareFor: 200 * Millisecond, NoShareMax: 2, Deadline: 500 * Millisecond}
}

func testConfigs(plant Plant) []Config {
	var out []Config
	for _, mode := range []Mode{ModeAuto, ModeHinted} {
		for _, join := range []JoinMode{JoinStrict, JoinRelaxed} {
			c := smallConfig(mode, join)
			c.Plant = plant
			out = append(out, c)
		}
	}
	return out
}

type world struct {
	t        *Table
	m        *model
	scope    work.KeyScope
	now      Instant
	nextReq  RequestID
	flights  []FlightID
	live     []RequestID
	ended    map[RequestID]NoticeKind
	gone     map[RequestID]bool
	trail    []string
	answered int
}

func newWorld(cfg Config) (*world, error) {
	t, err := New(cfg)
	if err != nil {
		return nil, err
	}
	scope := work.ScopeGlobal
	if cfg.Mode == ModeHinted {
		scope = work.ScopeTenant
	}
	return &world{t: t, m: newModel(cfg), scope: scope, ended: map[RequestID]NoticeKind{}, gone: map[RequestID]bool{}}, nil
}

func (w *world) pickFlight(b byte) (FlightID, bool) {
	if len(w.flights) == 0 {
		return 0, false
	}
	return w.flights[int(b)%len(w.flights)], true
}

func (w *world) step(o op) error {
	w.trail = append(w.trail, o.String())
	switch o.kind {
	case opJoin:
		w.nextReq++
		u := unitFor(o.a, w.scope)
		got := w.t.Join(w.now, w.nextReq, &u)
		want := w.m.join(w.now, w.nextReq, &u)
		if got != want {
			return fmt.Errorf("join %d: core %v/f%d, reference %v/f%d", w.nextReq, got.Verdict, got.Flight, want.Verdict, want.Flight)
		}
		if got.Verdict == VerdictFollow && w.t.cfg.Join == JoinStrict && w.flightState(got.Flight) == stateRunning {
			return fmt.Errorf("strict: request %d joined flight %d already running on the database", w.nextReq, got.Flight)
		}
		if got.Verdict == VerdictLead || got.Verdict == VerdictHold {
			w.flights = append(w.flights, got.Flight)
		}
		if got.Verdict.Waits() || got.Verdict == VerdictLead {
			w.live = append(w.live, w.nextReq)
		}
	case opLeave, opCancelAtResult:
		if len(w.live) == 0 {
			return w.settle()
		}
		id := w.live[int(o.a)%len(w.live)]
		why := LeaveCancel
		if o.b%2 == 1 {
			why = LeaveGone
		}
		got := w.t.Leave(w.now, id, why)
		want := w.m.leave(w.now, id)
		if got != want {
			return fmt.Errorf("leave %d: core %+v, reference %+v", id, got, want)
		}
		if got.Kind != LeftNone {
			w.gone[id] = true
			w.live = slices.DeleteFunc(w.live, func(r RequestID) bool { return r == id })
		}
		if o.kind == opCancelAtResult {
			if err := w.settle(); err != nil {
				return err
			}
			w.t.Finish(w.now, got.Flight, Result{})
			w.m.finish(w.now, got.Flight, Result{})
		}
	case opStart:
		if f, ok := w.pickFlight(o.a); ok {
			w.t.Started(w.now, f)
			w.m.started(w.now, f)
		}
	case opStore:
		if f, ok := w.pickFlight(o.a); ok {
			n := int(o.b) * 32
			got := w.t.Stored(w.now, f, n)
			want := w.m.stored(w.now, f, n)
			if got != want {
				return fmt.Errorf("store %d bytes on flight %d: core %v, reference %v", n, f, got, want)
			}
		}
	case opFinish:
		if f, ok := w.pickFlight(o.a); ok {
			r := Result{Failed: o.b%4 == 1, ErrorReply: o.b%4 == 2, Unshareable: o.b%4 == 3}
			w.t.Finish(w.now, f, r)
			w.m.finish(w.now, f, r)
		}
	case opAdvance:
		w.now += Instant(o.a) * 4 * Millisecond
		w.t.Sweep(w.now)
		w.m.advance(w.now)
	case opSweep:
		w.t.Sweep(w.now)
		w.m.advance(w.now)
	}
	return w.settle()
}

func (w *world) flightState(id FlightID) flightState {
	fi, ok := w.t.byID[id]
	if !ok {
		return 0
	}
	return w.t.flights[fi].state
}

func (w *world) settle() error {
	got := takeAll(w.t)
	want := w.m.takeAll()
	if noticesString(got) != noticesString(want) {
		return fmt.Errorf("notices differ:\n core      %s\n reference %s", noticesString(got), noticesString(want))
	}
	for _, n := range got {
		if n.Kind == NoticeStart {
			continue
		}
		if w.gone[n.Waiter] {
			return fmt.Errorf("request %d left its flight and was then sent %s", n.Waiter, n.Kind)
		}
		if k, ok := w.ended[n.Waiter]; ok {
			return fmt.Errorf("request %d answered twice (%s then %s)", n.Waiter, k, n.Kind)
		}
		w.ended[n.Waiter] = n.Kind
		w.answered++
		w.live = slices.DeleteFunc(w.live, func(r RequestID) bool { return r == n.Waiter })
	}
	if err := w.t.Check(); err != nil {
		return err
	}
	if got, want := w.t.Next(), w.m.next(); got != want {
		return fmt.Errorf("next wake-up: core %d, reference %d", got, want)
	}
	for _, k := range testKeys {
		for _, tenant := range []work.TenantID{"", "t0", "t1"} {
			if n := w.t.Running(work.ShareKey{Tenant: tenant, Text: []byte(k)}); n > 1 {
				return fmt.Errorf("%d flights of one key running on the database", n)
			}
		}
	}
	return nil
}

func (w *world) finishAll() error {
	for range 64 {
		if len(w.t.byID) == 0 {
			break
		}
		w.now += 100 * Millisecond
		w.t.Sweep(w.now)
		w.m.advance(w.now)
		if err := w.settle(); err != nil {
			return err
		}
	}
	w.now += w.t.cfg.NoShareFor + w.t.cfg.Deadline
	w.t.Sweep(w.now)
	w.m.advance(w.now)
	if err := w.settle(); err != nil {
		return err
	}
	if err := w.t.Quiesce(); err != nil {
		return err
	}
	return w.t.Snapshot().Check()
}

func runOps(cfg Config, ops []op) (*world, error) {
	w, err := newWorld(cfg)
	if err != nil {
		return nil, err
	}
	for _, o := range ops {
		if err := w.step(o); err != nil {
			return w, err
		}
	}
	return w, w.finishAll()
}

func trail(w *world) string {
	if w == nil {
		return ""
	}
	return fmt.Sprint(w.trail)
}
