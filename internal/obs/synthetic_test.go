package obs

import (
	"math/rand/v2"
	"slices"
	"strconv"
	"testing"
)

type synth struct {
	requests map[Labels]int64
	inUse    int64
	largest  int64
	waits    map[string]*Histogram
	tenants  []string
}

func newSynth() *synth {
	return &synth{requests: map[Labels]int64{}, waits: map[string]*Histogram{}}
}

var synthOutcomes = []string{"ok", "failed", "shed"}

func synthMetrics() []MetricDesc {
	return []MetricDesc{
		CounterDesc("requests", LabelTenant|LabelOutcome, "Synthetic requests by tenant and outcome."),
		CounterDesc("events", 0, "Synthetic events."),
		GaugeDesc("in_use", 0, "Synthetic level that sums across instances."),
		HighWaterDesc("largest", 0, "Largest synthetic value seen; merges by max."),
		HistogramDesc("wait", LabelTenant, UnitNs, "Synthetic wait per tenant."),
	}
}

type synthEvent struct {
	tenant  string
	outcome string
	delta   int64
	value   int64
}

func randomEvents(r *rand.Rand, n, tenants int) []synthEvent {
	out := make([]synthEvent, n)
	for i := range out {
		out[i] = synthEvent{
			tenant:  "t" + strconv.Itoa(r.IntN(tenants)),
			outcome: synthOutcomes[r.IntN(len(synthOutcomes))],
			delta:   r.Int64N(5) - 2,
			value:   r.Int64N(2_000_000_000),
		}
	}
	return out
}

func (s *synth) apply(ev synthEvent) {
	s.requests[Labels{Tenant: ev.tenant, Outcome: ev.outcome}]++
	s.inUse += ev.delta
	s.largest = max(s.largest, ev.value)
	h := s.waits[ev.tenant]
	if h == nil {
		h = NewDurationHistogram()
		s.waits[ev.tenant] = h
		s.tenants = append(s.tenants, ev.tenant)
		slices.Sort(s.tenants)
	}
	h.Record(ev.value)
}

func (s *synth) emit(e *Emitter) {
	total := int64(0)
	for _, t := range s.tenants {
		for _, o := range synthOutcomes {
			n := s.requests[Labels{Tenant: t, Outcome: o}]
			total += n
			e.Counter("requests", Labels{Tenant: t, Outcome: o}, n)
		}
		if e.Detail() {
			e.Histogram("wait", Labels{Tenant: t}, s.waits[t].Snapshot())
		}
	}
	e.Counter("events", Labels{}, total)
	e.Gauge("in_use", Labels{}, s.inUse)
	e.Gauge("largest", Labels{}, s.largest)
}

func synthCollector(t testing.TB, instance string, s *synth, detail bool) *Collector {
	t.Helper()
	c := NewCollector(Config{Instance: instance, Build: "b1", ConfigFingerprint: "f1", Detail: detail})
	if err := c.Register(Source{Component: ComponentPool, Metrics: synthMetrics(), Collect: s.emit}); err != nil {
		t.Fatal(err)
	}
	return c
}

func onlyComponent(s InstanceSnapshot, comp string) InstanceSnapshot {
	out := s
	out.Points = nil
	out.Hists = nil
	for _, p := range s.Points {
		if p.Component == comp {
			out.Points = append(out.Points, p)
		}
	}
	for _, h := range s.Hists {
		if h.Component == comp {
			out.Hists = append(out.Hists, h)
		}
	}
	return out
}
