package admission

import (
	"fmt"
	"math/big"
	"slices"
)

func refSquareFits(d Time, count int64, interval Time) bool {
	lhs := new(big.Int).Mul(big.NewInt(d), big.NewInt(d))
	lhs.Mul(lhs, big.NewInt(count))
	rhs := new(big.Int).Mul(big.NewInt(interval), big.NewInt(interval))
	return lhs.Cmp(rhs) <= 0
}

type opKind uint8

const (
	opPush opKind = iota
	opPop
	opRemove
	opExpire
	opAdvance
	opToNext
	opUnpop
	numOpKinds
)

type op struct {
	kind   opKind
	tenant int
	pick   int
	dt     Time
}

type world struct {
	cfg     Config
	q       *Queues
	ref     *refModel
	now     Time
	nextID  int64
	queued  []int64
	served  []servedRec
	shedAll []Shed
	log     []string
}

type servedRec struct {
	id   int64
	wait Time
	at   Time
}

func newWorld(cfg Config) (*world, error) {
	q, err := New(cfg)
	if err != nil {
		return nil, err
	}
	return &world{cfg: q.Config(), q: q, ref: newRef(cfg), now: 1}, nil
}

func tenantName(i int) string { return "t" + string(rune('a'+i%26)) }

func (w *world) drain() []Shed {
	var out []Shed
	for {
		s, ok := w.q.TakeShed()
		if !ok {
			return out
		}
		out = append(out, s)
	}
}

func (w *world) forget(id int64) {
	if i := slices.Index(w.queued, id); i >= 0 {
		w.queued = slices.Delete(w.queued, i, i+1)
	}
}

func (w *world) apply(o op) error {
	w.log = append(w.log, fmt.Sprintf("%d:%v", w.now, o))
	switch o.kind {
	case opPush:
		w.nextID++
		id := w.nextID
		name := tenantName(o.tenant)
		got := w.q.Push(w.now, name, id)
		want, err := w.ref.push(w.now, name, id)
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("push %d: core %v, reference %v", id, got, want)
		}
		if got {
			w.queued = append(w.queued, id)
		}
	case opPop:
		id, ok := w.q.Pop(w.now)
		rid, rok, err := w.ref.pop(w.now)
		if err != nil {
			return err
		}
		if id != rid || ok != rok {
			return fmt.Errorf("pop: core %d %v, reference %d %v", id, ok, rid, rok)
		}
		if ok {
			w.forget(id)
			w.served = append(w.served, servedRec{id: id, wait: w.q.lastPop.e.enq, at: w.now})
			w.served[len(w.served)-1].wait = w.now - w.q.lastPop.e.enq
		}
	case opRemove, opExpire:
		if len(w.queued) == 0 {
			return w.compare()
		}
		id := w.queued[o.pick%len(w.queued)]
		got := w.q.take(w.now, id, o.kind == opExpire)
		want, err := w.ref.take(w.now, id, o.kind == opExpire)
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("remove %d: core %v, reference %v", id, got, want)
		}
		w.forget(id)
	case opAdvance:
		w.now += o.dt
		w.q.Sweep(w.now)
		if err := w.ref.advance(w.now); err != nil {
			return err
		}
	case opToNext:
		at := w.q.Next()
		if at != w.ref.next() {
			return fmt.Errorf("next: core %d, reference %d", at, w.ref.next())
		}
		if at > w.now {
			w.now = at
		}
		w.q.Sweep(w.now)
		if err := w.ref.advance(w.now); err != nil {
			return err
		}
	case opUnpop:
		got := w.q.Unpop()
		want := w.ref.unpop()
		if got != want {
			return fmt.Errorf("unpop: core %v, reference %v", got, want)
		}
		if got && len(w.served) > 0 {
			last := w.served[len(w.served)-1]
			w.served = w.served[:len(w.served)-1]
			w.queued = append(w.queued, last.id)
		}
	}
	return w.compare()
}

func (w *world) compare() error {
	got := w.drain()
	want := w.ref.takeSheds()
	if !slices.Equal(got, want) {
		return fmt.Errorf("sheds: core %v, reference %v", got, want)
	}
	for _, s := range got {
		w.forget(s.ID)
	}
	w.shedAll = append(w.shedAll, got...)
	if w.q.Len() != w.ref.total() {
		return fmt.Errorf("len: core %d, reference %d", w.q.Len(), w.ref.total())
	}
	if w.q.Next() != w.ref.next() {
		return fmt.Errorf("next: core %d, reference %d", w.q.Next(), w.ref.next())
	}
	if len(w.q.list) != len(w.ref.tenants) {
		return fmt.Errorf("tenant queues: core %d, reference %d", len(w.q.list), len(w.ref.tenants))
	}
	for i, t := range w.q.list {
		r := w.ref.tenants[i]
		if t.name != r.name || t.lifo != r.lifo || t.ring.len() != len(r.items) {
			return fmt.Errorf("tenant %d: core %s lifo %v depth %d, reference %s lifo %v depth %d", i, t.name, t.lifo, t.ring.len(), r.name, r.lifo, len(r.items))
		}
		for j := 0; j < t.ring.len(); j++ {
			if t.ring.at(j).id != r.items[j].id {
				return fmt.Errorf("tenant %s position %d: core %d, reference %d", t.name, j, t.ring.at(j).id, r.items[j].id)
			}
		}
		if t.rule.State(w.q.last, t.ring.len()) != w.ref.state(r, w.ref.last) {
			return fmt.Errorf("tenant %s state: core %v, reference %v", t.name, t.rule.State(w.q.last, t.ring.len()), w.ref.state(r, w.ref.last))
		}
		if t.gauge.load() != LoadFromDelay(r.gaugeMin, w.cfg.Target) {
			return fmt.Errorf("tenant %s load: core %d, reference %d", t.name, t.gauge.load(), LoadFromDelay(r.gaugeMin, w.cfg.Target))
		}
	}
	if err := w.q.Check(); err != nil {
		return err
	}
	return w.claims()
}

func (w *world) claims() error {
	if w.cfg.Plant&PlantNoHeadSweep != 0 {
		return nil
	}
	if w.cfg.Rule == RuleTimeout {
		for _, t := range w.q.list {
			rule := t.rule.(*TimeoutRule)
			for j := 0; j < t.ring.len(); j++ {
				if age := w.now - t.ring.at(j).enq; age >= rule.Allowed(w.now) {
					return fmt.Errorf("claim 6: tenant %s item %d waited %d, allowed %d, still queued", t.name, t.ring.at(j).id, age, rule.Allowed(w.now))
				}
			}
		}
		for _, s := range w.served {
			if s.wait >= w.cfg.Interval {
				return fmt.Errorf("claim 2: request %d served after %d, interval %d", s.id, s.wait, w.cfg.Interval)
			}
		}
	}
	return nil
}

func (w *world) finish() error {
	for range 64 {
		w.queued = w.ref.ids()
		for len(w.queued) > 0 {
			if err := w.apply(op{kind: opRemove}); err != nil {
				return err
			}
		}
		if w.q.Next() == 0 {
			break
		}
		if err := w.apply(op{kind: opToNext}); err != nil {
			return err
		}
	}
	if err := w.q.Quiesce(); err != nil {
		return err
	}
	s := w.q.Snapshot()
	if s.Shedding != 0 || s.LIFOTenants != 0 || s.RuleStates[StateNormal] != s.Tenants.Value {
		return fmt.Errorf("steady state: shedding %d, lifo %d, states %v", s.Shedding, s.LIFOTenants, s.RuleStates)
	}
	return nil
}

func (o op) String() string {
	names := [...]string{"push", "pop", "remove", "expire", "advance", "to-next", "unpop"}
	return fmt.Sprintf("%s(t%d,p%d,dt%d)", names[o.kind], o.tenant, o.pick, o.dt)
}
