package retrytest

import "github.com/nickstrad/mini-gateway/internal/retry"

const EverythingAttempts = 3

type Everything struct{ s retry.Snapshot }

const KindEverything = int64(retry.NumKinds)

func NewEverything() *Everything { return &Everything{s: retry.Snapshot{Kind: KindEverything}} }

func (e *Everything) Attempt(retry.Time) { e.s.Attempts++ }

func (e *Everything) Decide(_ retry.Time, f retry.Failure) retry.Decision {
	e.s.Offered++
	if f.Attempt >= EverythingAttempts {
		e.s.Refused[retry.RefusedOnce]++
		return retry.Decision{Reason: retry.RefusedOnce}
	}
	e.s.Retries++
	return retry.Decision{Retry: true, Reason: retry.Granted}
}

func (e *Everything) Snapshot() retry.Snapshot { return e.s }
