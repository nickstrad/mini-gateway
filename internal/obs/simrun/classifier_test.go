package simrun_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/simrun"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/pgwire/pgwiretest/clsworld"
	"github.com/nickstrad/mini-gateway/internal/work"
)

func classifierRuns(dir string) error {
	for _, sc := range clsworld.Scenarios() {
		r, err := simrun.Classifier(sc, 1, simrun.Options{Dir: dir})
		if err == nil {
			err = r.Classifier.Verify()
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

func TestScenarioClassifierFlush(t *testing.T) {
	for _, sc := range clsworld.Scenarios() {
		t.Run(sc.Name, func(t *testing.T) {
			for _, seed := range flushSeeds(t) {
				t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
					replay := fmt.Sprintf("REPLAY: go test ./internal/obs/simrun -run '^TestScenarioClassifierFlush$/^%s$' -seed=%d -count=1", sc.Name, seed)
					a, err := simrun.Classifier(sc, seed, simrun.Options{Dir: runDir(t), Replay: replay})
					if err != nil {
						t.Fatalf("%v\n%s", err, replay)
					}
					if err := a.Classifier.Verify(); err != nil {
						t.Fatalf("%v\n%s", err, replay)
					}
					if err := a.Check(); err != nil {
						t.Fatalf("%v\n%s", err, replay)
					}
					b, err := simrun.Classifier(sc, seed, simrun.Options{Dir: t.TempDir(), Replay: replay})
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(files(t, a.RunDir), files(t, b.RunDir)) {
						t.Fatalf("flushed files differ between two runs of one seed\n%s", replay)
					}
					if !bytes.Equal(a.Prom.Last(), b.Prom.Last()) || !bytes.Equal(a.JSON.Last(), b.JSON.Last()) || !bytes.Equal(a.Log.Bytes(), b.Log.Bytes()) {
						t.Fatalf("adapter output differs between two runs of one seed\n%s", replay)
					}
					if a.Classifier.World.Sim.TraceDigest() != b.Classifier.World.Sim.TraceDigest() {
						t.Fatalf("trace digests differ\n%s", replay)
					}
					checkClassifierCounts(t, a.Final, a.Classifier)
				})
			}
		})
	}
}

func checkClassifierCounts(t *testing.T, s obs.InstanceSnapshot, r clsworld.Result) {
	t.Helper()
	c := r.Classifier
	pairs := []struct {
		metric string
		l      obs.Labels
		want   int64
	}{
		{"statements", obs.Labels{}, c.Classified},
		{"statements_by_class", obs.Labels{Outcome: "reject"}, c.ByClass[work.ClassReject]},
		{"statements_by_class", obs.Labels{Outcome: "start-lease"}, c.ByClass[work.ClassStartLease]},
		{"rejects", obs.Labels{Reason: "session-set"}, c.ByReason[work.RejectSessionSet]},
		{"replies", obs.Labels{Reason: "unsupported-feature"}, r.Replies.ByRow[pgwire.ErrRowUnsupportedFeature]},
		{"replies_built", obs.Labels{}, r.Replies.Total},
		{"extended_refused", obs.Labels{}, r.Sync.Refused},
		{"sql_statements", obs.Labels{}, c.Statements},
	}
	for _, p := range pairs {
		if got := s.Value(obs.ComponentClassifier, p.metric, p.l); got != p.want {
			t.Fatalf("classifier.%s %+v exported %d, snapshot %d", p.metric, p.l, got, p.want)
		}
	}
	if s.Value(obs.ComponentClassifier, "statements", obs.Labels{}) == 0 || s.Sum(obs.ComponentClassifier, "rejects") == 0 {
		t.Fatal("the classifier counters did not move")
	}
	if got, want := s.Value(obs.ComponentWire, "client_queries_seen", obs.Labels{}), r.Plan.SentRejects+r.Plan.SentAllowed; got != want {
		t.Fatalf("wire.client_queries_seen exported %d, the clients sent %d", got, want)
	}
}

var classifierSQL = map[string][]string{
	clsworld.NameRejectOnly: {"rejects-equal-sent.sql", "fakepg-statements-zero.sql"},
	clsworld.NameRejectList: {"forwarded-equal-started.sql"},
}

func classifierSQLFiles(t *testing.T, scenario string) []string {
	var out []string
	for _, f := range classifierSQL[scenario] {
		out = append(out, filepath.Join(repoRoot(t), "test", "analysis", scenario, f))
	}
	return out
}

func TestClassifierSQLFilesAreTheOnesInTheTree(t *testing.T) {
	for _, sc := range clsworld.Scenarios() {
		m, err := filepath.Glob(filepath.Join(repoRoot(t), "test", "analysis", sc.Name, "*.sql"))
		must(t, err)
		if len(m) != len(classifierSQL[sc.Name]) {
			t.Fatalf("%s has %d SQL files, the planted-number test knows %d", sc.Name, len(m), len(classifierSQL[sc.Name]))
		}
	}
}

func TestClassifierSQLCatchesPlantedNumbers(t *testing.T) {
	requireDuckDB(t)
	baselineRuns(t)
	cases := []struct {
		scenario string
		file     string
		plant    plant
	}{
		{clsworld.NameRejectOnly, "rejects-equal-sent.sql", plant{"classifier,rejects,counter,,,session-set,", plus(1)}},
		{clsworld.NameRejectOnly, "rejects-equal-sent.sql", plant{"wire,client_queries_seen,counter,,,,", plus(1)}},
		{clsworld.NameRejectOnly, "rejects-equal-sent.sql", plant{"classifier,statements,counter,,,,", plus(1)}},
		{clsworld.NameRejectOnly, "fakepg-statements-zero.sql", plant{"fakepg,statements_started,counter,,,,", plus(1)}},
		{clsworld.NameRejectOnly, "fakepg-statements-zero.sql", plant{"fakepg,startups_completed,counter,,,,", zero}},
		{clsworld.NameRejectList, "forwarded-equal-started.sql", plant{"fakepg,statements_started,counter,,,,", plus(1)}},
		{clsworld.NameRejectList, "forwarded-equal-started.sql", plant{"classifier,rejects,counter,,,listen,", plus(1)}},
		{clsworld.NameRejectList, "forwarded-equal-started.sql", plant{"wire,client_queries_seen,counter,,,,", plus(1)}},
	}
	for _, c := range cases {
		t.Run(c.scenario+"/"+c.file+"/"+c.plant.key, func(t *testing.T) {
			_, run := copyRun(t, c.scenario)
			file := filepath.Join(repoRoot(t), "test", "analysis", c.scenario, c.file)
			if out, err := csvAssert(t, nil, run, file); err != nil {
				t.Fatalf("the unplanted run fails: %v\n%s", err, out)
			}
			plantFinal(t, run, c.plant)
			out, err := csvAssert(t, nil, run, file)
			if err == nil || !strings.Contains(out, "violating rows") {
				t.Fatalf("the runner passed with a planted wrong number: %v\n%s", err, out)
			}
			if strings.Contains(out, "no rows for the metric under test") || strings.Contains(out, "duckdb exit") {
				t.Fatalf("failed for another reason\n%s", out)
			}
		})
	}
}

func TestClassifierRunsUseTheScenarioRunIDs(t *testing.T) {
	root := baselineRuns(t)
	for _, sc := range clsworld.Scenarios() {
		b, err := os.ReadFile(filepath.Join(root, sc.Name, "1", "base", "run.csv"))
		must(t, err)
		if want := fmt.Sprintf(",%s/1/base,%s,", sc.Name, sc.Name); !strings.Contains(string(b), want) {
			t.Fatalf("run.csv of %s does not hold %q:\n%s", sc.Name, want, b)
		}
		if out, err := csvAssert(t, nil, append([]string{filepath.Join(root, sc.Name, "1", "base")}, classifierSQLFiles(t, sc.Name)...)...); duckdbPath() != "" && err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
}
