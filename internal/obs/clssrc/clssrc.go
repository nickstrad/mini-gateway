// Package clssrc declares component "classifier" metrics and maps classifier,
// reply-builder, and extended-protocol discard snapshots into obs.Emitter
// without letting internal/pgwire depend on any adapter or catalog package.
package clssrc

import (
	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/work"
)

// Snapshot bundles the three fixed-size pgwire snapshots exported under
// component "classifier".
type Snapshot struct {
	Classifier pgwire.ClassifierSnapshot
	Replies    pgwire.ReplySnapshot
	Sync       pgwire.SyncDiscardSnapshot
}

var none = obs.Labels{}

// Metrics returns the 15 counter descriptors declared for component
// "classifier". The total reply counter is named "replies_built" rather than
// "replies_total" to avoid colliding with the Prometheus name of "replies".
func Metrics() []obs.MetricDesc {
	return []obs.MetricDesc{
		obs.CounterDesc("statements", 0, "Query messages classified.").AsHeadline(),
		obs.CounterDesc("statements_by_class", obs.LabelOutcome, "Query messages classified, by class (outcome)."),
		obs.CounterDesc("rejects", obs.LabelReason, "Query messages refused by the reject list, by reason."),
		obs.CounterDesc("opaque", 0, "Query messages the lexer could not tokenise."),
		obs.CounterDesc("safe_read", 0, "Query messages that fit the safe shape."),
		obs.CounterDesc("shareable", 0, "Query messages that fit the safe shape outside a transaction and were not rejected."),
		obs.CounterDesc("hinted", 0, "Query messages that begin with the share hint."),
		obs.CounterDesc("share_keys", 0, "Share keys produced (shareable or hinted, outside a transaction, not rejected)."),
		obs.CounterDesc("sql_statements", 0, "SQL statements in the classified Query messages."),
		obs.CounterDesc("replies", obs.LabelReason, "Replies the proxy built itself, by error-table row (reason)."),
		obs.CounterDesc("replies_built", 0, "Replies the proxy built itself, all rows."),
		obs.CounterDesc("reply_failures", 0, "Reply builds refused for bad input."),
		obs.CounterDesc("extended_refused", 0, "Extended-protocol messages refused."),
		obs.CounterDesc("extended_discarded", 0, "Messages dropped while discarding until Sync."),
		obs.CounterDesc("extended_synced", 0, "Discards ended by Sync."),
	}
}

// Source wraps snapshot as an obs.Source registered under obs.ComponentClassifier.
func Source(snapshot func() Snapshot) obs.Source {
	return obs.NewSource(obs.ComponentClassifier, Metrics(), snapshot, Map)
}

// Map emits every counter and every enum label slot (including zero rows) from
// s into e.
func Map(s Snapshot, e *obs.Emitter) {
	c := s.Classifier
	e.Counter("statements", none, c.Classified)
	for k, v := range c.ByClass {
		e.Counter("statements_by_class", obs.Labels{Outcome: work.Class(k).String()}, v)
	}
	for k, v := range c.ByReason {
		e.Counter("rejects", obs.Labels{Reason: work.RejectReason(k).String()}, v)
	}
	e.Counter("opaque", none, c.Opaque)
	e.Counter("safe_read", none, c.SafeRead)
	e.Counter("shareable", none, c.Shareable)
	e.Counter("hinted", none, c.Hinted)
	e.Counter("share_keys", none, c.Keys)
	e.Counter("sql_statements", none, c.Statements)
	for k, v := range s.Replies.ByRow {
		e.Counter("replies", obs.Labels{Reason: pgwire.ErrorRows[k].ID}, v)
	}
	e.Counter("replies_built", none, s.Replies.Total)
	e.Counter("reply_failures", none, s.Replies.Failed)
	e.Counter("extended_refused", none, s.Sync.Refused)
	e.Counter("extended_discarded", none, s.Sync.Discarded)
	e.Counter("extended_synced", none, s.Sync.Synced)
}
