package coalesce

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nickstrad/mini-gateway/internal/work"
)

type mFlight struct {
	id        FlightID
	key       string
	owner     RequestID
	followers []RequestID
	state     flightState
	detached  bool
	deadline  Instant
	bytes     int64
}

type mNoShare struct {
	key   string
	until Instant
}

type model struct {
	cfg      Config
	now      Instant
	flights  []*mFlight
	noShare  []mNoShare
	nextID   FlightID
	notices  []Notice
	left     map[RequestID]bool
	joinedAt map[RequestID]flightState
}

func newModel(cfg Config) *model {
	return &model{cfg: cfg.WithDefaults(), left: map[RequestID]bool{}, joinedAt: map[RequestID]flightState{}}
}

func (m *model) eligible(u *work.Unit) bool {
	if !u.HasKey {
		return false
	}
	switch m.cfg.Mode {
	case ModeAuto:
		return u.Shareable
	case ModeHinted:
		return u.Hinted
	}
	return false
}

func (m *model) advance(now Instant) {
	if now > m.now {
		m.now = now
	}
	for len(m.flights) > 0 && m.flights[0].deadline <= m.now {
		f := m.flights[0]
		m.note(NoticeExpireOwner, f.id, f.owner)
		for _, w := range f.followers {
			m.note(NoticeExpired, f.id, w)
		}
		m.remove(f)
	}
	for len(m.noShare) > 0 && m.noShare[0].until <= m.now {
		m.noShare = m.noShare[1:]
	}
}

func (m *model) note(k NoticeKind, f FlightID, w RequestID) {
	m.notices = append(m.notices, Notice{Kind: k, Flight: f, Waiter: w})
}

func (m *model) byKey(key string) (run, hold *mFlight) {
	for _, f := range m.flights {
		if f.key != key {
			continue
		}
		if f.state == stateHeld {
			hold = f
		} else {
			run = f
		}
	}
	return run, hold
}

func (m *model) keys() int {
	var ks []string
	for _, f := range m.flights {
		if !slices.Contains(ks, f.key) {
			ks = append(ks, f.key)
		}
	}
	return len(ks)
}

func (m *model) listed(key string) bool {
	for _, n := range m.noShare {
		if n.key == key {
			return true
		}
	}
	return false
}

func (m *model) flight(id FlightID) *mFlight {
	for _, f := range m.flights {
		if f.id == id {
			return f
		}
	}
	return nil
}

func (m *model) member(id RequestID) *mFlight {
	for _, f := range m.flights {
		if f.owner == id || slices.Contains(f.followers, id) {
			return f
		}
	}
	return nil
}

func (m *model) start(key string, owner RequestID, st flightState) *mFlight {
	m.nextID++
	f := &mFlight{id: m.nextID, key: key, owner: owner, state: st, deadline: m.now + m.cfg.Deadline}
	m.flights = append(m.flights, f)
	return f
}

func (m *model) join(now Instant, id RequestID, u *work.Unit) Joined {
	m.advance(now)
	if !m.eligible(u) {
		return Joined{}
	}
	key := keyOf(u.Key)
	if m.listed(key) || m.member(id) != nil {
		return Joined{}
	}
	run, hold := m.byKey(key)
	if run == nil && hold == nil {
		if m.keys() >= m.cfg.MaxFlights {
			return Joined{}
		}
		return Joined{Verdict: VerdictLead, Flight: m.start(key, id, stateQueued).id}
	}
	target := hold
	if run != nil && !run.detached && (m.cfg.Join == JoinRelaxed || run.state != stateRunning) {
		target = run
	}
	if target == nil {
		return Joined{Verdict: VerdictHold, Flight: m.start(key, id, stateHeld).id}
	}
	if 1+len(target.followers) >= m.cfg.MaxWaiters {
		return Joined{Verdict: VerdictRefuse, Flight: target.id}
	}
	target.followers = append(target.followers, id)
	m.joinedAt[id] = target.state
	return Joined{Verdict: VerdictFollow, Flight: target.id}
}

func (m *model) started(now Instant, id FlightID) {
	m.advance(now)
	if f := m.flight(id); f != nil && f.state == stateQueued {
		f.state = stateRunning
	}
}

func (m *model) detach(f *mFlight) {
	f.detached = true
	for _, w := range f.followers {
		m.note(NoticeRefused, f.id, w)
	}
	f.followers = nil
	f.bytes = 0
	m.listKey(f.key)
}

func (m *model) listKey(key string) {
	for i, n := range m.noShare {
		if n.key == key {
			m.noShare = slices.Delete(m.noShare, i, i+1)
			break
		}
	}
	if len(m.noShare) == m.cfg.NoShareMax {
		m.noShare = m.noShare[1:]
	}
	m.noShare = append(m.noShare, mNoShare{key: key, until: m.now + m.cfg.NoShareFor})
}

func (m *model) stored(now Instant, id FlightID, n int) bool {
	m.advance(now)
	f := m.flight(id)
	if f == nil || f.detached {
		return false
	}
	f.bytes += int64(n)
	if f.bytes > m.cfg.MaxReplyBytes {
		m.detach(f)
		return false
	}
	return true
}

func (m *model) finish(now Instant, id FlightID, r Result) {
	m.advance(now)
	f := m.flight(id)
	if f == nil {
		return
	}
	if !f.detached && r.Unshareable && !r.Failed {
		m.detach(f)
	}
	kind := NoticeReply
	if r.Failed {
		kind = NoticeError
	}
	for _, w := range f.followers {
		m.note(kind, f.id, w)
	}
	m.remove(f)
}

func (m *model) remove(f *mFlight) {
	i := slices.Index(m.flights, f)
	m.flights = slices.Delete(m.flights, i, i+1)
	if f.state == stateHeld {
		return
	}
	if _, hold := m.byKey(f.key); hold != nil {
		hold.state = stateQueued
		m.note(NoticeStart, hold.id, hold.owner)
	}
}

func (m *model) leave(now Instant, id RequestID) Left {
	m.advance(now)
	f := m.member(id)
	if f == nil {
		return Left{}
	}
	m.left[id] = true
	if id != f.owner {
		f.followers = slices.DeleteFunc(f.followers, func(w RequestID) bool { return w == id })
		return Left{Kind: LeftFollower, Flight: f.id}
	}
	if n := len(f.followers); n > 0 {
		f.owner = f.followers[n-1]
		f.followers = f.followers[:n-1]
		return Left{Kind: LeftHandoff, Flight: f.id, NewOwner: f.owner}
	}
	m.remove(f)
	return Left{Kind: LeftLast, Flight: f.id}
}

func (m *model) next() Instant {
	var at Instant
	if len(m.flights) > 0 {
		at = m.flights[0].deadline
	}
	if len(m.noShare) > 0 && (at == 0 || m.noShare[0].until < at) {
		at = m.noShare[0].until
	}
	return at
}

func (m *model) takeAll() []Notice {
	n := m.notices
	m.notices = nil
	return n
}

func takeAll(t *Table) []Notice {
	var out []Notice
	for {
		n, ok := t.TakeNotice()
		if !ok {
			return out
		}
		out = append(out, n)
	}
}

func noticesString(ns []Notice) string {
	var b strings.Builder
	for _, n := range ns {
		fmt.Fprintf(&b, "%s(f%d,w%d) ", n.Kind, n.Flight, n.Waiter)
	}
	return b.String()
}
