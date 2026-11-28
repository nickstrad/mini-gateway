package coalesce

import (
	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/work"
)

// Verdict is the action the caller must take for an incoming request after Join.
type Verdict uint8

const (
	// VerdictPass means the request is not shared and proceeds unshared.
	VerdictPass Verdict = iota
	// VerdictLead means the request owns a new flight and must enter the queue.
	VerdictLead
	// VerdictFollow means the request joined an existing flight and waits for a notice.
	VerdictFollow
	// VerdictHold means the request owns a strict-mode next flight waiting behind a running read.
	VerdictHold
	// VerdictRefuse means the flight reached MaxWaiters; caller answers 53M04 immediately.
	VerdictRefuse
	// NumVerdicts is the sentinel count of Verdict values.
	NumVerdicts
)

var verdictNames = [NumVerdicts]string{"pass", "lead", "follow", "hold", "refuse"}

// String returns the canonical verdict name, or "?" if out of range.
func (v Verdict) String() string {
	if v < NumVerdicts {
		return verdictNames[v]
	}
	return "?"
}

// Waits reports whether the request waits in the coalescer rather than entering
// the admission queue immediately.
func (v Verdict) Waits() bool { return v == VerdictFollow || v == VerdictHold }

// Joined is the result of Table.Join.
type Joined struct {
	Verdict Verdict
	Flight  FlightID
}

// NoticeKind classifies an asynchronous notice emitted for another request on
// a flight. Callers must drain Table.TakeNotice after every Table method.
type NoticeKind uint8

const (
	// NoticeReply delivers a completed shared reply to a follower.
	NoticeReply NoticeKind = iota + 1
	// NoticeError delivers a shared failure or error reply to a follower.
	NoticeError
	// NoticeRefused answers a follower with 53M04 after its flight detached at a cap.
	NoticeRefused
	// NoticeExpired answers a follower after its flight hit its deadline.
	NoticeExpired
	// NoticeExpireOwner tells the engine to end an expired flight's owner.
	NoticeExpireOwner
	// NoticeStart promotes a strict-mode held flight into the admission queue.
	NoticeStart
	// NumNoticeKinds is the sentinel count of NoticeKind values.
	NumNoticeKinds
)

var noticeNames = [NumNoticeKinds]string{"?", "reply", "error", "refused", "expired", "expire-owner", "start"}

// String returns the canonical notice name, or "?" if out of range.
func (k NoticeKind) String() string {
	if k < NumNoticeKinds {
		return noticeNames[k]
	}
	return "?"
}

// Notice instructs the engine to act on one waiter or owner of a flight.
type Notice struct {
	Kind   NoticeKind
	Flight FlightID
	Waiter RequestID
}

// Result describes how a flight's database statement ended for Table.Finish.
type Result struct {
	Failed      bool
	ErrorReply  bool
	Unshareable bool
}

// LeaveWhy records whether a waiter departed due to a cancel request or a
// closed/vanished client connection.
type LeaveWhy uint8

const (
	// LeaveCancel means the client sent a CancelRequest (counted as own-error).
	LeaveCancel LeaveWhy = iota + 1
	// LeaveGone means the client disconnected or vanished (counted as gone).
	LeaveGone
)

// LeftKind classifies the effect of Table.Leave on the flight.
type LeftKind uint8

const (
	// LeftNone means the request was not on any live flight.
	LeftNone LeftKind = iota
	// LeftFollower means a non-owner waiter was removed; the flight continues unchanged.
	LeftFollower
	// LeftHandoff means the owner left and ownership passed to NewOwner.
	LeftHandoff
	// LeftLast means the last remaining waiter left and the flight ended; cancel DB work.
	LeftLast
)

// Left is the result of Table.Leave.
type Left struct {
	Kind     LeftKind
	Flight   FlightID
	NewOwner RequestID
}

type flightState uint8

const (
	stateHeld flightState = iota + 1
	stateQueued
	stateRunning
)

type flight struct {
	id        FlightID
	entry     int
	owner     RequestID
	followers []RequestID
	state     flightState
	detached  bool
	deadline  Instant
	bytes     int64
	prev      int
	next      int
	used      bool
}

// entry tracks the live flights for one ShareKey: at most one current flight
// (queued or running) and, in strict mode, one held flight waiting behind it.
type entry struct {
	key  string
	run  int
	hold int
}

const none = -1

// Table is the sans-I/O coalescer flight table. Every method advances virtual
// time first (expiring due flights and no-share entries at or before now) and
// enqueues notices for other affected waiters; callers must drain TakeNotice
// after every call.
type Table struct {
	cfg         Config
	entries     []entry
	freeEntries []int
	byKey       map[string]int
	flights     []flight
	freeFlights []int
	byID        map[FlightID]int
	members     map[RequestID]int
	head        int
	tail        int
	nextID      FlightID
	now         Instant
	noShare     noShareList
	notices     []Notice
	noticeHead  int
	hot         hotKeys
	tenants     tenantStats
	snap        Snapshot
	waiters     *obs.Histogram
}

// New constructs a validated Table with defaulted bounds.
func New(cfg Config) (*Table, error) {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	h, err := obs.NewHistogram(1, MaxWaitersLimit)
	if err != nil {
		return nil, err
	}
	t := &Table{cfg: cfg, byKey: make(map[string]int), byID: make(map[FlightID]int), members: make(map[RequestID]int),
		head: none, tail: none, waiters: h}
	t.entries = make([]entry, 0, min(cfg.MaxFlights, 64))
	t.flights = make([]flight, 0, min(2*cfg.MaxFlights, 64))
	t.noShare = newNoShareList(cfg.NoShareMax)
	t.hot = hotKeys{max: cfg.HotKeys}
	t.tenants = newTenantStats()
	t.snap.Mode = cfg.Mode
	t.snap.Join = cfg.Join
	t.snap.MaxFlights = int64(cfg.MaxFlights)
	t.snap.MaxWaiters = int64(cfg.MaxWaiters)
	t.snap.MaxReplyBytes = cfg.MaxReplyBytes
	t.snap.NoShareMax = int64(cfg.NoShareMax)
	t.snap.NoShareFor = int64(cfg.NoShareFor)
	t.snap.HotKeysMax = int64(cfg.HotKeys)
	return t, nil
}

// Config returns the resolved configuration of t.
func (t *Table) Config() Config { return t.cfg }

// eligible checks only the classifier's precomputed flags and key presence;
// the coalescer never inspects SQL text.
func (t *Table) eligible(u *work.Unit) bool {
	if u == nil || !u.HasKey {
		return false
	}
	switch t.cfg.Mode {
	case ModeAuto:
		if t.cfg.Plant&PlantSafeReadDoor != 0 {
			return u.SafeRead
		}
		return u.Shareable
	case ModeHinted:
		return u.Hinted
	}
	return false
}

const keyLenBytes = 8

// keyOf encodes (Tenant, Text) with an 8-byte big-endian tenant length prefix
// so no two distinct pairs can collide even when either string holds 0x00.
func keyOf(k work.ShareKey) string {
	n := len(k.Tenant)
	b := make([]byte, keyLenBytes, keyLenBytes+n+len(k.Text))
	for i := range keyLenBytes {
		b[keyLenBytes-1-i] = byte(n >> (8 * i))
	}
	b = append(b, k.Tenant...)
	b = append(b, k.Text...)
	return string(b)
}

func splitKey(key string) (string, string) {
	if len(key) < keyLenBytes {
		return "", ""
	}
	n := 0
	for i := range keyLenBytes {
		n = n<<8 | int(key[i])
	}
	rest := key[keyLenBytes:]
	if n < 0 || n > len(rest) {
		return "", rest
	}
	return rest[:n], rest[n:]
}

// Join offers request id with work unit u at instant now. It expires due state
// first, then decides whether id passes unshared, leads a new flight, follows
// an existing flight, starts a strict-mode held flight, or is refused at MaxWaiters.
func (t *Table) Join(now Instant, id RequestID, u *work.Unit) Joined {
	t.advance(now)
	if !t.eligible(u) {
		t.snap.NotEligible++
		return Joined{}
	}
	t.snap.Offered++
	key := keyOf(u.Key)
	t.hot.touch(key)
	if t.noShare.has(key) {
		t.snap.PassedNoShare++
		return Joined{}
	}
	if _, dup := t.members[id]; dup {
		t.snap.PassedDuplicate++
		return Joined{}
	}
	ei, ok := t.byKey[key]
	if !ok {
		if len(t.byKey) >= t.cfg.MaxFlights {
			t.snap.Overflows[CapFlights]++
			t.snap.PassedFull++
			return Joined{}
		}
		ei = t.newEntry(key)
		fi := t.newFlight(ei, id, stateQueued)
		t.entries[ei].run = fi
		return Joined{Verdict: VerdictLead, Flight: t.flights[fi].id}
	}
	target := t.joinTarget(ei)
	if target == none {
		fi := t.newFlight(ei, id, stateHeld)
		t.entries[ei].hold = fi
		return Joined{Verdict: VerdictHold, Flight: t.flights[fi].id}
	}
	f := &t.flights[target]
	if 1+len(f.followers) >= t.cfg.MaxWaiters && t.cfg.Plant&PlantNoWaiterCap == 0 {
		t.snap.Overflows[CapWaiters]++
		t.snap.Refused++
		return Joined{Verdict: VerdictRefuse, Flight: f.id}
	}
	f.followers = append(f.followers, id)
	t.members[id] = target
	t.snap.Hits++
	t.snap.WaitersIn++
	t.snap.Waiting.Add(1)
	t.tenants.hit(u.Tenant)
	return Joined{Verdict: VerdictFollow, Flight: f.id}
}

// joinTarget returns the flight slot an arrival for entry ei may follow: in
// strict mode a running flight is skipped so arrivals follow (or create) e.hold;
// a detached flight is never joined in either mode.
func (t *Table) joinTarget(ei int) int {
	e := t.entries[ei]
	if e.run != none {
		f := t.flights[e.run]
		running := f.state == stateRunning && t.cfg.Plant&PlantStrictJoinsRunning == 0
		if !f.detached && (t.cfg.Join == JoinRelaxed || !running) {
			return e.run
		}
	}
	return e.hold
}

func (t *Table) newEntry(key string) int {
	var ei int
	if n := len(t.freeEntries); n > 0 {
		ei = t.freeEntries[n-1]
		t.freeEntries = t.freeEntries[:n-1]
	} else {
		t.entries = append(t.entries, entry{})
		ei = len(t.entries) - 1
	}
	t.entries[ei] = entry{key: key, run: none, hold: none}
	t.byKey[key] = ei
	t.snap.Entries.Set(int64(len(t.byKey)))
	return ei
}

func (t *Table) newFlight(ei int, owner RequestID, st flightState) int {
	var fi int
	if n := len(t.freeFlights); n > 0 {
		fi = t.freeFlights[n-1]
		t.freeFlights = t.freeFlights[:n-1]
	} else {
		t.flights = append(t.flights, flight{})
		fi = len(t.flights) - 1
	}
	t.nextID++
	followers := t.flights[fi].followers[:0]
	t.flights[fi] = flight{id: t.nextID, entry: ei, owner: owner, followers: followers, state: st,
		deadline: after(t.now, t.cfg.Deadline), prev: t.tail, next: none, used: true}
	// Append to the creation-ordered doubly linked list. Because every flight
	// uses the same Config.Deadline duration, t.head always has the earliest
	// deadline, making Next() and advance() O(1) per expiry.
	if t.tail != none {
		t.flights[t.tail].next = fi
	} else {
		t.head = fi
	}
	t.tail = fi
	t.byID[t.nextID] = fi
	t.members[owner] = fi
	t.snap.Started++
	t.snap.WaitersIn++
	t.snap.Waiting.Add(1)
	t.snap.Flights.Add(1)
	if st == stateHeld {
		t.snap.Held++
	}
	return fi
}

func (t *Table) flightIndex(id FlightID) (int, bool) {
	fi, ok := t.byID[id]
	return fi, ok
}

// Started marks flight id as running on the database once its query is written.
// In strict mode, later arrivals for the same key will no longer join id.
func (t *Table) Started(now Instant, id FlightID) {
	t.advance(now)
	fi, ok := t.flightIndex(id)
	if !ok || t.flights[fi].state != stateQueued {
		return
	}
	t.flights[fi].state = stateRunning
	t.snap.Running.Add(1)
	t.snap.Sent++
}

// Stored accounts for n additional reply bytes buffered for flight id. It
// returns false if the flight is gone, already detached, or just exceeded
// MaxReplyBytes (in which case followers receive NoticeRefused and the key is
// placed on the no-share list).
func (t *Table) Stored(now Instant, id FlightID, n int) bool {
	t.advance(now)
	fi, ok := t.flightIndex(id)
	if !ok || t.flights[fi].detached {
		return false
	}
	f := &t.flights[fi]
	f.bytes += int64(n)
	t.snap.StoredBytes.Add(int64(n))
	if f.bytes > t.cfg.MaxReplyBytes && t.cfg.Plant&PlantNoReplyCap == 0 {
		t.detach(fi, CapReply)
		return false
	}
	return true
}

func (t *Table) detach(fi int, c Cap) {
	f := &t.flights[fi]
	f.detached = true
	t.snap.Overflows[c]++
	t.snap.Detached++
	for _, w := range f.followers {
		t.notify(NoticeRefused, f.id, w)
		delete(t.members, w)
		t.snap.WaiterRefused++
		t.snap.Waiting.Add(-1)
	}
	f.followers = f.followers[:0]
	t.snap.StoredBytes.Add(-f.bytes)
	f.bytes = 0
	t.addNoShare(t.entries[f.entry].key)
}

func (t *Table) addNoShare(key string) {
	evicted, renewed := t.noShare.add(key, after(t.now, t.cfg.NoShareFor))
	if evicted {
		t.snap.NoShareEvicted++
	}
	if renewed {
		t.snap.NoShareRenewed++
	}
	t.snap.NoShareAdded++
	t.snap.NoShare.Set(int64(t.noShare.n))
}

// Finish completes or fails flight id, enqueues reply or error notices for all
// remaining followers, removes the flight, and promotes any strict-mode held
// flight behind it via NoticeStart.
func (t *Table) Finish(now Instant, id FlightID, r Result) {
	t.advance(now)
	fi, ok := t.flightIndex(id)
	if !ok {
		return
	}
	f := &t.flights[fi]
	if !f.detached && r.Unshareable && !r.Failed {
		t.detach(fi, CapUnshareable)
	}
	kind := NoticeReply
	switch {
	case r.Failed:
		kind = NoticeError
		t.snap.Failed++
		t.snap.SharedErrors++
	case r.ErrorReply:
		t.snap.Completed++
		t.snap.SharedErrors++
	default:
		t.snap.Completed++
	}
	t.waiters.Record(int64(1 + len(f.followers)))
	t.deliverAll(fi, kind)
	t.endFlight(fi)
}

func (t *Table) deliverAll(fi int, kind NoticeKind) {
	f := &t.flights[fi]
	for _, w := range f.followers {
		t.notify(kind, f.id, w)
		delete(t.members, w)
		t.outcome(kind)
	}
	f.followers = f.followers[:0]
	delete(t.members, f.owner)
	t.outcome(kind)
}

func (t *Table) outcome(kind NoticeKind) {
	t.snap.Waiting.Add(-1)
	if kind == NoticeReply {
		t.snap.WaiterReply++
		return
	}
	t.snap.WaiterSharedError++
}

func (t *Table) endFlight(fi int) {
	f := &t.flights[fi]
	ei := f.entry
	e := &t.entries[ei]
	if f.state == stateRunning {
		t.snap.Running.Add(-1)
	}
	t.snap.StoredBytes.Add(-f.bytes)
	t.snap.Flights.Add(-1)
	delete(t.byID, f.id)
	if f.prev != none {
		t.flights[f.prev].next = f.next
	} else {
		t.head = f.next
	}
	if f.next != none {
		t.flights[f.next].prev = f.prev
	} else {
		t.tail = f.prev
	}
	followers := f.followers[:0]
	*f = flight{followers: followers}
	t.freeFlights = append(t.freeFlights, fi)
	switch {
	case e.run == fi:
		e.run = none
		if e.hold != none {
			e.run, e.hold = e.hold, none
			h := &t.flights[e.run]
			h.state = stateQueued
			t.snap.Promoted++
			t.notify(NoticeStart, h.id, h.owner)
		}
	case e.hold == fi:
		e.hold = none
	}
	if e.run == none && e.hold == none {
		delete(t.byKey, e.key)
		*e = entry{run: none, hold: none}
		t.freeEntries = append(t.freeEntries, ei)
		t.snap.Entries.Set(int64(len(t.byKey)))
	}
}

// Leave removes request id from its flight. A non-owner departs quietly; an
// owner with followers hands ownership to the newest follower in O(1); the last
// remaining member ends the flight (LeftLast) so the engine cancels DB work.
func (t *Table) Leave(now Instant, id RequestID, why LeaveWhy) Left {
	t.advance(now)
	fi, ok := t.members[id]
	if !ok {
		t.snap.LeftUnknown++
		return Left{}
	}
	f := &t.flights[fi]
	delete(t.members, id)
	t.snap.Waiting.Add(-1)
	if why == LeaveCancel {
		t.snap.WaiterOwnError++
	} else {
		t.snap.WaiterGone++
	}
	if id != f.owner {
		if t.cfg.Plant&PlantLeaverGetsResult == 0 {
			f.followers = removeID(f.followers, id)
		}
		t.snap.FollowersLeft++
		return Left{Kind: LeftFollower, Flight: f.id}
	}
	if n := len(f.followers); n > 0 && t.cfg.Plant&PlantAnyLeaveCancels == 0 {
		f.owner = f.followers[n-1]
		f.followers = f.followers[:n-1]
		t.snap.Handoffs++
		return Left{Kind: LeftHandoff, Flight: f.id, NewOwner: f.owner}
	}
	id2 := f.id
	t.snap.Cancelled++
	for _, w := range f.followers {
		t.notify(NoticeError, f.id, w)
		delete(t.members, w)
		t.snap.Waiting.Add(-1)
		t.snap.WaiterSharedError++
	}
	f.followers = f.followers[:0]
	t.endFlight(fi)
	return Left{Kind: LeftLast, Flight: id2}
}

func removeID(ids []RequestID, id RequestID) []RequestID {
	for i, v := range ids {
		if v == id {
			copy(ids[i:], ids[i+1:])
			return ids[:len(ids)-1]
		}
	}
	return ids
}

// Sweep advances virtual time to now and expires any due flights and no-share entries.
func (t *Table) Sweep(now Instant) { t.advance(now) }

// advance enforces time-passes-before-event: flights and no-share entries due
// at or before now expire before the triggering operation runs.
func (t *Table) advance(now Instant) {
	if now > t.now {
		t.now = now
	}
	for t.head != none && t.flights[t.head].deadline <= t.now {
		t.expire(t.head)
	}
	for {
		key, ok := t.noShare.expire(t.now)
		if !ok {
			break
		}
		_ = key
		t.snap.NoShareExpired++
	}
	t.snap.NoShare.Set(int64(t.noShare.n))
}

func (t *Table) expire(fi int) {
	f := &t.flights[fi]
	t.snap.Expired++
	t.snap.SharedErrors++
	t.notify(NoticeExpireOwner, f.id, f.owner)
	delete(t.members, f.owner)
	t.snap.Waiting.Add(-1)
	t.snap.WaiterSharedError++
	for _, w := range f.followers {
		t.notify(NoticeExpired, f.id, w)
		delete(t.members, w)
		t.snap.Waiting.Add(-1)
		t.snap.WaiterSharedError++
	}
	f.followers = f.followers[:0]
	t.endFlight(fi)
}

func (t *Table) notify(k NoticeKind, f FlightID, w RequestID) {
	t.notices = append(t.notices, Notice{Kind: k, Flight: f, Waiter: w})
}

// TakeNotice pops the next pending Notice in FIFO order, or returns false when
// all notices have been drained.
func (t *Table) TakeNotice() (Notice, bool) {
	if t.noticeHead >= len(t.notices) {
		t.notices = t.notices[:0]
		t.noticeHead = 0
		return Notice{}, false
	}
	n := t.notices[t.noticeHead]
	t.noticeHead++
	return n, true
}

// Next returns the earliest upcoming flight deadline or no-share expiry, or 0
// if no timer needs to be armed.
func (t *Table) Next() Instant {
	next := Instant(0)
	if t.head != none {
		next = t.flights[t.head].deadline
	}
	if at, ok := t.noShare.next(); ok && (next == 0 || at < next) {
		next = at
	}
	return next
}

// Shutdown expires every remaining flight and clears the no-share list so
// Quiesce succeeds after draining notices.
func (t *Table) Shutdown(now Instant) {
	t.advance(now)
	for t.head != none {
		t.expire(t.head)
	}
	for t.noShare.n > 0 {
		t.noShare.drop()
		t.snap.NoShareExpired++
	}
	t.snap.NoShare.Set(0)
}

// Owner returns the current owner request ID of flight id, or false if id is gone.
func (t *Table) Owner(id FlightID) (RequestID, bool) {
	fi, ok := t.flightIndex(id)
	if !ok {
		return 0, false
	}
	return t.flights[fi].owner, true
}

// Followers returns the number of non-owner waiters on flight id, or 0 if gone.
func (t *Table) Followers(id FlightID) int {
	fi, ok := t.flightIndex(id)
	if !ok {
		return 0
	}
	return len(t.flights[fi].followers)
}

// Member returns the flight ID that request id belongs to, or false if none.
func (t *Table) Member(id RequestID) (FlightID, bool) {
	fi, ok := t.members[id]
	if !ok {
		return 0, false
	}
	return t.flights[fi].id, true
}

// Running returns how many non-detached flights owned by t for key are
// currently running on the database (at most 1).
func (t *Table) Running(key work.ShareKey) int {
	ei, ok := t.byKey[keyOf(key)]
	if !ok {
		return 0
	}
	n := 0
	for _, fi := range [2]int{t.entries[ei].run, t.entries[ei].hold} {
		if fi != none && t.flights[fi].state == stateRunning && !t.flights[fi].detached {
			n++
		}
	}
	return n
}
