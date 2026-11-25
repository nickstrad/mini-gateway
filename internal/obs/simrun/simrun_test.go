package simrun_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/csvflush"
	"github.com/nickstrad/mini-gateway/internal/obs/simrun"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/simtest"
	"github.com/nickstrad/mini-gateway/internal/sim/world"
)

func flushSeeds(t *testing.T) []int64 {
	if s, ok, err := simtest.Seed(); err != nil {
		t.Fatal(err)
	} else if ok {
		return []int64{s}
	}
	seeds, err := simtest.PinnedSeeds("testdata")
	if err != nil || len(seeds) == 0 {
		t.Fatalf("pinned seeds: %v", err)
	}
	if testing.Short() {
		return seeds[:1]
	}
	return seeds
}

func files(t *testing.T, dir string) []byte {
	t.Helper()
	var out []byte
	for _, f := range []string{csvflush.MetricsFile, csvflush.HistogramsFile, csvflush.RunFile} {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, b...)
	}
	return out
}

// TestScenarioFlush proves every baseline scenario runs with all four adapters,
// passes round-trip and replay byte-identity checks, and matches simulator counts.
func TestScenarioFlush(t *testing.T) {
	for _, sc := range world.Scenarios() {
		t.Run(sc.Name, func(t *testing.T) {
			for _, seed := range flushSeeds(t) {
				t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
					replay := fmt.Sprintf("REPLAY: go test ./internal/obs/simrun -run '^TestScenarioFlush$/^%s$' -seed=%d -count=1", sc.Name, seed)
					a, err := simrun.Scenario(sc, seed, simrun.Options{Dir: runDir(t)})
					if err != nil {
						t.Fatalf("%v\n%s", err, replay)
					}
					if err := sc.Verify(a.Result); err != nil {
						t.Fatalf("%v\n%s", err, replay)
					}
					if err := a.Check(); err != nil {
						t.Fatalf("%v\n%s", err, replay)
					}
					b, err := simrun.Scenario(sc, seed, simrun.Options{Dir: t.TempDir()})
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(files(t, a.RunDir), files(t, b.RunDir)) {
						t.Fatalf("flushed files differ between two runs of one seed\n%s", replay)
					}
					if !bytes.Equal(a.Prom.Last(), b.Prom.Last()) || !bytes.Equal(a.JSON.Last(), b.JSON.Last()) || !bytes.Equal(a.Log.Bytes(), b.Log.Bytes()) {
						t.Fatalf("adapter output differs between two runs of one seed\n%s", replay)
					}
					if a.Result.Sim.TraceDigest() != b.Result.Sim.TraceDigest() {
						t.Fatalf("trace digests differ\n%s", replay)
					}
					checkBaselineCounts(t, sc.Name, a.Final, a.Result)
				})
			}
		})
	}
}

func checkBaselineCounts(t *testing.T, name string, s obs.InstanceSnapshot, r *world.Result) {
	t.Helper()
	if got, want := s.Value(obs.ComponentFakepg, "sessions_peak", obs.Labels{}), r.Obs.DBSessionsPeak; got != want {
		t.Fatalf("sessions peak exported %d, simulator %d", got, want)
	}
	if got, want := s.Value(obs.ComponentFakepg, "statements", obs.Labels{Mode: "read"}), r.Obs.DBReads; got != want {
		t.Fatalf("reads exported %d, simulator %d", got, want)
	}
	if got, want := s.Sum(obs.ComponentWorkload, "intended"), r.Obs.Clients.Total.Intended; got != want {
		t.Fatalf("intended exported %d, simulator %d", got, want)
	}
	if got, want := s.Sum(obs.ComponentWorkload, "sent"), r.Obs.Clients.Total.Sent; got != want {
		t.Fatalf("sent exported %d, simulator %d", got, want)
	}
	switch name {
	case "direct-500-connections":
		if s.Value(obs.ComponentFakepg, "sessions_peak", obs.Labels{}) != world.ManyClients || s.Value(obs.ComponentWorkload, "clients", obs.Labels{}) != world.ManyClients {
			t.Fatal("500 / 500 not exported")
		}
	case "direct-1000-identical-reads":
		if s.Value(obs.ComponentFakepg, "statements", obs.Labels{Mode: "read"}) != world.IdenticalReads {
			t.Fatal("1,000 / 1,000 not exported")
		}
	}
}

// TestPublishIsAnEventOnTheVirtualClockAndFlushAtWorks proves periodic and
// explicit FlushAt publishes run on the virtual clock and stamp exact t_ns values.
func TestPublishIsAnEventOnTheVirtualClockAndFlushAtWorks(t *testing.T) {
	sc, _ := world.Lookup("direct-1000-identical-reads")
	at := sim.Time(20_500_000)
	r, err := simrun.Scenario(sc, 1, simrun.Options{Dir: t.TempDir(), Interval: 250_000_000, FlushAt: []sim.Time{at}})
	if err != nil {
		t.Fatal(err)
	}
	if r.PublishAt[0] != 0 || r.PublishAt[1] != at || r.PublishAt[2] != 250_000_000 {
		t.Fatalf("publish instants %v", r.PublishAt[:3])
	}
	run, err := csvflush.Read(r.RunDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range run.Metrics {
		if m.FlushSeq == 1 && m.TNs != int64(at) {
			t.Fatalf("flush 1 stamped %d, want the virtual instant %d", m.TNs, at)
		}
	}
	if r.Final.Value(obs.ComponentObs, obs.MetricPublishes, obs.Labels{}) != r.Publishes {
		t.Fatal("publishes counter")
	}
	if r.Final.Value(obs.ComponentObs, obs.MetricCSVFlushes, obs.Labels{}) != r.Publishes-1 {
		t.Fatalf("csv flushes %d, publishes %d", r.Final.Value(obs.ComponentObs, obs.MetricCSVFlushes, obs.Labels{}), r.Publishes)
	}
	if err := r.Check(); err != nil {
		t.Fatal(err)
	}
}

// TestDetailOffExportsNoHistogramAndNoTenant proves DetailOff suppresses
// histograms and folds per-tenant workload counters into the empty tenant.
func TestDetailOffExportsNoHistogramAndNoTenant(t *testing.T) {
	sc, _ := world.Lookup("direct-mixed-transactions")
	r, err := simrun.Scenario(sc, 1, simrun.Options{Dir: t.TempDir(), DetailOff: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Final.Hists) != 0 || len(r.Final.Tenants()) != 0 {
		t.Fatalf("detail off: %d histograms, tenants %v", len(r.Final.Hists), r.Final.Tenants())
	}
	if got, want := r.Final.Value(obs.ComponentWorkload, "intended", obs.Labels{}), r.Result.Obs.Clients.Total.Intended; got != want || got == 0 {
		t.Fatalf("folded intended %d want %d", got, want)
	}
	if err := r.Check(); err != nil {
		t.Fatal(err)
	}
}

func runDir(t *testing.T) string {
	if v := os.Getenv("RUN_DIR"); v != "" {
		return v
	}
	return t.TempDir()
}
