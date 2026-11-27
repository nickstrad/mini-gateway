package loadgen

import (
	"strconv"
	"strings"

	"github.com/nickstrad/mini-gateway/internal/obs"
)

const (
	nsPerMicro  = 1_000
	nsPerMilli  = 1_000_000
	tenths      = 10
	p50         = 50
	p99         = 99
	percentBase = 100
)

func AchievedTenths(s Snapshot) int64 {
	if s.Intended == 0 {
		return 0
	}
	return s.TargetRate * tenths * s.OnTime() / s.Intended
}

func FormatTenths(v int64) string {
	return strconv.FormatInt(v/tenths, 10) + "." + strconv.FormatInt(v%tenths, 10)
}

func FormatNs(ns int64) string {
	switch {
	case ns >= NsPerSecond:
		return FormatTenths(ns*tenths/NsPerSecond) + "s"
	case ns >= nsPerMilli:
		return FormatTenths(ns*tenths/nsPerMilli) + "ms"
	case ns >= nsPerMicro:
		return FormatTenths(ns*tenths/nsPerMicro) + "µs"
	}
	return strconv.FormatInt(ns, 10) + "ns"
}

func SnapshotLatencies(h obs.HistogramSnapshot) Latencies {
	return Latencies{P50: quantile(h, p50), P99: quantile(h, p99), Max: h.Max}
}

func quantile(h obs.HistogramSnapshot, p int64) int64 {
	if h.Count == 0 {
		return 0
	}
	rank := (h.Count*p + percentBase - 1) / percentBase
	rank = max(rank, 1)
	seen := int64(0)
	for _, b := range h.Buckets {
		seen += b.Count
		if seen >= rank {
			return b.Hi
		}
	}
	return h.Hi
}

func classes(s Snapshot) string {
	if len(s.ErrorsByClass) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(s.ErrorsByClass))
	for _, c := range s.ErrorsByClass {
		parts = append(parts, c.Class+":"+strconv.FormatInt(c.Count, 10))
	}
	return strings.Join(parts, " ")
}

type Latencies struct {
	P50, P99, Max int64
}

func StatusLine(elapsedNs int64, s Snapshot, l Latencies) string {
	var b strings.Builder
	b.WriteString("t=" + FormatTenths(elapsedNs*tenths/NsPerSecond) + "s")
	b.WriteString(" sent " + strconv.FormatInt(s.Sent, 10) + "/" + strconv.FormatInt(s.Intended, 10))
	b.WriteString(" completed " + strconv.FormatInt(s.CompletedTotal(), 10))
	b.WriteString(" errors " + classes(s))
	b.WriteString(" rate " + FormatTenths(AchievedTenths(s)) + "/" + strconv.FormatInt(s.TargetRate, 10) + "/s")
	b.WriteString(" p50 " + FormatNs(l.P50) + " p99 " + FormatNs(l.P99))
	b.WriteString(" late " + strconv.FormatInt(s.Late, 10) + " could-not-send " + strconv.FormatInt(s.CouldNotSend, 10))
	return b.String()
}

func Summary(s Snapshot, l Latencies, lfs Latencies) []string {
	i := func(v int64) string { return strconv.FormatInt(v, 10) }
	lines := []string{
		"intended " + i(s.Intended) + " of " + i(s.Planned) + " planned; sent " + i(s.Sent) + "; could not send " + i(s.CouldNotSend) +
			"; late " + i(s.Late) + "; in flight at the end " + i(s.InFlight),
		"completed " + i(s.CompletedTotal()) + ": ok " + i(s.Completed[OutcomeOK]) + ", db-error " + i(s.Completed[OutcomeDBError]) +
			", refused " + i(s.Completed[OutcomeRefused]) + ", lost " + i(s.Completed[OutcomeLost]),
		"errors by SQLSTATE class: " + classes(s),
		"latency from intended send: p50 " + FormatNs(l.P50) + " p99 " + FormatNs(l.P99) + " max " + FormatNs(l.Max),
		"latency from actual send:   p50 " + FormatNs(lfs.P50) + " p99 " + FormatNs(lfs.P99) + " max " + FormatNs(lfs.Max),
		"achieved " + FormatTenths(AchievedTenths(s)) + "/s on schedule of target " + i(s.TargetRate) + "/s",
	}
	if s.Shortfall() {
		lines = append(lines, "SHORTFALL: achieved "+FormatTenths(AchievedTenths(s))+"/s of target "+i(s.TargetRate)+"/s: "+
			i(s.Late)+" late, "+i(s.CouldNotSend)+" could not send")
	}
	return lines
}
