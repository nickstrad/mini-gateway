package workload

import (
	"slices"
	"strings"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/sim"
)

// Outcome classifies the final disposition of one intended client request.
type Outcome uint8

// Terminal request outcome categories used in counters and trace records.
const (
	OutcomeOK Outcome = iota + 1
	OutcomeError
	OutcomeLost
	OutcomeTimedOut
	OutcomeClientLeft
	OutcomeConnectFailed
	OutcomeDropped
)

var outcomeNames = [...]string{"", "ok", "error", "connection-lost", "timed-out", "client-left", "connect-failed", "dropped"}

// String returns the canonical lowercase outcome name.
func (o Outcome) String() string {
	if int(o) < len(outcomeNames) {
		return outcomeNames[o]
	}
	return "?"
}

// MaxCodes bounds the number of distinct SQLSTATE codes tracked per tenant.
const MaxCodes = 64

// CodeCount records how many requests for a tenant ended with a given SQLSTATE code.
type CodeCount struct {
	Code  string
	Count int64
}

type tenantStats struct {
	intended       int64
	sent           int64
	notYet         obs.Gauge
	inFlight       obs.Gauge
	dropped        int64
	unsentFailed   int64
	ok             int64
	errs           int64
	lost           int64
	timedOut       int64
	left           int64
	codes          []CodeCount
	codesOther     int64
	retries        int64
	connects       int64
	connectsOK     int64
	connectsFailed int64
	reconnects     int64
	open           obs.Gauge
	halfMessages   int64
	stopReads      int64
	cancelsSent    int64
	disconnects    int64
	burstDrops     int64
}

func (t *tenantStats) code(c string) {
	if c == "" {
		return
	}
	i, found := slices.BinarySearchFunc(t.codes, c, func(x CodeCount, k string) int { return strings.Compare(x.Code, k) })
	if found {
		t.codes[i].Count++
		return
	}
	if len(t.codes) >= MaxCodes {
		t.codesOther++
		return
	}
	t.codes = slices.Insert(t.codes, i, CodeCount{Code: c, Count: 1})
}

func (t *tenantStats) check() error {
	if err := obs.Conserve(obs.T("intended", t.intended), obs.T("sent", t.sent), obs.T("not yet sent", t.notYet.Value),
		obs.T("dropped", t.dropped), obs.T("failed before send", t.unsentFailed)); err != nil {
		return err
	}
	return obs.Conserve(obs.T("sent", t.sent), obs.T("ok", t.ok), obs.T("error", t.errs), obs.T("lost", t.lost),
		obs.T("timed out", t.timedOut), obs.T("client left", t.left), obs.T("in flight", t.inFlight.Value))
}

func (t *tenantStats) write(w sim.StateSink) {
	w.Int(t.intended, t.sent, t.notYet.Value, t.notYet.Peak, t.inFlight.Value, t.inFlight.Peak, t.dropped, t.unsentFailed)
	w.Int(t.ok, t.errs, t.lost, t.timedOut, t.left, t.retries, t.connects, t.connectsOK, t.connectsFailed, t.reconnects)
	w.Int(t.open.Value, t.open.Peak, t.halfMessages, t.stopReads, t.cancelsSent, t.disconnects, t.burstDrops, t.codesOther)
	for _, c := range t.codes {
		w.String(c.Code)
		w.Int(c.Count)
	}
}

// TenantSnapshot is a plain value struct of one tenant's (or the summed total's)
// request counters, connection gauges, error code histogram, and latency histograms.
type TenantSnapshot struct {
	Name             string
	Intended         int64
	Sent             int64
	NotYetSent       obs.Gauge
	InFlight         obs.Gauge
	Dropped          int64
	FailedBeforeSend int64
	OK               int64
	Errors           int64
	Lost             int64
	TimedOut         int64
	ClientLeft       int64
	ErrorsByCode     []CodeCount
	ErrorsOtherCodes int64
	Retries          int64
	Connects         int64
	ConnectsOK       int64
	ConnectsFailed   int64
	Reconnects       int64
	ConnsOpen        obs.Gauge
	HalfMessages     int64
	StoppedReading   int64
	CancelsSent      int64
	Disconnects      int64
	BurstDrops       int64
	Latency          obs.HistogramSnapshot
	LatencyFromSend  obs.HistogramSnapshot
}

// FaultSnapshot counts workload faults applied by kind and missed targets.
type FaultSnapshot struct {
	ByKind   [6]int64
	NoTarget int64
}

// Snapshot is a plain value copy of all workload metrics across every tenant and in total.
type Snapshot struct {
	Clients  int64
	Finished int64
	Faults   FaultSnapshot
	Total    TenantSnapshot
	Tenants  []TenantSnapshot
}

func (t *tenantState) snapshot() TenantSnapshot {
	s := &t.st
	return TenantSnapshot{
		Name: t.cfg.Name, Intended: s.intended, Sent: s.sent, NotYetSent: s.notYet, InFlight: s.inFlight,
		Dropped: s.dropped, FailedBeforeSend: s.unsentFailed, OK: s.ok, Errors: s.errs, Lost: s.lost,
		TimedOut: s.timedOut, ClientLeft: s.left, ErrorsByCode: slices.Clone(s.codes), ErrorsOtherCodes: s.codesOther,
		Retries: s.retries, Connects: s.connects, ConnectsOK: s.connectsOK, ConnectsFailed: s.connectsFailed,
		Reconnects: s.reconnects, ConnsOpen: s.open, HalfMessages: s.halfMessages, StoppedReading: s.stopReads,
		CancelsSent: s.cancelsSent, Disconnects: s.disconnects, BurstDrops: s.burstDrops,
		Latency: t.latency.Snapshot(), LatencyFromSend: t.fromSend.Snapshot(),
	}
}

// Snapshot returns a plain value snapshot of the workload's per-tenant and merged total metrics.
func (w *Workload) Snapshot() Snapshot {
	out := Snapshot{Clients: int64(len(w.clients)), Total: TenantSnapshot{Name: "total"},
		Faults: FaultSnapshot{ByKind: w.faults.byKind, NoTarget: w.faults.noTarget}}
	total := obsTotal{lat: obs.NewDurationHistogram(), send: obs.NewDurationHistogram()}
	for _, c := range w.clients {
		if c.finished() {
			out.Finished++
		}
	}
	for _, t := range w.tenants {
		ts := t.snapshot()
		out.Tenants = append(out.Tenants, ts)
		total.add(&out.Total, ts)
		_ = total.lat.Merge(t.latency)
		_ = total.send.Merge(t.fromSend)
	}
	out.Total.Latency = total.lat.Snapshot()
	out.Total.LatencyFromSend = total.send.Snapshot()
	return out
}

type obsTotal struct {
	lat  *obs.Histogram
	send *obs.Histogram
}

func (o obsTotal) add(t *TenantSnapshot, s TenantSnapshot) {
	t.Intended += s.Intended
	t.Sent += s.Sent
	t.NotYetSent.Value += s.NotYetSent.Value
	t.NotYetSent.Peak += s.NotYetSent.Peak
	t.InFlight.Value += s.InFlight.Value
	t.InFlight.Peak += s.InFlight.Peak
	t.Dropped += s.Dropped
	t.FailedBeforeSend += s.FailedBeforeSend
	t.OK += s.OK
	t.Errors += s.Errors
	t.Lost += s.Lost
	t.TimedOut += s.TimedOut
	t.ClientLeft += s.ClientLeft
	for _, c := range s.ErrorsByCode {
		i, found := slices.BinarySearchFunc(t.ErrorsByCode, c.Code, func(x CodeCount, k string) int { return strings.Compare(x.Code, k) })
		if found {
			t.ErrorsByCode[i].Count += c.Count
		} else {
			t.ErrorsByCode = slices.Insert(t.ErrorsByCode, i, c)
		}
	}
	t.ErrorsOtherCodes += s.ErrorsOtherCodes
	t.Retries += s.Retries
	t.Connects += s.Connects
	t.ConnectsOK += s.ConnectsOK
	t.ConnectsFailed += s.ConnectsFailed
	t.Reconnects += s.Reconnects
	t.ConnsOpen.Value += s.ConnsOpen.Value
	t.ConnsOpen.Peak += s.ConnsOpen.Peak
	t.HalfMessages += s.HalfMessages
	t.StoppedReading += s.StoppedReading
	t.CancelsSent += s.CancelsSent
	t.Disconnects += s.Disconnects
	t.BurstDrops += s.BurstDrops
}

// Code returns the count of requests that ended with SQLSTATE code c, or 0.
func (s TenantSnapshot) Code(c string) int64 {
	for _, x := range s.ErrorsByCode {
		if x.Code == c {
			return x.Count
		}
	}
	return 0
}

// Check verifies the two workload conservation laws (intended and sent) on this snapshot.
func (s TenantSnapshot) Check() error {
	if err := obs.Conserve(obs.T("intended", s.Intended), obs.T("sent", s.Sent), obs.T("not yet sent", s.NotYetSent.Value),
		obs.T("dropped", s.Dropped), obs.T("failed before send", s.FailedBeforeSend)); err != nil {
		return err
	}
	return obs.Conserve(obs.T("sent", s.Sent), obs.T("ok", s.OK), obs.T("error", s.Errors), obs.T("lost", s.Lost),
		obs.T("timed out", s.TimedOut), obs.T("client left", s.ClientLeft), obs.T("in flight", s.InFlight.Value))
}
