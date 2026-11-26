// Package work defines the protocol-free vocabulary shared between the wire
// boundary and the core engine components (pool, admission, coalescer, retry).
//
// This package imports only the standard library and names no database wire
// protocol types, so cores can be tested against synthetic workloads before
// any wire layer is attached.
package work

// TenantID identifies a client tenant (the startup user name in v1).
type TenantID string

// Class describes the routing decision the engine makes for a classified unit
// of work.
type Class uint8

const (
	// ClassNone is the zero value and is never produced by a classifier.
	ClassNone Class = iota
	// ClassReject refuses the message locally without forwarding any byte to
	// the database.
	ClassReject
	// ClassStartLease admits a new transaction lease outside an open
	// transaction.
	ClassStartLease
	// ClassContinueLease routes a statement directly onto the connection
	// already held by an open transaction.
	ClassContinueLease
	// ClassAnswerLocally is reserved for future local answers; never produced
	// in v1.
	ClassAnswerLocally
	// NumClasses is the sentinel count of Class values for fixed-size arrays.
	NumClasses
)

var classNames = [NumClasses]string{"none", "reject", "start-lease", "continue-lease", "answer-locally"}

// String returns the stable lowercase label used in metrics and diagnostics.
func (c Class) String() string {
	if c < NumClasses {
		return classNames[c]
	}
	return "invalid"
}

// Forwards reports whether a unit of this class is sent to the database.
func (c Class) Forwards() bool { return c == ClassStartLease || c == ClassContinueLease }

// Tri is a three-valued fact used where an outcome cannot always be known
// after a failure in flight.
type Tri uint8

const (
	// No means the fact is known to be false.
	No Tri = iota
	// Yes means the fact is known to be true.
	Yes
	// Unknown means the proxy cannot determine whether the fact holds.
	Unknown
)

// String returns "no", "yes", "unknown", or "invalid".
func (t Tri) String() string {
	switch t {
	case No:
		return "no"
	case Yes:
		return "yes"
	case Unknown:
		return "unknown"
	}
	return "invalid"
}

// RejectReason identifies why a message was refused locally.
type RejectReason uint8

const (
	// RejectNone indicates the message was not rejected.
	RejectNone RejectReason = iota
	// RejectSessionSet refuses session-scoped SET and lexing-setting changes.
	RejectSessionSet
	// RejectReset refuses session RESET statements.
	RejectReset
	// RejectPrepare refuses SQL PREPARE statements.
	RejectPrepare
	// RejectExecute refuses SQL EXECUTE statements.
	RejectExecute
	// RejectDeallocate refuses SQL DEALLOCATE statements.
	RejectDeallocate
	// RejectListen refuses LISTEN subscriptions.
	RejectListen
	// RejectUnlisten refuses UNLISTEN statements.
	RejectUnlisten
	// RejectCursorWithHold refuses DECLARE ... WITH HOLD cursors.
	RejectCursorWithHold
	// RejectTempObject refuses temporary-object creation and pg_temp references.
	RejectTempObject
	// RejectAdvisoryLock refuses session-scoped advisory-lock functions.
	RejectAdvisoryLock
	// RejectSetConfig refuses session-scoped or unreadable set_config calls.
	RejectSetConfig
	// RejectDiscard refuses client-issued DISCARD statements.
	RejectDiscard
	// RejectLoad refuses LOAD statements.
	RejectLoad
	// RejectCopy refuses COPY statements in any direction.
	RejectCopy
	// RejectTwoPhase refuses two-phase commit statements.
	RejectTwoPhase
	// RejectExtendedProtocol refuses extended-protocol and function-call
	// messages; produced by the wire layer, never by the SQL classifier.
	RejectExtendedProtocol
	// NumRejectReasons is the sentinel count of RejectReason values for
	// fixed-size counter arrays.
	NumRejectReasons
)

var rejectNames = [NumRejectReasons]string{
	"none", "session-set", "reset", "prepare", "execute", "deallocate", "listen", "unlisten",
	"cursor-with-hold", "temp-object", "advisory-lock", "set-config", "discard", "load", "copy",
	"two-phase", "extended-protocol",
}

// String returns the stable counter label for the reject reason.
func (r RejectReason) String() string {
	if r < NumRejectReasons {
		return rejectNames[r]
	}
	return "invalid"
}

// KeyScope controls whether identical reads may be shared across all tenants
// or only within the same tenant.
type KeyScope uint8

const (
	// ScopeGlobal shares identical read keys across all tenants (Key.Tenant
	// is left empty).
	ScopeGlobal KeyScope = iota
	// ScopeTenant restricts read sharing to requests from the same tenant.
	ScopeTenant
)

// ShareKey identifies an eligible read for in-flight coalescing. Text holds
// an owned copy of the exact raw query bytes.
type ShareKey struct {
	Tenant TenantID
	Text   []byte
}

// Equal reports whether two share keys match on both tenant scope and exact
// query bytes.
func (k ShareKey) Equal(o ShareKey) bool {
	return k.Tenant == o.Tenant && string(k.Text) == string(o.Text)
}

// Unit is the classifier's complete description of one client query message.
type Unit struct {
	Tenant     TenantID
	Class      Class
	Reason     RejectReason
	SafeRead   bool
	Shareable  bool
	Hinted     bool
	Opaque     bool
	Statements int
	Key        ShareKey
	HasKey     bool
}

// Release records the state of a database connection when a unit finishes.
type Release uint8

const (
	// ReleaseClean means the connection returned idle and is ready for reset.
	ReleaseClean Release = iota
	// ReleaseHeld means the client remains inside a transaction and keeps the
	// connection.
	ReleaseHeld
	// ReleaseDirty means the connection left a clean protocol boundary and
	// must go through cleanup or closure.
	ReleaseDirty
)

// String returns "clean", "held", "dirty", or "invalid".
func (r Release) String() string {
	switch r {
	case ReleaseClean:
		return "clean"
	case ReleaseHeld:
		return "held"
	case ReleaseDirty:
		return "dirty"
	}
	return "invalid"
}

// ErrorClass groups unit failures by cause for the engine and retry policy.
type ErrorClass uint8

const (
	// ErrNone indicates the unit completed without error.
	ErrNone ErrorClass = iota
	// ErrRefused indicates the proxy refused the unit before forwarding.
	ErrRefused
	// ErrDatabase indicates the database itself returned an error reply.
	ErrDatabase
	// ErrConnection indicates the database connection failed.
	ErrConnection
	// ErrTimeout indicates a deadline expired.
	ErrTimeout
	// ErrCancelled indicates the client cancelled the unit.
	ErrCancelled
	// ErrProtocol indicates a framing or wire-protocol violation.
	ErrProtocol
	// NumErrorClasses is the sentinel count of ErrorClass values.
	NumErrorClasses
)

var errorClassNames = [NumErrorClasses]string{"none", "refused", "database", "connection", "timeout", "cancelled", "protocol"}

// String returns the stable lowercase label for the error class.
func (e ErrorClass) String() string {
	if e < NumErrorClasses {
		return errorClassNames[e]
	}
	return "invalid"
}

// Outcome summarizes what happened to a unit of work and its connection so
// callers and retry policies know whether anything reached the database.
type Outcome struct {
	Release      Release
	Reached      Tri
	WriteApplied Tri
	Error        ErrorClass
}

// Refusal identifies a local pre-forward refusal that the wire boundary maps
// to a fixed error-contract row.
type Refusal uint8

const (
	// RefusalNone indicates the unit was not refused locally.
	RefusalNone Refusal = iota
	// RefusalShed indicates the admission queue shed or rejected the unit.
	RefusalShed
	// RefusalRate indicates the per-tenant rate gate refused the unit.
	RefusalRate
	// RefusalBreaker indicates the database circuit breaker is open.
	RefusalBreaker
	// RefusalCoalesceCap indicates a coalescer waiter or reply-size cap hit.
	RefusalCoalesceCap
	// RefusalDraining indicates the proxy instance is draining.
	RefusalDraining
	// RefusalUnsupported indicates a rejected feature or unsupported message.
	RefusalUnsupported
	// RefusalCancelledQueued indicates the client cancelled while queued.
	RefusalCancelledQueued
	// NumRefusals is the sentinel count of Refusal values.
	NumRefusals
)

var refusalNames = [NumRefusals]string{"none", "shed", "rate", "breaker", "coalesce-cap", "draining", "unsupported", "cancelled-queued"}

// String returns the stable lowercase label for the refusal kind.
func (r Refusal) String() string {
	if r < NumRefusals {
		return refusalNames[r]
	}
	return "invalid"
}
