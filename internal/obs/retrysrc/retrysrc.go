// Package retrysrc maps retry.Snapshot into component "retry" metrics for the
// observability collector. Retries add no health rule.
package retrysrc

import (
	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/retry"
)

const (
	lr = obs.LabelReason

	// MetricLevel is the gauge name for the retry budget level in tenths of a token.
	MetricLevel = "budget_level"
)

var none = obs.Labels{}

// Metrics declares every metric exported under obs.ComponentRetry.
func Metrics() []obs.MetricDesc {
	return []obs.MetricDesc{
		obs.GaugeDesc("policy", 0, "Retry policy in force: 0 off, 1 pre-forward-once, 2 a test-only policy."),
		obs.CounterDesc("attempts", 0, "First attempts forwarded to the database; each deposits a tenth of a token.").AsHeadline(),
		obs.CounterDesc("offered", 0, "Failed statements offered to the retry policy; each ends retried or refused for one reason."),
		obs.CounterDesc("retries", 0, "Retries granted; each withdraws one token."),
		obs.CounterDesc("refused", lr, "Failures not retried, by reason (off, once, in-transaction, reached, breaker, draining, deadline, budget)."),
		obs.CounterDesc("exhaustions", 0, "Retries refused because the budget held less than one token."),
		obs.GaugeDesc(MetricLevel, 0, "Retry budget held now, in tenths of a token."),
		obs.HighWaterDesc("budget_level_peak", 0, "Most retry budget held at once, in tenths of a token."),
		obs.GaugeDesc("budget_cap", 0, "The retry budget's cap, in tenths of a token (retry.bucket_cap)."),
		obs.GaugeDesc("budget_floor", 0, "The retry budget's starting level, in tenths of a token (retry.floor divided by fleet.instances)."),
		obs.CounterDesc("deposits", 0, "Tenths of a token deposited, the starting floor included."),
		obs.CounterDesc("withdrawals", 0, "Tenths of a token withdrawn by retries."),
		obs.CounterDesc("expired", 0, "Tenths of a token lost because the budget was at its cap."),
	}
}

// Map emits every counter and gauge in s to e under obs.ComponentRetry.
func Map(s retry.Snapshot, e *obs.Emitter) {
	e.Gauge("policy", none, s.Kind)
	e.Counter("attempts", none, s.Attempts)
	e.Counter("offered", none, s.Offered)
	e.Counter("retries", none, s.Retries)
	for r := retry.RefusedOff; r < retry.NumReasons; r++ {
		e.Counter("refused", obs.Labels{Reason: r.String()}, s.Refused[r])
	}
	e.Counter("exhaustions", none, s.Refused[retry.RefusedBudget])
	e.Gauge(MetricLevel, none, s.Level.Value)
	e.Gauge("budget_level_peak", none, s.Level.Peak)
	e.Gauge("budget_cap", none, s.Cap)
	e.Gauge("budget_floor", none, s.Floor)
	e.Counter("deposits", none, s.Deposits)
	e.Counter("withdrawals", none, s.Withdrawals)
	e.Counter("expired", none, s.Expired)
}

// Source wraps snap as an obs.Source for obs.ComponentRetry.
func Source(snap func() retry.Snapshot) obs.Source {
	return obs.NewSource(obs.ComponentRetry, Metrics(), snap, Map)
}
