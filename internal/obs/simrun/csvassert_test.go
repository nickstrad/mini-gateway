package simrun_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/simrun"
	"github.com/nickstrad/mini-gateway/internal/sim/world"
	"github.com/nickstrad/mini-gateway/test/e2e"
)

const envRequireDuckDB = "MG_REQUIRE_DUCKDB"

func repoRoot(t *testing.T) string {
	t.Helper()
	r, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func duckdbPath() string {
	if p, err := exec.LookPath("duckdb"); err == nil {
		return p
	}
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".local", "bin", "duckdb")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func requireDuckDB(t *testing.T) {
	t.Helper()
	if duckdbPath() != "" {
		return
	}
	if os.Getenv(envRequireDuckDB) == "1" {
		t.Fatalf("%s=1 and duckdb is not on PATH or in ~/.local/bin", envRequireDuckDB)
	}
	t.Skipf("duckdb not installed; set %s=1 to make this a failure", envRequireDuckDB)
}

var (
	baselineOnce   sync.Once
	baselineRoot   string
	baselineErr    error
	baselineFinals []obs.InstanceSnapshot
)

func baselineRuns(t *testing.T) string {
	t.Helper()
	baselineOnce.Do(func() {
		dir, err := os.MkdirTemp("", "csvassert-")
		if err != nil {
			baselineErr = err
			return
		}
		baselineRoot = dir
		for _, sc := range world.Scenarios() {
			r, err := simrun.Scenario(sc, 1, simrun.Options{Dir: dir})
			if err == nil {
				err = r.Check()
				baselineFinals = append(baselineFinals, r.Final)
			}
			if err != nil {
				baselineErr = err
				return
			}
		}
		if baselineErr = wireRuns(dir); baselineErr == nil {
			baselineErr = classifierRuns(dir)
		}
		if baselineErr == nil {
			baselineErr = engineRuns(dir)
		}
		if baselineErr == nil {
			baselineErr = loadgenRuns()
		}
	})
	if baselineErr != nil {
		t.Fatal(baselineErr)
	}
	return baselineRoot
}

func csvAssert(t *testing.T, env []string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(filepath.Join(repoRoot(t), "scripts", "csv-assert.sh"), args...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func copyRun(t *testing.T, scenario string) (root, run string) {
	t.Helper()
	src := filepath.Join(baselineRuns(t), scenario, "1", "base")
	root = t.TempDir()
	run = filepath.Join(root, scenario, "1", "base")
	if err := os.MkdirAll(run, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"metrics.csv", "histograms.csv", "run.csv"} {
		b, err := os.ReadFile(filepath.Join(src, f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(run, f), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root, run
}

func writeSQL(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "claim.sql")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestCSVAssertBaselineCountsFromFlushedFiles proves all five baseline SQL
// assertions pass over flushed CSV files under DuckDB 1.5.x.
func TestCSVAssertBaselineCountsFromFlushedFiles(t *testing.T) {
	requireDuckDB(t)
	root := baselineRuns(t)
	dirs, err := filepath.Glob(filepath.Join(repoRoot(t), "test", "analysis", "*"))
	if err != nil {
		t.Fatal(err)
	}
	ran, tierR := 0, 0
	for _, d := range dirs {
		sqls, _ := filepath.Glob(filepath.Join(d, "*.sql"))
		if len(sqls) == 0 {
			continue
		}
		name := filepath.Base(d)
		if strings.HasPrefix(name, e2e.TierRPrefix) {
			if !slices.Contains(e2e.TierRScenarios(), name) {
				t.Fatalf("%s has SQL files but no tier R test writes that scenario", name)
			}
			tierR += len(sqls)
			continue
		}
		out, err := csvAssert(t, nil, append([]string{filepath.Join(root, name)}, sqls...)...)
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, out)
		}
		if want := fmt.Sprintf("%d SQL files run, 0 failures", len(sqls)); !strings.Contains(out, want) || !strings.Contains(out, "v1.5.") {
			t.Fatalf("%s: unexpected output\n%s", name, out)
		}
		ran += len(sqls)
	}
	if ran+tierR != analysisFiles(t) || ran == 0 || tierR == 0 {
		t.Fatalf("%d SQL files run over the simulator runs and %d left to the tier R runs, of %d", ran, tierR, analysisFiles(t))
	}
}

// TestCSVAssertFailsClosed proves scripts/csv-assert.sh exits non-zero naming
// the reason on planted wrong numbers, missing metrics, missing run.csv, bad
// schema versions/headers, bad SQL, missing roots, and wrong DuckDB versions.
func TestCSVAssertFailsClosed(t *testing.T) {
	requireDuckDB(t)
	const good = "-- requires: fakepg.sessions_peak\nSELECT 'x' FROM final WHERE component = 'fakepg' AND metric = 'sessions_peak' AND value <> 500;\n"
	t.Run("passes-when-true", func(t *testing.T) {
		root, _ := copyRun(t, "direct-500-connections")
		if out, err := csvAssert(t, nil, root, writeSQL(t, good)); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	})
	cases := []struct {
		name   string
		sql    string
		mutate func(t *testing.T, run string)
		want   string
	}{
		{"planted-wrong-number", strings.ReplaceAll(good, "<> 500", "<> 501"), nil, "violating rows"},
		{"no-rows-for-the-metric", "-- requires: fakepg.no_such_metric\nSELECT 'x' WHERE false;\n", nil, "no rows for the metric under test"},
		{"no-requires-line", "SELECT 'x' WHERE false;\n", nil, "no '-- requires:"},
		{"missing-run-csv", good, func(t *testing.T, run string) { must(t, os.Remove(filepath.Join(run, "run.csv"))) }, "missing run.csv"},
		{"unknown-schema-version", good, func(t *testing.T, run string) {
			p := filepath.Join(run, "metrics.csv")
			b, err := os.ReadFile(p)
			must(t, err)
			must(t, os.WriteFile(p, []byte(strings.ReplaceAll(string(b), "\n1,", "\n2,")), 0o644))
		}, "unknown schema version 2"},
		{"unknown-header", good, func(t *testing.T, run string) {
			p := filepath.Join(run, "histograms.csv")
			b, err := os.ReadFile(p)
			must(t, err)
			must(t, os.WriteFile(p, []byte(strings.Replace(string(b), "bucket_hi", "bucket_top", 1)), 0o644))
		}, "unknown schema (header"},
		{"bad-sql", "-- requires: fakepg.sessions_peak\nSELECT nonsense FROM nowhere;\n", nil, "duckdb exit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, run := copyRun(t, "direct-500-connections")
			if tc.mutate != nil {
				tc.mutate(t, run)
			}
			out, err := csvAssert(t, nil, root, writeSQL(t, tc.sql))
			if err == nil {
				t.Fatalf("the runner passed\n%s", out)
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("the message does not name the reason %q\n%s", tc.want, out)
			}
		})
	}
	t.Run("scenario-without-runs", func(t *testing.T) {
		root, _ := copyRun(t, "direct-reconnect-burst")
		out, err := csvAssert(t, nil, root)
		if err == nil || !strings.Contains(out, "has SQL files but no run") {
			t.Fatalf("%v\n%s", err, out)
		}
	})
	t.Run("root-missing", func(t *testing.T) {
		out, err := csvAssert(t, nil, filepath.Join(t.TempDir(), "nope"))
		if err == nil || !strings.Contains(out, "does not exist") {
			t.Fatalf("%v\n%s", err, out)
		}
	})
	t.Run("wrong-duckdb-version", func(t *testing.T) {
		bin := t.TempDir()
		must(t, os.WriteFile(filepath.Join(bin, "duckdb"), []byte("#!/bin/sh\necho 'v2.0.0 (Future) 0000'\n"), 0o755))
		root, _ := copyRun(t, "direct-500-connections")
		out, err := csvAssert(t, []string{"PATH=" + bin + ":/usr/bin:/bin"}, root, writeSQL(t, good))
		if err == nil || !strings.Contains(out, "not the pinned family") {
			t.Fatalf("%v\n%s", err, out)
		}
	})
}

func secondRun(t *testing.T, run, newID string, edit func(metrics string) string) string {
	t.Helper()
	dst := filepath.Join(filepath.Dir(filepath.Dir(run)), "2", "base")
	must(t, os.MkdirAll(dst, 0o755))
	oldID := "direct-500-connections/1/base"
	for _, f := range []string{"metrics.csv", "histograms.csv", "run.csv"} {
		b, err := os.ReadFile(filepath.Join(run, f))
		must(t, err)
		text := strings.ReplaceAll(string(b), ","+oldID+",", ","+newID+",")
		if f == "metrics.csv" {
			text = edit(text)
		}
		must(t, os.WriteFile(filepath.Join(dst, f), []byte(text), 0o644))
	}
	return dst
}

func editFile(t *testing.T, path string, edit func(string) string) {
	t.Helper()
	b, err := os.ReadFile(path)
	must(t, err)
	must(t, os.WriteFile(path, []byte(edit(string(b))), 0o644))
}

// TestCSVAssertChecksEveryRunAndEveryField proves multi-run validation fails
// when any run is empty, lacks a required metric, duplicates a run_id, or holds
// an empty required numeric cell, while passing valid multi-run, histogram, and
// quoted-newline inputs.
func TestCSVAssertChecksEveryRunAndEveryField(t *testing.T) {
	requireDuckDB(t)
	const good = "-- requires: fakepg.sessions_peak\nSELECT 'x' FROM final WHERE component = 'fakepg' AND metric = 'sessions_peak' AND value <> 500;\n"
	const secondID = "direct-500-connections/2/base"
	headerOnly := func(s string) string { return s[:strings.IndexByte(s, '\n')+1] }
	withoutMetric := func(s string) string {
		var keep []string
		for _, l := range strings.SplitAfter(s, "\n") {
			if !strings.Contains(l, ",fakepg,sessions_peak,") {
				keep = append(keep, l)
			}
		}
		return strings.Join(keep, "")
	}
	failing := []struct {
		name   string
		mutate func(t *testing.T, root, run string)
		want   string
	}{
		{"one-of-two-runs-empty", func(t *testing.T, root, run string) { secondRun(t, run, secondID, headerOnly) }, "direct-500-connections/2/base"},
		{"one-of-two-runs-lacks-the-metric", func(t *testing.T, root, run string) { secondRun(t, run, secondID, withoutMetric) }, "no rows for the metric under test fakepg.sessions_peak"},
		{"two-runs-share-a-run-id", func(t *testing.T, root, run string) {
			secondRun(t, run, "direct-500-connections/1/base", func(s string) string { return s })
		}, "run ids"},
		{"empty-value", func(t *testing.T, root, run string) {
			editFile(t, filepath.Join(run, "metrics.csv"), func(s string) string {
				i := strings.Index(s, ",fakepg,sessions_peak,")
				j := i + strings.IndexByte(s[i:], '\n')
				k := strings.LastIndexByte(s[:j], ',')
				return s[:k+1] + s[j:]
			})
		}, "empty field"},
		{"empty-bucket-count", func(t *testing.T, root, run string) {
			editFile(t, filepath.Join(run, "histograms.csv"), func(s string) string {
				j := strings.IndexByte(s[strings.IndexByte(s, '\n')+1:], '\n') + strings.IndexByte(s, '\n') + 1
				k := strings.LastIndexByte(s[:j], ',')
				return s[:k+1] + s[j:]
			})
		}, "empty field"},
	}
	for _, tc := range failing {
		t.Run(tc.name, func(t *testing.T) {
			root, run := copyRun(t, "direct-500-connections")
			tc.mutate(t, root, run)
			out, err := csvAssert(t, nil, root, writeSQL(t, good))
			if err == nil {
				t.Fatalf("the runner passed\n%s", out)
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("the message does not name the reason %q\n%s", tc.want, out)
			}
		})
	}
	t.Run("two-good-runs-pass", func(t *testing.T) {
		root, run := copyRun(t, "direct-500-connections")
		secondRun(t, run, secondID, func(s string) string { return s })
		if out, err := csvAssert(t, nil, root, writeSQL(t, good)); err != nil || !strings.Contains(out, "over 2 runs") {
			t.Fatalf("%v\n%s", err, out)
		}
	})
	t.Run("histogram-requirement", func(t *testing.T) {
		root, _ := copyRun(t, "direct-500-connections")
		sql := "-- requires: workload.latency\nSELECT 'x' FROM final_hist WHERE component = 'workload' AND metric = 'latency' AND count <= 0;\n"
		if out, err := csvAssert(t, nil, root, writeSQL(t, sql)); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	})
	t.Run("quoted-newlines-in-the-fixture", func(t *testing.T) {
		run := filepath.Join(t.TempDir(), "fixture", "1", "base")
		must(t, os.MkdirAll(run, 0o755))
		for _, f := range []string{"metrics", "histograms", "run"} {
			b, err := os.ReadFile(filepath.Join(repoRoot(t), "internal", "obs", "csvflush", "testdata", "fixture-"+f+".csv"))
			must(t, err)
			if f == "metrics" && !strings.Contains(string(b), "\"new\nline\"") {
				t.Fatal("the fixture has no quoted newline")
			}
			must(t, os.WriteFile(filepath.Join(run, f+".csv"), b, 0o644))
		}
		sql := "-- requires: admission.zero\nSELECT 'x' FROM final WHERE component = 'admission' AND metric = 'zero' AND value <> 0;\n"
		if out, err := csvAssert(t, nil, run, writeSQL(t, sql)); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	})
}

// TestCSVAssertFailsWithoutDuckDB proves scripts/csv-assert.sh fails closed
// rather than skipping when duckdb is absent from PATH and ~/.local/bin.
func TestCSVAssertFailsWithoutDuckDB(t *testing.T) {
	out, err := csvAssert(t, []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir()}, t.TempDir())
	if err == nil || !strings.Contains(out, "duckdb is not installed") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestMain(m *testing.M) {
	code := m.Run()
	if baselineRoot != "" {
		_ = os.RemoveAll(baselineRoot)
	}
	os.Exit(code)
}
