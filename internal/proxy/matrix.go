package proxy

import (
	"strings"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/work"
)

const noRow = pgwire.ErrRowCount

type MatrixRow struct {
	Fault   string
	Where   string
	Row     pgwire.ErrRowID
	Client  string
	Budget  string
	Reached work.Tri
	InTx    bool
	Chunk   int
	Tests   []string
}

func (m MatrixRow) Retried() bool {
	return m.Row < noRow && m.Row.ProxyRetry() == pgwire.ProxyRetryWhenNotReached && m.Reached == work.No && !m.InTx
}

func (m MatrixRow) clientCell() string {
	if m.Row >= noRow {
		return m.Client
	}
	r := m.Row.Row()
	out := "`" + r.ID + "`"
	if r.Code != "" {
		out += " " + r.Severity() + " " + r.Code
	}
	if m.Client != "" {
		out += "; " + m.Client
	}
	return out
}

func (m MatrixRow) reachedCell() string {
	if m.Reached > work.Unknown {
		return "—"
	}
	if m.Reached == work.Yes {
		return "may have"
	}
	return m.Reached.String()
}

func (m MatrixRow) retriedCell() string {
	switch {
	case m.Retried():
		return "yes, once (policy pre-forward-once; budget, breaker closed, before the request deadline)"
	case m.InTx:
		return "no (inside a transaction)"
	case m.Reached == work.Yes || m.Reached == work.Unknown:
		return "no (may have reached the database)"
	case m.Row >= noRow:
		return "no (no failed statement)"
	}
	return "no (refused before the database; the client may retry)"
}

const notApplicable = work.Tri(9)

func FaultMatrixRows() []MatrixRow {
	no, yes, na := work.No, work.Yes, notApplicable
	return []MatrixRow{
		{"Database reset before the reply, statement a safe read", "after the Query was written", pgwire.ErrRowLostAfterRead, "usable", "immediate", yes, false, 6,
			[]string{"e2e-kill-before-read", "TestTierRDatabaseKilled"}},
		{"Same, statement a write", "after the Query was written", pgwire.ErrRowLostAfterWrite, "usable", "immediate", yes, false, 6,
			[]string{"e2e-kill-before-write", "TestRetryNeverOnACloseWithoutAWriteReport"}},
		{"First write of the request refused with zero bytes accepted (a lent connection found dead)", "lease, before the first byte", pgwire.ErrRowLostAfterRead, "when the policy declines", "immediate", no, false, 12,
			[]string{"TestRetryAZeroByteRefusalRunsAgainOnANewConnectionAndTheClientGetsOneReply", "TestWriteRefusedWithZeroBytesIsNotReached", "retry-storm", "retry-no-double-apply"}},
		{"Write refused after part of it was accepted", "forward", pgwire.ErrRowLostAfterWrite, "usable", "immediate", yes, false, 12,
			[]string{"TestRetryNeverAfterBytesWereAccepted", "TestWriteRefusedAfterPartBytesIsMayHaveReached"}},
		{"Relay cut before forwarding / after forwarding / after the write applied (kill points)", "forward", pgwire.ErrRowLostAfterWrite, "usable", "immediate", yes, false, 12,
			[]string{"retry-no-double-apply", "retry-storm"}},
		{"Database reset during the reply", "after part of the reply was relayed", pgwire.ErrRowPartialReply, "", "immediate", yes, false, 6,
			[]string{"e2e-kill-during-reply", "TestRetryNeverAfterARelayedByte", "retry-no-double-apply"}},
		{"Database reset right after the reply", "during the reset", noRow, "none (the client has its reply)", "—", na, false, 6,
			[]string{"e2e-kill-after-reply", "retry-no-double-apply"}},
		{"Database reset between statements in a transaction", "after ReadyForQuery T", pgwire.ErrRowLostInTransaction, "", "immediate", yes, true, 6,
			[]string{"e2e-kill-mid-transaction", "TestTierRDatabaseKilled"}},
		{"First write refused with zero bytes inside a transaction", "statement inside a transaction", pgwire.ErrRowLostInTransaction, "", "immediate", no, true, 12,
			[]string{"TestRetryNeverInsideATransactionWhateverReachedSays"}},
		{"Loss or deadline mid-statement inside a transaction, no COMMIT/END/PREPARE", "statement inside a transaction", pgwire.ErrRowLostMidStatementInTransaction, "", "timeouts.statement (loss: immediate)", yes, true, 7,
			[]string{"TestCoreLossMidStatementInsideATransactionIsFatal08006UnlessItMayCommit", "TestRetryNeverInsideATransactionMidStatement"}},
		{"Same, the message may end the transaction", "statement inside a transaction", pgwire.ErrRowLostAfterWrite, "usable", "timeouts.statement (loss: immediate)", yes, true, 7,
			[]string{"TestCoreLossMidStatementInsideATransactionIsFatal08006UnlessItMayCommit"}},
		{"Database accepts a query and never answers", "statement", pgwire.ErrRowLostAfterWrite, "08006 for a safe read", "timeouts.statement", yes, false, 6,
			[]string{"e2e-silent-db-query", "TestRetryNeverOnStatementSilence", "pool-mass-blackhole"}},
		{"Peer forgot the connection (reset one network delay after the write)", "statement", pgwire.ErrRowLostAfterWrite, "", "immediate", yes, false, 7,
			[]string{"pool-mass-peer-forgot"}},
		{"Retried request still waiting or running at the request's deadline", "queue or statement of the retry", pgwire.ErrRowShed, "or 08007 when forwarded", "arrival + timeouts.statement", no, false, 12,
			[]string{"TestRetryGrantedJustBeforeTheRequestDeadlineAndBoundedByIt", "TestRetryForwardedStatementEndsAtTheRequestDeadline"}},
		{"Zero-byte refusal at the request's deadline", "lease", pgwire.ErrRowLostAfterRead, "no retry at equality", "immediate", no, false, 12,
			[]string{"TestRetryRefusedAtTheRequestDeadlineExactly"}},
		{"Zero-byte refusal whose loss opens the breaker", "lease", pgwire.ErrRowLostAfterRead, "no retry", "immediate", no, false, 12,
			[]string{"TestRetryNeverWhenTheLossOpensTheBreaker"}},
		{"Any death of an idle connection (stale or suspect)", "check before reuse", noRow, "none; the check absorbs it", "pool.check_timeout", na, false, 7,
			[]string{"pool-check-hangs", "pool-mass-blackhole"}},
		{"Polite close (FIN) of an idle connection", "idle", noRow, "none", "—", na, false, 7, []string{"pool-polite-close"}},
		{"Database down", "breaker gate", pgwire.ErrRowBreakerOpen, "usable", "immediate", no, false, 7,
			[]string{"pool-db-restart", "TestCoreBreakerRefusesAtOnceWithNoDial"}},
		{"Database accepts and never answers startup", "connect", pgwire.ErrRowShed, "usable", "the queue rule or timeouts.queue_wait", no, false, 6,
			[]string{"e2e-silent-db-startup", "TestTierRSilentDatabase"}},
		{"Queue full, or the queue rule's allowed wait passed", "queue", pgwire.ErrRowShed, "usable", "immediate / queue.interval", no, false, 8,
			[]string{"e2e-queue-full", "admission-first-burst-timeout"}},
		{"Client cancel while queued", "queue", pgwire.ErrRowCancelledQueued, "usable", "immediate", no, false, 7,
			[]string{"TestCoreCancelWhileQueuedAnswers57014"}},
		{"Reject-list statement, extended-protocol message", "reject stage", pgwire.ErrRowUnsupportedFeature, "usable", "immediate", no, false, 6,
			[]string{"e2e-reject-list", "TestTierRRejectListAndExtendedProtocol"}},
		{"Draining", "draining stage", pgwire.ErrRowDraining, "closed when idle", "immediate", no, false, 6,
			[]string{"TestDrainingRefusesNewWorkWith53M05AndClosesWhenIdle", "TestRetryNeverWhileDraining"}},
		{"Client idle inside a transaction", "between statements", pgwire.ErrRowIdleInTransaction, "", "timeouts.idle_in_transaction", yes, true, 7,
			[]string{"pool-idle-in-tx"}},
		{"Client vanishes mid-statement or mid-transaction", "lease", noRow, "none (no client)", "timeouts.cleanup", na, false, 6,
			[]string{"e2e-client-vanish", "pool-vanish"}},
		{"Client stops reading a large reply", "relay", noRow, "none; client dropped", "timeouts.client_write", na, false, 6,
			[]string{"e2e-slow-reader"}},
		{"Reset reply wrong (error, tag, status, silence, drop, late)", "release", noRow, "none; connection closed", "timeouts.reset", na, false, 6,
			[]string{"e2e-reset-error", "e2e-reset-silent"}},
		{"Plaintext startup under require", "startup", pgwire.ErrRowTLSRequired, "", "immediate", no, false, 6,
			[]string{"TestPlaintextStartupUnderRequireGets28000"}},
		{"Client cap", "accept", pgwire.ErrRowTooManyClients, "", "immediate", no, false, 6, []string{"TestClientCapRefusesWith53300"}},
	}
}

func FaultMatrixMarkdown() []byte {
	var b strings.Builder
	b.WriteString("# Fault × path matrix\n\n")
	b.WriteString("Generated by `scripts/gen-docs.sh` from `internal/proxy/matrix.go` (`FaultMatrixRows`). Do not edit by hand: a test fails when this file differs from the code.\n\n")
	b.WriteString("One row per fault and place in the conversation: the error-contract row the client gets (`docs/error-contract.md`), the deadline that bounds its wait, whether the request may have reached the database, and whether the proxy itself retries it. The proxy retries only a failure where nothing reached the database for certain, outside a transaction; such a failure is answered with `lost-after-read` when the policy declines. Tests are Go test names or simulator scenario names (`test/e2e`).\n\n")
	b.WriteString("| Fault | Where | Client gets | Budget | Reached the database? | Retried by the proxy? | Chunk | Tests |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, m := range FaultMatrixRows() {
		tests := make([]string, len(m.Tests))
		for i, t := range m.Tests {
			tests[i] = "`" + t + "`"
		}
		cells := []string{m.Fault, m.Where, m.clientCell(), m.Budget, m.reachedCell(), m.retriedCell(), itoa(m.Chunk), strings.Join(tests, ", ")}
		b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
	}
	return []byte(b.String())
}
