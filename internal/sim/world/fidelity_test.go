package world_test

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/sim/world"
)

var update = flag.Bool("update", false, "rewrite the recorded Postgres 17 transcript (tier R only)")

const recording = "testdata/fidelity/postgres17.txt"

func simTranscript(t *testing.T) world.Transcript {
	t.Helper()
	target, err := world.NewSimTarget(1)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := world.RunScript(world.ProbeScript(), target)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func TestFidelityAgainstRecordedPostgres17(t *testing.T) {
	data, err := os.ReadFile(filepath.FromSlash(recording))
	if err != nil {
		t.Fatalf("the recorded transcript is missing (scripts/tier-r.sh with MG_TIER_R_UPDATE=1 writes it): %v", err)
	}
	if err := world.Compare(world.ParseTranscript(string(data)), simTranscript(t)); err != nil {
		t.Fatalf("the fake differs from Postgres 17:\n%v", err)
	}
}

func TestFakeTranscript(t *testing.T) {
	t.Log("\n" + simTranscript(t).String())
}
