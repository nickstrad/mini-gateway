package csvflush

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/nickstrad/mini-gateway/internal/obs"
)

// ErrMismatch is returned by RoundTrip when the final flushed CSV rows do not
// match the final in-memory snapshot exactly.
var ErrMismatch = errors.New("csvflush: flushed files differ from the snapshot")

// MetricRow is one decoded row from metrics.csv.
type MetricRow struct {
	RunID    string
	FlushSeq int64
	TNs      int64
	Instance string
	Point    obs.Point
}

// HistRow is one decoded bucket row from histograms.csv.
type HistRow struct {
	RunID    string
	FlushSeq int64
	TNs      int64
	Instance string
	Point    obs.HistPoint
	Bucket   obs.Bucket
}

// RunRow is the single decoded summary row from run.csv.
type RunRow struct {
	Info       RunInfo
	Flushes    int64
	DurationNs int64
}

// Run holds every decoded row of a completed run directory.
type Run struct {
	Metrics []MetricRow
	Hists   []HistRow
	Run     RunRow
}

func readCSV(path string, header []string) ([][]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	r := csv.NewReader(bytes.NewReader(keepCarriageReturns(raw)))
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

// keepCarriageReturns doubles \r\n before encoding/csv parses the buffer.
// Writer always terminates records with \n, so any \r\n in the file lives
// inside a quoted field; Go's encoding/csv strips one \r before \n inside
// quoted fields, so doubling preserves the original carriage return verbatim.
func keepCarriageReturns(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\r\r\n"))
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

// Read parses run.csv, metrics.csv, and histograms.csv from runDir, checking
// schema version 1 headers, column counts, schema_version cells, and integer
// fields. A missing run.csv causes Read to fail.
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
		// Convert CSV's exclusive bucket_hi back to obs.Bucket's inclusive Hi.
		out.Hists = append(out.Hists, HistRow{RunID: rec[1], FlushSeq: v[0], TNs: v[1], Instance: rec[4],
			Point:  obs.HistPoint{Component: rec[5], Metric: rec[6], Labels: obs.Labels{Tenant: rec[7], Outcome: rec[8], Reason: rec[9], Mode: rec[10]}, Unit: obs.Unit(rec[11])},
			Bucket: obs.Bucket{Lo: v[2], Hi: v[3] - 1, Count: v[4]}})
	}
	return out, nil
}

// Snapshot reconstructs the InstanceSnapshot of instance at flush sequence seq.
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

// FlushSeqs returns the sorted, distinct flush_seq values present for instance.
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

// RoundTrip reads runDir back from disk and verifies that the final flush for
// each snapshot in final has strictly ordered, duplicate-free rows matching
// every scalar point and histogram bucket value for value. An extra tenant row
// is accepted only if the immediately preceding flush held the exact same row.
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
		if err := run.checkOrder(want.Instance, want.Seq); err != nil {
			errs = append(errs, err)
			continue
		}
		got := run.Snapshot(want.Instance, want.Seq)
		var prev *obs.InstanceSnapshot
		if len(seqs) > 1 {
			p := run.Snapshot(want.Instance, seqs[len(seqs)-2])
			prev = &p
		}
		errs = append(errs, comparePoints(want, got, prev), compareHists(want, got, prev))
	}
	if run.Run.Flushes != int64(len(run.FlushSeqs(firstInstance(final)))) && len(final) > 0 {
		errs = append(errs, fmt.Errorf("%w: run.csv says %d flushes, metrics.csv has %d", ErrMismatch, run.Run.Flushes, len(run.FlushSeqs(firstInstance(final)))))
	}
	return errors.Join(errs...)
}

func (r *Run) checkOrder(instance string, seq int64) error {
	var prev *MetricRow
	for i := range r.Metrics {
		m := &r.Metrics[i]
		if m.Instance != instance || m.FlushSeq != seq {
			continue
		}
		if prev != nil && obs.ComparePoint(prev.Point, m.Point) >= 0 {
			return fmt.Errorf("%w: instance %q flush %d: metric row %s.%s %+v is a duplicate or out of order", ErrMismatch, instance, seq, m.Point.Component, m.Point.Metric, m.Point.Labels)
		}
		prev = m
	}
	var prevH *HistRow
	for i := range r.Hists {
		h := &r.Hists[i]
		if h.Instance != instance || h.FlushSeq != seq {
			continue
		}
		if prevH != nil {
			c := obs.CompareHist(prevH.Point, h.Point)
			if c > 0 || c == 0 && (prevH.Point.Unit != h.Point.Unit || prevH.Bucket.Lo >= h.Bucket.Lo) {
				return fmt.Errorf("%w: instance %q flush %d: bucket row of %s.%s %+v is a duplicate or out of order", ErrMismatch, instance, seq, h.Point.Component, h.Point.Metric, h.Point.Labels)
			}
		}
		prevH = h
	}
	return nil
}

func firstInstance(s []obs.InstanceSnapshot) string {
	if len(s) == 0 {
		return ""
	}
	return s[0].Instance
}

func persistedPoint(g obs.Point, prev *obs.InstanceSnapshot) bool {
	if g.Labels.Tenant == "" || prev == nil {
		return false
	}
	p, ok := prev.Point(g.Component, g.Metric, g.Labels)
	return ok && p == g
}

func persistedHist(g obs.HistPoint, prev *obs.InstanceSnapshot) bool {
	if g.Labels.Tenant == "" || prev == nil {
		return false
	}
	p, ok := prev.Hist(g.Component, g.Metric, g.Labels)
	return ok && p.Unit == g.Unit && slices.Equal(p.Buckets, g.Buckets)
}

func comparePoints(want, got obs.InstanceSnapshot, prev *obs.InstanceSnapshot) error {
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
		if _, ok := want.Point(g.Component, g.Metric, g.Labels); !ok && !persistedPoint(g, prev) {
			errs = append(errs, fmt.Errorf("%w: %s.%s %+v in the file only, and not the same row of the flush before", ErrMismatch, g.Component, g.Metric, g.Labels))
		}
	}
	return errors.Join(errs...)
}

func compareHists(want, got obs.InstanceSnapshot, prev *obs.InstanceSnapshot) error {
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
		if _, ok := want.Hist(g.Component, g.Metric, g.Labels); !ok && !persistedHist(g, prev) {
			errs = append(errs, fmt.Errorf("%w: histogram %s.%s %+v in the file only, and not the same rows of the flush before", ErrMismatch, g.Component, g.Metric, g.Labels))
		}
	}
	return errors.Join(errs...)
}
