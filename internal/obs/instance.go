package obs

import (
	"cmp"
	"slices"
	"strings"
)

// Point is one scalar counter or gauge measurement inside an InstanceSnapshot.
type Point struct {
	Component string
	Metric    string
	Kind      Kind
	Labels    Labels
	Value     int64
}

// HistPoint is one full-resolution integer histogram measurement inside an
// InstanceSnapshot. Only non-empty buckets are stored.
type HistPoint struct {
	Component string
	Metric    string
	Labels    Labels
	Unit      Unit
	Buckets   []Bucket
}

// Count returns the total number of samples across all buckets of h.
func (h HistPoint) Count() int64 {
	n := int64(0)
	for _, b := range h.Buckets {
		n += b.Count
	}
	return n
}

// InstanceSnapshot is the immutable, sorted output of one Collector publish.
// By design it carries no timestamp: adapters that need a timestamp (CSV flush,
// OTLP) stamp it from their own clock so snapshots obey the replay law.
type InstanceSnapshot struct {
	Instance          string
	Build             string
	ConfigFingerprint string
	Seq               int64
	Points            []Point
	Hists             []HistPoint
}

// ComparePoint orders two points by (Component, Metric, Labels), matching the
// canonical row order required by CSV schema version 1.
func ComparePoint(a, b Point) int {
	if c := strings.Compare(a.Component, b.Component); c != 0 {
		return c
	}
	if c := strings.Compare(a.Metric, b.Metric); c != 0 {
		return c
	}
	return CompareLabels(a.Labels, b.Labels)
}

// CompareHist orders two histogram points by (Component, Metric, Labels).
func CompareHist(a, b HistPoint) int {
	if c := strings.Compare(a.Component, b.Component); c != 0 {
		return c
	}
	if c := strings.Compare(a.Metric, b.Metric); c != 0 {
		return c
	}
	return CompareLabels(a.Labels, b.Labels)
}

func compareBucket(a, b Bucket) int {
	if c := cmp.Compare(a.Lo, b.Lo); c != 0 {
		return c
	}
	return cmp.Compare(a.Hi, b.Hi)
}

// Clone returns a deep copy of s so callers cannot mutate a published snapshot.
func (s InstanceSnapshot) Clone() InstanceSnapshot {
	out := s
	out.Points = slices.Clone(s.Points)
	out.Hists = make([]HistPoint, len(s.Hists))
	for i, h := range s.Hists {
		h.Buckets = slices.Clone(h.Buckets)
		out.Hists[i] = h
	}
	// Preserve nil vs empty slice so Equal and tests see an exact copy.
	if s.Hists == nil {
		out.Hists = nil
	}
	return out
}

// Sort puts Points, Hists, and each histogram's Buckets into canonical order.
// Point and Hist lookups rely on this ordering for binary search.
func (s *InstanceSnapshot) Sort() {
	slices.SortFunc(s.Points, ComparePoint)
	slices.SortFunc(s.Hists, CompareHist)
	for i := range s.Hists {
		slices.SortFunc(s.Hists[i].Buckets, compareBucket)
	}
}

// Point looks up a scalar point by key using binary search on a sorted snapshot.
func (s InstanceSnapshot) Point(component, metric string, l Labels) (Point, bool) {
	i, found := slices.BinarySearchFunc(s.Points, Point{Component: component, Metric: metric, Labels: l}, ComparePoint)
	if !found {
		return Point{}, false
	}
	return s.Points[i], true
}

// Value returns the integer value of a scalar point, or 0 if absent.
func (s InstanceSnapshot) Value(component, metric string, l Labels) int64 {
	p, _ := s.Point(component, metric, l)
	return p.Value
}

// Sum returns the sum of all scalar points matching (component, metric) across
// all label sets.
func (s InstanceSnapshot) Sum(component, metric string) int64 {
	n := int64(0)
	for _, p := range s.Points {
		if p.Component == component && p.Metric == metric {
			n += p.Value
		}
	}
	return n
}

// Hist looks up a histogram point by key using binary search on a sorted snapshot.
func (s InstanceSnapshot) Hist(component, metric string, l Labels) (HistPoint, bool) {
	i, found := slices.BinarySearchFunc(s.Hists, HistPoint{Component: component, Metric: metric, Labels: l}, CompareHist)
	if !found {
		return HistPoint{}, false
	}
	return s.Hists[i], true
}

// Tenants returns the sorted, deduplicated list of non-empty tenant labels
// present in s's scalar points and histograms.
func (s InstanceSnapshot) Tenants() []string {
	var out []string
	for _, p := range s.Points {
		if p.Labels.Tenant != "" {
			out = append(out, p.Labels.Tenant)
		}
	}
	for _, h := range s.Hists {
		if h.Labels.Tenant != "" {
			out = append(out, h.Labels.Tenant)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// Equal reports whether a and b have identical metadata, points, and histogram buckets.
func Equal(a, b InstanceSnapshot) bool {
	if a.Instance != b.Instance || a.Build != b.Build || a.ConfigFingerprint != b.ConfigFingerprint || a.Seq != b.Seq {
		return false
	}
	if !slices.Equal(a.Points, b.Points) || len(a.Hists) != len(b.Hists) {
		return false
	}
	for i := range a.Hists {
		x, y := a.Hists[i], b.Hists[i]
		if CompareHist(x, y) != 0 || x.Unit != y.Unit || !slices.Equal(x.Buckets, y.Buckets) {
			return false
		}
	}
	return true
}
