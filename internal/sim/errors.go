package sim

import (
	"errors"
	"fmt"
)

var (
	ErrBudgetExhausted  = errors.New("sim: step budget exhausted")
	ErrTimeFrozen       = errors.New("sim: time not advancing")
	ErrHeapFull         = errors.New("sim: event heap full")
	ErrUnknownFaultKind = errors.New("sim: unknown fault kind")
	ErrRegistryFull     = errors.New("sim: registry full")
	ErrDuplicateName    = errors.New("sim: duplicate registration")
)

type StallError struct {
	Cause  error
	Seed   int64
	Steps  int64
	Now    Time
	Replay string
}

func (e *StallError) Error() string {
	return fmt.Sprintf("%v: seed %d, step %d, time %d\n%s", e.Cause, e.Seed, e.Steps, int64(e.Now), e.Replay)
}

func (e *StallError) Unwrap() error { return e.Cause }

type PanicError struct {
	Value  string
	Seed   int64
	Steps  int64
	Now    Time
	Replay string
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("sim: actor panic: %s: seed %d, step %d, time %d\n%s", e.Value, e.Seed, e.Steps, int64(e.Now), e.Replay)
}

type InvariantError struct {
	Name   string
	Err    error
	Seed   int64
	Steps  int64
	Now    Time
	Replay string
}

func (e *InvariantError) Error() string {
	return fmt.Sprintf("sim: invariant %s: %v: seed %d, step %d, time %d\n%s", e.Name, e.Err, e.Seed, e.Steps, int64(e.Now), e.Replay)
}

func (e *InvariantError) Unwrap() error { return e.Err }

type QuiesceError struct {
	Name string
	Err  error
}

func (e *QuiesceError) Error() string { return fmt.Sprintf("sim: quiesce %s: %v", e.Name, e.Err) }

func (e *QuiesceError) Unwrap() error { return e.Err }

type FaultError struct {
	Fault Fault
	Err   error
}

func (e *FaultError) Error() string {
	return fmt.Sprintf("sim: fault kind %d target %d at %d: %v", e.Fault.Kind, e.Fault.Target, int64(e.Fault.At), e.Err)
}

func (e *FaultError) Unwrap() error { return e.Err }
