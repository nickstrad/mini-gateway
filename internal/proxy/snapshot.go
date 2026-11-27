package proxy

import (
	"errors"
	"fmt"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
)

// ResetFailure classifies why a post-lease DISCARD ALL reset failed and caused
// the database connection to be closed.
type ResetFailure uint8

const (
	// ResetFailError records an ErrorResponse during reset.
	ResetFailError ResetFailure = iota
	// ResetFailWrongTag records an unexpected, missing, or duplicate CommandComplete tag.
	ResetFailWrongTag
	// ResetFailWrongStatus records a ReadyForQuery status byte other than 'I'.
	ResetFailWrongStatus
	// ResetFailDeadline records a reset exceeding timeouts.reset.
	ResetFailDeadline
	// ResetFailConnLost records the database closing before the reset completed.
	ResetFailConnLost
	// ResetFailSurprise records an unexpected message, framing error, or trailing bytes.
	ResetFailSurprise
	// ResetFailClosed records a reset aborted by proxy shutdown.
	ResetFailClosed
	// NumResetFailures is the cardinality of ResetFailure for metric arrays.
	NumResetFailures
)

var resetFailNames = [NumResetFailures]string{"error", "wrong-tag", "wrong-status", "deadline", "connection-lost",
	"protocol-surprise", "closed"}

// String returns the canonical metric label name for the reset failure.
func (r ResetFailure) String() string {
	if r < NumResetFailures {
		return resetFailNames[r]
	}
	return "?"
}

// Outcome classifies the final disposition of an accepted or rejected request.
type Outcome uint8

const (
	// OutcomeCompleted indicates a reply reached its end marker.
	OutcomeCompleted Outcome = iota
	// OutcomeFailed indicates an accepted request failed after acceptance.
	OutcomeFailed
	// OutcomeShed indicates a request was refused by the queue (full or expired).
	OutcomeShed
	// OutcomeRejected indicates a request was refused before acceptance.
	OutcomeRejected
	// NumOutcomes is the cardinality of Outcome.
	NumOutcomes
)

var outcomeNames = [NumOutcomes]string{"completed", "failed", "shed", "rejected"}

// String returns the canonical outcome label name.
func (o Outcome) String() string {
	if o < NumOutcomes {
		return outcomeNames[o]
	}
	return "?"
}

// NumTLSVersions is the number of client TLS buckets (plain, TLS 1.2, TLS 1.3).
const NumTLSVersions = 3

// Snapshot is a plain value copy of all engine counters, gauges, and histograms
// exported under component "engine".
type Snapshot struct {
	ClientsConnected obs.Gauge
	ClientsInStartup obs.Gauge
	ClientsInTx      obs.Gauge
	ClientsByTLS     [NumTLSVersions]int64
	ClientsAccepted  int64
	ClientsRefused   int64
	ClientCloses     [NumCloseReasons]int64
	CancelRequests   int64

	Received          int64
	Accepted          int64
	Completed         int64
	Failed            int64
	Shed              int64
	Rejected          int64
	Queued            obs.Gauge
	InFlight          obs.Gauge
	ByRow             [pgwire.ErrRowCount]int64
	StagePassed       [NumStages]int64
	StageRefuse       [NumStages]int64
	FailedNoReply     int64
	NonRequestReplies int64
	ReachedNo         int64
	ReachedYes        int64
	RetryAsked        int64

	StatementTime obs.HistogramSnapshot
	QueueWait     obs.HistogramSnapshot
	QueueCap      int64

	RelayHighWater  int64
	RelayCap        int64
	RelayPauses     int64
	ClientsDropped  int64
	ParamStatusTurn int64

	LeasesStarted    int64
	LeasesEnded      int64
	LeasesEndedReset int64
	LeasesEndedClose int64
	LeasesEndedAsIs  int64
	ResetsStarted    int64
	ResetsCompleted  int64
	ResetsFailed     [NumResetFailures]int64
	Resetting        obs.Gauge
	ResetTime        obs.HistogramSnapshot
	IgnoredDBData    int64

	DBOpen         obs.Gauge
	DBConnecting   obs.Gauge
	DBLent         obs.Gauge
	DBIdle         obs.Gauge
	DBDials        int64
	DBDialFailures int64
	DBReady        int64
	DBCloses       [NumCloseReasons]int64
	DBConnectFails [pgwire.NumClasses]int64
	DBCap          int64
	DBDown         bool

	TimersPending obs.Gauge
	Publishes     int64
	Draining      bool
	Events        int64

	LeasesEndedDirty   int64
	ResetsNotAtRelease int64
	Reseeds            int64
	NoReply            int64
	CancelsSent        int64
	CancelsDone        int64
	CancelConns        obs.Gauge
	CancelsForwarded   int64
	CancelsQueued      int64
	CancelsRefused     int64
	CancelsUnmatched   int64
	CancelsIgnored     int64
	BreakerFlushed     int64
	IdleInTxRolledBack int64

	RetryOffered    int64
	RetriedRequests int64
	RetryPending    obs.Gauge
	RetryEnds       [NumRetryEnds]int64
}

// Check enforces exact counter conservation and capacity invariants on s.
func (s Snapshot) Check() error {
	var errs []error
	if err := obs.Conserve(obs.T("received", s.Received), obs.T("accepted", s.Accepted), obs.T("rejected", s.Rejected)); err != nil {
		errs = append(errs, err)
	}
	if err := obs.Conserve(obs.T("accepted", s.Accepted), obs.T("completed", s.Completed), obs.T("failed", s.Failed),
		obs.T("shed", s.Shed), obs.T("queued", s.Queued.Value), obs.T("in_flight", s.InFlight.Value)); err != nil {
		errs = append(errs, err)
	}
	var rows int64
	for _, v := range s.ByRow {
		rows += v
	}
	if want := s.Failed + s.Shed + s.Rejected; rows+s.FailedNoReply != want+s.NonRequestReplies {
		errs = append(errs, fmt.Errorf("replies by row %d + failures without a reply %d, want failed+shed+rejected %d", rows, s.FailedNoReply, want))
	}
	if s.ReachedNo+s.ReachedYes != s.Failed {
		errs = append(errs, fmt.Errorf("reached facts %d+%d for %d failures", s.ReachedNo, s.ReachedYes, s.Failed))
	}
	var failed int64
	for _, v := range s.ResetsFailed {
		failed += v
	}
	if s.ResetsStarted != s.ResetsCompleted+failed+s.Resetting.Value {
		errs = append(errs, fmt.Errorf("resets started %d != completed %d + failed %d + resetting %d", s.ResetsStarted,
			s.ResetsCompleted, failed, s.Resetting.Value))
	}
	if s.LeasesEnded != s.LeasesEndedReset+s.LeasesEndedClose+s.LeasesEndedAsIs+s.LeasesEndedDirty {
		errs = append(errs, fmt.Errorf("leases ended %d != reset %d + closed %d + as-is %d + dirty %d", s.LeasesEnded,
			s.LeasesEndedReset, s.LeasesEndedClose, s.LeasesEndedAsIs, s.LeasesEndedDirty))
	}
	if s.LeasesEndedReset+s.ResetsNotAtRelease != s.ResetsStarted {
		errs = append(errs, fmt.Errorf("leases ended with a reset %d + other resets %d != resets started %d", s.LeasesEndedReset,
			s.ResetsNotAtRelease, s.ResetsStarted))
	}
	if s.CancelsSent != s.CancelsDone+s.CancelConns.Value {
		errs = append(errs, fmt.Errorf("cancels sent %d != done %d + open %d", s.CancelsSent, s.CancelsDone, s.CancelConns.Value))
	}
	if s.DBOpen.Value+s.DBConnecting.Value > s.DBCap {
		errs = append(errs, fmt.Errorf("database connections %d open + %d connecting above the cap %d", s.DBOpen.Value,
			s.DBConnecting.Value, s.DBCap))
	}
	if s.Queued.Value > s.QueueCap {
		errs = append(errs, fmt.Errorf("queue depth %d above its cap %d", s.Queued.Value, s.QueueCap))
	}
	if err := s.retryConserved(); err != nil {
		errs = append(errs, err)
	}
	if s.RelayHighWater > s.RelayCap {
		errs = append(errs, fmt.Errorf("relay buffer high-water %d above its cap %d", s.RelayHighWater, s.RelayCap))
	}
	return errors.Join(errs...)
}

// ShellSnapshot is a plain value copy of the goroutine shell's mailbox,
// goroutine, socket, and admin counters exported beside Engine.Snapshot.
type ShellSnapshot struct {
	MailboxDepth     obs.Gauge
	MailboxCap       int64
	MailboxFull      int64
	Goroutines       obs.Gauge
	ClientReaders    obs.Gauge
	ClientWriters    obs.Gauge
	DBReaders        obs.Gauge
	DBWriters        obs.Gauge
	Accepted         int64
	AdminRequests    int64
	HandshakesFailed int64
	WriteDeadlines   int64
	ReadDeadlines    int64
	AcceptRefused    int64
	AcceptErrors     int64
	QueueOverflows   int64
	StaleEvents      int64
}
