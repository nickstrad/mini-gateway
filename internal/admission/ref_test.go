package admission

import (
	"fmt"
	"slices"
)

type refItem struct {
	id  int64
	enq Time
	seq int64
}

type refTenant struct {
	name    string
	items   []refItem
	created Time

	lastEmpty Time

	firstAboveTime Time
	dropNext       Time
	count          int64
	lastCount      int64
	dropping       bool
	pendingAdvance bool

	follyMin     Time
	follyOver    bool
	follyEnd     Time
	follyReset   bool
	gaugeMin     Time
	gaugeEnd     Time
	gaugeReset   bool
	lifo         bool
	lastOverload Time
	emptyAt      Time
	shedAt       Time
	hasShed      bool
}

type refModel struct {
	cfg     Config
	tenants []*refTenant
	last    Time
	seq     int64
	sheds   []Shed
	served  int64
	pushes  int64
	removed int64
	lastPop *refPop
	shedBy  [NumReasons]int64
}

type refPop struct {
	t    *refTenant
	item refItem
	back bool
}

func newRef(cfg Config) *refModel { return &refModel{cfg: cfg.WithDefaults()} }

func (m *refModel) total() int {
	n := 0
	for _, t := range m.tenants {
		n += len(t.items)
	}
	return n
}

func (m *refModel) find(name string) *refTenant {
	for _, t := range m.tenants {
		if t.name == name {
			return t
		}
	}
	return nil
}

func (m *refModel) overloaded(t *refTenant, now Time) bool {
	if len(t.items) == 0 {
		return false
	}
	switch m.cfg.Rule {
	case RuleTimeout:
		return now-t.lastEmpty >= m.cfg.Interval
	case RuleControlLaw:
		return t.dropping
	case RuleFolly:
		return t.follyOver
	}
	return false
}

func (m *refModel) state(t *refTenant, now Time) RuleState {
	if !m.overloaded(t, now) {
		return StateNormal
	}
	switch m.cfg.Rule {
	case RuleTimeout:
		return StateStanding
	case RuleControlLaw:
		return StateDropping
	}
	return StateOverloaded
}

func (m *refModel) allowed(t *refTenant, now Time) Time {
	if now-t.lastEmpty >= m.cfg.Interval {
		return m.cfg.Target
	}
	return m.cfg.Interval
}

func (m *refModel) itemDue(t *refTenant, it refItem) Time {
	switch m.cfg.Rule {
	case RuleTimeout:
		return max(t.lastEmpty+m.cfg.Interval, it.enq+m.cfg.Target)
	case RuleControlLaw:
		if t.dropping {
			return t.dropNext
		}
		if t.firstAboveTime != 0 {
			return t.firstAboveTime
		}
		return it.enq + m.cfg.Target
	}
	return 0
}

func (m *refModel) due(t *refTenant) Time {
	var at Time
	take := func(v Time) {
		if v != 0 && (at == 0 || v < at) {
			at = v
		}
	}
	if len(t.items) == 0 {
		if !t.lifo {
			take(t.emptyAt + QuietIntervals*m.cfg.Interval)
		}
	} else if m.cfg.Plant&PlantNoHeadSweep == 0 {
		if m.cfg.Rule == RuleTimeout {
			for _, it := range t.items {
				take(m.itemDue(t, it))
			}
		} else {
			take(m.itemDue(t, t.items[0]))
		}
	}
	if t.lifo && m.cfg.LIFO == LIFOAdaptive && !m.overloaded(t, m.last) {
		take(t.lastOverload + m.cfg.LIFOHold)
	}
	if t.hasShed && m.last < t.shedAt+m.cfg.Interval {
		take(t.shedAt + m.cfg.Interval)
	}
	return at
}

func (m *refModel) next() Time {
	var at Time
	for _, t := range m.tenants {
		if v := m.due(t); v != 0 && (at == 0 || v < at) {
			at = v
		}
	}
	return at
}

func (m *refModel) lifoObserve(t *refTenant, now Time) {
	if m.cfg.LIFO == LIFOOff {
		return
	}
	if m.overloaded(t, now) {
		t.lastOverload = now
		t.lifo = true
		return
	}
	if t.lifo && (m.cfg.Plant&PlantNoLIFOHysteresis != 0 || now-t.lastOverload >= m.cfg.LIFOHold) {
		t.lifo = false
	}
}

func (m *refModel) shed(t *refTenant, it refItem, now Time, r Reason) {
	m.shedBy[r]++
	t.shedAt, t.hasShed = now, true
	m.sheds = append(m.sheds, Shed{ID: it.id, Tenant: t.name, Reason: r, Wait: now - it.enq})
}

func (m *refModel) becameEmpty(t *refTenant, now Time) {
	t.emptyAt = now
	t.lastEmpty = now
	t.firstAboveTime = 0
	t.dropping = false
	t.pendingAdvance = false
}

func (m *refModel) codelOK(t *refTenant, now Time) bool {
	sojourn := now - t.items[0].enq
	if sojourn < m.cfg.Target {
		t.firstAboveTime = 0
		return false
	}
	if t.firstAboveTime == 0 {
		t.firstAboveTime = now + m.cfg.Interval
		return false
	}
	return now >= t.firstAboveTime
}

func (m *refModel) codelSweep(t *refTenant, now Time) {
	for len(t.items) > 0 {
		ok := m.codelOK(t, now)
		if t.pendingAdvance {
			t.pendingAdvance = false
			if !ok {
				t.dropping = false
				return
			}
			t.dropNext = refLaw(t.dropNext, m.cfg.Interval, t.count)
		}
		if t.dropping {
			if !ok {
				t.dropping = false
				return
			}
			if now < t.dropNext {
				return
			}
			t.count = min(t.count+1, MaxDropCount)
			t.pendingAdvance = true
		} else {
			if !ok {
				return
			}
			delta := t.count - t.lastCount
			t.count = 1
			if delta > 1 && now-t.dropNext < 16*m.cfg.Interval {
				t.count = delta
			}
			t.dropNext = refLaw(now, m.cfg.Interval, t.count)
			t.lastCount = t.count
			t.dropping = true
		}
		it := t.items[0]
		t.items = t.items[1:]
		m.shed(t, it, now, ReasonRule)
	}
	m.becameEmpty(t, now)
}

func (m *refModel) timeoutSweep(t *refTenant, now Time) error {
	allowed := m.allowed(t, now)
	expired := 0
	for i, it := range t.items {
		if now-it.enq >= allowed {
			if i != expired {
				return fmt.Errorf("item %d expired but an older one did not", it.id)
			}
			expired++
		}
	}
	for _, it := range t.items[:expired] {
		m.shed(t, it, now, ReasonRule)
	}
	t.items = t.items[expired:]
	if expired > 0 && len(t.items) == 0 {
		m.becameEmpty(t, now)
	}
	return nil
}

func (m *refModel) step(now Time) error {
	m.last = max(m.last, now)
	for _, t := range m.tenants {
		m.lifoObserve(t, now)
		if m.cfg.Plant&PlantNoHeadSweep == 0 && len(t.items) > 0 {
			switch m.cfg.Rule {
			case RuleTimeout:
				if err := m.timeoutSweep(t, now); err != nil {
					return err
				}
			case RuleControlLaw:
				m.codelSweep(t, now)
			}
		}
		m.lifoObserve(t, now)
	}
	kept := m.tenants[:0]
	for _, t := range m.tenants {
		if len(t.items) == 0 && now >= t.emptyAt+QuietIntervals*m.cfg.Interval && !t.lifo {
			continue
		}
		kept = append(kept, t)
	}
	m.tenants = kept
	return nil
}

func (m *refModel) advance(now Time) error {
	m.lastPop = nil
	for {
		at := m.next()
		if at == 0 || at > now {
			return m.step(now)
		}
		if err := m.step(at); err != nil {
			return err
		}
	}
}

func (m *refModel) push(now Time, name string, id int64) (bool, error) {
	if err := m.advance(now); err != nil {
		return false, err
	}
	m.pushes++
	t := m.find(name)
	if t == nil {
		if len(m.tenants) >= m.cfg.MaxTenants {
			victim := -1
			for i, c := range m.tenants {
				if len(c.items) == 0 && (victim < 0 || c.emptyAt < m.tenants[victim].emptyAt) {
					victim = i
				}
			}
			if victim < 0 {
				m.shedBy[ReasonTenants]++
				return false, nil
			}
			m.tenants = slices.Delete(m.tenants, victim, victim+1)
		}
		t = &refTenant{name: name, created: now, emptyAt: now, follyEnd: now, follyReset: true, gaugeEnd: now, gaugeReset: true}
		m.tenants = append(m.tenants, t)
	}
	if len(t.items) == m.cfg.TenantCapacity {
		it := t.items[0]
		t.items = t.items[1:]
		m.shed(t, it, now, ReasonOverflow)
		if len(t.items) == 0 {
			m.becameEmpty(t, now)
		}
	} else if m.total() >= m.cfg.Capacity {
		var o *refTenant
		for _, c := range m.tenants {
			if len(c.items) > 0 && (o == nil || c.items[0].seq < o.items[0].seq) {
				o = c
			}
		}
		it := o.items[0]
		o.items = o.items[1:]
		m.shed(o, it, now, ReasonTotal)
		if len(o.items) == 0 {
			m.becameEmpty(o, now)
		}
	}
	if len(t.items) == 0 && m.cfg.Plant&PlantFirstBurst == 0 {
		t.lastEmpty = now
	}
	m.seq++
	t.items = append(t.items, refItem{id: id, enq: now, seq: m.seq})
	m.lifoObserve(t, now)
	return true, nil
}

func (m *refModel) follyVerdict(t *refTenant, now, delay Time) bool {
	if now > t.follyEnd && !t.follyReset {
		t.follyReset = true
		t.follyEnd = now + m.cfg.Interval
		t.follyOver = t.follyMin > m.cfg.Target
	}
	if t.follyReset {
		t.follyReset = false
		t.follyMin = delay
		return false
	}
	t.follyMin = min(t.follyMin, delay)
	return t.follyOver && delay > 2*m.cfg.Target
}

func (m *refModel) gauge(t *refTenant, now, delay Time) {
	if now > t.gaugeEnd && !t.gaugeReset {
		t.gaugeReset = true
		t.gaugeEnd = now + m.cfg.Interval
	}
	if t.gaugeReset {
		t.gaugeReset = false
		t.gaugeMin = delay
		return
	}
	t.gaugeMin = min(t.gaugeMin, delay)
}

func (m *refModel) pop(now Time) (int64, bool, error) {
	if err := m.advance(now); err != nil {
		return 0, false, err
	}
	for {
		var t *refTenant
		for _, c := range m.tenants {
			if len(c.items) > 0 && (t == nil || c.items[0].seq < t.items[0].seq) {
				t = c
			}
		}
		if t == nil {
			return 0, false, nil
		}
		var it refItem
		back := t.lifo
		if back {
			it = t.items[len(t.items)-1]
			t.items = t.items[:len(t.items)-1]
		} else {
			it = t.items[0]
			t.items = t.items[1:]
		}
		delay := now - it.enq
		if m.cfg.Rule == RuleFolly && m.follyVerdict(t, now, delay) {
			m.shed(t, it, now, ReasonAtPop)
			if len(t.items) == 0 {
				m.becameEmpty(t, now)
			}
			continue
		}
		m.served++
		m.gauge(t, now, delay)
		m.lastPop = &refPop{t: t, item: it, back: back}
		if len(t.items) == 0 {
			m.becameEmpty(t, now)
		}
		return it.id, true, nil
	}
}

func (m *refModel) unpop() bool {
	p := m.lastPop
	m.lastPop = nil
	if p == nil || m.find(p.t.name) != p.t {
		return false
	}
	if p.back {
		p.t.items = append(p.t.items, p.item)
	} else {
		p.t.items = append([]refItem{p.item}, p.t.items...)
	}
	m.served--
	return true
}

func (m *refModel) take(now Time, id int64, expired bool) (bool, error) {
	if err := m.advance(now); err != nil {
		return false, err
	}
	for _, t := range m.tenants {
		for i, it := range t.items {
			if it.id != id {
				continue
			}
			t.items = slices.Delete(t.items, i, i+1)
			if expired {
				m.shedBy[ReasonDeadline]++
				t.shedAt, t.hasShed = now, true
			} else {
				m.removed++
			}
			if len(t.items) == 0 {
				m.becameEmpty(t, now)
			}
			return true, nil
		}
	}
	return false, nil
}

func (m *refModel) ids() []int64 {
	var out []int64
	for _, t := range m.tenants {
		for _, it := range t.items {
			out = append(out, it.id)
		}
	}
	return out
}

func (m *refModel) takeSheds() []Shed {
	s := m.sheds
	m.sheds = nil
	return s
}

func refLaw(t, interval Time, count int64) Time {
	if count <= 1 {
		return t + interval
	}
	lo, hi := Time(0), interval
	for lo < hi {
		mid := lo + (hi-lo+1)/2
		if refSquareFits(mid, count, interval) {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return t + lo
}
