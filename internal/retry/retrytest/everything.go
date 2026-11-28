// Package retrytest provides test-only retry policies kept out of the binary's
// import graph.
package retrytest

import "github.com/nickstrad/mini-gateway/internal/retry"

// EverythingAttempts is the per-request retry cap of the ablation policy.
const EverythingAttempts = 3

// Everything is a deliberately unsafe ablation policy that retries every
// offered failure up to EverythingAttempts times regardless of Reached,
// transaction state, breaker state, or budget.
type Everything struct{ s retry.Snapshot }

// KindEverything distinguishes Everything in Snapshot.Kind so Snapshot.Check
// verifies offer conservation without enforcing PreForwardOnce's budget bound.
const KindEverything = int64(retry.NumKinds)

// NewEverything constructs a test-only retry-everything ablation policy.
func NewEverything() *Everything { return &Everything{s: retry.Snapshot{Kind: KindEverything}} }

// Attempt records a forwarded first attempt.
func (e *Everything) Attempt(retry.Time) { e.s.Attempts++ }

// Decide grants a retry for any failure until f.Attempt reaches
// EverythingAttempts.
func (e *Everything) Decide(_ retry.Time, f retry.Failure) retry.Decision {
	e.s.Offered++
	if f.Attempt >= EverythingAttempts {
		e.s.Refused[retry.RefusedOnce]++
		return retry.Decision{Reason: retry.RefusedOnce}
	}
	e.s.Retries++
	return retry.Decision{Retry: true, Reason: retry.Granted}
}

// Snapshot returns a copy of e's counters.
func (e *Everything) Snapshot() retry.Snapshot { return e.s }
