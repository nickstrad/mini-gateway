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

const (
	DefaultSeeds    = 3
	DefaultSeedBase = 1
	SeedsFile       = "seeds.txt"
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

func Sweep(t *testing.T, fn func(t *testing.T, seed int64)) {
	t.Helper()
	for _, seed := range Seeds(t) {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) { fn(t, seed) })
	}
}
