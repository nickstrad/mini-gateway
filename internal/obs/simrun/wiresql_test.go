package simrun_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/obs/simrun"
	"github.com/nickstrad/mini-gateway/internal/pgwire/pgwiretest/wireworld"
)

var wireSQL = []string{"startup-failures.sql", "tls-requests.sql", "handshake-failures.sql"}

func wireRuns(dir string) error {
	for _, sc := range wireworld.Scenarios() {
		r, err := simrun.Wire(sc, 1, simrun.Options{Dir: dir})
		if err == nil {
			err = sc.Verify(r.Wire)
		}
		if err == nil {
			err = r.Check()
		}
		if err != nil {
			return err
		}
		baselineFinals = append(baselineFinals, r.Final)
	}
	return nil
}

func analysisFiles(t *testing.T) int {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(repoRoot(t), "test", "analysis", "*", "*.sql"))
	if err != nil || len(m) == 0 {
		t.Fatalf("no SQL files: %v", err)
	}
	return len(m)
}

type plant struct {
	key  string
	edit func(v int64) int64
}

func lastSeq(t *testing.T, metrics string) string {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(metrics, "\n"), "\n")
	return strings.Split(lines[len(lines)-1], ",")[2]
}

func plantFinal(t *testing.T, run string, plants ...plant) {
	t.Helper()
	path := filepath.Join(run, "metrics.csv")
	b, err := os.ReadFile(path)
	must(t, err)
	seq := lastSeq(t, string(b))
	lines := strings.SplitAfter(string(b), "\n")
	for _, p := range plants {
		hit := false
		for i, l := range lines {
			f := strings.Split(strings.TrimSuffix(l, "\n"), ",")
			if len(f) != 13 || f[2] != seq || strings.Join(f[5:12], ",") != p.key {
				continue
			}
			v, err := strconv.ParseInt(f[12], 10, 64)
			must(t, err)
			f[12] = strconv.FormatInt(p.edit(v), 10)
			lines[i] = strings.Join(f, ",") + "\n"
			hit = true
			break
		}
		if !hit {
			t.Fatalf("no final row %s in %s", p.key, path)
		}
	}
	must(t, os.WriteFile(path, []byte(strings.Join(lines, "")), 0o644))
}

func plus(n int64) func(int64) int64 { return func(v int64) int64 { return v + n } }

func zero(int64) int64 { return 0 }

func wireSQLFiles(t *testing.T, scenario string) []string {
	var out []string
	for _, f := range wireSQL {
		out = append(out, filepath.Join(repoRoot(t), "test", "analysis", scenario, f))
	}
	return out
}

func requireEveryFileFails(t *testing.T, run, scenario string) {
	t.Helper()
	out, err := csvAssert(t, nil, append([]string{run}, wireSQLFiles(t, scenario)...)...)
	if err == nil {
		t.Fatalf("the runner passed with a planted wrong number\n%s", out)
	}
	for _, f := range wireSQL {
		if !strings.Contains(out, filepath.Join(scenario, f)+": ") || strings.Contains(out, "ok: test/analysis/"+scenario+"/"+f) {
			t.Fatalf("%s did not catch the planted number\n%s", f, out)
		}
	}
	if strings.Contains(out, "no rows for the metric under test") || strings.Contains(out, "duckdb exit") {
		t.Fatalf("failed for another reason\n%s", out)
	}
}

func expectedFailure(sc wireworld.Scenario) (string, bool) {
	for r := range sc.Want.Client {
		return "wire,client_startups_failed,counter,,," + r.String() + ",", true
	}
	for c := range sc.Want.Database {
		return "wire,database_startups_failed,counter,,," + c.String() + ",", true
	}
	return "", false
}

// TestWireSQLCatchesPlantedNumbers proves all three SQL assertion files fail
// with violating rows on planted wrong numbers and dropped startup failures
// across all 19 wire scenarios.
func TestWireSQLCatchesPlantedNumbers(t *testing.T) {
	requireDuckDB(t)
	baselineRuns(t)
	for _, sc := range wireworld.Scenarios() {
		t.Run(sc.Name, func(t *testing.T) {
			t.Parallel()
			_, run := copyRun(t, sc.Name)
			plantFinal(t, run,
				plant{"wire,client_startups_failed,counter,,,deadline,", plus(1)},
				plant{"workload,startup_tls_requests,counter," + wireworld.TenantName(0) + ",,,", plus(7)},
				plant{"wire,client_tls_failed,counter,,,failed,", plus(1)})
			requireEveryFileFails(t, run, sc.Name)
			key, ok := expectedFailure(sc)
			if !ok {
				return
			}
			_, run = copyRun(t, sc.Name)
			plantFinal(t, run, plant{key, zero})
			out, err := csvAssert(t, nil, run, wireSQLFiles(t, sc.Name)[0])
			if err == nil || !strings.Contains(out, "violating rows") {
				t.Fatalf("a dropped startup failure was not caught: %v\n%s", err, out)
			}
		})
	}
}

// TestWireRunsUseTheScenarioRunIDs proves each wire scenario's run.csv records
// the expected "<scenario>/1/base" run ID.
func TestWireRunsUseTheScenarioRunIDs(t *testing.T) {
	root := baselineRuns(t)
	for _, sc := range wireworld.Scenarios() {
		b, err := os.ReadFile(filepath.Join(root, sc.Name, "1", "base", "run.csv"))
		must(t, err)
		if want := fmt.Sprintf(",%s/1/base,%s,", sc.Name, sc.Name); !strings.Contains(string(b), want) {
			t.Fatalf("run.csv of %s does not hold %q:\n%s", sc.Name, want, b)
		}
	}
}
