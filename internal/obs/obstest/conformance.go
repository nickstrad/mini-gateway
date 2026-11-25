package obstest

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/obs"
)

type Fidelity uint8

const (
	Exact Fidelity = iota
	Folded
	Headline
)

type Instance struct {
	Adapter obs.Adapter
	Output  func(t testing.TB) []byte
	Decoded func(t testing.TB) obs.InstanceSnapshot
}

type Subject struct {
	Name        string
	Fidelity    Fidelity
	New         func(t testing.TB, reg *obs.Registry) Instance
	NewFailing  func(t testing.TB, reg *obs.Registry) obs.Adapter
	LabelPolicy func(decoded obs.InstanceSnapshot) error
}

var HostileTenants = []string{
	"alpha", "beta", "other", `quo"te`, "new\nline", `back\slash`, "comma,semi;colon", " lead space",
	strings.Repeat("long", 40), "ünïcödé", "eq=sign", "{brace}",
}

type fixtureState struct {
	round int64
}

func fixtureMetrics() []obs.MetricDesc {
	return []obs.MetricDesc{
		obs.CounterDesc("requests", obs.LabelTenant|obs.LabelOutcome, "Fixture requests by tenant and outcome."),
		obs.CounterDesc("shed", obs.LabelReason, "Fixture sheds by reason."),
		obs.CounterDesc("zero", 0, "A fixture counter that stays 0.").AsHeadline(),
		obs.GaugeDesc("depth", obs.LabelMode, "Fixture queue depth by mode.").AsHeadline(),
		obs.GaugeDesc("tenant_depth", obs.LabelTenant, "Fixture depth per tenant."),
		obs.HighWaterDesc("depth_peak", 0, "Fixture peak depth."),
		obs.HistogramDesc("wait", obs.LabelTenant, obs.UnitNs, "Fixture wait per tenant."),
		obs.HistogramDesc("size", 0, obs.UnitBytes, "Fixture sizes."),
	}
}

func (f *fixtureState) emit(e *obs.Emitter) {
	r := f.round
	for i, tn := range HostileTenants {
		k := int64(i + 1)
		e.Counter("requests", obs.Labels{Tenant: tn, Outcome: "ok"}, 10*k+r)
		e.Counter("requests", obs.Labels{Tenant: tn, Outcome: "failed"}, k)
		e.Gauge("tenant_depth", obs.Labels{Tenant: tn}, k-3)
		h, _ := obs.NewHistogram(obs.DurationLo, obs.DurationHi)
		h.RecordN(50_000*k, k)
		h.RecordN(3_000_000*k, 2)
		h.Record(obs.DurationHi + 1)
		e.Histogram("wait", obs.Labels{Tenant: tn}, h.Snapshot())
	}
	e.Counter("shed", obs.Labels{Reason: "queue-full"}, 7+r)
	e.Counter("shed", obs.Labels{Reason: "timeout"}, 0)
	e.Counter("zero", obs.Labels{}, 0)
	e.Gauge("depth", obs.Labels{Mode: "fifo"}, 4)
	e.Gauge("depth", obs.Labels{Mode: "lifo"}, -2)
	e.Gauge("depth_peak", obs.Labels{}, 99)
	h, _ := obs.NewHistogram(0, 1<<40)
	h.RecordN(1, 3)
	h.RecordN(5000, 1)
	h.RecordN(1<<30, 2)
	e.Histogram("size", obs.Labels{}, h.Snapshot())
}

func Fixture(t testing.TB) (*obs.Collector, func()) {
	t.Helper()
	f := &fixtureState{}
	c := obs.NewCollector(obs.Config{Instance: "inst-1", Build: "build-x", ConfigFingerprint: "cfg-1", Detail: true})
	if err := c.Register(obs.Source{Component: obs.ComponentAdmission, Metrics: fixtureMetrics(), Collect: f.emit}); err != nil {
		t.Fatal(err)
	}
	return c, func() { f.round++ }
}

func FixtureSnapshot(t testing.TB) (obs.InstanceSnapshot, *obs.Registry) {
	t.Helper()
	c, _ := Fixture(t)
	s, err := c.Publish()
	if err != nil {
		t.Fatal(err)
	}
	return s, c.Registry()
}

type Check struct {
	Name string
	Run  func(t testing.TB, sub Subject, snap obs.InstanceSnapshot, reg *obs.Registry)
}

func Conformance(t *testing.T, sub Subject) {
	snap, reg := FixtureSnapshot(t)
	for _, c := range Checks() {
		t.Run(c.Name, func(t *testing.T) { c.Run(t, sub, snap, reg) })
	}
}

func Checks() []Check {
	return []Check{
		{"values", func(t testing.TB, sub Subject, snap obs.InstanceSnapshot, reg *obs.Registry) {
			in := sub.New(t, reg)
			if err := in.Adapter.Export(snap); err != nil {
				t.Fatal(err)
			}
			in.Output(t)
			got := in.Decoded(t)
			if err := compare(sub.Fidelity, reg, snap, got); err != nil {
				t.Fatal(err)
			}
		}},
		{"labels", func(t testing.TB, sub Subject, snap obs.InstanceSnapshot, reg *obs.Registry) {
			in := sub.New(t, reg)
			if err := in.Adapter.Export(snap); err != nil {
				t.Fatal(err)
			}
			in.Output(t)
			got := in.Decoded(t)
			for _, p := range got.Points {
				if err := declaredLabels(reg, p.Component, p.Metric, p.Labels); err != nil {
					t.Fatal(err)
				}
			}
			for _, h := range got.Hists {
				if err := declaredLabels(reg, h.Component, h.Metric, h.Labels); err != nil {
					t.Fatal(err)
				}
			}
			if sub.LabelPolicy != nil {
				if err := sub.LabelPolicy(got); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{"deterministic", func(t testing.TB, sub Subject, snap obs.InstanceSnapshot, reg *obs.Registry) {
			var outs [][]byte
			for i := 0; i < 2; i++ {
				s2, _ := FixtureSnapshot(t)
				in := sub.New(t, reg)
				if err := in.Adapter.Export(s2); err != nil {
					t.Fatal(err)
				}
				outs = append(outs, in.Output(t))
			}
			if len(outs[0]) == 0 || !bytes.Equal(outs[0], outs[1]) {
				t.Fatalf("equal inputs gave different or empty outputs (%d and %d bytes)", len(outs[0]), len(outs[1]))
			}
		}},
		{"no-retained-reference", func(t testing.TB, sub Subject, snap obs.InstanceSnapshot, reg *obs.Registry) {
			ref := sub.New(t, reg)
			if err := ref.Adapter.Export(snap); err != nil {
				t.Fatal(err)
			}
			want := ref.Output(t)
			c := snap.Clone()
			in := sub.New(t, reg)
			if err := in.Adapter.Export(c); err != nil {
				t.Fatal(err)
			}
			if !obs.Equal(c, snap) {
				t.Fatal("the adapter modified the snapshot it was given")
			}
			Scribble(&c)
			if got := in.Output(t); !bytes.Equal(got, want) {
				t.Fatal("changing the snapshot after Export changed the adapter's output: it kept a reference")
			}
		}},
		{"error-counted-not-propagated", func(t testing.TB, sub Subject, snap obs.InstanceSnapshot, reg *obs.Registry) {
			if sub.NewFailing == nil {
				t.Fatal("subject has no failing mode")
			}
			c, _ := Fixture(t)
			a := sub.NewFailing(t, c.Registry())
			if err := c.Attach(a); err != nil {
				t.Fatal(err)
			}
			if _, err := c.Publish(); err != nil {
				t.Fatalf("an adapter error reached the collector: %v", err)
			}
			s, err := c.Publish()
			if err != nil {
				t.Fatal(err)
			}
			if n := s.Value(obs.ComponentObs, obs.MetricExportErrors, obs.Labels{Reason: a.Name()}); n != 1 {
				t.Fatalf("export errors for %s = %d, want 1", a.Name(), n)
			}
		}},
	}
}

func Scribble(s *obs.InstanceSnapshot) {
	for i := range s.Points {
		s.Points[i].Value = -7777
		s.Points[i].Labels.Tenant = "scribbled"
	}
	for i := range s.Hists {
		for j := range s.Hists[i].Buckets {
			s.Hists[i].Buckets[j].Count = -7777
		}
	}
	s.Instance = "scribbled"
}

func declaredLabels(reg *obs.Registry, comp, metric string, l obs.Labels) error {
	d, ok := reg.Lookup(comp, metric)
	if !ok {
		return fmt.Errorf("%s.%s is not declared", comp, metric)
	}
	if extra := l.Set() &^ d.Labels; extra != 0 {
		return fmt.Errorf("%s.%s carries undeclared labels %v", comp, metric, extra.Names())
	}
	return nil
}

type aggKey struct {
	comp, metric, outcome, reason, mode string
}

func aggregate(reg *obs.Registry, s obs.InstanceSnapshot) ([]aggKey, []int64) {
	var keys []aggKey
	var vals []int64
	cmpKey := func(a, b aggKey) int {
		return strings.Compare(a.comp+"\x00"+a.metric+"\x00"+a.outcome+"\x00"+a.reason+"\x00"+a.mode,
			b.comp+"\x00"+b.metric+"\x00"+b.outcome+"\x00"+b.reason+"\x00"+b.mode)
	}
	add := func(k aggKey, v int64, rule obs.MergeRule) {
		i, found := slices.BinarySearchFunc(keys, k, cmpKey)
		if !found {
			keys = slices.Insert(keys, i, k)
			vals = slices.Insert(vals, i, v)
			return
		}
		if rule == obs.MergeMax {
			vals[i] = max(vals[i], v)
		} else {
			vals[i] += v
		}
	}
	for _, p := range s.Points {
		d, _ := reg.Lookup(p.Component, p.Metric)
		add(aggKey{p.Component, p.Metric, p.Labels.Outcome, p.Labels.Reason, p.Labels.Mode}, p.Value, d.Merge)
	}
	for _, h := range s.Hists {
		add(aggKey{h.Component, h.Metric + "#count", h.Labels.Outcome, h.Labels.Reason, h.Labels.Mode}, h.Count(), obs.MergeSum)
	}
	return keys, vals
}

func untenanted(s obs.InstanceSnapshot) obs.InstanceSnapshot {
	out := obs.InstanceSnapshot{}
	for _, p := range s.Points {
		if p.Labels.Tenant == "" {
			out.Points = append(out.Points, p)
		}
	}
	return out
}

func compare(f Fidelity, reg *obs.Registry, want, got obs.InstanceSnapshot) error {
	switch f {
	case Exact:
		w, g := want, got
		w.Build, w.ConfigFingerprint, g.Build, g.ConfigFingerprint = "", "", "", ""
		if !obs.Equal(w, g) {
			return fmt.Errorf("decoded output differs from the snapshot: %d/%d points, %d/%d histograms; first difference: %s",
				len(g.Points), len(w.Points), len(g.Hists), len(w.Hists), firstDiff(w, g))
		}
	case Folded:
		if !slices.Equal(untenanted(want).Points, untenanted(got).Points) {
			return fmt.Errorf("untenanted points differ: %s", firstDiff(untenanted(want), untenanted(got)))
		}
		wk, wv := aggregate(reg, want)
		gk, gv := aggregate(reg, got)
		if !slices.Equal(wk, gk) || !slices.Equal(wv, gv) {
			return errors.New("totals over tenants differ between the snapshot and the decoded output")
		}
	case Headline:
		if len(got.Points) == 0 {
			return errors.New("headline output carries no value")
		}
		for _, p := range got.Points {
			w, ok := want.Point(p.Component, p.Metric, p.Labels)
			if !ok || w.Value != p.Value {
				return fmt.Errorf("headline %s.%s = %d, snapshot %d (present %v)", p.Component, p.Metric, p.Value, w.Value, ok)
			}
		}
		return nil
	}
	var missing []string
	for _, d := range reg.All() {
		inSnap, inOut := false, false
		for _, p := range want.Points {
			inSnap = inSnap || p.Component == d.Component && p.Metric == d.Name
		}
		for _, h := range want.Hists {
			inSnap = inSnap || h.Component == d.Component && h.Metric == d.Name
		}
		for _, p := range got.Points {
			inOut = inOut || p.Component == d.Component && p.Metric == d.Name
		}
		for _, h := range got.Hists {
			inOut = inOut || h.Component == d.Component && h.Metric == d.Name
		}
		if inSnap && !inOut {
			missing = append(missing, d.Component+"."+d.Name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("declared metrics missing from the output: %v", missing)
	}
	return nil
}

func firstDiff(a, b obs.InstanceSnapshot) string {
	for i := 0; i < min(len(a.Points), len(b.Points)); i++ {
		if a.Points[i] != b.Points[i] {
			return fmt.Sprintf("point %d: want %+v got %+v", i, a.Points[i], b.Points[i])
		}
	}
	for i := 0; i < min(len(a.Hists), len(b.Hists)); i++ {
		if obs.CompareHist(a.Hists[i], b.Hists[i]) != 0 || !slices.Equal(a.Hists[i].Buckets, b.Hists[i].Buckets) || a.Hists[i].Unit != b.Hists[i].Unit {
			return fmt.Sprintf("histogram %d: want %s/%+v %v got %s/%+v %v", i, a.Hists[i].Metric, a.Hists[i].Labels, a.Hists[i].Buckets,
				b.Hists[i].Metric, b.Hists[i].Labels, b.Hists[i].Buckets)
		}
	}
	return "lengths " + strconv.Itoa(len(a.Points)) + "/" + strconv.Itoa(len(b.Points))
}
