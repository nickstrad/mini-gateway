package pool

import (
	"errors"
	"fmt"

	"github.com/nickstrad/mini-gateway/internal/obs"
)

type Snapshot struct {
	Cap           int64
	MaxConnecting int64
	RatePerSec    int64
	MaxCancels    int64

	Open               obs.Gauge
	Connecting         obs.Gauge
	OpenPlusConnecting obs.Gauge
	Idle               obs.Gauge
	Lent               obs.Gauge
	Checking           obs.Gauge
	Cleaning           obs.Gauge
	Resetting          obs.Gauge
	Suspect            obs.Gauge

	Dials        int64
	DialsOK      int64
	DialFailures int64
	RateLimited  int64
	Closes       [NumCloseReasons]int64

	Checkouts        int64
	CheckoutsLent    int64
	CheckoutsRefused int64
	Exhausted        int64
	LentAsIs         int64
	TenantSwitches   int64

	Ends        [NumEndKinds]int64
	DirtyReused [NumEndKinds]int64
	DirtyClosed [NumEndKinds]int64
	Rollbacks   int64

	Resets       [NumResetCauses]int64
	ResetsOK     [NumResetCauses]int64
	ResetsFailed [NumResetCauses]int64
	Reseeds      int64

	Checks         int64
	ChecksByResult [NumCheckResults]int64

	Cancels         int64
	ClientCancels   int64
	CancelsDone     int64
	CancelsExpired  int64
	CancelsRefused  int64
	CancelsInFlight obs.Gauge

	NoReply int64

	Breaker            int64
	BreakerTransitions [NumTransitions]int64
	BreakerTrials      int64
	BreakerFailures    int64

	LeaseTime     obs.HistogramSnapshot
	IdleAgeAtLend obs.HistogramSnapshot
}

func sum(v []int64) int64 {
	var n int64
	for _, x := range v {
		n += x
	}
	return n
}

func (s Snapshot) Check() error {
	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if s.Open.Value+s.Connecting.Value > s.Cap {
		fail("pool: %d open + %d connecting above the cap %d", s.Open.Value, s.Connecting.Value, s.Cap)
	}
	if s.Open.Value != s.Idle.Value+s.Lent.Value+s.Checking.Value+s.Cleaning.Value+s.Resetting.Value {
		fail("pool: open %d != idle %d + lent %d + checking %d + cleaning %d + resetting %d", s.Open.Value, s.Idle.Value,
			s.Lent.Value, s.Checking.Value, s.Cleaning.Value, s.Resetting.Value)
	}
	if s.Dials != s.DialsOK+s.DialFailures+s.Connecting.Value {
		fail("pool: dials %d != opened %d + failed %d + connecting %d", s.Dials, s.DialsOK, s.DialFailures, s.Connecting.Value)
	}
	if s.DialFailures != s.Closes[CloseConnectFailed] {
		fail("pool: dial failures %d != connect_failed closes %d", s.DialFailures, s.Closes[CloseConnectFailed])
	}
	if closed := sum(s.Closes[:]) - s.Closes[CloseConnectFailed]; s.DialsOK != closed+s.Open.Value {
		fail("pool: opened %d != closed %d + open %d", s.DialsOK, closed, s.Open.Value)
	}
	if s.Checkouts != s.CheckoutsLent+s.CheckoutsRefused {
		fail("pool: checkouts %d != lent %d + refused %d", s.Checkouts, s.CheckoutsLent, s.CheckoutsRefused)
	}
	if s.CheckoutsLent != sum(s.Ends[:])+s.Lent.Value {
		fail("pool: leases %d != ended %d + lent %d", s.CheckoutsLent, sum(s.Ends[:]), s.Lent.Value)
	}
	if s.Checks != sum(s.ChecksByResult[:])+s.Checking.Value {
		fail("pool: checks %d != results %d + checking %d", s.Checks, sum(s.ChecksByResult[:]), s.Checking.Value)
	}
	if s.Cancels != s.CancelsDone+s.CancelsExpired+s.CancelsInFlight.Value {
		fail("pool: cancels %d != done %d + expired %d + in flight %d", s.Cancels, s.CancelsDone, s.CancelsExpired, s.CancelsInFlight.Value)
	}
	if s.CancelsInFlight.Value > s.MaxCancels {
		fail("pool: %d cancels in flight above the cap %d", s.CancelsInFlight.Value, s.MaxCancels)
	}
	for k := range NumResetCauses {
		if s.Resets[k] < s.ResetsOK[k]+s.ResetsFailed[k] {
			fail("pool: %s resets %d < ok %d + failed %d", k, s.Resets[k], s.ResetsOK[k], s.ResetsFailed[k])
		}
	}
	if inFlight := sum(s.Resets[:]) - sum(s.ResetsOK[:]) - sum(s.ResetsFailed[:]); inFlight != s.Resetting.Value {
		fail("pool: resets in flight %d != resetting %d", inFlight, s.Resetting.Value)
	}
	if s.Suspect.Value > s.Open.Value {
		fail("pool: %d suspect above %d open", s.Suspect.Value, s.Open.Value)
	}
	return errors.Join(errs...)
}
