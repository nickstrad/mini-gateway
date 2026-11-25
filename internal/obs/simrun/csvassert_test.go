package simrun_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/simrun"
	"github.com/nickstrad/mini-gateway/internal/sim/world"
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

func TestCSVAssertBaselineCountsFromFlushedFiles(t *testing.T) {
	requireDuckDB(t)
	out, err := csvAssert(t, nil, baselineRuns(t))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "5 SQL files run, 0 failures") || !strings.Contains(out, "v1.5.") {
		t.Fatalf("unexpected output\n%s", out)
	}
}

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
