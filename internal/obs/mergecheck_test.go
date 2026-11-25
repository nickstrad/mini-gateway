package obs

import (
	"errors"
	"math"
	"testing"
)

// TestMergeChecksEveryPointAndHistogramAgainstTheRegistry proves Merge validates
// singleton and multi-snapshot points and histograms against reg, rejecting
// undeclared names, wrong kinds, and wrong units.
func TestMergeChecksEveryPointAndHistogramAgainstTheRegistry(t *testing.T) {
	reg := synthRegistry(t)
	ghost := InstanceSnapshot{Points: []Point{{Component: ComponentPool, Metric: "ghost", Kind: KindCounter, Value: 1}}}
	if _, err := Merge(reg, "x", ghost); !errors.Is(err, ErrUndeclared) {
		t.Fatalf("an undeclared point in one snapshot merged: %v", err)
	}
	events := InstanceSnapshot{Points: []Point{{Component: ComponentPool, Metric: "events", Kind: KindCounter, Value: 1}}}
	if _, err := Merge(reg, "x", ghost, events); !errors.Is(err, ErrUndeclared) {
		t.Fatalf("an undeclared point beside a declared one merged: %v", err)
	}
	wrongKind := InstanceSnapshot{Points: []Point{{Component: ComponentPool, Metric: "events", Kind: KindGauge, Value: 1}}}
	if _, err := Merge(reg, "x", wrongKind); !errors.Is(err, ErrMergeKind) {
		t.Fatalf("a point of the wrong kind merged: %v", err)
	}
	hist := HistPoint{Component: ComponentPool, Metric: "ghost_hist", Unit: UnitNs, Buckets: []Bucket{{1, 1, 2}}}
	ghostHist := InstanceSnapshot{Hists: []HistPoint{hist}}
	if _, err := Merge(reg, "x", ghostHist, ghostHist); !errors.Is(err, ErrUndeclared) {
		t.Fatalf("an undeclared histogram merged: %v", err)
	}
	if _, err := Merge(reg, "x", ghostHist); !errors.Is(err, ErrUndeclared) {
		t.Fatalf("an undeclared histogram in one snapshot merged: %v", err)
	}
	hist.Metric, hist.Unit = "wait", UnitBytes
	if _, err := Merge(reg, "x", InstanceSnapshot{Hists: []HistPoint{hist}}); !errors.Is(err, ErrMergeKind) {
		t.Fatalf("a histogram of the wrong unit merged: %v", err)
	}
	hist.Metric, hist.Unit = "events", UnitCount
	if _, err := Merge(reg, "x", InstanceSnapshot{Hists: []HistPoint{hist}}); !errors.Is(err, ErrMergeKind) {
		t.Fatalf("a counter merged as a histogram: %v", err)
	}
}

// TestMergeWithoutARegistrySumsAndDoesNotPanic proves Merge with a nil or empty
// registry sums values cleanly without panicking.
func TestMergeWithoutARegistrySumsAndDoesNotPanic(t *testing.T) {
	a := InstanceSnapshot{Points: []Point{{Component: ComponentPool, Metric: "events", Kind: KindCounter, Value: 2}}}
	for _, reg := range []*Registry{nil, {}} {
		m, err := Merge(reg, "x", a, a)
		if err != nil {
			t.Fatal(err)
		}
		if m.Value(ComponentPool, "events", Labels{}) != 4 {
			t.Fatalf("%+v", m.Points)
		}
	}
}

// TestMergeRefusesOverflow proves Merge and MergeBuckets return ErrMergeOverflow
// instead of wrapping signed 64-bit sums while MergeMax still handles MaxInt64.
func TestMergeRefusesOverflow(t *testing.T) {
	reg := synthRegistry(t)
	mk := func(metric string, kind Kind, v int64) InstanceSnapshot {
		return InstanceSnapshot{Points: []Point{{Component: ComponentPool, Metric: metric, Kind: kind, Value: v}}}
	}
	cases := []struct {
		name string
		a, b InstanceSnapshot
	}{
		{"counter", mk("events", KindCounter, math.MaxInt64), mk("events", KindCounter, 1)},
		{"gauge up", mk("in_use", KindGauge, math.MaxInt64), mk("in_use", KindGauge, 1)},
		{"gauge down", mk("in_use", KindGauge, math.MinInt64), mk("in_use", KindGauge, -1)},
	}
	for _, tc := range cases {
		if _, err := Merge(reg, "x", tc.a, tc.b); !errors.Is(err, ErrMergeOverflow) {
			t.Fatalf("%s: want ErrMergeOverflow, got %v", tc.name, err)
		}
	}
	if m, err := Merge(reg, "x", mk("largest", KindGauge, math.MaxInt64), mk("largest", KindGauge, 1)); err != nil || m.Points[0].Value != math.MaxInt64 {
		t.Fatalf("max merge: %v %+v", err, m.Points)
	}
	h := func(n int64) InstanceSnapshot {
		return InstanceSnapshot{Hists: []HistPoint{{Component: ComponentPool, Metric: "wait", Unit: UnitNs, Buckets: []Bucket{{1, 1, n}}}}}
	}
	if _, err := Merge(reg, "x", h(math.MaxInt64), h(1)); !errors.Is(err, ErrMergeOverflow) {
		t.Fatalf("bucket overflow: %v", err)
	}
	if _, err := MergeBuckets([]Bucket{{1, 1, math.MaxInt64}}, []Bucket{{1, 1, 1}}); !errors.Is(err, ErrMergeOverflow) {
		t.Fatalf("MergeBuckets overflow: %v", err)
	}
}
