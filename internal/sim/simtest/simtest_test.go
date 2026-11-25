package simtest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
)

// Proves PinnedSeeds returns nil when seeds.txt is absent, strips comments and
// annotations, and rejects malformed lines.
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

// Proves Seeds reads DST_SEEDS and DST_SEED_BASE and lets DST_SEED override
// the sweep to a single seed.
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

// Proves CheckDeterministic accepts repeatable runs and returns
// ErrNondeterministic when two runs of one seed diverge.
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

// Proves ReplayCommand and RunPattern produce an exact, anchored go test
// reproduction command.
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

// Proves Seeds places pinned seeds first, deduplicates overlaps with the swept
// range, and Sweep runs each seed as a subtest.
func TestSeedsDeduplicatePinnedAndSwept(t *testing.T) {
	if _, ok, _ := Seed(); ok {
		t.Skip("an explicit seed is set")
	}
	dir := t.TempDir()
	_ = os.Mkdir(filepath.Join(dir, "testdata"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "testdata", SeedsFile), []byte("3\n50\n3\n"), 0o644)
	t.Chdir(dir)
	t.Setenv("DST_SEEDS", "4")
	t.Setenv("DST_SEED_BASE", "2")
	if got := Seeds(t); !slices.Equal(got, []int64{3, 50, 2, 4, 5}) {
		t.Fatalf("%v", got)
	}
	var ran []string
	Sweep(t, func(t *testing.T, seed int64) { ran = append(ran, t.Name()) })
	if len(ran) != 5 || !strings.HasSuffix(ran[0], "/seed=3") {
		t.Fatalf("%v", ran)
	}
}

// Proves the custom -seed flag value parses signed integers and rejects invalid strings.
func TestSeedFlagParsing(t *testing.T) {
	var v seedValue
	if v.String() != "" || v.Set("x") == nil {
		t.Fatal("bad value")
	}
	if err := v.Set("-12"); err != nil || v.String() != "-12" {
		t.Fatal(v.String())
	}
	var nilValue *seedValue
	if nilValue.String() != "" {
		t.Fatal("nil")
	}
}

type fakeTB struct {
	testing.TB
	name string
	logs []string
}

func (f *fakeTB) Helper()                 {}
func (f *fakeTB) Name() string            { return f.name }
func (f *fakeTB) Log(args ...any)         { f.logs = append(f.logs, fmt.Sprint(args...)) }
func (f *fakeTB) Logf(s string, a ...any) { f.logs = append(f.logs, fmt.Sprintf(s, a...)) }

// Proves Report prints seed, error, trace tail, artifact path, PIN line, and
// ends with the REPLAY command, while ignoring a nil simulator.
func TestReportPrintsTailPinAndReplay(t *testing.T) {
	s := run(11)
	s.Fail(errors.New("boom"))
	dir := t.TempDir()
	t.Setenv("DST_ARTIFACTS", dir)
	f := &fakeTB{name: "TestX/seed=11"}
	Report(f, s)
	if len(f.logs) != 1 {
		t.Fatalf("%d log calls", len(f.logs))
	}
	out := f.logs[0]
	for _, want := range []string{"seed: 11", "error: boom", "trace tail (10 of 10)", "PIN: echo '11 TestX/seed=11'", "artifact: ", "REPLAY: go test ./internal/sim/simtest -run '^TestX$/^seed=11$' -seed=11 -count=1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if !strings.HasPrefix(lines[len(lines)-1], "REPLAY: ") {
		t.Fatal("REPLAY is not the last line")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), "-seed11.trace.txt") {
		t.Fatalf("artifact %v", entries)
	}
	Report(f, nil)
	if len(f.logs) != 1 {
		t.Fatal("nil sim reported")
	}
}
