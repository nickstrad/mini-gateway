package pgwire

import (
	"errors"
	"fmt"
)

// Reason classifies why a ClientSession ended before reaching ReadyForQuery.
type Reason uint8

// Client-side startup close reasons indexed into ClientWireSnapshot.StartupsFailed.
const (
	ReasonNone Reason = iota
	ReasonClientClosed
	ReasonDeadline
	ReasonViolation
	ReasonTLSFailed
	ReasonPlaintextRefused
	ReasonProtocolVersion
	ReasonParamRefused
	ReasonNoUser
	ReasonAuthFailed
	ReasonUnknownDatabase
	ReasonCancelRequest
	NumReasons
)

var reasonNames = [NumReasons]string{"none", "client-closed", "deadline", "protocol-violation", "tls-failed",
	"plaintext-refused", "protocol-version", "parameter-refused", "no-user", "auth-failed", "unknown-database",
	"cancel-request"}

// String returns the canonical hyphenated name of r.
func (r Reason) String() string {
	if r < NumReasons {
		return reasonNames[r]
	}
	return "unknown"
}

// Class classifies a BackendSession startup failure so the pool's circuit
// breaker can count failures by cause.
type Class uint8

// Database-side startup failure classes indexed into DatabaseWireSnapshot.StartupsFailed.
const (
	ClassNone Class = iota
	ClassTLSRefused
	ClassTLSFailed
	ClassAuthFailed
	ClassAuthUnsupported
	ClassTooManyConnections
	ClassServerFatal
	ClassViolation
	ClassDeadline
	ClassLost
	NumClasses
)

var classNames = [NumClasses]string{"none", "tls-refused", "tls-failed", "auth-failed", "auth-unsupported",
	"too-many-connections", "server-fatal", "protocol-violation", "deadline", "connection-lost"}

// String returns the canonical hyphenated name of c.
func (c Class) String() string {
	if c < NumClasses {
		return classNames[c]
	}
	return "unknown"
}

// Violation identifies a wire-protocol violation detected by a session or tracker.
type Violation uint8

// Wire-protocol violation kinds indexed into SideCounters.Violations.
const (
	ViolationNone Violation = iota
	ViolationBadLength
	ViolationTooLarge
	ViolationBytesBeforeHandshake
	ViolationSecondTLSRequest
	ViolationSecondGSSRequest
	ViolationBadStartupPacket
	ViolationUnexpectedMessage
	ViolationBadMessage
	ViolationBadTLSAnswer
	ViolationUnknownMessage
	ViolationBadHandshake
	NumViolations
)

var violationNames = [NumViolations]string{"none", "bad-length", "too-large", "bytes-before-handshake",
	"second-tls-request", "second-gss-request", "bad-startup-packet", "unexpected-message", "bad-message",
	"bad-tls-answer", "unknown-message", "bad-handshake"}

// String returns the canonical hyphenated name of v.
func (v Violation) String() string {
	if v < NumViolations {
		return violationNames[v]
	}
	return "unknown"
}

// RequestKind identifies a pre-startup untyped request packet on the client leg.
type RequestKind uint8

// Pre-startup request kinds indexed into ClientWireSnapshot.Requests.
const (
	RequestTLS RequestKind = iota
	RequestGSS
	RequestCancel
	NumRequestKinds
)

var requestKindNames = [NumRequestKinds]string{"tls", "gss", "cancel"}

// String returns the canonical name of k.
func (k RequestKind) String() string {
	if k < NumRequestKinds {
		return requestKindNames[k]
	}
	return "unknown"
}

// Answer records the single-byte reply ('S', 'N', or none) to an encryption request.
type Answer uint8

// Encryption-request answer slots indexed into Requests and TLSAnswers arrays.
const (
	AnswerYes Answer = iota
	AnswerNo
	AnswerNone
	NumAnswers
)

var answerNames = [NumAnswers]string{"S", "N", "none"}

// String returns "S", "N", or "none".
func (a Answer) String() string {
	if a < NumAnswers {
		return answerNames[a]
	}
	return "unknown"
}

// HandshakeOutcome reports how a TLS handshake ended to HandshakeDone.
type HandshakeOutcome uint8

// TLS handshake outcomes indexed into TLSFailed arrays (excluding HandshakeCompleted).
const (
	HandshakeCompleted HandshakeOutcome = iota
	HandshakeFailed
	HandshakeFailedVerification
	HandshakeDeadline
	HandshakeViolation
	NumHandshakeOutcomes
)

var handshakeNames = [NumHandshakeOutcomes]string{"completed", "failed", "failed-verification", "deadline", "protocol-violation"}

// String returns the canonical hyphenated name of h.
func (h HandshakeOutcome) String() string {
	if h < NumHandshakeOutcomes {
		return handshakeNames[h]
	}
	return "unknown"
}

// TLSVersion identifies the negotiated TLS protocol version on a completed handshake.
type TLSVersion uint8

// TLS version slots indexed into TLSCompleted arrays.
const (
	TLSVersionOther TLSVersion = iota
	TLSVersion12
	TLSVersion13
	NumTLSVersions
)

var tlsVersionNames = [NumTLSVersions]string{"other", "1.2", "1.3"}

// String returns "1.2", "1.3", or "other".
func (v TLSVersion) String() string {
	if v < NumTLSVersions {
		return tlsVersionNames[v]
	}
	return "unknown"
}

// HandshakeResult is delivered to ClientSession.HandshakeDone or
// BackendSession.HandshakeDone after the transport finishes (or fails) TLS.
type HandshakeResult struct {
	Outcome HandshakeOutcome
	Version TLSVersion
}

func (r HandshakeResult) normalized() HandshakeResult {
	if r.Outcome >= NumHandshakeOutcomes {
		r.Outcome = HandshakeFailed
	}
	if r.Version >= NumTLSVersions {
		r.Version = TLSVersionOther
	}
	return r
}

// SideCounters holds message, byte, violation, and oversized-frame counters
// common to both the client and database legs.
type SideCounters struct {
	MessagesIn  int64
	MessagesOut int64
	BytesIn     int64
	BytesOut    int64
	Violations  [NumViolations]int64
	Oversized   int64
}

// ClientWireSnapshot is a plain value copy of all client-facing wire counters
// and startup gauges.
type ClientWireSnapshot struct {
	StartupsBegun     int64
	StartupsCompleted int64
	StartupsFailed    [NumReasons]int64
	InStartup         int64
	InStartupPeak     int64
	Requests          [NumRequestKinds][NumAnswers]int64
	TLSCompleted      [NumTLSVersions]int64
	TLSFailed         [NumHandshakeOutcomes]int64
	PlaintextRefused  int64
	AuthFailures      [NumAuthCauses]int64
	CancelRequests    int64
	CancelAfterTLS    int64
	Terminated        int64
	QueriesSeen       int64
	ExtendedRefused   int64
	SideCounters
}

// DatabaseWireSnapshot is a plain value copy of all database-facing wire
// counters, startup gauges, and query-cycle tracker counts.
type DatabaseWireSnapshot struct {
	StartupsBegun      int64
	StartupsCompleted  int64
	StartupsFailed     [NumClasses]int64
	InStartup          int64
	InStartupPeak      int64
	TLSRequests        int64
	TLSAnswers         [NumAnswers]int64
	TLSCompleted       [NumTLSVersions]int64
	TLSFailed          [NumHandshakeOutcomes]int64
	AuthFailures       [NumAuthCauses]int64
	ServerFatals       int64
	Replies            int64
	ReadyByStatus      [3]int64
	Errors             int64
	FatalErrors        int64
	ParameterStatus    int64
	Notices            int64
	Notifications      int64
	EndWithoutMarker   int64
	UnknownPassThrough int64
	SideCounters
}

// WireSnapshot is the raw observability export of internal/pgwire, grouping
// client-side and database-side counters with no maps, pointers, or wall clock.
type WireSnapshot struct {
	Client   ClientWireSnapshot
	Database DatabaseWireSnapshot
}

// WireStats accumulates wire counters for a proxy instance (single-threaded,
// matching the sans-I/O engine).
type WireStats struct {
	snap WireSnapshot
}

// Snapshot returns a plain value copy of the current wire counters.
func (w *WireStats) Snapshot() WireSnapshot {
	if w == nil {
		return WireSnapshot{}
	}
	return w.snap
}

func sumInt64(xs []int64) int64 {
	var t int64
	for _, x := range xs {
		t += x
	}
	return t
}

// ErrConservation is wrapped by WireSnapshot.Check when startup conservation
// or TLS-request accounting fails.
var ErrConservation = errors.New("pgwire: wire snapshot conservation")

// Check verifies startup conservation on both legs (begun == completed +
// failed + in progress), non-negative startup gauges, no unreasoned failures,
// and unanswered database TLS requests bounded by database sessions in startup.
func (s WireSnapshot) Check() error {
	var errs []error
	c := s.Client
	if got := c.StartupsCompleted + sumInt64(c.StartupsFailed[:]) + c.InStartup; got != c.StartupsBegun {
		errs = append(errs, fmt.Errorf("%w: client startups begun %d != completed %d + failed %d + in progress %d",
			ErrConservation, c.StartupsBegun, c.StartupsCompleted, sumInt64(c.StartupsFailed[:]), c.InStartup))
	}
	d := s.Database
	if got := d.StartupsCompleted + sumInt64(d.StartupsFailed[:]) + d.InStartup; got != d.StartupsBegun {
		errs = append(errs, fmt.Errorf("%w: database startups begun %d != completed %d + failed %d + in progress %d",
			ErrConservation, d.StartupsBegun, d.StartupsCompleted, sumInt64(d.StartupsFailed[:]), d.InStartup))
	}
	if c.InStartup < 0 || d.InStartup < 0 {
		errs = append(errs, fmt.Errorf("%w: negative sessions in startup", ErrConservation))
	}
	if c.StartupsFailed[ReasonNone] != 0 || d.StartupsFailed[ClassNone] != 0 {
		errs = append(errs, fmt.Errorf("%w: a startup failed without a reason", ErrConservation))
	}
	if waiting := d.TLSRequests - sumInt64(d.TLSAnswers[:]); waiting < 0 || waiting > d.InStartup {
		errs = append(errs, fmt.Errorf("%w: database TLS requests %d, answers %d, sessions in startup %d",
			ErrConservation, d.TLSRequests, sumInt64(d.TLSAnswers[:]), d.InStartup))
	}
	return errors.Join(errs...)
}
