package pgwire

import (
	"errors"
	"fmt"
)

type Reason uint8

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
	ReasonTerminate
	NumReasons
)

var reasonNames = [NumReasons]string{"none", "client-closed", "deadline", "protocol-violation", "tls-failed",
	"plaintext-refused", "protocol-version", "parameter-refused", "no-user", "auth-failed", "unknown-database",
	"cancel-request", "terminate"}

func (r Reason) String() string {
	if r < NumReasons {
		return reasonNames[r]
	}
	return "unknown"
}

type Class uint8

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

func (c Class) String() string {
	if c < NumClasses {
		return classNames[c]
	}
	return "unknown"
}

type Violation uint8

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
	NumViolations
)

var violationNames = [NumViolations]string{"none", "bad-length", "too-large", "bytes-before-handshake",
	"second-tls-request", "second-gss-request", "bad-startup-packet", "unexpected-message", "bad-message",
	"bad-tls-answer", "unknown-message"}

func (v Violation) String() string {
	if v < NumViolations {
		return violationNames[v]
	}
	return "unknown"
}

type RequestKind uint8

const (
	RequestTLS RequestKind = iota
	RequestGSS
	RequestCancel
	NumRequestKinds
)

var requestKindNames = [NumRequestKinds]string{"tls", "gss", "cancel"}

func (k RequestKind) String() string {
	if k < NumRequestKinds {
		return requestKindNames[k]
	}
	return "unknown"
}

type Answer uint8

const (
	AnswerYes Answer = iota
	AnswerNo
	AnswerNone
	NumAnswers
)

var answerNames = [NumAnswers]string{"S", "N", "none"}

func (a Answer) String() string {
	if a < NumAnswers {
		return answerNames[a]
	}
	return "unknown"
}

type HandshakeOutcome uint8

const (
	HandshakeCompleted HandshakeOutcome = iota
	HandshakeFailed
	HandshakeFailedVerification
	HandshakeDeadline
	HandshakeViolation
	NumHandshakeOutcomes
)

var handshakeNames = [NumHandshakeOutcomes]string{"completed", "failed", "failed-verification", "deadline", "protocol-violation"}

func (h HandshakeOutcome) String() string {
	if h < NumHandshakeOutcomes {
		return handshakeNames[h]
	}
	return "unknown"
}

type TLSVersion uint8

const (
	TLSVersionOther TLSVersion = iota
	TLSVersion12
	TLSVersion13
	NumTLSVersions
)

var tlsVersionNames = [NumTLSVersions]string{"other", "1.2", "1.3"}

func (v TLSVersion) String() string {
	if v < NumTLSVersions {
		return tlsVersionNames[v]
	}
	return "unknown"
}

type HandshakeResult struct {
	Outcome HandshakeOutcome
	Version TLSVersion
}

type SideCounters struct {
	MessagesIn  int64
	MessagesOut int64
	BytesIn     int64
	BytesOut    int64
	Violations  [NumViolations]int64
	Oversized   int64
}

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

type WireSnapshot struct {
	Client   ClientWireSnapshot
	Database DatabaseWireSnapshot
}

type WireStats struct {
	snap WireSnapshot
}

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

var ErrConservation = errors.New("pgwire: wire snapshot conservation")

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
	if c.StartupsFailed[ReasonNone] != 0 || c.StartupsFailed[ReasonTerminate] != 0 || d.StartupsFailed[ClassNone] != 0 {
		errs = append(errs, fmt.Errorf("%w: a startup failed without a reason", ErrConservation))
	}
	if d.TLSRequests != sumInt64(d.TLSAnswers[:]) {
		errs = append(errs, fmt.Errorf("%w: database TLS requests %d != answers %d", ErrConservation, d.TLSRequests, sumInt64(d.TLSAnswers[:])))
	}
	return errors.Join(errs...)
}
