package simrun_test

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/simrun"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/pgwire/pgwiretest/wireworld"
)

// TestScenarioWireFlush flushes all 19 wire scenarios on pinned seeds through
// all four observability adapters and proves Verify, Run.Check, byte-identical
// replay, and exact counter parity with WireSnapshot.
func TestScenarioWireFlush(t *testing.T) {
	for _, sc := range wireworld.Scenarios() {
		t.Run(sc.Name, func(t *testing.T) {
			for _, seed := range flushSeeds(t) {
				t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
					replay := fmt.Sprintf("REPLAY: go test ./internal/obs/simrun -run '^TestScenarioWireFlush$/^%s$' -seed=%d -count=1", sc.Name, seed)
					a, err := simrun.Wire(sc, seed, simrun.Options{Dir: runDir(t), Replay: replay})
					if err != nil {
						t.Fatalf("%v\n%s", err, replay)
					}
					if err := sc.Verify(a.Wire); err != nil {
						t.Fatalf("%v\n%s", err, replay)
					}
					if err := a.Check(); err != nil {
						t.Fatalf("%v\n%s", err, replay)
					}
					b, err := simrun.Wire(sc, seed, simrun.Options{Dir: t.TempDir(), Replay: replay})
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(files(t, a.RunDir), files(t, b.RunDir)) {
						t.Fatalf("flushed files differ between two runs of one seed\n%s", replay)
					}
					if !bytes.Equal(a.Prom.Last(), b.Prom.Last()) || !bytes.Equal(a.JSON.Last(), b.JSON.Last()) || !bytes.Equal(a.Log.Bytes(), b.Log.Bytes()) {
						t.Fatalf("adapter output differs between two runs of one seed\n%s", replay)
					}
					if a.Wire.World.Sim.TraceDigest() != b.Wire.World.Sim.TraceDigest() {
						t.Fatalf("trace digests differ\n%s", replay)
					}
					checkWireCounts(t, a.Final, a.Wire)
				})
			}
		})
	}
}

func checkWireCounts(t *testing.T, s obs.InstanceSnapshot, r wireworld.Result) {
	t.Helper()
	c, d := r.Final.Client, r.Final.Database
	pairs := []struct {
		metric string
		l      obs.Labels
		want   int64
	}{
		{"client_startups_begun", obs.Labels{}, c.StartupsBegun},
		{"client_startups_completed", obs.Labels{}, c.StartupsCompleted},
		{"database_startups_completed", obs.Labels{}, d.StartupsCompleted},
		{"client_prestartup_requests", obs.Labels{Mode: "tls", Outcome: "S"}, c.Requests[pgwire.RequestTLS][pgwire.AnswerYes]},
		{"database_tls_answers", obs.Labels{Outcome: "S"}, d.TLSAnswers[pgwire.AnswerYes]},
		{"client_bytes_in", obs.Labels{}, c.BytesIn},
		{"database_replies", obs.Labels{}, d.Replies},
	}
	for _, p := range pairs {
		if got := s.Value(obs.ComponentWire, p.metric, p.l); got != p.want {
			t.Fatalf("wire.%s %+v exported %d, snapshot %d", p.metric, p.l, got, p.want)
		}
	}
	if s.Value(obs.ComponentWire, "client_startups_completed", obs.Labels{}) == 0 {
		t.Fatal("the wire counters did not move")
	}
	if got, want := s.Sum(obs.ComponentWorkload, "startup_tls_requests"), r.Workload.Total.Startup.TLSRequests; got != want {
		t.Fatalf("workload TLS requests exported %d, simulator %d", got, want)
	}
}
