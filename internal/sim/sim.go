package sim

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"time"
)

const (
	DefaultMaxSteps       int64 = 50_000_000
	DefaultMaxSameInstant int64 = 1_000_000
	DefaultMaxEvents            = 1 << 22
	MaxRegistrations            = 256
)

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

func DefaultReplay(scenario string, seed int64) string {
	if scenario == "" {
		return "REPLAY: seed " + strconv.FormatInt(seed, 10) + " (no scenario name was given to the simulator)"
	}
	return "REPLAY: go run ./cmd/mgsim run " + scenario + " -seed " + strconv.FormatInt(seed, 10)
}

func (s *Sim) Now() Time                      { return s.now }
func (s *Sim) Seed() int64                    { return s.seed }
func (s *Sim) Steps() int64                   { return s.steps }
func (s *Sim) Err() error                     { return s.err }
func (s *Sim) Replay() string                 { return s.replay }
func (s *Sim) SetReplay(line string)          { s.replay = line }
func (s *Sim) Stream(label string) *rand.Rand { return NewStream(s.seed, label) }

func (s *Sim) Fail(err error) {
	if s.err == nil && err != nil {
		s.err = err
	}
}

type Timer struct {
	s  *Sim
	ev *event
}

func (t Timer) Pending() bool { return t.ev != nil && t.ev.hi >= 0 }

func (t Timer) When() Time {
	if t.ev == nil {
		return 0
	}
	return t.ev.at
}

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

func (s *Sim) At(t Time, fn func()) Timer { return s.schedule(&event{at: t, fn: fn}) }

func (s *Sim) After(d time.Duration, fn func()) Timer { return s.At(Later(s.now, d), fn) }

func (s *Sim) AtHandler(t Time, h Handler, code uint64) Timer {
	return s.schedule(&event{at: t, h: h, code: code})
}

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
		s.sameInstant = 0
	}
	return s.err
}

func (s *Sim) RunFor(d time.Duration) error { return s.RunUntil(Later(s.now, d)) }

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

func (s *Sim) RegisterInvariant(name string, fn func() error) error {
	return s.RegisterSampledInvariant(name, 1, fn)
}

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

func (s *Sim) CheckInvariants() error {
	if s.err != nil {
		return s.err
	}
	s.checkInvariants(true)
	return s.err
}

func (s *Sim) Verify() error { return s.CheckInvariants() }

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
