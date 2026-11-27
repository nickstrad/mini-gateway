package csvflush_test

import (
	"encoding/csv"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/csvflush"
	"github.com/nickstrad/mini-gateway/internal/obs/obstest"
)

func headerSize() int64 {
	return int64(len(strings.Join(csvflush.MetricsHeader(), ",")) + 1 + len(strings.Join(csvflush.HistogramsHeader(), ",")) + 1)
}

func filesSize(t *testing.T, dir string) int64 {
	t.Helper()
	n := int64(0)
	for _, f := range []string{csvflush.MetricsFile, csvflush.HistogramsFile, csvflush.RunFile} {
		if fi, err := os.Stat(filepath.Join(dir, f)); err == nil {
			n += fi.Size()
		}
	}
	return n
}

// TestByteCapCountsTheHeaders proves MaxBytes includes the CSV header bytes on
// both the first flush (sync and async) and on a zero-flush Close.
func TestByteCapCountsTheHeaders(t *testing.T) {
	s, _ := obstest.FixtureSnapshot(t)
	free := newWriter(t, config(t.TempDir()))
	if err := free.Export(s); err != nil {
		t.Fatal(err)
	}
	total := free.FlushStats().BytesWritten
	body := total - headerSize()
	if body <= 0 {
		t.Fatalf("total %d, headers %d", total, headerSize())
	}
	for _, async := range []bool{false, true} {
		for _, limit := range []int64{1, headerSize() - 1, body, total - 1} {
			cfg := config(t.TempDir())
			cfg.MaxBytes, cfg.Async, cfg.Strict = limit, async, false
			w := newWriter(t, cfg)
			_ = w.Export(s)
			if err := w.Close(); err != nil {
				t.Fatalf("async %v cap %d: real-process mode must carry on: %v", async, limit, err)
			}
			st := w.FlushStats()
			if st.BytesWritten > limit || filesSize(t, w.RunDir()) > limit {
				t.Fatalf("async %v cap %d: counted %d, on disk %d", async, limit, st.BytesWritten, filesSize(t, w.RunDir()))
			}
			if st.FlushErrors == 0 || st.RowsDropped == 0 || st.Flushes != 0 {
				t.Fatalf("async %v cap %d: the cap was not counted: %+v", async, limit, st)
			}
		}
	}
	cfg := config(t.TempDir())
	cfg.MaxBytes, cfg.Strict = headerSize()-1, false
	w := newWriter(t, cfg)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if st := w.FlushStats(); st.BytesWritten > cfg.MaxBytes || filesSize(t, w.RunDir()) > cfg.MaxBytes || st.FlushErrors == 0 {
		t.Fatalf("a close without flushes passed the cap: %+v, on disk %d", st, filesSize(t, w.RunDir()))
	}
	if _, err := os.Stat(filepath.Join(w.RunDir(), csvflush.RunFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("run.csv written after the cap")
	}
}

// TestRowsOfOneFlushAreOrderedBySeqThenInstance proves multi-snapshot Flush
// writes rows ordered by (flush_seq, instance) regardless of argument order.
func TestRowsOfOneFlushAreOrderedBySeqThenInstance(t *testing.T) {
	p := func(v int64) []obs.Point {
		return []obs.Point{{Component: obs.ComponentPool, Metric: "requests", Kind: obs.KindCounter, Value: v}}
	}
	w := newWriter(t, config(t.TempDir()))
	if err := w.Flush(obs.InstanceSnapshot{Instance: "a", Seq: 2, Points: p(1)}, obs.InstanceSnapshot{Instance: "b", Seq: 1, Points: p(2)}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r := records(t, filepath.Join(w.RunDir(), csvflush.MetricsFile))
	if len(r) != 3 || r[1][2] != "1" || r[1][4] != "b" || r[2][2] != "2" || r[2][4] != "a" {
		t.Fatalf("rows %v", r)
	}
}

func records(t *testing.T, path string) [][]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestZeroFlushCloseCountsAFailedOpen proves a zero-flush Close whose file open fails increments FlushErrors.
func TestZeroFlushCloseCountsAFailedOpen(t *testing.T) {
	cfg := config(t.TempDir())
	cfg.Strict = false
	csvflush.SetCreate(&cfg, func(string) (io.WriteCloser, error) { return nil, errors.New("disk gone") })
	w := newWriter(t, cfg)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if st := w.FlushStats(); st.FlushErrors != 1 {
		t.Fatalf("a close whose open failed must count one flush error: %+v", st)
	}
	if err := w.Err(); err == nil || !strings.Contains(err.Error(), "disk gone") {
		t.Fatalf("the open error must be kept: %v", err)
	}
}
