package csvflush_test

import (
	"encoding/csv"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/csvflush"
)

func completedRun(t *testing.T, snaps ...obs.InstanceSnapshot) string {
	t.Helper()
	w := newWriter(t, config(t.TempDir()))
	for _, s := range snaps {
		if err := w.Export(s); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return w.RunDir()
}

func saveRecords(t *testing.T, path string, recs [][]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := csv.NewWriter(f)
	if err := w.WriteAll(recs); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func tenantSnapshots() (first, final obs.InstanceSnapshot) {
	first = obs.InstanceSnapshot{Instance: "i", Points: []obs.Point{
		{Component: obs.ComponentPool, Metric: "requests", Kind: obs.KindCounter, Labels: obs.Labels{Tenant: "quiet"}, Value: 3},
		{Component: obs.ComponentPool, Metric: "requests", Kind: obs.KindCounter, Labels: obs.Labels{Tenant: "busy"}, Value: 4},
	}, Hists: []obs.HistPoint{
		{Component: obs.ComponentPool, Metric: "wait", Labels: obs.Labels{Tenant: "quiet"}, Unit: obs.UnitNs, Buckets: []obs.Bucket{{Lo: 1, Hi: 1, Count: 2}, {Lo: 8, Hi: 9, Count: 1}}},
	}}
	first.Sort()
	final = obs.InstanceSnapshot{Instance: "i", Seq: 1, Points: []obs.Point{
		{Component: obs.ComponentPool, Metric: "requests", Kind: obs.KindCounter, Labels: obs.Labels{Tenant: "busy"}, Value: 9},
	}}
	return first, final
}

// TestRoundTripRejectsDuplicateAndFabricatedRows proves RoundTrip allows valid
// persisted tenant rows while rejecting duplicates, out-of-order rows, mutated
// persisted values, and fabricated tenant points or histograms.
func TestRoundTripRejectsDuplicateAndFabricatedRows(t *testing.T) {
	first, final := tenantSnapshots()
	clean := completedRun(t, first, final)
	if err := csvflush.RoundTrip(clean, final); err != nil {
		t.Fatalf("persisted rows of a quiet tenant: %v", err)
	}
	mutations := []struct {
		name string
		file string
		edit func(recs [][]string) [][]string
	}{
		{"duplicate final metric row", csvflush.MetricsFile, func(r [][]string) [][]string { return append(r, slices.Clone(r[len(r)-1])) }},
		{"fabricated tenant row", csvflush.MetricsFile, func(r [][]string) [][]string {
			x := slices.Clone(r[len(r)-1])
			x[8], x[12] = "zz_never_observed", "999"
			return append(r, x)
		}},
		{"persisted value changed", csvflush.MetricsFile, func(r [][]string) [][]string {
			for _, x := range r[1:] {
				if x[2] == "1" && x[8] == "quiet" {
					x[12] = "5"
				}
			}
			return r
		}},
		{"duplicate final bucket row", csvflush.HistogramsFile, func(r [][]string) [][]string { return append(r, slices.Clone(r[len(r)-1])) }},
		{"fabricated tenant histogram", csvflush.HistogramsFile, func(r [][]string) [][]string {
			x := slices.Clone(r[len(r)-1])
			x[7] = "zz_never_observed"
			return append(r, x)
		}},
		{"rows out of order", csvflush.MetricsFile, func(r [][]string) [][]string {
			n := len(r)
			r[n-1], r[n-2] = r[n-2], r[n-1]
			return r
		}},
	}
	for _, m := range mutations {
		t.Run(m.name, func(t *testing.T) {
			dir := completedRun(t, first, final)
			p := filepath.Join(dir, m.file)
			saveRecords(t, p, m.edit(records(t, p)))
			if err := csvflush.RoundTrip(dir, final); !errors.Is(err, csvflush.ErrMismatch) {
				t.Fatalf("round trip accepted it: %v", err)
			}
		})
	}
	t.Run("fabricated tenant in a single flush", func(t *testing.T) {
		dir := completedRun(t, first)
		p := filepath.Join(dir, csvflush.MetricsFile)
		r := records(t, p)
		x := slices.Clone(r[len(r)-1])
		x[8], x[12] = "zz_never_observed", "999"
		saveRecords(t, p, append(r, x))
		if err := csvflush.RoundTrip(dir, first); !errors.Is(err, csvflush.ErrMismatch) {
			t.Fatalf("round trip accepted it: %v", err)
		}
	})
}

// TestCarriageReturnsInLabelsRoundTrip proves CR and CRLF sequences inside
// quoted instance and tenant labels survive CSV flush and RoundTrip verbatim.
func TestCarriageReturnsInLabelsRoundTrip(t *testing.T) {
	names := []string{"a\r\nb", "c\rd", "e\r\r\nf", "g\r", "\r\n"}
	s := obs.InstanceSnapshot{Instance: "i\r\nj"}
	for i, n := range names {
		s.Points = append(s.Points, obs.Point{Component: obs.ComponentPool, Metric: "requests", Kind: obs.KindCounter, Labels: obs.Labels{Tenant: n}, Value: int64(i)})
		s.Hists = append(s.Hists, obs.HistPoint{Component: obs.ComponentPool, Metric: "wait", Labels: obs.Labels{Tenant: n}, Unit: obs.UnitNs, Buckets: []obs.Bucket{{Lo: 1, Hi: 1, Count: int64(i + 1)}}})
	}
	s.Sort()
	dir := completedRun(t, s)
	if err := csvflush.RoundTrip(dir, s); err != nil {
		t.Fatal(err)
	}
	run, err := csvflush.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if _, ok := run.Snapshot("i\r\nj", 0).Point(obs.ComponentPool, "requests", obs.Labels{Tenant: n}); !ok {
			t.Fatalf("tenant %q not read back", n)
		}
	}
}
