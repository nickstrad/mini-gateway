package obs

import (
	"errors"
	"fmt"
	"math"
	"slices"
)

var (
	ErrMergeKind     = errors.New("obs: points of one metric disagree on kind or unit")
	ErrMergeOverflow = errors.New("obs: merged value overflows int64")
)

const MergedBuild = "mixed"

func Merge(reg *Registry, instance string, snaps ...InstanceSnapshot) (InstanceSnapshot, error) {
	out := InstanceSnapshot{Instance: instance}
	for i, s := range snaps {
		if i == 0 {
			out.Build, out.ConfigFingerprint, out.Seq = s.Build, s.ConfigFingerprint, s.Seq
		}
		if s.Build != out.Build {
			out.Build = MergedBuild
		}
		if s.ConfigFingerprint != out.ConfigFingerprint {
			out.ConfigFingerprint = MergedBuild
		}
		out.Seq = max(out.Seq, s.Seq)
		out.Points = append(out.Points, s.Points...)
		for _, h := range s.Hists {
			h.Buckets = slices.Clone(h.Buckets)
			out.Hists = append(out.Hists, h)
		}
	}
	if reg != nil && reg.Len() > 0 {
		if err := checkDeclared(reg, out); err != nil {
			return InstanceSnapshot{}, err
		}
	}
	out.Sort()
	points, err := mergePoints(reg, out.Points)
	if err != nil {
		return InstanceSnapshot{}, err
	}
	hists, err := mergeHists(out.Hists)
	if err != nil {
		return InstanceSnapshot{}, err
	}
	out.Points, out.Hists = points, hists
	return out, nil
}

func checkDeclared(reg *Registry, s InstanceSnapshot) error {
	for _, p := range s.Points {
		d, ok := reg.Lookup(p.Component, p.Metric)
		if !ok {
			return fmt.Errorf("%w: %s.%s", ErrUndeclared, p.Component, p.Metric)
		}
		if d.Kind != p.Kind {
			return fmt.Errorf("%w: %s.%s is a %s, merged as a %s", ErrMergeKind, p.Component, p.Metric, d.Kind, p.Kind)
		}
	}
	for _, h := range s.Hists {
		d, ok := reg.Lookup(h.Component, h.Metric)
		if !ok {
			return fmt.Errorf("%w: histogram %s.%s", ErrUndeclared, h.Component, h.Metric)
		}
		if d.Kind != KindHistogram || d.Unit != h.Unit {
			return fmt.Errorf("%w: %s.%s is a %s in %q, merged as a histogram in %q", ErrMergeKind, h.Component, h.Metric, d.Kind, d.Unit, h.Unit)
		}
	}
	return nil
}

func mergeRule(reg *Registry, component, metric string) MergeRule {
	if reg == nil {
		return MergeSum
	}
	d, _ := reg.Lookup(component, metric)
	return d.Merge
}

func mergePoints(reg *Registry, ps []Point) ([]Point, error) {
	out := ps[:0]
	for _, p := range ps {
		n := len(out)
		if n == 0 || ComparePoint(out[n-1], p) != 0 {
			out = append(out, p)
			continue
		}
		if out[n-1].Kind != p.Kind {
			return nil, fmt.Errorf("%w: %s.%s", ErrMergeKind, p.Component, p.Metric)
		}
		v, ok := combineChecked(mergeRule(reg, p.Component, p.Metric), out[n-1].Value, p.Value)
		if !ok {
			return nil, fmt.Errorf("%w: %s.%s %v", ErrMergeOverflow, p.Component, p.Metric, p.Labels)
		}
		out[n-1].Value = v
	}
	return out, nil
}

func mergeHists(hs []HistPoint) ([]HistPoint, error) {
	out := hs[:0]
	for _, h := range hs {
		n := len(out)
		if n == 0 || CompareHist(out[n-1], h) != 0 {
			out = append(out, h)
			continue
		}
		if out[n-1].Unit != h.Unit {
			return nil, fmt.Errorf("%w: %s.%s", ErrMergeKind, h.Component, h.Metric)
		}
		b, err := MergeBuckets(out[n-1].Buckets, h.Buckets)
		if err != nil {
			return nil, fmt.Errorf("%w: %s.%s %v", err, h.Component, h.Metric, h.Labels)
		}
		out[n-1].Buckets = b
	}
	return out, nil
}

func combineChecked(m MergeRule, a, b int64) (int64, bool) {
	if m == MergeMax {
		return max(a, b), true
	}
	return addChecked(a, b)
}

func addChecked(a, b int64) (int64, bool) {
	if b > 0 && a > math.MaxInt64-b || b < 0 && a < math.MinInt64-b {
		return 0, false
	}
	return a + b, true
}

func MergeBuckets(a, b []Bucket) ([]Bucket, error) {
	out := make([]Bucket, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case j == len(b) || i < len(a) && compareBucket(a[i], b[j]) < 0:
			out = append(out, a[i])
			i++
		case i == len(a) || compareBucket(a[i], b[j]) > 0:
			out = append(out, b[j])
			j++
		default:
			x := a[i]
			n, ok := addChecked(x.Count, b[j].Count)
			if !ok {
				return nil, ErrMergeOverflow
			}
			x.Count = n
			out = append(out, x)
			i++
			j++
		}
	}
	return out, nil
}
