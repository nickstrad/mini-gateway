//go:build !clscount

// Package clscount provides a compile-time switch for per-step lexer and
// classifier work counting. It lives in its own zero-import package because
// internal/pgwire is determinism-linted, where //go:build constraints are
// disallowed.
package clscount

// On reports whether per-step work counting is compiled into this build.
// It is false in default builds so the compiler eliminates every counting
// block at compile time.
const On = false

// Tally counts work steps for one lexer or classifier call. In default builds
// it is zero-sized and all operations are no-ops.
type Tally struct{}

// Ref is a value handle to a Tally passed to free scanning helpers. In default
// builds it is zero-sized so helper call ABIs stay unchanged.
type Ref struct{}

// Bump records one work step (no-op in default builds).
func (*Tally) Bump() {}

// Add records n work steps (no-op in default builds).
func (*Tally) Add(int) {}

// Value returns the accumulated step count, which is always 0 in default builds.
func (*Tally) Value() int { return 0 }

// Ref returns a handle targeting this tally.
func (*Tally) Ref() Ref { return Ref{} }

// Bump records one work step on the referenced tally (no-op in default builds).
func (Ref) Bump() {}
