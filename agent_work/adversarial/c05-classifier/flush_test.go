package adv_test

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/obs/simrun"
	p "github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/pgwire/pgwiretest/clsworld"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
	"github.com/nickstrad/mini-gateway/internal/work"
)

func flushed(t *testing.T, dir string, n int64) *simrun.Run {
	t.Helper()
	r, e := simrun.Classifier(clsworld.RejectOnly(), n, simrun.Options{Dir: dir, Interval: 37 * time.Millisecond})
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Check(); e != nil {
		t.Fatal(e)
	}
	return r
}
func TestAdv_11_ManySeedWireRefusalConservation(t *testing.T) {
	mark(t)
	count := 12
	if os.Getenv("ADV_LONG") == "1" {
		count = 1000
	}
	for i := 0; i < count; i++ {
		n := seed + int64(i)*7919
		r := clsworld.Run(n, clsworld.RejectOnly(), "")
		if e := r.Verify(); e != nil {
			t.Fatalf("seed=%d %v", n, e)
		}
		if r.PG.StatementsStarted != 0 || r.Classifier.ByClass[work.ClassReject] != r.Plan.SentRejects || r.Plan.SentRejects == 0 {
			t.Fatalf("seed=%d sent=%d rejected=%d database=%d", n, r.Plan.SentRejects, r.Classifier.ByClass[work.ClassReject], r.PG.StatementsStarted)
		}
	}
}
func records(t *testing.T, name string) [][]string {
	t.Helper()
	f, e := os.Open(name)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	r, e := csv.NewReader(f).ReadAll()
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func writeRecords(t *testing.T, name string, rows [][]string) {
	t.Helper()
	f, e := os.Create(name)
	if e != nil {
		t.Fatal(e)
	}
	w := csv.NewWriter(f)
	w.WriteAll(rows)
	if e = w.Error(); e != nil {
		t.Fatal(e)
	}
	if e = f.Close(); e != nil {
		t.Fatal(e)
	}
}
func indexes(row []string) map[string]int {
	m := map[string]int{}
	for i, s := range row {
		m[s] = i
	}
	return m
}
func TestAdv_11_FlushedTruthAndMissingMetricFailClosed(t *testing.T) {
	mark(t)
	r := flushed(t, t.TempDir(), seed)
	name := filepath.Join(r.RunDir, "metrics.csv")
	rows := records(t, name)
	h := indexes(rows[0])
	last := int64(0)
	for _, row := range rows[1:] {
		n, _ := strconv.ParseInt(row[h["flush_seq"]], 10, 64)
		last = max(last, n)
	}
	rejects, sent, started := int64(0), int64(0), int64(0)
	found := map[string]bool{}
	for _, row := range rows[1:] {
		if row[h["flush_seq"]] != strconv.FormatInt(last, 10) {
			continue
		}
		key := row[h["component"]] + "." + row[h["metric"]]
		v, e := strconv.ParseInt(row[h["value"]], 10, 64)
		if e != nil {
			t.Fatal(e)
		}
		switch key {
		case "classifier.rejects":
			rejects += v
		case "wire.client_queries_seen":
			sent += v
		case "fakepg.statements_started":
			started += v
		}
		found[key] = true
	}
	if !found["fakepg.statements_started"] || !found["wire.client_queries_seen"] || sent == 0 || sent != rejects || started != 0 {
		t.Fatalf("CSV sent=%d rejects=%d started=%d found=%v", sent, rejects, started, found)
	}
	script := filepath.Join(root(), "scripts/csv-assert.sh")
	query := filepath.Join(root(), "test/analysis/classifier-reject-only/fakepg-statements-zero.sql")
	out, e := command(t, "bash", script, r.RunDir, query)
	if e != nil {
		t.Fatalf("baseline rejected: %v %s", e, out)
	}
	filtered := rows[:1:1]
	for _, row := range rows[1:] {
		if row[h["component"]] == "fakepg" && row[h["metric"]] == "statements_started" {
			continue
		}
		filtered = append(filtered, row)
	}
	writeRecords(t, name, filtered)
	out, e = command(t, "bash", script, r.RunDir, query)
	if e == nil {
		t.Fatalf("missing database counter silently counted as zero: %s", out)
	}
	if !strings.Contains(string(out), "statements_started") {
		t.Fatalf("failed for unrelated reason: %s", out)
	}
}
func TestAdv_11_CrossProcessFlushReplay(t *testing.T) {
	if dir := os.Getenv("ADV_REPLAY_DIR"); dir != "" {
		flushed(t, dir, seed+43)
		return
	}
	mark(t)
	dirs := []string{t.TempDir(), t.TempDir()}
	for _, d := range dirs {
		cmd := exec.Command(os.Args[0], "-test.run=^TestAdv_11_CrossProcessFlushReplay$", "-test.timeout=30s")
		cmd.Env = append(os.Environ(), "ADV_REPLAY_DIR="+d)
		out, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("child: %v %s", e, out)
		}
	}
	for _, name := range []string{"metrics.csv", "histograms.csv", "run.csv"} {
		rel := filepath.Join(clsworld.NameRejectOnly, fmt.Sprint(seed+43), "base", name)
		a, e := os.ReadFile(filepath.Join(dirs[0], rel))
		if e != nil {
			t.Fatal(e)
		}
		b, e := os.ReadFile(filepath.Join(dirs[1], rel))
		if e != nil {
			t.Fatal(e)
		}
		if !bytes.Equal(a, b) {
			t.Errorf("cross-process %s differs", name)
		}
	}
}

func TestAdv_11_PipelinedWriteSandwiches(t *testing.T) {
	mark(t)
	for _, window := range []int{1, 7, 31, 65536} {
		s := sim.New(sim.Config{Seed: seed + int64(window), Scenario: t.Name()})
		n := simnet.New(s)
		db, e := fakepg.New(n, fakepg.Config{})
		if e != nil {
			t.Fatal(e)
		}
		gw, e := clsworld.NewGateway(n, db.Addr(), p.ClassifierConfig{})
		if e != nil {
			t.Fatal(e)
		}
		opts := simnet.DefaultOptions()
		opts.Window = window
		probe := workload.NewProbe(n, clsworld.Addr, opts)
		probe.Startup("app", "app")
		run := func() {
			t.Helper()
			if e := s.RunUntilQuiet(); e != nil {
				t.Fatal(e)
			}
		}
		run()
		for _, middle := range []string{"RESET ALL", "LISTEN adversary", "DISCARD PLANS"} {
			probe.Query("INSERT INTO kv VALUES ('before', 'x'); " + middle + "; INSERT INTO kv VALUES ('after', 'y')")
		}
		run()
		if db.Snapshot().StartupsCompleted != 1 || db.Snapshot().StatementsStarted != 0 || db.Snapshot().WritesApplied != 0 || gw.Classifier.Snapshot().ByClass[work.ClassReject] != 3 {
			t.Fatalf("window=%d db=%+v classifier=%+v", window, db.Snapshot(), gw.Classifier.Snapshot())
		}
		errors := 0
		for _, msg := range probe.Messages() {
			if msg.Code == "0A000" {
				errors++
			}
		}
		if errors != 3 {
			t.Fatalf("window=%d errors=%d trace=%s", window, errors, probe.Trace())
		}
		probe.Query("SELECT 1")
		run()
		if db.Snapshot().StatementsStarted != 1 {
			t.Fatalf("window=%d recovery not forwarded", window)
		}
		probe.Terminate()
		run()
		probe.Close()
		run()
		if e := s.CheckQuiesce(); e != nil {
			t.Fatal(e)
		}
	}
}
