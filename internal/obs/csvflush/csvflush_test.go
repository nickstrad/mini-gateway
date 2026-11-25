package csvflush_test

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/csvflush"
	"github.com/nickstrad/mini-gateway/internal/obs/obstest"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

type fixedClock struct{ t int64 }

func (c *fixedClock) now() int64 {
	c.t += 1_000_000
	return c.t
}

func config(dir string) csvflush.Config {
	c := &fixedClock{}
	return csvflush.Config{Dir: dir, Clock: c.now, Strict: true,
		Run: csvflush.RunInfo{RunID: "fixture/1/base", Scenario: "fixture", Tier: csvflush.TierSim, Seed: 1, Arm: "base", Build: "build-x", ConfigFingerprint: "cfg-1"}}
}

func readAll(t testing.TB, runDir string) []byte {
	t.Helper()
	var out []byte
	for _, f := range []string{csvflush.MetricsFile, csvflush.HistogramsFile, csvflush.RunFile} {
		b, err := os.ReadFile(filepath.Join(runDir, f))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, b...)
	}
	return out
}

func newWriter(t testing.TB, cfg csvflush.Config) *csvflush.Writer {
	t.Helper()
	w, err := csvflush.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

type errWriter struct{ left int }

func (e *errWriter) Write(b []byte) (int, error) {
	if e.left < len(b) {
		return 0, errors.New("no space left on device")
	}
	e.left -= len(b)
	return len(b), nil
}

func (e *errWriter) Close() error { return nil }

func failingCreate(after int) func(string) (io.WriteCloser, error) {
	return func(string) (io.WriteCloser, error) { return &errWriter{left: after}, nil }
}

func subject() obstest.Subject {
	return obstest.Subject{
		Name:     csvflush.AdapterName,
		Fidelity: obstest.Exact,
		New: func(t testing.TB, _ *obs.Registry) obstest.Instance {
			w := newWriter(t, config(t.TempDir()))
			var out []byte
			closed := false
			output := func(t testing.TB) []byte {
				if !closed {
					closed = true
					if err := w.Close(); err != nil {
						t.Fatal(err)
					}
					out = readAll(t, w.RunDir())
				}
				return out
			}
			return obstest.Instance{Adapter: w, Output: output, Decoded: func(t testing.TB) obs.InstanceSnapshot {
				output(t)
				run, err := csvflush.Read(w.RunDir())
				if err != nil {
					t.Fatal(err)
				}
				seqs := run.FlushSeqs("inst-1")
				return run.Snapshot("inst-1", seqs[len(seqs)-1])
			}}
		},
		NewFailing: func(t testing.TB, _ *obs.Registry) obs.Adapter {
			cfg := config(t.TempDir())
			cfg.Strict = false
			csvflush.SetCreate(&cfg, failingCreate(0))
			return newWriter(t, cfg)
		},
	}
}

func TestConformance(t *testing.T) { obstest.Conformance(t, subject()) }

func TestGoldenHeader(t *testing.T) {
	want := map[string]string{
		csvflush.MetricsFile:    "schema_version,run_id,flush_seq,t_ns,instance,component,metric,kind,tenant,outcome,reason,mode,value",
		csvflush.HistogramsFile: "schema_version,run_id,flush_seq,t_ns,instance,component,metric,tenant,outcome,reason,mode,unit,bucket_lo,bucket_hi,count",
		csvflush.RunFile:        "schema_version,run_id,scenario,tier,seed,arm,instances,build,config_fingerprint,flushes,duration_ns",
	}
	w := newWriter(t, config(t.TempDir()))
	s, _ := obstest.FixtureSnapshot(t)
	if err := w.Export(s); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	for f, h := range want {
		b, err := os.ReadFile(filepath.Join(w.RunDir(), f))
		if err != nil {
			t.Fatal(err)
		}
		if first, _, _ := strings.Cut(string(b), "\n"); first != h {
			t.Fatalf("%s header\n got %s\nwant %s", f, first, h)
		}
	}
	if strings.Join(csvflush.MetricsHeader(), ",") != want[csvflush.MetricsFile] || csvflush.SchemaVersion != 1 {
		t.Fatal("schema constants changed")
	}
}

func twoFlushes(t testing.TB, cfg csvflush.Config) (*csvflush.Writer, obs.InstanceSnapshot) {
	t.Helper()
	c, next := obstest.Fixture(t)
	w := newWriter(t, cfg)
	if err := c.Attach(w); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Publish(); err != nil {
		t.Fatal(err)
	}
	next()
	last, err := c.Publish()
	if err != nil {
		t.Fatal(err)
	}
	return w, last
}

func TestGoldenFilesAndByteIdentity(t *testing.T) {
	var outs [][]byte
	for i := 0; i < 2; i++ {
		w, _ := twoFlushes(t, config(t.TempDir()))
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		outs = append(outs, readAll(t, w.RunDir()))
		if i == 0 {
			for _, f := range []string{csvflush.MetricsFile, csvflush.HistogramsFile, csvflush.RunFile} {
				b, _ := os.ReadFile(filepath.Join(w.RunDir(), f))
				obstest.Golden(t, *update, "fixture-"+f, b)
			}
		}
	}
	if !bytes.Equal(outs[0], outs[1]) {
		t.Fatal("same input, different files")
	}
}

func TestRowsSortedZeroRowsPresentIntegersOnly(t *testing.T) {
	w, last := twoFlushes(t, config(t.TempDir()))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := csvflush.RoundTrip(w.RunDir(), last); err != nil {
		t.Fatal(err)
	}
	run, err := csvflush.Read(w.RunDir())
	if err != nil {
		t.Fatal(err)
	}
	if run.Run.Flushes != 2 || len(run.FlushSeqs("inst-1")) != 2 {
		t.Fatalf("flushes %d", run.Run.Flushes)
	}
	for _, seq := range []int64{0, 1} {
		s := run.Snapshot("inst-1", seq)
		if p, ok := s.Point(obs.ComponentAdmission, "zero", obs.Labels{}); !ok || p.Value != 0 {
			t.Fatalf("flush %d: the zero counter has no row", seq)
		}
		if p, ok := s.Point(obs.ComponentAdmission, "shed", obs.Labels{Reason: "timeout"}); !ok || p.Value != 0 {
			t.Fatalf("flush %d: zero-valued labelled row missing", seq)
		}
	}
	for i := 1; i < len(run.Metrics); i++ {
		a, b := run.Metrics[i-1], run.Metrics[i]
		if a.FlushSeq > b.FlushSeq || a.FlushSeq == b.FlushSeq && obs.ComparePoint(a.Point, b.Point) >= 0 {
			t.Fatalf("rows not sorted at %d", i)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(w.RunDir(), csvflush.MetricsFile))
	if bytes.ContainsAny(raw, ".eE") && bytes.Contains(raw, []byte("e+")) {
		t.Fatal("a float in metrics.csv")
	}
}

func TestTenantRowsPersistAfterTheTenantGoesQuiet(t *testing.T) {
	var reg obs.Registry
	_ = reg.Declare(obs.ComponentAdmission, obs.CounterDesc("requests", obs.LabelTenant, "R."))
	tenants := []string{"a", "b"}
	c := obs.NewCollector(obs.Config{Instance: "i", Detail: true})
	n := int64(0)
	err := c.Register(obs.Source{Component: obs.ComponentAdmission, Metrics: []obs.MetricDesc{obs.CounterDesc("requests", obs.LabelTenant, "R.")},
		Collect: func(e *obs.Emitter) {
			n++
			for _, tn := range tenants {
				e.Counter("requests", obs.Labels{Tenant: tn}, n)
			}
		}})
	if err != nil {
		t.Fatal(err)
	}
	w := newWriter(t, config(t.TempDir()))
	if err := c.Attach(w); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Publish(); err != nil {
		t.Fatal(err)
	}
	tenants = []string{"a"}
	last, _ := c.Publish()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	run, err := csvflush.Read(w.RunDir())
	if err != nil {
		t.Fatal(err)
	}
	s := run.Snapshot("i", 1)
	if s.Value(obs.ComponentAdmission, "requests", obs.Labels{Tenant: "b"}) != 1 || s.Value(obs.ComponentAdmission, "requests", obs.Labels{Tenant: "a"}) != 2 {
		t.Fatalf("tenant b's row did not persist with its last value: %+v", s.Points)
	}
	if err := csvflush.RoundTrip(w.RunDir(), last); err != nil {
		t.Fatal(err)
	}
}

func TestSizeCapIsCountedAndAFailureInTestMode(t *testing.T) {
	for _, strict := range []bool{true, false} {
		cfg := config(t.TempDir())
		cfg.MaxBytes = 4000
		cfg.Strict = strict
		c, next := obstest.Fixture(t)
		w := newWriter(t, cfg)
		if err := c.Attach(w); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 3; i++ {
			if _, err := c.Publish(); err != nil {
				t.Fatal(err)
			}
			next()
		}
		s, _ := c.Publish()
		if s.Value(obs.ComponentObs, obs.MetricCSVFlushErrors, obs.Labels{}) == 0 || s.Value(obs.ComponentObs, obs.MetricCSVRowsDropped, obs.Labels{}) == 0 {
			t.Fatalf("the cap was not counted: %+v", s.Points)
		}
		if s.Value(obs.ComponentObs, obs.MetricExportErrors, obs.Labels{Reason: csvflush.AdapterName}) == 0 {
			t.Fatal("export errors did not move")
		}
		err := w.Close()
		if strict && !errors.Is(err, csvflush.ErrDropped) {
			t.Fatalf("test mode: Close = %v, want a failure", err)
		}
		if !strict && err != nil {
			t.Fatalf("real-process mode must carry on: %v", err)
		}
		if _, err := os.Stat(filepath.Join(w.RunDir(), csvflush.RunFile)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("run.csv written after the writer stopped")
		}
		if st := w.FlushStats(); st.BytesWritten > cfg.MaxBytes {
			t.Fatalf("wrote %d bytes past the cap %d", st.BytesWritten, cfg.MaxBytes)
		}
	}
}

func TestWriteErrorIsCountedAndAFailureInTestMode(t *testing.T) {
	cfg := config(t.TempDir())
	csvflush.SetCreate(&cfg, failingCreate(500))
	w := newWriter(t, cfg)
	s, _ := obstest.FixtureSnapshot(t)
	if err := w.Export(s); err == nil {
		t.Fatal("write error not reported to the collector")
	}
	st := w.FlushStats()
	if st.FlushErrors != 1 || st.RowsDropped != int64(len(s.Points))+bucketRows(s) {
		t.Fatalf("%+v", st)
	}
	if err := w.Close(); err == nil {
		t.Fatal("test mode must fail")
	}
}

func bucketRows(s obs.InstanceSnapshot) int64 {
	n := int64(0)
	for _, h := range s.Hists {
		n += int64(len(h.Buckets))
	}
	return n
}

func TestAsyncWriterNeverBlocksTheCaller(t *testing.T) {
	cfg := config(t.TempDir())
	cfg.Async, cfg.Strict, cfg.QueueCap = true, false, 4
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	csvflush.SetGate(&cfg, func() {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
	})
	w := newWriter(t, cfg)
	c, next := obstest.Fixture(t)
	if err := c.Attach(w); err != nil {
		t.Fatal(err)
	}
	publish := func() time.Duration {
		start := time.Now()
		if _, err := c.Publish(); err != nil {
			t.Fatal(err)
		}
		next()
		return time.Since(start)
	}
	publish()
	<-entered
	for i := 0; i < cfg.QueueCap; i++ {
		publish()
	}
	if d := w.FlushStats().QueueDepth; d != int64(cfg.QueueCap) {
		t.Fatalf("queue depth %d", d)
	}
	for i := 0; i < 5; i++ {
		if d := publish(); d > time.Second {
			t.Fatalf("a publish with a full queue took %v", d)
		}
	}
	st := w.FlushStats()
	if st.FlushErrors != 5 || st.RowsDropped == 0 || st.Flushes != 0 {
		t.Fatalf("drops not counted: %+v", st)
	}
	if _, err := os.Stat(filepath.Join(w.RunDir(), csvflush.RunFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("run.csv exists before Close")
	}
	close(release)
	last, _ := c.Last()
	before := w.FlushStats()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	st = w.FlushStats()
	if st.Flushes != int64(cfg.QueueCap)+2 || st.FlushErrors != 5 || st.QueueDepth != 0 {
		t.Fatalf("after close: %+v", st)
	}
	if st.Flushes < before.Flushes || st.FlushErrors < before.FlushErrors || st.RowsDropped < before.RowsDropped || st.BytesWritten < before.BytesWritten {
		t.Fatalf("a counter went down at Close: before %+v, after %+v", before, st)
	}
	if err := csvflush.RoundTrip(w.RunDir(), last); err != nil {
		t.Fatalf("the final flush on close did not land the last snapshot: %v", err)
	}
}

func TestAsyncWriterUnderConcurrentStatsReads(t *testing.T) {
	cfg := config(t.TempDir())
	cfg.Async, cfg.QueueCap = true, 2
	w := newWriter(t, cfg)
	c, next := obstest.Fixture(t)
	if err := c.Attach(w); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			_ = w.FlushStats()
		}
	}()
	for i := 0; i < 50; i++ {
		if _, err := c.Publish(); err != nil {
			t.Fatal(err)
		}
		next()
	}
	<-done
	cfg2 := w.FlushStats()
	_ = w.Close()
	if cfg2.Flushes+cfg2.FlushErrors+cfg2.QueueDepth == 0 {
		t.Fatal("nothing happened")
	}
}

func TestRoundTripCatchesPlantedBugs(t *testing.T) {
	for name, plant := range map[string]func(*csvflush.Config){
		"drop-last-bucket": csvflush.PlantDropLastBucket,
		"stale-value":      csvflush.PlantStaleValue,
	} {
		t.Run(name, func(t *testing.T) {
			cfg := config(t.TempDir())
			w, last := twoFlushes(t, cfg)
			_ = w.Close()
			if err := csvflush.RoundTrip(w.RunDir(), last); err != nil {
				t.Fatalf("clean run: %v", err)
			}
			cfg = config(t.TempDir())
			plant(&cfg)
			w, last = twoFlushes(t, cfg)
			_ = w.Close()
			if err := csvflush.RoundTrip(w.RunDir(), last); !errors.Is(err, csvflush.ErrMismatch) {
				t.Fatalf("planted bug not caught: %v", err)
			}
		})
	}
}

func TestReadRejectsIncompleteOrForeignRuns(t *testing.T) {
	w, _ := twoFlushes(t, config(t.TempDir()))
	if _, err := csvflush.Read(w.RunDir()); err == nil {
		t.Fatal("a run without run.csv was read")
	}
	_ = w.Close()
	if _, err := csvflush.Read(w.RunDir()); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(w.RunDir(), csvflush.MetricsFile)
	b, _ := os.ReadFile(p)
	if err := os.WriteFile(p, bytes.Replace(b, []byte("\n1,"), []byte("\n2,"), 1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := csvflush.Read(w.RunDir()); err == nil {
		t.Fatal("schema version 2 was read")
	}
}

func TestRunIDValidation(t *testing.T) {
	for _, id := range []string{"", "../x", "a//b", "/abs", "a/./b", "sp ace", strings.Repeat("a/", 9) + "a"} {
		if csvflush.ValidRunID(id) == nil {
			t.Errorf("%q accepted", id)
		}
	}
	if err := csvflush.ValidRunID("direct-500-connections/1/base"); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkFlushFixture(b *testing.B) {
	s, _ := obstest.FixtureSnapshot(b)
	for _, async := range []bool{false, true} {
		name := "sync"
		if async {
			name = "async"
		}
		b.Run(name, func(b *testing.B) {
			cfg := config(b.TempDir())
			cfg.Async, cfg.Strict, cfg.QueueCap = async, false, csvflush.MaxQueueCap
			w := newWriter(b, cfg)
			b.ReportAllocs()
			for b.Loop() {
				_ = w.Export(s)
			}
			b.StopTimer()
			_ = w.Close()
		})
	}
}
