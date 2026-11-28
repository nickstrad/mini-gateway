package coalesce

import (
	"errors"
	"fmt"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/work"
)

// Cap identifies which coalescer bound was reached on an overflow or detach.
type Cap uint8

const (
	// CapWaiters means a flight reached Config.MaxWaiters (owner included).
	CapWaiters Cap = iota
	// CapReply means a flight's stored reply exceeded Config.MaxReplyBytes.
	CapReply
	// CapFlights means distinct live keys reached Config.MaxFlights.
	CapFlights
	// CapUnshareable means a completed reply ended with a non-idle transaction status.
	CapUnshareable
	// NumCaps is the sentinel count of Cap values.
	NumCaps
)

var capNames = [NumCaps]string{"waiters", "reply", "flights", "unshareable"}

// String returns the canonical reason label for c, or "?" if out of range.
func (c Cap) String() string {
	if c < NumCaps {
		return capNames[c]
	}
	return "?"
}

// TenantHits records the cumulative coalesced hits for one tenant (or OtherTenant).
type TenantHits struct {
	Tenant work.TenantID
	Hits   int64
}

// HotKey records one entry from the opt-in Space-Saving hot-key tracker.
// Key text stays inside the snapshot; exporters emit rank counts only.
type HotKey struct {
	Tenant work.TenantID
	Text   string
	Count  int64
}

// Snapshot is a plain-value copy of Table counters, gauges, histogram,
// per-tenant hits, and opt-in hot-key counts.
type Snapshot struct {
	Mode          Mode
	Join          JoinMode
	MaxFlights    int64
	MaxWaiters    int64
	MaxReplyBytes int64
	NoShareMax    int64
	NoShareFor    int64
	HotKeysMax    int64

	NotEligible     int64
	Offered         int64
	Started         int64
	Hits            int64
	Refused         int64
	PassedNoShare   int64
	PassedFull      int64
	PassedDuplicate int64
	Held            int64
	Promoted        int64
	Sent            int64

	Completed    int64
	Failed       int64
	Cancelled    int64
	Expired      int64
	Detached     int64
	SharedErrors int64
	Handoffs     int64

	WaitersIn         int64
	WaiterReply       int64
	WaiterSharedError int64
	WaiterOwnError    int64
	WaiterGone        int64
	WaiterRefused     int64
	FollowersLeft     int64
	LeftUnknown       int64

	Overflows [NumCaps]int64

	NoShareAdded   int64
	NoShareExpired int64
	NoShareEvicted int64
	NoShareRenewed int64

	Waiting     obs.Gauge
	Flights     obs.Gauge
	Running     obs.Gauge
	Entries     obs.Gauge
	StoredBytes obs.Gauge
	NoShare     obs.Gauge

	WaitersPerFlight obs.HistogramSnapshot
	PerTenant        []TenantHits
	HotKeys          []HotKey
}

// Snapshot returns a full, sorted, plain-value copy of t's observability state.
func (t *Table) Snapshot() Snapshot {
	s := t.snap
	s.WaitersPerFlight = t.waiters.Snapshot()
	s.PerTenant = t.tenants.snapshot()
	s.HotKeys = t.hot.snapshot()
	return s
}

// Counters returns t's scalar counters and gauges without cloning slices or histograms.
func (t *Table) Counters() Snapshot { return t.snap }

// Check verifies exact conservation across offers, waiters, flights, and
// no-share entries, plus table bounds and per-tenant hit totals.
func (s Snapshot) Check() error {
	var errs []error
	add := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}
	add(obs.Conserve(obs.T("offered", s.Offered), obs.T("started", s.Started), obs.T("hits", s.Hits),
		obs.T("refused", s.Refused), obs.T("passed_no_share", s.PassedNoShare), obs.T("passed_full", s.PassedFull),
		obs.T("passed_duplicate", s.PassedDuplicate)))
	add(obs.Conserve(obs.T("waiters_in", s.WaitersIn), obs.T("reply", s.WaiterReply), obs.T("shared_error", s.WaiterSharedError),
		obs.T("own_error", s.WaiterOwnError), obs.T("gone", s.WaiterGone), obs.T("refused", s.WaiterRefused),
		obs.T("waiting", s.Waiting.Value)))
	add(obs.Conserve(obs.T("started", s.Started), obs.T("completed", s.Completed), obs.T("failed", s.Failed),
		obs.T("cancelled", s.Cancelled), obs.T("expired", s.Expired), obs.T("flights", s.Flights.Value)))
	add(obs.Conserve(obs.T("no_share_added", s.NoShareAdded), obs.T("expired", s.NoShareExpired),
		obs.T("evicted", s.NoShareEvicted), obs.T("renewed", s.NoShareRenewed), obs.T("listed", s.NoShare.Value)))
	if s.Entries.Value > s.MaxFlights || s.Entries.Value > s.Flights.Value {
		errs = append(errs, fmt.Errorf("%d keys for %d flights, cap %d", s.Entries.Value, s.Flights.Value, s.MaxFlights))
	}
	if s.Running.Value > s.Entries.Value {
		errs = append(errs, fmt.Errorf("%d flights running for %d keys", s.Running.Value, s.Entries.Value))
	}
	if s.NoShare.Value > s.NoShareMax {
		errs = append(errs, fmt.Errorf("%d keys on the no-share list, cap %d", s.NoShare.Value, s.NoShareMax))
	}
	if s.StoredBytes.Value < 0 || s.StoredBytes.Value > s.Flights.Value*(s.MaxReplyBytes+maxStep) {
		errs = append(errs, fmt.Errorf("%d bytes stored for %d flights", s.StoredBytes.Value, s.Flights.Value))
	}
	var hits int64
	for _, h := range s.PerTenant {
		hits += h.Hits
	}
	if s.PerTenant != nil && hits != s.Hits {
		errs = append(errs, fmt.Errorf("per-tenant hits %d, total %d", hits, s.Hits))
	}
	return errors.Join(errs...)
}

const maxStep = 1 << 26

// Check verifies snapshot conservation and internal consistency between the
// creation-ordered flight list, key entries, member map, and gauges.
func (t *Table) Check() error {
	if err := t.snap.Check(); err != nil {
		return err
	}
	running := int64(0)
	members := 0
	flights := 0
	for fi := t.head; fi != none; fi = t.flights[fi].next {
		f := t.flights[fi]
		flights++
		members += 1 + len(f.followers)
		if f.state == stateRunning {
			running++
		}
		if len(f.followers) >= t.cfg.MaxWaiters && t.cfg.Plant&PlantNoWaiterCap == 0 {
			return fmt.Errorf("flight %d has %d followers, cap %d waiters", f.id, len(f.followers), t.cfg.MaxWaiters)
		}
		e := t.entries[f.entry]
		if e.run != fi && e.hold != fi {
			return fmt.Errorf("flight %d is not on its key", f.id)
		}
		if e.hold == fi && f.state != stateHeld {
			return fmt.Errorf("flight %d waits behind a running flight but is %d", f.id, f.state)
		}
		if f.prev == none && t.head != fi || f.next != none && t.flights[f.next].deadline < f.deadline {
			return fmt.Errorf("flight %d out of deadline order", f.id)
		}
	}
	if int64(flights) != t.snap.Flights.Value || running != t.snap.Running.Value {
		return fmt.Errorf("%d flights (%d running) listed, gauges say %d (%d)", flights, running, t.snap.Flights.Value, t.snap.Running.Value)
	}
	if members != len(t.members) || int64(members) != t.snap.Waiting.Value {
		return fmt.Errorf("%d waiters on flights, %d members, gauge %d", members, len(t.members), t.snap.Waiting.Value)
	}
	if len(t.byID) != flights {
		return fmt.Errorf("%d flight ids for %d flights", len(t.byID), flights)
	}
	return nil
}

// Quiesce verifies that no flights, waiters, no-share entries, stored bytes,
// or undrained notices remain in t.
func (t *Table) Quiesce() error {
	var errs []error
	if t.head != none || len(t.byKey) != 0 || len(t.members) != 0 {
		errs = append(errs, fmt.Errorf("coalescer: %d keys, %d flights, %d waiters", len(t.byKey), t.snap.Flights.Value, len(t.members)))
	}
	if t.noShare.n != 0 {
		errs = append(errs, fmt.Errorf("coalescer: %d keys on the no-share list", t.noShare.n))
	}
	if t.noticeHead < len(t.notices) {
		errs = append(errs, fmt.Errorf("coalescer: %d notices not taken", len(t.notices)-t.noticeHead))
	}
	if t.snap.StoredBytes.Value != 0 {
		errs = append(errs, fmt.Errorf("coalescer: %d reply bytes stored", t.snap.StoredBytes.Value))
	}
	return errors.Join(errs...)
}
