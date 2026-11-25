// Package sim implements the deterministic single-threaded simulation kernel:
// virtual nanosecond time with saturating arithmetic, isolated named random
// streams, an opaque-event min-heap loop with cancellable timers, invariant
// and quiesce checks, a split-digest decision trace, and fault scheduling with
// 1-minimal shrinking.
//
// The loop knows no domain event kinds, network connections, or database state;
// world packages build on top of it by scheduling closures or Handlers and
// registering state writers, invariants, quiesce checks, and fault kinds.
package sim

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"time"
)

// Default loop safety bounds and registry caps.
const (
	// DefaultMaxSteps is the step budget when Config.MaxSteps is not set.
	DefaultMaxSteps int64 = 50_000_000
	// DefaultMaxSameInstant caps consecutive events at one virtual instant.
	DefaultMaxSameInstant int64 = 1_000_000
	// DefaultMaxEvents caps the number of events queued simultaneously.
	DefaultMaxEvents = 1 << 22
	// MaxRegistrations bounds invariant, quiesce, and state writer lists.
	MaxRegistrations = 256
)

// Config configures a new Sim instance. Zero-valued numeric bounds fall back
// to their package defaults.
type Config struct {
	Seed           int64
	Scenario       string
	Replay         string
	MaxSteps       int64
	MaxSameInstant int64
	MaxEvents      int
	TraceOff       bool
	TraceRing      int
}

type invariant struct {
	name  string
	every int64
	fn    func() error
}

type named struct {
	name string
	fn   func() error
}

type stateWriter struct {
	name string
	fn   func(w StateSink)
}

// Sim is a single-threaded discrete-event simulator. All methods must be
// called from one goroutine; nothing in Sim reads wall-clock time or uses
// global randomness.
type Sim struct {
	seed           int64
	replay         string
	maxSteps       int64
	maxSameInstant int64
	maxEvents      int

	now         Time
	seq         uint64
	steps       int64
	sameInstant int64
	q           eventQueue
	background  int64
	err         error

	scheduled       int64
	fired           int64
	cancelled       int64
	heapPeak        int64
	invariantChecks int64

	invariants []invariant
	quiesce    []named
	states     []stateWriter
	trace      trace
	faults     faultRegistry
}

// New constructs a simulator starting at virtual time 0 with the given Config.
func New(cfg Config) *Sim {
	s := &Sim{
		seed:           cfg.Seed,
		maxSteps:       cfg.MaxSteps,
		maxSameInstant: cfg.MaxSameInstant,
		maxEvents:      cfg.MaxEvents,
	}
	if s.maxSteps <= 0 {
		s.maxSteps = DefaultMaxSteps
	}
	if s.maxSameInstant <= 0 {
		s.maxSameInstant = DefaultMaxSameInstant
	}
	if s.maxEvents <= 0 {
		s.maxEvents = DefaultMaxEvents
	}
	s.replay = cfg.Replay
	if s.replay == "" {
		s.replay = DefaultReplay(cfg.Scenario, cfg.Seed)
	}
	s.trace.init(!cfg.TraceOff, cfg.TraceRing)
	return s
}

// DefaultReplay builds the REPLAY line embedded in kernel error messages when
// Config.Replay is empty.
func DefaultReplay(scenario string, seed int64) string {
	if scenario == "" {
		return "REPLAY: seed " + strconv.FormatInt(seed, 10) + " (no scenario name was given to the simulator)"
	}
	return "REPLAY: go run ./cmd/mgsim run " + scenario + " -seed " + strconv.FormatInt(seed, 10)
}

// Now returns the current virtual time.
func (s *Sim) Now() Time { return s.now }

// Seed returns the root seed configured for this run.
func (s *Sim) Seed() int64 { return s.seed }

// Steps returns the number of events popped and dispatched so far.
func (s *Sim) Steps() int64 { return s.steps }

// Err returns the first sticky failure recorded by the loop, or nil.
func (s *Sim) Err() error { return s.err }

// Replay returns the REPLAY command line attached to failure reports.
func (s *Sim) Replay() string { return s.replay }

// SetReplay replaces the REPLAY line used in subsequent failure reports.
func (s *Sim) SetReplay(line string) { s.replay = line }

// Stream returns a fresh deterministic random stream for (s.Seed(), label).
// Callers should retain the returned *rand.Rand; repeated calls with the same
// label yield identical sequences from the start.
func (s *Sim) Stream(label string) *rand.Rand { return NewStream(s.seed, label) }

// Fail records err as the run's failure if no error is already set. Once
// failed, the loop refuses further steps so the first root cause is preserved.
func (s *Sim) Fail(err error) {
	if s.err == nil && err != nil {
		s.err = err
	}
}

// Timer is a value handle to a scheduled event. The zero value is inactive and
// safe to inspect or cancel.
type Timer struct {
	s  *Sim
	ev *event
}

// Pending reports whether the timer is still queued in the event heap.
func (t Timer) Pending() bool { return t.ev != nil && t.ev.hi >= 0 }

// When returns the virtual time at which the timer is scheduled to fire, or 0
// for the zero Timer.
func (t Timer) When() Time {
	if t.ev == nil {
		return 0
	}
	return t.ev.at
}

// Cancel removes the timer from the event heap and reports whether it was
// still pending. A cancelled timer never fires.
func (t Timer) Cancel() bool {
	if t.ev == nil || !t.s.q.remove(t.ev) {
		return false
	}
	t.s.cancelled++
	if t.ev.background {
		t.s.background--
	}
	return true
}

// MarkBackground marks a pending timer as background work so RunUntilQuiet
// stops when only background timers remain and CheckQuiesce does not report it
// as a leak. Calling it more than once on the same timer is a no-op.
func (t Timer) MarkBackground() {
	if !t.Pending() || t.ev.background {
		return
	}
	t.ev.background = true
	t.s.background++
}

func (s *Sim) schedule(e *event) Timer {
	if len(s.q) >= s.maxEvents {
		s.Fail(&StallError{Cause: ErrHeapFull, Seed: s.seed, Steps: s.steps, Now: s.now, Replay: s.replay})
		return Timer{}
	}
	// Never let an event move virtual time backwards; past deadlines run at now.
	if e.at < s.now {
		e.at = s.now
	}
	s.seq++
	e.seq = s.seq
	s.q.push(e)
	s.scheduled++
	if n := int64(len(s.q)); n > s.heapPeak {
		s.heapPeak = n
	}
	return Timer{s: s, ev: e}
}

// At schedules fn to run at virtual time t (or Now if t is in the past).
func (s *Sim) At(t Time, fn func()) Timer { return s.schedule(&event{at: t, fn: fn}) }

// After schedules fn to run after duration d from Now (saturating at MaxTime).
func (s *Sim) After(d time.Duration, fn func()) Timer { return s.At(Later(s.now, d), fn) }

// AtHandler schedules h.OnEvent(code) to run at virtual time t.
func (s *Sim) AtHandler(t Time, h Handler, code uint64) Timer {
	return s.schedule(&event{at: t, h: h, code: code})
}

// AfterHandler schedules h.OnEvent(code) to run after duration d from Now.
func (s *Sim) AfterHandler(d time.Duration, h Handler, code uint64) Timer {
	return s.AtHandler(Later(s.now, d), h, code)
}

func (s *Sim) guard(e *event) {
	defer func() {
		if r := recover(); r != nil {
			s.Fail(&PanicError{Value: fmt.Sprint(r), Seed: s.seed, Steps: s.steps, Now: s.now, Replay: s.replay})
		}
	}()
	if e.h != nil {
		e.h.OnEvent(e.code)
		return
	}
	if e.fn != nil {
		e.fn()
	}
}

func (s *Sim) stall(cause error) error {
	s.Fail(&StallError{Cause: cause, Seed: s.seed, Steps: s.steps, Now: s.now, Replay: s.replay})
	return s.err
}

// Step pops and executes the next event in (time, sequence) order, runs any
// due invariants, and reports whether an event ran and the loop's sticky error.
func (s *Sim) Step() (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	e := s.q.peek()
	if e == nil {
		return false, nil
	}
	if s.steps >= s.maxSteps {
		return false, s.stall(ErrBudgetExhausted)
	}
	// Count the first step at any new instant as 1 so MaxSameInstant enforces
	// the exact same bound at t == 0 and at t > 0.
	if e.at > s.now {
		s.now = e.at
		s.sameInstant = 1
	} else {
		s.sameInstant++
		if s.sameInstant > s.maxSameInstant {
			return false, s.stall(ErrTimeFrozen)
		}
	}
	s.q.pop()
	if e.background {
		s.background--
	}
	s.steps++
	s.fired++
	s.guard(e)
	if s.err == nil {
		s.checkInvariants(false)
	}
	return true, s.err
}

// RunUntil executes every queued event at or before t and then advances Now to
// t if the loop succeeded.
func (s *Sim) RunUntil(t Time) error {
	for s.err == nil {
		e := s.q.peek()
		if e == nil || e.at > t {
			break
		}
		if _, err := s.Step(); err != nil {
			return err
		}
	}
	if s.err == nil && s.now < t {
		s.now = t
		// Moving the clock past the last fired event starts a fresh instant.
		s.sameInstant = 0
	}
	return s.err
}

// RunFor executes events for duration d from Now, saturating the target time.
func (s *Sim) RunFor(d time.Duration) error { return s.RunUntil(Later(s.now, d)) }

// RunUntilQuiet steps the loop until no non-background events remain or an
// error occurs.
func (s *Sim) RunUntilQuiet() error {
	for s.err == nil && int64(len(s.q)) > s.background {
		if _, err := s.Step(); err != nil {
			return err
		}
	}
	return s.err
}

func (s *Sim) register(n int) error {
	if n >= MaxRegistrations {
		err := fmt.Errorf("%w: more than %d entries", ErrRegistryFull, MaxRegistrations)
		s.Fail(err)
		return err
	}
	return nil
}

// RegisterInvariant registers a check that runs after every Step.
func (s *Sim) RegisterInvariant(name string, fn func() error) error {
	return s.RegisterSampledInvariant(name, 1, fn)
}

// RegisterSampledInvariant registers an invariant checked every N steps (and
// unconditionally on CheckInvariants). Values of every < 1 are treated as 1.
func (s *Sim) RegisterSampledInvariant(name string, every int64, fn func() error) error {
	if err := s.register(len(s.invariants)); err != nil {
		return err
	}
	if every < 1 {
		every = 1
	}
	s.invariants = append(s.invariants, invariant{name: name, every: every, fn: fn})
	return nil
}

// RegisterQuiesce registers an end-of-scenario leak check invoked by
// CheckQuiesce.
func (s *Sim) RegisterQuiesce(name string, fn func() error) error {
	if err := s.register(len(s.quiesce)); err != nil {
		return err
	}
	s.quiesce = append(s.quiesce, named{name: name, fn: fn})
	return nil
}

func (s *Sim) callCheck(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &PanicError{Value: fmt.Sprint(r), Seed: s.seed, Steps: s.steps, Now: s.now, Replay: s.replay}
		}
	}()
	return fn()
}

func (s *Sim) checkInvariants(all bool) {
	for _, inv := range s.invariants {
		if !all && s.steps%inv.every != 0 {
			continue
		}
		s.invariantChecks++
		if err := s.callCheck(inv.fn); err != nil {
			var pe *PanicError
			if errors.As(err, &pe) {
				s.Fail(pe)
				return
			}
			s.Fail(&InvariantError{Name: inv.name, Err: err, Seed: s.seed, Steps: s.steps, Now: s.now, Replay: s.replay})
			return
		}
	}
}

// CheckInvariants runs every registered invariant, including sampled ones, and
// returns the loop's sticky error if any check fails.
func (s *Sim) CheckInvariants() error {
	if s.err != nil {
		return s.err
	}
	s.checkInvariants(true)
	return s.err
}

// Verify checks all registered invariants. It aliases CheckInvariants to keep
// the method name expected by simtest.CheckDeterministic.
func (s *Sim) Verify() error { return s.CheckInvariants() }

// CheckQuiesce verifies that no non-background timers remain in the heap and
// that every registered quiesce callback returns nil. All failures are joined.
func (s *Sim) CheckQuiesce() error {
	var errs []error
	if n := int64(len(s.q)) - s.background; n > 0 {
		errs = append(errs, &QuiesceError{Name: "sim.timers", Err: fmt.Errorf("%d pending timers that are not background", n)})
	}
	for _, q := range s.quiesce {
		if err := s.callCheck(q.fn); err != nil {
			errs = append(errs, &QuiesceError{Name: q.name, Err: err})
		}
	}
	return errors.Join(errs...)
}

// Snapshot is a plain, deterministic copy of the kernel's counters and gauges.
// Scheduled always equals Fired + Cancelled + HeapLen.
type Snapshot struct {
	Steps            int64
	Scheduled        int64
	Fired            int64
	Cancelled        int64
	HeapLen          int64
	HeapPeak         int64
	PendingTimers    int64
	BackgroundTimers int64
	TraceRecords     int64
	TraceRingLen     int64
	FaultsApplied    int64
	InvariantChecks  int64
}

// Snapshot returns the kernel's current counters and steady-state gauges.
func (s *Sim) Snapshot() Snapshot {
	return Snapshot{
		Steps:            s.steps,
		Scheduled:        s.scheduled,
		Fired:            s.fired,
		Cancelled:        s.cancelled,
		HeapLen:          int64(len(s.q)),
		HeapPeak:         s.heapPeak,
		PendingTimers:    int64(len(s.q)) - s.background,
		BackgroundTimers: s.background,
		TraceRecords:     s.trace.count,
		TraceRingLen:     int64(s.trace.ringLen()),
		FaultsApplied:    s.faults.applied,
		InvariantChecks:  s.invariantChecks,
	}
}
