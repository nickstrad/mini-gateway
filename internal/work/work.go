package work

type TenantID string

type Class uint8

const (
	ClassNone Class = iota
	ClassReject
	ClassStartLease
	ClassContinueLease
	ClassAnswerLocally
	NumClasses
)

var classNames = [NumClasses]string{"none", "reject", "start-lease", "continue-lease", "answer-locally"}

func (c Class) String() string {
	if c < NumClasses {
		return classNames[c]
	}
	return "invalid"
}

func (c Class) Forwards() bool { return c == ClassStartLease || c == ClassContinueLease }

type Tri uint8

const (
	No Tri = iota
	Yes
	Unknown
)

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

type RejectReason uint8

const (
	RejectNone RejectReason = iota
	RejectSessionSet
	RejectReset
	RejectPrepare
	RejectExecute
	RejectDeallocate
	RejectListen
	RejectUnlisten
	RejectCursorWithHold
	RejectTempObject
	RejectAdvisoryLock
	RejectSetConfig
	RejectDiscard
	RejectLoad
	RejectCopy
	RejectTwoPhase
	RejectExtendedProtocol
	NumRejectReasons
)

var rejectNames = [NumRejectReasons]string{
	"none", "session-set", "reset", "prepare", "execute", "deallocate", "listen", "unlisten",
	"cursor-with-hold", "temp-object", "advisory-lock", "set-config", "discard", "load", "copy",
	"two-phase", "extended-protocol",
}

func (r RejectReason) String() string {
	if r < NumRejectReasons {
		return rejectNames[r]
	}
	return "invalid"
}

type KeyScope uint8

const (
	ScopeGlobal KeyScope = iota
	ScopeTenant
)

type ShareKey struct {
	Tenant TenantID
	Text   []byte
}

func (k ShareKey) Equal(o ShareKey) bool {
	return k.Tenant == o.Tenant && string(k.Text) == string(o.Text)
}

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

type Release uint8

const (
	ReleaseClean Release = iota
	ReleaseHeld
	ReleaseDirty
)

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

type ErrorClass uint8

const (
	ErrNone ErrorClass = iota
	ErrRefused
	ErrDatabase
	ErrConnection
	ErrTimeout
	ErrCancelled
	ErrProtocol
	NumErrorClasses
)

var errorClassNames = [NumErrorClasses]string{"none", "refused", "database", "connection", "timeout", "cancelled", "protocol"}

func (e ErrorClass) String() string {
	if e < NumErrorClasses {
		return errorClassNames[e]
	}
	return "invalid"
}

type Outcome struct {
	Release      Release
	Reached      Tri
	WriteApplied Tri
	Error        ErrorClass
}

type Refusal uint8

const (
	RefusalNone Refusal = iota
	RefusalShed
	RefusalRate
	RefusalBreaker
	RefusalCoalesceCap
	RefusalDraining
	RefusalUnsupported
	RefusalCancelledQueued
	NumRefusals
)

var refusalNames = [NumRefusals]string{"none", "shed", "rate", "breaker", "coalesce-cap", "draining", "unsupported", "cancelled-queued"}

func (r Refusal) String() string {
	if r < NumRefusals {
		return refusalNames[r]
	}
	return "invalid"
}
