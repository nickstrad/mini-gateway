package simtest

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
)

func TestPinnedSeedsParse(t *testing.T) {
	dir := t.TempDir()
	if s, err := PinnedSeeds(dir); err != nil || s != nil {
		t.Fatalf("missing file: %v %v", s, err)
	}
	_ = os.WriteFile(filepath.Join(dir, SeedsFile), []byte("# c\n\n7 note\n-3\n"), 0o644)
	if s, err := PinnedSeeds(dir); err != nil || !slices.Equal(s, []int64{7, -3}) {
		t.Fatalf("%v %v", s, err)
	}
	_ = os.WriteFile(filepath.Join(dir, SeedsFile), []byte("x\n"), 0o644)
	if _, err := PinnedSeeds(dir); err == nil {
		t.Fatal("bad line accepted")
	}
}

func TestSeedsFromEnvironment(t *testing.T) {
	if _, ok, _ := Seed(); ok {
		t.Skip("an explicit seed is set")
	}
	t.Setenv("DST_SEEDS", "4")
	t.Setenv("DST_SEED_BASE", "10")
	if got := Seeds(t); !slices.Equal(got, []int64{10, 11, 12, 13}) {
		t.Fatalf("%v", got)
	}
	t.Setenv("DST_SEED", "99")
	if got := Seeds(t); !slices.Equal(got, []int64{99}) {
		t.Fatalf("%v", got)
	}
}

func run(seed int64) *sim.Sim {
	s := sim.New(sim.Config{Seed: seed})
	r := s.Stream("x")
	sum := int64(0)
	s.RegisterState("sum", func(w sim.StateSink) { w.Int(sum) })
	for i := 0; i < 10; i++ {
		s.After(time.Duration(r.IntN(10)), func() { sum += r.Int64N(100); s.Trace(sim.K("x"), sum, 0, 0, 0) })
	}
	_ = s.RunUntilQuiet()
	return s
}

func TestCheckDeterministic(t *testing.T) {
	if _, err := CheckDeterministic(3, run); err != nil {
		t.Fatal(err)
	}
	calls := 0
	flaky := func(seed int64) *sim.Sim {
		calls++
		return run(seed + int64(calls))
	}
	if _, err := CheckDeterministic(3, flaky); !errors.Is(err, ErrNondeterministic) {
		t.Fatalf("got %v", err)
	}
	RequireDeterministic(t, 5, run)
}

func TestReplayCommand(t *testing.T) {
	got := ReplayCommand(t, 42)
	want := "REPLAY: go test ./internal/sim/simtest -run '^TestReplayCommand$' -seed=42 -count=1"
	if got != want {
		t.Fatalf("got %q", got)
	}
	if RunPattern("A/b.c") != "^A$/^b\\.c$" {
		t.Fatal(RunPattern("A/b.c"))
	}
	if !strings.HasPrefix(got, "REPLAY: ") {
		t.Fatal(got)
	}
}
