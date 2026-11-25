// Package simtest provides test plumbing for deterministic simulation suites:
// -seed and DST_SEED/DST_SEEDS seed selection, pinned seed files, multi-seed
// sweeps, run-twice replay verification, and failure reports with REPLAY lines.
//
// Adapted from object-store internal/sim/simtest at commit
// 25fb899317955eb7d87186f940702e55dc0e3cde. Because it reads flags,
// environment variables, and files, simtest is excluded from detlint.
package simtest

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Default seed-sweep settings and the filename loaded from testdata/.
const (
	// DefaultSeeds is the number of consecutive seeds swept when DST_SEEDS is unset.
	DefaultSeeds = 3
	// DefaultSeedBase is the starting seed when DST_SEED_BASE is unset.
	DefaultSeedBase = 1
	// SeedsFile is the pinned-seeds filename read from testdata/.
	SeedsFile = "seeds.txt"
)

type seedValue struct {
	set bool
	v   int64
}

func (s *seedValue) String() string {
	if s == nil || !s.set {
		return ""
	}
	return strconv.FormatInt(s.v, 10)
}

func (s *seedValue) Set(v string) error {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return fmt.Errorf("simtest: -seed %q: %w", v, err)
	}
	s.v, s.set = n, true
	return nil
}

var seedFlag seedValue

func init() {
	flag.Var(&seedFlag, "seed", "")
}

// Seed returns the explicitly pinned single seed from the -seed flag or
// DST_SEED environment variable, reporting whether one was configured.
func Seed() (int64, bool, error) {
	if seedFlag.set {
		return seedFlag.v, true, nil
	}
	v := os.Getenv("DST_SEED")
	if v == "" {
		return 0, false, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("simtest: DST_SEED=%q: %w", v, err)
	}
	return n, true, nil
}

func envInt(name string, def int64) (int64, error) {
	v := os.Getenv(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("simtest: %s=%q: %w", name, v, err)
	}
	return n, nil
}

// PinnedSeeds reads integer seeds from dir/seeds.txt, ignoring blank lines and
// '#' comments and taking the first whitespace-delimited field on each line.
// A missing file returns (nil, nil).
func PinnedSeeds(dir string) ([]int64, error) {
	data, err := os.ReadFile(filepath.Join(dir, SeedsFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("simtest: pinned seeds: %w", err)
	}
	var out []int64
	for i, raw := range strings.Split(string(data), "\n") {
		text := strings.TrimSpace(raw)
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		n, err := strconv.ParseInt(strings.Fields(text)[0], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("simtest: %s line %d: %w", SeedsFile, i+1, err)
		}
		out = append(out, n)
	}
	return out, nil
}

// Seeds returns the deduplicated list of seeds for the current test: either
// the single -seed / DST_SEED override, or testdata/seeds.txt followed by
// DST_SEEDS consecutive seeds starting at DST_SEED_BASE.
func Seeds(t testing.TB) []int64 {
	t.Helper()
	if seed, ok, err := Seed(); err != nil {
		t.Fatal(err)
	} else if ok {
		return []int64{seed}
	}
	count, err1 := envInt("DST_SEEDS", DefaultSeeds)
	base, err2 := envInt("DST_SEED_BASE", DefaultSeedBase)
	pinned, err3 := PinnedSeeds("testdata")
	if err := errors.Join(err1, err2, err3); err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	var out []int64
	for _, s := range pinned {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for i := int64(0); i < count; i++ {
		s := base + i
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// Sweep runs fn as a subtest named "seed=<n>" for every seed returned by Seeds.
func Sweep(t *testing.T, fn func(t *testing.T, seed int64)) {
	t.Helper()
	for _, seed := range Seeds(t) {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) { fn(t, seed) })
	}
}
