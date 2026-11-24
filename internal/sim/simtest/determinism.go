package simtest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/sim"
)

const TailLines = 20

var ErrNondeterministic = errors.New("simtest: same seed gave different digests")

func CheckDeterministic(seed int64, run func(seed int64) *sim.Sim) (*sim.Sim, error) {
	a := run(seed)
	b := run(seed)
	da, db := a.Digests(), b.Digests()
	if da != db {
		return a, fmt.Errorf("%w: seed %d: first run %s, second run %s", ErrNondeterministic, seed, da, db)
	}
	if err := a.Verify(); err != nil {
		return a, err
	}
	return a, nil
}

func RequireDeterministic(t testing.TB, seed int64, run func(seed int64) *sim.Sim) *sim.Sim {
	t.Helper()
	s, err := CheckDeterministic(seed, run)
	t.Cleanup(func() {
		if t.Failed() {
			Report(t, s)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func moduleRoot(dir string) (string, bool) {
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func packagePath() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	root, ok := moduleRoot(wd)
	if !ok {
		return "."
	}
	rel, err := filepath.Rel(root, wd)
	if err != nil || rel == "." {
		return "."
	}
	return "./" + filepath.ToSlash(rel)
}

func RunPattern(name string) string {
	parts := strings.Split(name, "/")
	for i, p := range parts {
		parts[i] = "^" + regexp.QuoteMeta(p) + "$"
	}
	return strings.Join(parts, "/")
}

func ReplayCommand(t testing.TB, seed int64) string {
	return fmt.Sprintf("REPLAY: go test %s -run '%s' -seed=%d -count=1", packagePath(), RunPattern(t.Name()), seed)
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9_.=-]+`)

func writeArtifact(t testing.TB, s *sim.Sim) (string, error) {
	dir := os.Getenv("DST_ARTIFACTS")
	if dir == "" {
		return "", nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("simtest: artifacts: %w", err)
	}
	name := unsafeName.ReplaceAllString(t.Name(), "_") + fmt.Sprintf("-seed%d.trace.txt", s.Seed())
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("simtest: artifacts: %w", err)
	}
	werr := s.WriteLog(f)
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		return "", fmt.Errorf("simtest: artifacts: %w", err)
	}
	return path, nil
}

func Report(t testing.TB, s *sim.Sim) {
	t.Helper()
	if s == nil {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "simulation failed\nseed: %d\nerror: %v\ndigests: %s\nsteps: %d, virtual time: %d\n",
		s.Seed(), s.Err(), s.Digests(), s.Steps(), s.Now())
	tail := s.LogTail(TailLines)
	fmt.Fprintf(&b, "trace tail (%d of %d):\n", len(tail), s.LogLen())
	for _, line := range tail {
		b.WriteString("  " + line + "\n")
	}
	if path, err := writeArtifact(t, s); err != nil {
		fmt.Fprintf(&b, "artifact: %v\n", err)
	} else if path != "" {
		fmt.Fprintf(&b, "artifact: %s\n", path)
	}
	fmt.Fprintf(&b, "PIN: echo '%d %s' >> testdata/%s\n", s.Seed(), t.Name(), SeedsFile)
	b.WriteString(ReplayCommand(t, s.Seed()))
	t.Log(b.String())
}
