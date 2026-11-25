package adv_test

import (
	"bytes"
	"encoding/csv"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/csvflush"
)

func writer(t *testing.T, dir, id string, edit func(*csvflush.Config)) *csvflush.Writer {
	t.Helper()
	cfg := csvflush.Config{Dir: dir, Run: csvflush.RunInfo{RunID: id, Scenario: "adversarial", Tier: "S", Seed: seed, Instances: 1}, Clock: func() int64 { return 17 }, Strict: true}
	if edit != nil {
		edit(&cfg)
	}
	w, e := csvflush.New(cfg)
	must(t, e)
	return w
}
func completed(t *testing.T, s obs.InstanceSnapshot) string {
	t.Helper()
	w := writer(t, t.TempDir(), "run", nil)
	must(t, w.Export(s))
	must(t, w.Close())
	return w.RunDir()
}
func records(t *testing.T, p string) [][]string {
	t.Helper()
	r, e := csv.NewReader(bytes.NewReader(read(t, p))).ReadAll()
	must(t, e)
	return r
}
func saveRecords(t *testing.T, p string, r [][]string) {
	t.Helper()
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	must(t, w.WriteAll(r))
	must(t, os.WriteFile(p, b.Bytes(), 0600))
}
func TestAdv_3_CRLFLabelFidelity(t *testing.T) {
	start(t)
	s := snapshot()
	s.Points[1].Labels.Tenant = "a\r\nb"
	s.Hists[0].Labels.Tenant = "a\r\nb"
	d := completed(t, s)
	if e := csvflush.RoundTrip(d, s); e != nil {
		t.Fatalf("legal tenant containing CRLF did not round trip byte-exactly: %v", e)
	}
}
func TestAdv_3_InterleavedInstancesAndQuietRows(t *testing.T) {
	start(t)
	w := writer(t, t.TempDir(), "run", nil)
	a := snapshot()
	b := a.Clone()
	b.Instance = "0"
	must(t, w.Flush(a, b))
	if _, e := os.Stat(filepath.Join(w.RunDir(), "run.csv")); !os.IsNotExist(e) {
		t.Fatalf("completion marker exists before close: %v", e)
	}
	a.Seq = 1
	b.Seq = 1
	a.Points = a.Points[:1]
	a.Hists = nil
	must(t, w.Flush(a, b))
	must(t, w.Close())
	r, e := csvflush.Read(w.RunDir())
	must(t, e)
	if len(r.Metrics) != 8 || len(r.Hists) != 8 {
		t.Fatalf("quiet rows lost: metrics=%d hist=%d want 8 each", len(r.Metrics), len(r.Hists))
	}
	for i := 1; i < len(r.Metrics); i++ {
		p, q := r.Metrics[i-1], r.Metrics[i]
		if p.FlushSeq > q.FlushSeq || p.FlushSeq == q.FlushSeq && (p.Instance > q.Instance || p.Instance == q.Instance && obs.ComparePoint(p.Point, q.Point) > 0) {
			t.Fatalf("rows out of order at %d: %+v then %+v", i, p, q)
		}
	}
	if !slices.Equal(records(t, filepath.Join(w.RunDir(), "metrics.csv"))[0], strings.Split("schema_version,run_id,flush_seq,t_ns,instance,component,metric,kind,tenant,outcome,reason,mode,value", ",")) {
		t.Fatal("metrics schema columns changed")
	}
}
func TestAdv_3_ZeroMetricWithNonTenantLabels(t *testing.T) {
	start(t)
	c := obs.NewCollector(obs.Config{Detail: true})
	must(t, c.Register(obs.Source{Component: "pool", Metrics: []obs.MetricDesc{obs.CounterDesc("refusals", obs.LabelReason, "Refusals.")}, Collect: func(e *obs.Emitter) {}}))
	s, e := c.Publish()
	if e != nil {
		return
	}
	d := completed(t, s)
	r, e := csvflush.Read(d)
	must(t, e)
	for _, p := range r.Metrics {
		if p.Point.Component == "pool" && p.Point.Metric == "refusals" {
			return
		}
	}
	t.Fatal("registered metric with reason but no tenant label emitted no zero row and collector reported no missing metric")
}
func TestAdv_4_DuplicateScalarCorruption(t *testing.T) {
	start(t)
	s := snapshot()
	d := completed(t, s)
	must(t, csvflush.RoundTrip(d, s))
	p := filepath.Join(d, "metrics.csv")
	rs := records(t, p)
	rs = append(rs, slices.Clone(rs[len(rs)-1]))
	saveRecords(t, p, rs)
	if e := csvflush.RoundTrip(d, s); e == nil {
		t.Fatal("round trip accepted duplicate final scalar row (CSV sums now double that tenant)")
	}
}
func TestAdv_4_PhantomTenantCorruption(t *testing.T) {
	start(t)
	s := snapshot()
	d := completed(t, s)
	must(t, csvflush.RoundTrip(d, s))
	p := filepath.Join(d, "metrics.csv")
	rs := records(t, p)
	extra := slices.Clone(rs[len(rs)-1])
	extra[8] = "zz_never_observed"
	extra[12] = "999"
	saveRecords(t, p, append(rs, extra))
	if e := csvflush.RoundTrip(d, s); e == nil {
		t.Fatal("round trip accepted fabricated tenant with value 999 that never appeared in any snapshot")
	}
}
func TestAdv_4_HistogramUnitCorruption(t *testing.T) {
	start(t)
	s := snapshot()
	d := completed(t, s)
	must(t, csvflush.RoundTrip(d, s))
	p := filepath.Join(d, "histograms.csv")
	rs := records(t, p)
	for i := 1; i < len(rs); i++ {
		rs[i][11] = "bytes"
	}
	saveRecords(t, p, rs)
	if e := csvflush.RoundTrip(d, s); e == nil {
		t.Fatal("round trip ignored ns-to-bytes histogram unit corruption")
	}
}
func TestAdv_6_FirstFlushHeaderCap(t *testing.T) {
	start(t)
	w := writer(t, t.TempDir(), "run", func(c *csvflush.Config) { c.MaxBytes = 200; c.Async = true; c.Strict = false })
	s := snapshot()
	s.Hists = nil
	s.Points = s.Points[:1]
	must(t, w.Export(s))
	must(t, w.Close())
	st := w.FlushStats()
	var size int64
	for _, f := range []string{"metrics.csv", "histograms.csv", "run.csv"} {
		if fi, e := os.Stat(filepath.Join(w.RunDir(), f)); e == nil {
			size += fi.Size()
		}
	}
	if size > 200 || st.BytesWritten > 200 {
		t.Fatalf("cap=200 but files=%d counted=%d (first flush did not reserve headers)", size, st.BytesWritten)
	}
	if st.FlushErrors == 0 {
		t.Fatalf("cap reached without error count: %+v", st)
	}
}
func TestAdv_6_AsyncDiskErrorRecoveryBoundary(t *testing.T) {
	start(t)
	w := writer(t, t.TempDir(), "run", func(c *csvflush.Config) { c.Async = true; c.Strict = false; c.QueueCap = 2 })
	must(t, os.Symlink("/dev/full", filepath.Join(w.RunDir(), "metrics.csv")))
	s := snapshot()
	_ = w.Export(s)
	deadline := time.Now().Add(3 * time.Second)
	for w.FlushStats().FlushErrors == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if w.FlushStats().FlushErrors == 0 {
		t.Fatal("disk error not counted")
	}
	for i := 0; i < 8; i++ {
		_ = w.Export(s)
	}
	must(t, w.Close())
	st := w.FlushStats()
	if st.FlushErrors < 9 || st.RowsDropped < 9*4 {
		t.Fatalf("disk failure plus queued/subsequent jobs not counted: %+v", st)
	}
	if _, e := os.Stat(filepath.Join(w.RunDir(), "run.csv")); !os.IsNotExist(e) {
		t.Fatal("failed run acquired completion marker")
	}
	if e := w.Export(s); e != csvflush.ErrClosed {
		t.Fatalf("reuse after close=%v", e)
	}
}
func TestAdv_6_FullQueueWithBlockedFilesystem(t *testing.T) {
	start(t)
	w := writer(t, t.TempDir(), "run", func(c *csvflush.Config) { c.Async = true; c.Strict = false; c.QueueCap = 1 })
	p := filepath.Join(w.RunDir(), "metrics.csv")
	must(t, syscall.Mkfifo(p, 0600))
	s := snapshot()
	must(t, w.Export(s))
	deadline := time.Now().Add(3 * time.Second)
	for w.FlushStats().QueueDepth != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	} // Worker blocks opening FIFO, independently of any product test hook.
	if w.FlushStats().QueueDepth != 0 {
		t.Fatal("worker did not take initial job")
	}
	must(t, w.Export(s))
	before := w.FlushStats()
	done := make(chan error, 1)
	go func() { done <- w.Export(s) }()
	select {
	case e := <-done:
		if e == nil {
			t.Error("full queue accepted third job")
		}
	case <-time.After(time.Second):
		t.Error("full queue blocked producer")
	}
	after := w.FlushStats()
	if after.FlushErrors != before.FlushErrors+1 || after.RowsDropped != before.RowsDropped+4 {
		t.Errorf("full queue drop count before=%+v after=%+v", before, after)
	}
	drained := make(chan struct{})
	go func() {
		f, e := os.Open(p)
		if e == nil {
			var b [4096]byte
			for {
				if _, e = f.Read(b[:]); e != nil {
					break
				}
			}
			f.Close()
		}
		close(drained)
	}()
	must(t, w.Close())
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Error("FIFO drainer leaked")
	}
}
func runner(t *testing.T, dir, query string) ([]byte, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "attack.sql")
	must(t, os.WriteFile(p, []byte(query), 0600))
	c := exec.Command("bash", filepath.Join(root(t), "scripts/csv-assert.sh"), dir, p)
	c.Env = append(os.Environ(), "PATH="+os.Getenv("PATH")+":/root/.local/bin")
	return c.CombinedOutput()
}

const goodSQL = "-- requires: pool.requests\nSELECT * FROM final WHERE component='pool' AND metric='requests' AND value <> 7;\n"

func runnerControl(t *testing.T, d string) {
	t.Helper()
	out, e := runner(t, d, goodSQL)
	if e != nil {
		t.Fatalf("positive SQL control failed: %v\n%s", e, out)
	}
}
func TestAdv_7_MissingMetricInOneOfTwoRuns(t *testing.T) {
	start(t)
	dir := t.TempDir()
	s := snapshot()
	w := writer(t, dir, "a", nil)
	must(t, w.Export(s))
	must(t, w.Close())
	runnerControl(t, w.RunDir())
	s.Points = s.Points[:1]
	s.Hists = nil
	w = writer(t, dir, "b", nil)
	must(t, w.Export(s))
	must(t, w.Close())
	out, e := runner(t, dir, goodSQL)
	if e == nil {
		t.Fatalf("runner accepted required metric absent from run b because run a has it:\n%s", out)
	}
}
func TestAdv_7_NullNumericValue(t *testing.T) {
	start(t)
	s := snapshot()
	d := completed(t, s)
	runnerControl(t, d)
	p := filepath.Join(d, "metrics.csv")
	rs := records(t, p)
	rs[len(rs)-1][12] = ""
	saveRecords(t, p, rs)
	out, e := runner(t, d, goodSQL)
	if e == nil {
		t.Fatalf("missing integer became NULL, bypassed value <> 7, and runner passed:\n%s", out)
	}
}
func TestAdv_7_ReturnedNullViolationRow(t *testing.T) {
	start(t)
	d := completed(t, snapshot())
	runnerControl(t, d)
	out, e := runner(t, d, "-- requires: pool.requests\nSELECT NULL WHERE EXISTS (SELECT 1 FROM final);\n")
	if e == nil {
		t.Fatalf("one NULL violation row disappeared in shell command substitution:\n%s", out)
	}
}
