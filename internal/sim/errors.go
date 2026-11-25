package sim

import (
	"errors"
	"fmt"
)

// Sentinel errors reported by the simulator kernel on resource bounds,
// livelocks, or invalid registrations.
var (
	// ErrBudgetExhausted indicates a run reached Config.MaxSteps events.
	ErrBudgetExhausted = errors.New("sim: step budget exhausted")
	// ErrTimeFrozen indicates more than Config.MaxSameInstant events ran
	// consecutively without advancing virtual time.
	ErrTimeFrozen = errors.New("sim: time not advancing")
	// ErrHeapFull indicates scheduling an event would exceed Config.MaxEvents.
	ErrHeapFull = errors.New("sim: event heap full")
	// ErrUnknownFaultKind indicates a scheduled fault referenced an
	// unregistered FaultKind.
	ErrUnknownFaultKind = errors.New("sim: unknown fault kind")
	// ErrRegistryFull indicates an invariant, quiesce, state, or fault
	// registry exceeded its fixed cap.
	ErrRegistryFull = errors.New("sim: registry full")
	// ErrDuplicateName indicates a state writer or fault kind was registered
	// twice with the same identifier.
	ErrDuplicateName = errors.New("sim: duplicate registration")
)

// StallError reports that the event loop stopped because a safety bound
// (ErrBudgetExhausted, ErrTimeFrozen, or ErrHeapFull) tripped.
type StallError struct {
	Cause  error
	Seed   int64
	Steps  int64
	Now    Time
	Replay string
}

// Error formats the stall cause, simulation coordinates, and replay command.
func (e *StallError) Error() string {
	return fmt.Sprintf("%v: seed %d, step %d, time %d\n%s", e.Cause, e.Seed, e.Steps, int64(e.Now), e.Replay)
}

// Unwrap returns the underlying sentinel cause for errors.Is.
func (e *StallError) Unwrap() error { return e.Cause }

// PanicError converts a panic inside an event, invariant, or quiesce callback
// into a typed failure carrying the seed and replay line. Stack traces are
// omitted because runtime/debug is outside the determinism lint's import list.
type PanicError struct {
	Value  string
	Seed   int64
	Steps  int64
	Now    Time
	Replay string
}

// Error formats the panic value, simulation coordinates, and replay command.
func (e *PanicError) Error() string {
	return fmt.Sprintf("sim: actor panic: %s: seed %d, step %d, time %d\n%s", e.Value, e.Seed, e.Steps, int64(e.Now), e.Replay)
}

// InvariantError reports that a registered invariant returned a non-nil error.
type InvariantError struct {
	Name   string
	Err    error
	Seed   int64
	Steps  int64
	Now    Time
	Replay string
}

// Error formats the failing invariant name, cause, coordinates, and replay line.
func (e *InvariantError) Error() string {
	return fmt.Sprintf("sim: invariant %s: %v: seed %d, step %d, time %d\n%s", e.Name, e.Err, e.Seed, e.Steps, int64(e.Now), e.Replay)
}

// Unwrap returns the error produced by the invariant callback.
func (e *InvariantError) Unwrap() error { return e.Err }

// QuiesceError reports a resource leak detected by Sim.CheckQuiesce.
type QuiesceError struct {
	Name string
	Err  error
}

// Error formats the leaking subsystem name and its diagnostic message.
func (e *QuiesceError) Error() string { return fmt.Sprintf("sim: quiesce %s: %v", e.Name, e.Err) }

// Unwrap returns the underlying check error.
func (e *QuiesceError) Unwrap() error { return e.Err }

// FaultError reports that applying a Fault failed or named an unknown kind.
type FaultError struct {
	Fault Fault
	Err   error
}

// Error formats the fault coordinates and the failure returned by its handler.
func (e *FaultError) Error() string {
	return fmt.Sprintf("sim: fault kind %d target %d at %d: %v", e.Fault.Kind, e.Fault.Target, int64(e.Fault.At), e.Err)
}

// Unwrap returns the underlying fault error.
func (e *FaultError) Unwrap() error { return e.Err }
