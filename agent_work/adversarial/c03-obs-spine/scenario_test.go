package adv_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/obs/simrun"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/world"
)

func scenarioRun(t *testing.T, name string, n int64, dir string, off bool) *simrun.Run {
	t.Helper()
	sc, ok := world.Lookup(name)
	if !ok {
		t.Fatal(name)
	}
	r, e := simrun.Scenario(sc, n, simrun.Options{Dir: dir, DetailOff: off, Interval: 337 * time.Millisecond, FlushAt: []sim.Time{0, 1, sim.Time(337 * time.Millisecond), sim.Time(sc.Duration)}})
	must(t, e)
	must(t, sc.Verify(r.Result))
	must(t, r.Check())
	return r
}

// The test executable itself is the child, avoiding compilation in subprocesses.
// Only the parent counts as an attack; the helper has no TestAdv_ prefix.
func TestReplayWorker(t *testing.T) {
	dir := os.Getenv("C03_ADV_CHILD_DIR")
	if dir == "" {
		return
	}
	n, e := strconv.ParseInt(os.Getenv("C03_ADV_CHILD_SEED"), 10, 64)
	must(t, e)
	scenarioRun(t, os.Getenv("C03_ADV_CHILD_SCENARIO"), n, dir, os.Getenv("C03_ADV_CHILD_OFF") == "1")
}
func crossProcess(t *testing.T, name string, n int64, off bool) {
	t.Helper()
	start(t)
	t.Logf("scenario=%s seed=%d detailOff=%v", name, n, off)
	var dirs []string
	for i := 0; i < 2; i++ {
		d := t.TempDir()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReplayWorker$", "-test.count=1")
		env := append(os.Environ(), "C03_ADV_CHILD_DIR="+d, "C03_ADV_CHILD_SCENARIO="+name, fmt.Sprintf("C03_ADV_CHILD_SEED=%d", n), fmt.Sprintf("GOMAXPROCS=%d", i+1))
		if off {
			env = append(env, "C03_ADV_CHILD_OFF=1")
		}
		cmd.Env = env
		out, e := cmd.CombinedOutput()
		cancel()
		if e != nil {
			t.Fatalf("child %d: %v\n%s", i, e, out)
		}
		dirs = append(dirs, filepath.Join(d, name, strconv.FormatInt(n, 10), "base"))
	}
	for _, f := range []string{"metrics.csv", "histograms.csv", "run.csv"} {
		a, b := read(t, filepath.Join(dirs[0], f)), read(t, filepath.Join(dirs[1], f))
		if !bytes.Equal(a, b) {
			t.Fatalf("seed=%d file=%s differs across process/GOMAXPROCS: lengths=%d/%d", n, f, len(a), len(b))
		}
	}
}
func TestAdv_5_TiedPublishCrossProcess(t *testing.T) {
	crossProcess(t, "direct-500-connections", seed, false)
}
func TestAdv_5_DetailOffCrossProcess(t *testing.T) {
	crossProcess(t, "direct-1000-identical-reads", seed+1, true)
}
func TestAdv_5_OverloadCrossProcess(t *testing.T) {
	crossProcess(t, "direct-open-loop-overload", seed+2, false)
}

func baselineSQL(t *testing.T, name string, which int) {
	t.Helper()
	start(t)
	seeds := []int64{seed + 11}
	if os.Getenv("ADV_LONG") == "1" {
		seeds = []int64{seed + 11, seed + 12, seed + 13, seed + 14, seed + 15, seed + 16, seed + 17, seed + 18}
	}
	for _, n := range seeds {
		t.Logf("baseline seed=%d", n)
		r := scenarioRun(t, name, n, t.TempDir(), n%2 == 0)
		o := r.Result.Obs
		var sql string
		switch which {
		case 0:
			if o.DBConnsPeak != 500 || o.DBSessionsPeak != 500 {
				t.Fatalf("simulator peaks=%d/%d want 500", o.DBConnsPeak, o.DBSessionsPeak)
			}
			sql = fmt.Sprintf("-- requires: fakepg.conns_open_peak fakepg.sessions_peak\nSELECT metric FROM final WHERE component='fakepg' AND ((metric='conns_open_peak' AND value<>%d) OR (metric='sessions_peak' AND value<>%d));\n", o.DBConnsPeak, o.DBSessionsPeak)
		case 1:
			if o.DBReads != 1000 {
				t.Fatalf("simulator reads=%d want 1000", o.DBReads)
			}
			sql = fmt.Sprintf("-- requires: fakepg.statements workload.requests\nSELECT 'bad reads' WHERE (SELECT sum(value) FROM final WHERE component='fakepg' AND metric='statements' AND mode='read') IS DISTINCT FROM %d UNION ALL SELECT 'bad client successes' WHERE (SELECT sum(value) FROM final WHERE component='workload' AND metric='requests' AND outcome='ok') IS DISTINCT FROM %d;\n", o.DBReads, o.Clients.Total.OK)
		case 2:
			if o.Clients.Total.Intended == 0 || o.Clients.Total.Sent != o.Clients.Total.Intended {
				t.Fatalf("simulator sent/intended=%d/%d", o.Clients.Total.Sent, o.Clients.Total.Intended)
			}
			sql = fmt.Sprintf("-- requires: workload.sent workload.intended\nSELECT metric FROM final WHERE component='workload' AND metric IN ('sent','intended') GROUP BY metric HAVING sum(value) <> CASE metric WHEN 'sent' THEN %d ELSE %d END;\n", o.Clients.Total.Sent, o.Clients.Total.Intended)
		}
		out, e := runner(t, r.RunDir, sql)
		if e != nil {
			t.Fatalf("seed=%d SQL disagrees with simulator: %v\n%s\n%s", n, e, sql, out)
		} // Independent canary makes the same SQL compare against an impossible exact value.
		bad := strings.ReplaceAll(sql, "<>500", "<>501")
		if which == 1 {
			bad = strings.ReplaceAll(sql, "FROM 1000", "FROM 1001")
		}
		if which == 2 {
			bad = strings.ReplaceAll(sql, fmt.Sprintf("THEN %d", o.Clients.Total.Sent), "THEN -1")
		}
		if bad == sql {
			t.Fatal("canary failed to change SQL")
		}
		out, e = runner(t, r.RunDir, bad)
		if e == nil {
			t.Fatalf("seed=%d planted incorrect exact count passed: %s", n, out)
		}
	}
}
func TestAdv_11_ConnectionPeaksAtIrregularPublishes(t *testing.T) {
	baselineSQL(t, "direct-500-connections", 0)
}
func TestAdv_11_IdenticalReadCountsAtTiedPublishes(t *testing.T) {
	baselineSQL(t, "direct-1000-identical-reads", 1)
}
func TestAdv_11_OverloadCountsWithDetailAblation(t *testing.T) {
	baselineSQL(t, "direct-open-loop-overload", 2)
}
