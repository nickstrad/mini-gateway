package proxy

import (
	"errors"
	"fmt"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
)

type ResetFailure uint8

const (
	ResetFailError ResetFailure = iota
	ResetFailWrongTag
	ResetFailWrongStatus
	ResetFailDeadline
	ResetFailConnLost
	ResetFailSurprise
	ResetFailClosed
	NumResetFailures
)

var resetFailNames = [NumResetFailures]string{"error", "wrong-tag", "wrong-status", "deadline", "connection-lost",
	"protocol-surprise", "closed"}

func (r ResetFailure) String() string {
	if r < NumResetFailures {
		return resetFailNames[r]
	}
	return "?"
}

type Outcome uint8

const (
	OutcomeCompleted Outcome = iota
	OutcomeFailed
	OutcomeShed
	OutcomeRejected
	NumOutcomes
)

var outcomeNames = [NumOutcomes]string{"completed", "failed", "shed", "rejected"}

func (o Outcome) String() string {
	if o < NumOutcomes {
		return outcomeNames[o]
	}
	return "?"
}

const NumTLSVersions = 3

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
}

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
	if s.LeasesEnded != s.LeasesEndedReset+s.LeasesEndedClose+s.LeasesEndedAsIs {
		errs = append(errs, fmt.Errorf("leases ended %d != reset %d + closed %d + as-is %d", s.LeasesEnded,
			s.LeasesEndedReset, s.LeasesEndedClose, s.LeasesEndedAsIs))
	}
	if s.LeasesEndedReset != s.ResetsStarted {
		errs = append(errs, fmt.Errorf("leases ended with a reset %d != resets started %d", s.LeasesEndedReset, s.ResetsStarted))
	}
	if s.DBOpen.Value+s.DBConnecting.Value > s.DBCap {
		errs = append(errs, fmt.Errorf("database connections %d open + %d connecting above the cap %d", s.DBOpen.Value,
			s.DBConnecting.Value, s.DBCap))
	}
	if s.Queued.Value > s.QueueCap {
		errs = append(errs, fmt.Errorf("queue depth %d above its cap %d", s.Queued.Value, s.QueueCap))
	}
	if s.RelayHighWater > s.RelayCap {
		errs = append(errs, fmt.Errorf("relay buffer high-water %d above its cap %d", s.RelayHighWater, s.RelayCap))
	}
	return errors.Join(errs...)
}

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
