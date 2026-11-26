package pgwire

import "github.com/nickstrad/mini-gateway/internal/work"

type ErrRowID uint8

const (
	ErrRowShed ErrRowID = iota
	ErrRowRateGate
	ErrRowBreakerOpen
	ErrRowCoalesceCap
	ErrRowDraining
	ErrRowUnsupportedFeature
	ErrRowUnsupportedExtended
	ErrRowCancelledQueued
	ErrRowLostAfterRead
	ErrRowLostAfterWrite
	ErrRowLostInTransaction
	ErrRowIdleInTransaction
	ErrRowTLSRequired
	ErrRowTLSHandshake
	ErrRowStartupParameter
	ErrRowAuthFailed
	ErrRowTooManyClients
	ErrRowProtocolViolation
	ErrRowPartialReply
	ErrRowPassThrough
	ErrRowCount
)

type ErrorRowReply uint8

const (
	ErrorRowError ErrorRowReply = iota + 1
	ErrorRowFatal
	ErrorRowErrorUntilSync
	ErrorRowCloseOnly
	ErrorRowPassThrough
)

func (r ErrorRowReply) String() string {
	switch r {
	case ErrorRowError:
		return "ERROR + ReadyForQuery"
	case ErrorRowFatal:
		return "FATAL, then close"
	case ErrorRowErrorUntilSync:
		return "ERROR, discard until Sync, ReadyForQuery"
	case ErrorRowCloseOnly:
		return "close, no message"
	case ErrorRowPassThrough:
		return "Postgres's own reply, unchanged"
	}
	return "?"
}

type ErrorRowConn uint8

const (
	ErrorRowConnUsable ErrorRowConn = iota + 1
	ErrorRowConnClosedWhenIdle
	ErrorRowConnClosed
	ErrorRowConnAsPostgres
)

func (c ErrorRowConn) String() string {
	switch c {
	case ErrorRowConnUsable:
		return "stays usable"
	case ErrorRowConnClosedWhenIdle:
		return "closed when idle"
	case ErrorRowConnClosed:
		return "closed"
	case ErrorRowConnAsPostgres:
		return "as Postgres decides"
	}
	return "?"
}

type ErrorRow struct {
	ID           string
	Situation    string
	Reply        ErrorRowReply
	Code         string
	Message      string
	Reached      work.Tri
	WriteApplied work.Tri
	Retry        string
	Conn         ErrorRowConn
	Refusal      work.Refusal
	Chunk        int
}

func (r ErrorRow) Severity() string {
	switch r.Reply {
	case ErrorRowError, ErrorRowErrorUntilSync:
		return "ERROR"
	case ErrorRowFatal:
		return "FATAL"
	}
	return ""
}

func (r ErrorRow) HasMessage() bool { return r.Severity() != "" }

func (r ErrorRow) CounterKey() string { return "reply_" + r.ID }

func (id ErrRowID) Row() ErrorRow {
	if id < ErrRowCount {
		return ErrorRows[id]
	}
	return ErrorRow{}
}

var ErrorRows = [ErrRowCount]ErrorRow{
	ErrRowShed: {"shed", "Shed by the queue rule, or the queue is full", ErrorRowError, "53M01",
		"mini-gateway: request shed by the admission queue", work.No, work.No, "yes, after backing off", ErrorRowConnUsable, work.RefusalShed, 8},
	ErrRowRateGate: {"rate-gate", "Refused by the per-tenant rate gate", ErrorRowError, "53M02",
		"mini-gateway: request refused by the rate gate", work.No, work.No, "yes, later", ErrorRowConnUsable, work.RefusalRate, 10},
	ErrRowBreakerOpen: {"breaker-open", "Database marked down (breaker open)", ErrorRowError, "53M03",
		"mini-gateway: database unavailable", work.No, work.No, "yes, later", ErrorRowConnUsable, work.RefusalBreaker, 7},
	ErrRowCoalesceCap: {"coalesce-cap", "Coalescer waiter cap or shared-reply cap exceeded", ErrorRowError, "53M04",
		"mini-gateway: shared read is full", work.No, work.No, "yes", ErrorRowConnUsable, work.RefusalCoalesceCap, 11},
	ErrRowDraining: {"draining", "Instance draining", ErrorRowError, "53M05",
		"mini-gateway: instance is draining", work.No, work.No, "yes, on another instance", ErrorRowConnClosedWhenIdle, work.RefusalDraining, 13},
	ErrRowUnsupportedFeature: {"unsupported-feature", "Statement uses a feature on the reject list", ErrorRowError, "0A000",
		"mini-gateway: feature not supported by a transaction-pooling proxy", work.No, work.No, "no, change the client", ErrorRowConnUsable, work.RefusalUnsupported, 5},
	ErrRowUnsupportedExtended: {"unsupported-extended", "Extended-protocol or function-call message", ErrorRowErrorUntilSync, "0A000",
		"mini-gateway: extended query protocol not supported", work.No, work.No, "no, change the client", ErrorRowConnUsable, work.RefusalUnsupported, 5},
	ErrRowCancelledQueued: {"cancelled-queued", "Cancelled while queued", ErrorRowError, "57014",
		"canceling statement due to user request", work.No, work.No, "caller's choice", ErrorRowConnUsable, work.RefusalCancelledQueued, 7},
	ErrRowLostAfterRead: {"lost-after-read", "Database connection lost or reply timed out after a statement that fits the safe shape (SafeRead) was sent, outside a transaction", ErrorRowError, "08006",
		"mini-gateway: database connection lost", work.Yes, work.No, "yes", ErrorRowConnUsable, work.RefusalNone, 6},
	ErrRowLostAfterWrite: {"lost-after-write", "Same, but the statement could write, or was COMMIT", ErrorRowError, "08007",
		"mini-gateway: database connection lost; the transaction may have committed", work.Yes, work.Unknown, "not without checking", ErrorRowConnUsable, work.RefusalNone, 6},
	ErrRowLostInTransaction: {"lost-in-transaction", "Database connection lost while the client was inside a transaction, between statements", ErrorRowFatal, "08006",
		"mini-gateway: database connection lost inside a transaction", work.Yes, work.No, "yes, the whole transaction", ErrorRowConnClosed, work.RefusalNone, 6},
	ErrRowIdleInTransaction: {"idle-in-transaction", "Transaction left idle past the deadline (rolled back on the database)", ErrorRowFatal, "25P03",
		"mini-gateway: terminating connection due to idle-in-transaction timeout", work.Yes, work.No, "yes, the whole transaction", ErrorRowConnClosed, work.RefusalNone, 7},
	ErrRowTLSRequired: {"tls-required", "Plaintext startup message while client_tls.mode is require", ErrorRowFatal, "28000",
		"mini-gateway: TLS is required", work.No, work.No, "no, set the client's sslmode", ErrorRowConnClosed, work.RefusalNone, 4},
	ErrRowTLSHandshake: {"tls-handshake", "TLS handshake failed or timed out", ErrorRowCloseOnly, "",
		"", work.No, work.No, "caller's choice", ErrorRowConnClosed, work.RefusalNone, 4},
	ErrRowStartupParameter: {"startup-parameter", "Startup parameter differs from the pool's fixed session settings (application_name is accepted and ignored)", ErrorRowFatal, "0A000",
		"mini-gateway: startup parameter not supported", work.No, work.No, "no, change the client", ErrorRowConnClosed, work.RefusalUnsupported, 4},
	ErrRowAuthFailed: {"auth-failed", "SCRAM authentication failed or the tenant is unknown", ErrorRowFatal, "28P01",
		"password authentication failed", work.No, work.No, "no, fix the credentials", ErrorRowConnClosed, work.RefusalNone, 4},
	ErrRowTooManyClients: {"too-many-clients", "Client connection cap or the cap on clients in startup reached", ErrorRowFatal, "53300",
		"sorry, too many clients already", work.No, work.No, "yes, after backing off", ErrorRowConnClosed, work.RefusalNone, 7},
	ErrRowProtocolViolation: {"protocol-violation", "Malformed or unexpected client message, or a client deadline passed mid-message", ErrorRowFatal, "08P01",
		"mini-gateway: protocol violation", work.No, work.No, "no", ErrorRowConnClosed, work.RefusalNone, 4},
	ErrRowPartialReply: {"partial-reply", "Reply already partly sent to the client when the database connection failed", ErrorRowCloseOnly, "",
		"", work.Yes, work.Unknown, "per the rows above", ErrorRowConnClosed, work.RefusalNone, 6},
	ErrRowPassThrough: {"pass-through", "An error from Postgres itself", ErrorRowPassThrough, "",
		"", work.Yes, work.Unknown, "as Postgres says", ErrorRowConnAsPostgres, work.RefusalNone, 6},
}

func ErrRowFor(r work.Refusal) (ErrRowID, bool) {
	for id := ErrRowID(0); id < ErrRowCount; id++ {
		if ErrorRows[id].Refusal == r && r != work.RefusalNone && ErrorRows[id].Reply == ErrorRowError {
			return id, true
		}
	}
	return 0, false
}
