package obs

import (
	"cmp"
	"slices"
	"strings"
)

type Point struct {
	Component string
	Metric    string
	Kind      Kind
	Labels    Labels
	Value     int64
}

type HistPoint struct {
	Component string
	Metric    string
	Labels    Labels
	Unit      Unit
	Buckets   []Bucket
}

func (h HistPoint) Count() int64 {
	n := int64(0)
	for _, b := range h.Buckets {
		n += b.Count
	}
	return n
}

type InstanceSnapshot struct {
	Instance          string
	Build             string
	ConfigFingerprint string
	Seq               int64
	Points            []Point
	Hists             []HistPoint
}

func ComparePoint(a, b Point) int {
	if c := strings.Compare(a.Component, b.Component); c != 0 {
		return c
	}
	if c := strings.Compare(a.Metric, b.Metric); c != 0 {
		return c
	}
	return CompareLabels(a.Labels, b.Labels)
}

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

func (s InstanceSnapshot) Clone() InstanceSnapshot {
	out := s
	out.Points = slices.Clone(s.Points)
	out.Hists = make([]HistPoint, len(s.Hists))
	for i, h := range s.Hists {
		h.Buckets = slices.Clone(h.Buckets)
		out.Hists[i] = h
	}
	if s.Hists == nil {
		out.Hists = nil
	}
	return out
}

func (s *InstanceSnapshot) Sort() {
	slices.SortFunc(s.Points, ComparePoint)
	slices.SortFunc(s.Hists, CompareHist)
	for i := range s.Hists {
		slices.SortFunc(s.Hists[i].Buckets, compareBucket)
	}
}

func (s InstanceSnapshot) Point(component, metric string, l Labels) (Point, bool) {
	i, found := slices.BinarySearchFunc(s.Points, Point{Component: component, Metric: metric, Labels: l}, ComparePoint)
	if !found {
		return Point{}, false
	}
	return s.Points[i], true
}

func (s InstanceSnapshot) Value(component, metric string, l Labels) int64 {
	p, _ := s.Point(component, metric, l)
	return p.Value
}

func (s InstanceSnapshot) Sum(component, metric string) int64 {
	n := int64(0)
	for _, p := range s.Points {
		if p.Component == component && p.Metric == metric {
			n += p.Value
		}
	}
	return n
}

func (s InstanceSnapshot) Hist(component, metric string, l Labels) (HistPoint, bool) {
	i, found := slices.BinarySearchFunc(s.Hists, HistPoint{Component: component, Metric: metric, Labels: l}, CompareHist)
	if !found {
		return HistPoint{}, false
	}
	return s.Hists[i], true
}

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
