package csvflush

import (
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/nickstrad/mini-gateway/internal/obs"
)

var ErrMismatch = errors.New("csvflush: flushed files differ from the snapshot")

type MetricRow struct {
	RunID    string
	FlushSeq int64
	TNs      int64
	Instance string
	Point    obs.Point
}

type HistRow struct {
	RunID    string
	FlushSeq int64
	TNs      int64
	Instance string
	Point    obs.HistPoint
	Bucket   obs.Bucket
}

type RunRow struct {
	Info       RunInfo
	Flushes    int64
	DurationNs int64
}

type Run struct {
	Metrics []MetricRow
	Hists   []HistRow
	Run     RunRow
}

func readCSV(path string, header []string) ([][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = len(header)
	recs, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(recs) == 0 || !slices.Equal(recs[0], header) {
		return nil, fmt.Errorf("%s: header is not schema version %d", path, SchemaVersion)
	}
	for i, rec := range recs[1:] {
		if rec[0] != strconv.Itoa(SchemaVersion) {
			return nil, fmt.Errorf("%s: row %d: schema_version %q", path, i+2, rec[0])
		}
	}
	return recs[1:], nil
}

func ints(path string, row int, fields ...string) ([]int64, error) {
	out := make([]int64, len(fields))
	for i, f := range fields {
		v, err := strconv.ParseInt(f, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s: row %d: %q is not an integer", path, row+2, f)
		}
		out[i] = v
	}
	return out, nil
}

func kindOf(s string) (obs.Kind, error) {
	switch s {
	case "counter":
		return obs.KindCounter, nil
	case "gauge":
		return obs.KindGauge, nil
	}
	return 0, fmt.Errorf("kind %q", s)
}

func Read(runDir string) (*Run, error) {
	out := &Run{}
	rp := filepath.Join(runDir, RunFile)
	runRecs, err := readCSV(rp, RunHeader())
	if err != nil {
		return nil, err
	}
	if len(runRecs) != 1 {
		return nil, fmt.Errorf("%s: %d rows, want 1", rp, len(runRecs))
	}
	r := runRecs[0]
	n, err := ints(rp, 0, r[4], r[6], r[9], r[10])
	if err != nil {
		return nil, err
	}
	out.Run = RunRow{Info: RunInfo{RunID: r[1], Scenario: r[2], Tier: r[3], Seed: n[0], Arm: r[5], Instances: n[1], Build: r[7], ConfigFingerprint: r[8]},
		Flushes: n[2], DurationNs: n[3]}
	mp := filepath.Join(runDir, MetricsFile)
	recs, err := readCSV(mp, MetricsHeader())
	if err != nil {
		return nil, err
	}
	for i, rec := range recs {
		v, err := ints(mp, i, rec[2], rec[3], rec[12])
		if err != nil {
			return nil, err
		}
		k, err := kindOf(rec[7])
		if err != nil {
			return nil, fmt.Errorf("%s: row %d: %w", mp, i+2, err)
		}
		out.Metrics = append(out.Metrics, MetricRow{RunID: rec[1], FlushSeq: v[0], TNs: v[1], Instance: rec[4],
			Point: obs.Point{Component: rec[5], Metric: rec[6], Kind: k, Labels: obs.Labels{Tenant: rec[8], Outcome: rec[9], Reason: rec[10], Mode: rec[11]}, Value: v[2]}})
	}
	hp := filepath.Join(runDir, HistogramsFile)
	recs, err = readCSV(hp, HistogramsHeader())
	if err != nil {
		return nil, err
	}
	for i, rec := range recs {
		v, err := ints(hp, i, rec[2], rec[3], rec[12], rec[13], rec[14])
		if err != nil {
			return nil, err
		}
		out.Hists = append(out.Hists, HistRow{RunID: rec[1], FlushSeq: v[0], TNs: v[1], Instance: rec[4],
			Point:  obs.HistPoint{Component: rec[5], Metric: rec[6], Labels: obs.Labels{Tenant: rec[7], Outcome: rec[8], Reason: rec[9], Mode: rec[10]}, Unit: obs.Unit(rec[11])},
			Bucket: obs.Bucket{Lo: v[2], Hi: v[3] - 1, Count: v[4]}})
	}
	return out, nil
}

func (r *Run) Snapshot(instance string, seq int64) obs.InstanceSnapshot {
	s := obs.InstanceSnapshot{Instance: instance, Seq: seq}
	for _, m := range r.Metrics {
		if m.Instance == instance && m.FlushSeq == seq {
			s.Points = append(s.Points, m.Point)
		}
	}
	for _, h := range r.Hists {
		if h.Instance != instance || h.FlushSeq != seq {
			continue
		}
		n := len(s.Hists)
		if n == 0 || obs.CompareHist(s.Hists[n-1], h.Point) != 0 {
			s.Hists = append(s.Hists, h.Point)
			n++
		}
		s.Hists[n-1].Buckets = append(s.Hists[n-1].Buckets, h.Bucket)
	}
	return s
}

func (r *Run) FlushSeqs(instance string) []int64 {
	var out []int64
	for _, m := range r.Metrics {
		if m.Instance == instance {
			out = append(out, m.FlushSeq)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func RoundTrip(runDir string, final ...obs.InstanceSnapshot) error {
	run, err := Read(runDir)
	if err != nil {
		return err
	}
	var errs []error
	for _, want := range final {
		seqs := run.FlushSeqs(want.Instance)
		if len(seqs) == 0 {
			errs = append(errs, fmt.Errorf("%w: instance %q has no rows", ErrMismatch, want.Instance))
			continue
		}
		if last := seqs[len(seqs)-1]; last != want.Seq {
			errs = append(errs, fmt.Errorf("%w: instance %q: last flush_seq %d, final snapshot %d", ErrMismatch, want.Instance, last, want.Seq))
			continue
		}
		got := run.Snapshot(want.Instance, want.Seq)
		errs = append(errs, comparePoints(want, got), compareHists(want, got))
	}
	if run.Run.Flushes != int64(len(run.FlushSeqs(firstInstance(final)))) && len(final) > 0 {
		errs = append(errs, fmt.Errorf("%w: run.csv says %d flushes, metrics.csv has %d", ErrMismatch, run.Run.Flushes, len(run.FlushSeqs(firstInstance(final)))))
	}
	return errors.Join(errs...)
}

func firstInstance(s []obs.InstanceSnapshot) string {
	if len(s) == 0 {
		return ""
	}
	return s[0].Instance
}

func comparePoints(want, got obs.InstanceSnapshot) error {
	var errs []error
	for _, p := range want.Points {
		g, ok := got.Point(p.Component, p.Metric, p.Labels)
		switch {
		case !ok:
			errs = append(errs, fmt.Errorf("%w: %s.%s %+v missing from the file", ErrMismatch, p.Component, p.Metric, p.Labels))
		case g != p:
			errs = append(errs, fmt.Errorf("%w: %s.%s %+v: file %s %d, snapshot %s %d", ErrMismatch, p.Component, p.Metric, p.Labels, g.Kind, g.Value, p.Kind, p.Value))
		}
	}
	for _, g := range got.Points {
		if _, ok := want.Point(g.Component, g.Metric, g.Labels); !ok && g.Labels.Tenant == "" {
			errs = append(errs, fmt.Errorf("%w: %s.%s %+v in the file only", ErrMismatch, g.Component, g.Metric, g.Labels))
		}
	}
	return errors.Join(errs...)
}

func compareHists(want, got obs.InstanceSnapshot) error {
	var errs []error
	for _, h := range want.Hists {
		g, ok := got.Hist(h.Component, h.Metric, h.Labels)
		if !ok {
			if len(h.Buckets) > 0 {
				errs = append(errs, fmt.Errorf("%w: histogram %s.%s %+v missing from the file", ErrMismatch, h.Component, h.Metric, h.Labels))
			}
			continue
		}
		if g.Unit != h.Unit || !slices.Equal(g.Buckets, h.Buckets) {
			errs = append(errs, fmt.Errorf("%w: histogram %s.%s %+v: %d bucket rows in the file, %d in the snapshot, or a count differs", ErrMismatch, h.Component, h.Metric, h.Labels, len(g.Buckets), len(h.Buckets)))
		}
	}
	for _, g := range got.Hists {
		if _, ok := want.Hist(g.Component, g.Metric, g.Labels); !ok && g.Labels.Tenant == "" {
			errs = append(errs, fmt.Errorf("%w: histogram %s.%s %+v in the file only", ErrMismatch, g.Component, g.Metric, g.Labels))
		}
	}
	return errors.Join(errs...)
}
