package pgwire

import "github.com/nickstrad/mini-gateway/internal/work"

// ErrRowID indexes the fixed error-contract table ErrorRows. New rows must be
// appended before ErrRowCount so existing row indices stay stable.
type ErrRowID uint8

const (
	// ErrRowShed is returned when the admission queue sheds or rejects a query.
	ErrRowShed ErrRowID = iota
	// ErrRowRateGate is returned when the per-tenant rate gate refuses a query.
	ErrRowRateGate
	// ErrRowBreakerOpen is returned when the database circuit breaker is open.
	ErrRowBreakerOpen
	// ErrRowCoalesceCap is returned when a coalescer waiter or reply cap hits.
	ErrRowCoalesceCap
	// ErrRowDraining is returned while the proxy instance is draining.
	ErrRowDraining
	// ErrRowUnsupportedFeature is returned for statements on the reject list.
	ErrRowUnsupportedFeature
	// ErrRowUnsupportedExtended is returned for extended-protocol and
	// function-call messages before discarding until Sync.
	ErrRowUnsupportedExtended
	// ErrRowCancelledQueued is returned when a client cancels a queued query.
	ErrRowCancelledQueued
	// ErrRowLostAfterRead is returned when the database connection fails after
	// a SafeRead query was sent outside a transaction (SQLSTATE 08006).
	ErrRowLostAfterRead
	// ErrRowLostAfterWrite is returned when the database connection fails after
	// any other query or COMMIT was sent (SQLSTATE 08007).
	ErrRowLostAfterWrite
	// ErrRowLostInTransaction is returned when the database connection fails
	// between statements of an open transaction.
	ErrRowLostInTransaction
	// ErrRowIdleInTransaction is returned when an idle transaction times out.
	ErrRowIdleInTransaction
	// ErrRowTLSRequired is returned for plaintext startup when TLS is required.
	ErrRowTLSRequired
	// ErrRowTLSHandshake records a failed or timed-out TLS handshake (close
	// without sending a Postgres message).
	ErrRowTLSHandshake
	// ErrRowStartupParameter is returned for unsupported startup parameters.
	ErrRowStartupParameter
	// ErrRowAuthFailed is returned for failed authentication or unknown tenant.
	ErrRowAuthFailed
	// ErrRowTooManyClients is returned when the client or startup cap is full.
	ErrRowTooManyClients
	// ErrRowProtocolViolation is returned for malformed messages or mid-message
	// read deadlines.
	ErrRowProtocolViolation
	// ErrRowPartialReply records a backend failure after part of the reply was
	// already written to the client (close without sending a Postgres message).
	ErrRowPartialReply
	// ErrRowPassThrough accounts for Postgres's own ErrorResponse relayed
	// unchanged; ReplyBuilder refuses to synthesize a message for it.
	ErrRowPassThrough
	ErrRowUnknownDatabase
	ErrRowProtocolVersion
	ErrRowNoUser
	// ErrRowCount is the sentinel count of rows in ErrorRows.
	ErrRowCount
)

// ErrorRowReply specifies what wire reply and end marker a row produces.
type ErrorRowReply uint8

const (
	// ErrorRowError sends ErrorResponse (ERROR) followed by ReadyForQuery.
	ErrorRowError ErrorRowReply = iota + 1
	// ErrorRowFatal sends ErrorResponse (FATAL) with no ReadyForQuery and
	// closes the client connection.
	ErrorRowFatal
	// ErrorRowErrorUntilSync sends ErrorResponse (ERROR) and defers
	// ReadyForQuery until the client's Sync arrives.
	ErrorRowErrorUntilSync
	// ErrorRowCloseOnly closes the client connection without writing bytes.
	ErrorRowCloseOnly
	// ErrorRowPassThrough relays Postgres's own error unchanged.
	ErrorRowPassThrough
)

// String returns the human-readable wire-reply description for docs/error-contract.md.
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

// ErrorRowConn describes the client connection state after a row's reply.
type ErrorRowConn uint8

const (
	// ErrorRowConnUsable leaves the client connection open and usable.
	ErrorRowConnUsable ErrorRowConn = iota + 1
	// ErrorRowConnClosedWhenIdle closes the connection once it is idle ('I').
	ErrorRowConnClosedWhenIdle
	// ErrorRowConnClosed closes the client connection immediately.
	ErrorRowConnClosed
	// ErrorRowConnAsPostgres leaves connection fate to Postgres's own reply.
	ErrorRowConnAsPostgres
)

// String returns the connection-fate description for docs/error-contract.md.
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

// ErrorRow is one entry in the proxy's wire error contract.
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

// Severity returns "ERROR", "FATAL", or "" for rows that build no message.
func (r ErrorRow) Severity() string {
	switch r.Reply {
	case ErrorRowError, ErrorRowErrorUntilSync:
		return "ERROR"
	case ErrorRowFatal:
		return "FATAL"
	}
	return ""
}

// HasMessage reports whether ReplyBuilder encodes an ErrorResponse for this row.
func (r ErrorRow) HasMessage() bool { return r.Severity() != "" }

// CounterKey returns the documented counter key ("reply_<id>") for the row.
func (r ErrorRow) CounterKey() string { return "reply_" + r.ID }

// Row returns the ErrorRow for id, or the zero ErrorRow if id is out of range.
func (id ErrRowID) Row() ErrorRow {
	if id < ErrRowCount {
		return ErrorRows[id]
	}
	return ErrorRow{}
}

// ErrorRows is the single source of truth for every proxy-made error reply and
// for the generated docs/error-contract.md table. Private SQLSTATE codes
// 53M01..53M05 sit inside standard class 53 (insufficient resources) and are
// reserved for pre-forward refusals.
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
	ErrRowUnknownDatabase: {"unknown-database", "Startup names a database other than the pool's", ErrorRowFatal, "3D000",
		"database does not exist", work.No, work.No, "no, fix the database name", ErrorRowConnClosed, work.RefusalNone, 4},
	ErrRowProtocolVersion: {"protocol-version", "Startup asks for a protocol major version other than 3 (version 2 and older get the old-format error)", ErrorRowFatal, "0A000",
		"unsupported frontend protocol", work.No, work.No, "no, change the client", ErrorRowConnClosed, work.RefusalNone, 4},
	ErrRowNoUser: {"no-user", "Startup packet names no user", ErrorRowFatal, "28000",
		"no PostgreSQL user name specified in startup packet", work.No, work.No, "no, fix the client", ErrorRowConnClosed, work.RefusalNone, 4},
}

func AppendRowFatal(dst []byte, id ErrRowID, message string) []byte {
	row := id.Row()
	if message == "" {
		message = row.Message
	}
	return AppendErrorResponse(dst, SeverityFatal, row.Code, message)
}

// ErrRowFor maps a core pre-forward refusal to its ErrorRowError entry.
// RefusalUnsupported maps to ErrRowUnsupportedFeature; callers handling
// extended-protocol or startup refusals select those rows directly.
func ErrRowFor(r work.Refusal) (ErrRowID, bool) {
	for id := ErrRowID(0); id < ErrRowCount; id++ {
		if ErrorRows[id].Refusal == r && r != work.RefusalNone && ErrorRows[id].Reply == ErrorRowError {
			return id, true
		}
	}
	return 0, false
}
