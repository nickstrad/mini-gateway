//go:build clscount

// Package clscount provides a compile-time switch for per-step lexer and
// classifier work counting. It lives in its own zero-import package because
// internal/pgwire is determinism-linted, where //go:build constraints are
// disallowed.
package clscount

// On reports whether per-step work counting is compiled into this build.
// It is true under -tags clscount so linear-growth tests observe every lexer
// loop step and classifier lexer-pass step.
const On = true

// Tally counts work steps for one lexer or classifier call.
type Tally struct{ n int }

// Ref is a value handle to a Tally passed to free scanning helpers so they
// record loop steps on the caller's counter without shared state.
type Ref struct{ tally *Tally }

// Bump records one work step.
func (t *Tally) Bump() { t.n++ }

// Add records n work steps.
func (t *Tally) Add(n int) { t.n += n }

// Value returns the accumulated step count.
func (t *Tally) Value() int { return t.n }

// Ref returns a handle targeting this tally.
func (t *Tally) Ref() Ref { return Ref{tally: t} }

// Bump records one work step on the referenced tally.
func (r Ref) Bump() { r.tally.n++ }
