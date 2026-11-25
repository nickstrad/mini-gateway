package obs

import (
	"errors"
	"fmt"
	"slices"
)

var ErrMergeKind = errors.New("obs: points of one metric disagree on kind or unit")

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
		rule := MergeSum
		if d, ok := reg.Lookup(p.Component, p.Metric); ok {
			rule = d.Merge
		} else if reg != nil && reg.Len() > 0 {
			return nil, fmt.Errorf("%w: %s.%s", ErrUndeclared, p.Component, p.Metric)
		}
		out[n-1].Value = combine(rule, out[n-1].Value, p.Value)
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
		out[n-1].Buckets = MergeBuckets(out[n-1].Buckets, h.Buckets)
	}
	return out, nil
}

func MergeBuckets(a, b []Bucket) []Bucket {
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
			x.Count += b[j].Count
			out = append(out, x)
			i++
			j++
		}
	}
	return out
}
