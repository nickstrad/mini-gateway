package obs

import (
	"errors"
	"strings"
	"testing"
)

// TestDetailOffDropsHistogramsAtTheEmitter proves Emitter.Histogram suppresses
// histograms and clamp counters when detail is off while still rejecting invalid calls.
func TestDetailOffDropsHistogramsAtTheEmitter(t *testing.T) {
	h := NewDurationHistogram()
	h.Record(5000)
	h.Record(-1)
	snap := h.Snapshot()
	descs := []MetricDesc{HistogramDesc("wait", LabelTenant, UnitNs, "W."), CounterDesc("events", 0, "E.")}
	collect := func(detail bool, emit func(e *Emitter)) (InstanceSnapshot, error) {
		c := NewCollector(Config{Instance: "i", Detail: detail})
		if err := c.Register(Source{Component: ComponentPool, Metrics: descs, Collect: emit}); err != nil {
			t.Fatal(err)
		}
		return c.Collect()
	}
	unconditional := func(e *Emitter) {
		e.Counter("events", Labels{}, 1)
		e.Histogram("wait", Labels{Tenant: "a"}, snap)
	}
	on, err := collect(true, unconditional)
	if err != nil || len(on.Hists) != 1 || on.Value(ComponentPool, "wait"+ClampedLowSuffix, Labels{Tenant: "a"}) != 1 {
		t.Fatalf("detail on: %v %+v", err, on)
	}
	off, err := collect(false, unconditional)
	if err != nil {
		t.Fatal(err)
	}
	if len(off.Hists) != 0 {
		t.Fatalf("histogram exported with detail off: %+v", off.Hists)
	}
	for _, p := range off.Points {
		if strings.HasPrefix(p.Metric, "wait") {
			t.Fatalf("histogram clamp counter exported with detail off: %+v", p)
		}
	}
	bad := []struct {
		emit func(e *Emitter)
		want error
	}{
		{func(e *Emitter) { e.Counter("events", Labels{}, 1); e.Histogram("ghost", Labels{}, snap) }, ErrUndeclared},
		{func(e *Emitter) { e.Counter("events", Labels{}, 1); e.Histogram("wait", Labels{Reason: "r"}, snap) }, ErrBadLabel},
		{func(e *Emitter) { e.Counter("events", Labels{}, 1); e.Histogram("events", Labels{}, snap) }, ErrWrongKind},
	}
	for i, b := range bad {
		if _, err := collect(false, b.emit); !errors.Is(err, b.want) {
			t.Fatalf("case %d with detail off: want %v, got %v", i, b.want, err)
		}
	}
}

type flakyAdapter struct{ err error }

func (f *flakyAdapter) Name() string                  { return "flaky" }
func (f *flakyAdapter) Export(InstanceSnapshot) error { return f.err }

// TestCollectorHealthFollowsTheLatestPublishAndRecovers proves Collector.Health
// degrades immediately on publish 1 adapter or emit errors and recovers to ok once
// the failing adapter or source clears.
func TestCollectorHealthFollowsTheLatestPublishAndRecovers(t *testing.T) {
	ht := healthTable(t)
	c := NewCollector(Config{Instance: "i"})
	fl := &flakyAdapter{err: errors.New("disk full")}
	if err := c.Attach(fl); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Publish(); err != nil {
		t.Fatal(err)
	}
	if h := c.Health(ht); h.State != StateDegraded || !strings.Contains(h.Checks[0].Reason, "flaky") {
		t.Fatalf("after a failing first publish: %+v", h)
	}
	fl.err = nil
	if _, err := c.Publish(); err != nil {
		t.Fatal(err)
	}
	if h := c.Health(ht); h.State != StateOK {
		t.Fatalf("after the adapter recovered: %+v", h)
	}
	snap, _ := c.Last()
	if snap.Value(ComponentObs, MetricExportErrors, Labels{Reason: "flaky"}) != 1 {
		t.Fatalf("export errors are a counter and keep the failure: %+v", snap.Points)
	}
	broken := true
	if err := c.Register(Source{Component: ComponentPool, Metrics: []MetricDesc{CounterDesc("events", 0, "E.")}, Collect: func(e *Emitter) {
		if !broken {
			e.Counter("events", Labels{}, 1)
		}
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Publish(); !errors.Is(err, ErrMissing) {
		t.Fatalf("want ErrMissing, got %v", err)
	}
	if h := c.Health(ht); h.State != StateDegraded || !strings.Contains(h.Checks[0].Reason, "metric errors") {
		t.Fatalf("after a publish with an emit error: %+v", h)
	}
	broken = false
	if _, err := c.Publish(); err != nil {
		t.Fatal(err)
	}
	if h := c.Health(ht); h.State != StateOK {
		t.Fatalf("after the source recovered: %+v", h)
	}
}

// TestRegistryRefusesExpositionNameClashes proves Declare refuses declarations
// within one component whose Prometheus suffixes (_total, _bucket, _count, _sum,
// or clamp counters) would collide.
func TestRegistryRefusesExpositionNameClashes(t *testing.T) {
	clashes := [][]MetricDesc{
		{CounterDesc("x", 0, "C."), GaugeDesc("x_total", 0, "G.")},
		{GaugeDesc("x_total", 0, "G."), CounterDesc("x", 0, "C.")},
		{HistogramDesc("wait", LabelTenant, UnitNs, "H."), GaugeDesc("wait_count", LabelTenant, "G.")},
		{HistogramDesc("wait", 0, UnitNs, "H."), GaugeDesc("wait_bucket", 0, "G.")},
		{GaugeDesc("wait_sum", 0, "G."), HistogramDesc("wait", 0, UnitNs, "H.")},
		{HistogramDesc("wait", 0, UnitNs, "H."), CounterDesc("wait_clamped_low", 0, "C.")},
	}
	for i, descs := range clashes {
		var reg Registry
		if err := reg.Declare(ComponentPool, descs...); !errors.Is(err, ErrBadDesc) {
			t.Fatalf("case %d declared: %v", i, err)
		}
	}
	var reg Registry
	if err := reg.Declare(ComponentPool, CounterDesc("x", 0, "C."), GaugeDesc("x_count", 0, "G."), HistogramDesc("y", 0, UnitNs, "H."), CounterDesc("y_total", 0, "C.")); err != nil {
		t.Fatal(err)
	}
	if err := reg.Declare(ComponentRetry, GaugeDesc("x_total", 0, "G.")); err != nil {
		t.Fatalf("names of different components do not clash: %v", err)
	}
}

// TestEveryMetricWithoutATenantLabelIsEmittedAtEveryCollect proves non-tenant
// closed-label metrics return ErrMissing when omitted, while tenant-labelled
// metrics remain optional when no tenants exist.
func TestEveryMetricWithoutATenantLabelIsEmittedAtEveryCollect(t *testing.T) {
	descs := []MetricDesc{CounterDesc("refusals", LabelReason, "R."), CounterDesc("requests", LabelTenant|LabelOutcome, "Q.")}
	for _, detail := range []bool{true, false} {
		c := NewCollector(Config{Instance: "i", Detail: detail})
		if err := c.Register(Source{Component: ComponentPool, Metrics: descs, Collect: func(e *Emitter) {}}); err != nil {
			t.Fatal(err)
		}
		_, err := c.Collect()
		if !errors.Is(err, ErrMissing) || !strings.Contains(err.Error(), "pool.refusals") || strings.Contains(err.Error(), "pool.requests") {
			t.Fatalf("detail %v: want ErrMissing for pool.refusals only, got %v", detail, err)
		}
	}
}

// TestOpenLabelsAreOptionalAndNeedANonTenantLabel proves WithOpenLabels requires
// a non-tenant label and exempts that metric from requiring a row when empty.
func TestOpenLabelsAreOptionalAndNeedANonTenantLabel(t *testing.T) {
	var reg Registry
	for _, d := range []MetricDesc{CounterDesc("a", 0, "A.").WithOpenLabels(), CounterDesc("b", LabelTenant|LabelReason, "B.").WithOpenLabels()} {
		if err := reg.Declare(ComponentPool, d); !errors.Is(err, ErrBadDesc) {
			t.Fatalf("%s declared with open labels: %v", d.Name, err)
		}
	}
	c := NewCollector(Config{Instance: "i", Detail: true})
	if err := c.Register(Source{Component: ComponentPool, Metrics: []MetricDesc{CounterDesc("errors", LabelReason, "E.").WithOpenLabels()}, Collect: func(e *Emitter) {}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Collect(); err != nil {
		t.Fatalf("an open-labelled metric with no value yet is an error: %v", err)
	}
}
