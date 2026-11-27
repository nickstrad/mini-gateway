package csvflush_test

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

const c03Seed int64 = 730031

func c03Start(t *testing.T) {
	t.Helper()
	t.Logf("seed=%d REPLAY: go test -count=1 -run '^%s$' ./internal/obs/csvflush", c03Seed, t.Name())
}

func c03Must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func c03Root(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("../../..")
	c03Must(t, err)
	return p
}

func c03Read(t *testing.T, p string) []byte {
	t.Helper()
	b, e := os.ReadFile(p)
	c03Must(t, e)
	return b
}

func c03Snapshot() obs.InstanceSnapshot {
	return obs.InstanceSnapshot{Instance: "a", Build: "build", ConfigFingerprint: "cfg", Points: []obs.Point{{Component: "pool", Metric: "peak", Kind: obs.KindGauge, Value: 0}, {Component: "pool", Metric: "requests", Kind: obs.KindCounter, Labels: obs.Labels{Tenant: "tenant"}, Value: 7}}, Hists: []obs.HistPoint{{Component: "pool", Metric: "wait", Labels: obs.Labels{Tenant: "tenant"}, Unit: obs.UnitNs, Buckets: []obs.Bucket{{Lo: 1, Hi: 1, Count: 3}, {Lo: 8, Hi: 9, Count: 4}}}}}
}

func c03Writer(t *testing.T, dir, id string, edit func(*csvflush.Config)) *csvflush.Writer {
	t.Helper()
	cfg := csvflush.Config{Dir: dir, Run: csvflush.RunInfo{RunID: id, Scenario: "adversarial", Tier: "S", Seed: c03Seed, Instances: 1}, Clock: func() int64 { return 17 }, Strict: true}
	if edit != nil {
		edit(&cfg)
	}
	w, e := csvflush.New(cfg)
	c03Must(t, e)
	return w
}

func c03Completed(t *testing.T, s obs.InstanceSnapshot) string {
	t.Helper()
	w := c03Writer(t, t.TempDir(), "run", nil)
	c03Must(t, w.Export(s))
	c03Must(t, w.Close())
	return w.RunDir()
}

func c03Records(t *testing.T, p string) [][]string {
	t.Helper()
	r, e := csv.NewReader(bytes.NewReader(c03Read(t, p))).ReadAll()
	c03Must(t, e)
	return r
}

func c03SaveRecords(t *testing.T, p string, r [][]string) {
	t.Helper()
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	c03Must(t, w.WriteAll(r))
	c03Must(t, os.WriteFile(p, b.Bytes(), 0600))
}

func TestAdv_3_CRLFLabelFidelity(t *testing.T) {
	c03Start(t)
	s := c03Snapshot()
	s.Points[1].Labels.Tenant = "a\r\nb"
	s.Hists[0].Labels.Tenant = "a\r\nb"
	d := c03Completed(t, s)
	if e := csvflush.RoundTrip(d, s); e != nil {
		t.Fatalf("legal tenant containing CRLF did not round trip byte-exactly: %v", e)
	}
}

func TestAdv_3_InterleavedInstancesAndQuietRows(t *testing.T) {
	c03Start(t)
	w := c03Writer(t, t.TempDir(), "run", nil)
	a := c03Snapshot()
	b := a.Clone()
	b.Instance = "0"
	c03Must(t, w.Flush(a, b))
	if _, e := os.Stat(filepath.Join(w.RunDir(), "run.csv")); !os.IsNotExist(e) {
		t.Fatalf("completion marker exists before close: %v", e)
	}
	a.Seq = 1
	b.Seq = 1
	a.Points = a.Points[:1]
	a.Hists = nil
	c03Must(t, w.Flush(a, b))
	c03Must(t, w.Close())
	r, e := csvflush.Read(w.RunDir())
	c03Must(t, e)
	if len(r.Metrics) != 8 || len(r.Hists) != 8 {
		t.Fatalf("quiet rows lost: metrics=%d hist=%d want 8 each", len(r.Metrics), len(r.Hists))
	}
	for i := 1; i < len(r.Metrics); i++ {
		p, q := r.Metrics[i-1], r.Metrics[i]
		if p.FlushSeq > q.FlushSeq || p.FlushSeq == q.FlushSeq && (p.Instance > q.Instance || p.Instance == q.Instance && obs.ComparePoint(p.Point, q.Point) > 0) {
			t.Fatalf("rows out of order at %d: %+v then %+v", i, p, q)
		}
	}
	if !slices.Equal(c03Records(t, filepath.Join(w.RunDir(), "metrics.csv"))[0], strings.Split("schema_version,run_id,flush_seq,t_ns,instance,component,metric,kind,tenant,outcome,reason,mode,value", ",")) {
		t.Fatal("metrics schema columns changed")
	}
}

func TestAdv_3_ZeroMetricWithNonTenantLabels(t *testing.T) {
	c03Start(t)
	c := obs.NewCollector(obs.Config{Detail: true})
	c03Must(t, c.Register(obs.Source{Component: "pool", Metrics: []obs.MetricDesc{obs.CounterDesc("refusals", obs.LabelReason, "Refusals.")}, Collect: func(e *obs.Emitter) {}}))
	s, e := c.Publish()
	if e != nil {
		return
	}
	d := c03Completed(t, s)
	r, e := csvflush.Read(d)
	c03Must(t, e)
	for _, p := range r.Metrics {
		if p.Point.Component == "pool" && p.Point.Metric == "refusals" {
			return
		}
	}
	t.Fatal("registered metric with reason but no tenant label emitted no zero row and collector reported no missing metric")
}

func TestAdv_4_DuplicateScalarCorruption(t *testing.T) {
	c03Start(t)
	s := c03Snapshot()
	d := c03Completed(t, s)
	c03Must(t, csvflush.RoundTrip(d, s))
	p := filepath.Join(d, "metrics.csv")
	rs := c03Records(t, p)
	rs = append(rs, slices.Clone(rs[len(rs)-1]))
	c03SaveRecords(t, p, rs)
	if e := csvflush.RoundTrip(d, s); e == nil {
		t.Fatal("round trip accepted duplicate final scalar row (CSV sums now double that tenant)")
	}
}

func TestAdv_4_PhantomTenantCorruption(t *testing.T) {
	c03Start(t)
	s := c03Snapshot()
	d := c03Completed(t, s)
	c03Must(t, csvflush.RoundTrip(d, s))
	p := filepath.Join(d, "metrics.csv")
	rs := c03Records(t, p)
	extra := slices.Clone(rs[len(rs)-1])
	extra[8] = "zz_never_observed"
	extra[12] = "999"
	c03SaveRecords(t, p, append(rs, extra))
	if e := csvflush.RoundTrip(d, s); e == nil {
		t.Fatal("round trip accepted fabricated tenant with value 999 that never appeared in any snapshot")
	}
}

func TestAdv_4_HistogramUnitCorruption(t *testing.T) {
	c03Start(t)
	s := c03Snapshot()
	d := c03Completed(t, s)
	c03Must(t, csvflush.RoundTrip(d, s))
	p := filepath.Join(d, "histograms.csv")
	rs := c03Records(t, p)
	for i := 1; i < len(rs); i++ {
		rs[i][11] = "bytes"
	}
	c03SaveRecords(t, p, rs)
	if e := csvflush.RoundTrip(d, s); e == nil {
		t.Fatal("round trip ignored ns-to-bytes histogram unit corruption")
	}
}

func TestAdv_6_FirstFlushHeaderCap(t *testing.T) {
	c03Start(t)
	w := c03Writer(t, t.TempDir(), "run", func(c *csvflush.Config) { c.MaxBytes = 200; c.Async = true; c.Strict = false })
	s := c03Snapshot()
	s.Hists = nil
	s.Points = s.Points[:1]
	c03Must(t, w.Export(s))
	c03Must(t, w.Close())
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

const c03GuardDeadline = 30 * time.Second

func TestAdv_6_AsyncDiskErrorRecoveryBoundary(t *testing.T) {
	c03Start(t)
	w := c03Writer(t, t.TempDir(), "run", func(c *csvflush.Config) { c.Async = true; c.Strict = false; c.QueueCap = 2 })
	c03Must(t, os.Symlink("/dev/full", filepath.Join(w.RunDir(), "metrics.csv")))
	s := c03Snapshot()
	_ = w.Export(s)
	deadline := time.Now().Add(c03GuardDeadline)
	for w.FlushStats().FlushErrors == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if w.FlushStats().FlushErrors == 0 {
		t.Fatal("disk error not counted")
	}
	for i := 0; i < 8; i++ {
		_ = w.Export(s)
	}
	c03Must(t, w.Close())
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
	c03Start(t)
	w := c03Writer(t, t.TempDir(), "run", func(c *csvflush.Config) { c.Async = true; c.Strict = false; c.QueueCap = 1 })
	p := filepath.Join(w.RunDir(), "metrics.csv")
	c03Must(t, syscall.Mkfifo(p, 0600))
	s := c03Snapshot()
	c03Must(t, w.Export(s))
	deadline := time.Now().Add(c03GuardDeadline)
	for w.FlushStats().QueueDepth != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if w.FlushStats().QueueDepth != 0 {
		t.Fatal("worker did not take initial job")
	}
	c03Must(t, w.Export(s))
	before := w.FlushStats()
	done := make(chan error, 1)
	go func() { done <- w.Export(s) }()
	select {
	case e := <-done:
		if e == nil {
			t.Error("full queue accepted third job")
		}
	case <-time.After(c03GuardDeadline):
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
	c03Must(t, w.Close())
	select {
	case <-drained:
	case <-time.After(c03GuardDeadline):
		t.Error("FIFO drainer leaked")
	}
}

func c03Runner(t *testing.T, dir, query string) ([]byte, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "attack.sql")
	c03Must(t, os.WriteFile(p, []byte(query), 0600))
	c := exec.Command("bash", filepath.Join(c03Root(t), "scripts/csv-assert.sh"), dir, p)
	c.Env = append(os.Environ(), "PATH="+os.Getenv("PATH")+":/root/.local/bin")
	return c.CombinedOutput()
}

const c03GoodSQL = "-- requires: pool.requests\nSELECT * FROM final WHERE component='pool' AND metric='requests' AND value <> 7;\n"

func c03RunnerControl(t *testing.T, d string) {
	t.Helper()
	out, e := c03Runner(t, d, c03GoodSQL)
	if e != nil {
		t.Fatalf("positive SQL control failed: %v\n%s", e, out)
	}
}

func TestTierRAdvC03_7_MissingMetricInOneOfTwoRuns(t *testing.T) {
	c03Start(t)
	if os.Getenv("MG_TIER_R") != "1" {
		t.Skip("tier R: set MG_TIER_R=1 (runs scripts/csv-assert.sh with DuckDB)")
	}
	dir := t.TempDir()
	s := c03Snapshot()
	w := c03Writer(t, dir, "a", nil)
	c03Must(t, w.Export(s))
	c03Must(t, w.Close())
	c03RunnerControl(t, w.RunDir())
	s.Points = s.Points[:1]
	s.Hists = nil
	w = c03Writer(t, dir, "b", nil)
	c03Must(t, w.Export(s))
	c03Must(t, w.Close())
	out, e := c03Runner(t, dir, c03GoodSQL)
	if e == nil {
		t.Fatalf("runner accepted required metric absent from run b because run a has it:\n%s", out)
	}
}

func TestTierRAdvC03_7_NullNumericValue(t *testing.T) {
	c03Start(t)
	if os.Getenv("MG_TIER_R") != "1" {
		t.Skip("tier R: set MG_TIER_R=1 (runs scripts/csv-assert.sh with DuckDB)")
	}
	s := c03Snapshot()
	d := c03Completed(t, s)
	c03RunnerControl(t, d)
	p := filepath.Join(d, "metrics.csv")
	rs := c03Records(t, p)
	rs[len(rs)-1][12] = ""
	c03SaveRecords(t, p, rs)
	out, e := c03Runner(t, d, c03GoodSQL)
	if e == nil {
		t.Fatalf("missing integer became NULL, bypassed value <> 7, and runner passed:\n%s", out)
	}
}

func TestTierRAdvC03_7_ReturnedNullViolationRow(t *testing.T) {
	c03Start(t)
	if os.Getenv("MG_TIER_R") != "1" {
		t.Skip("tier R: set MG_TIER_R=1 (runs scripts/csv-assert.sh with DuckDB)")
	}
	d := c03Completed(t, c03Snapshot())
	c03RunnerControl(t, d)
	out, e := c03Runner(t, d, "-- requires: pool.requests\nSELECT NULL WHERE EXISTS (SELECT 1 FROM final);\n")
	if e == nil {
		t.Fatalf("one NULL violation row disappeared in shell command substitution:\n%s", out)
	}
}
