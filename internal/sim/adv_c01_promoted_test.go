// Property and regression tests for simulator kernel determinism, stream
// isolation, timer ordering, shrinking, quiescence, and determinism-lint boundaries.
package sim_test

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
)

const c01Seed int64 = 731921

func c01Mark(t *testing.T) {
	t.Helper()
	t.Logf("seed=%d; REPLAY: go test -count=1 ./internal/sim -run '^%s$' -v", c01Seed, t.Name())
}

func c01Must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func c01ReplayWork() sim.Digests {
	s := sim.New(sim.Config{Seed: c01Seed, TraceRing: 3})
	r := s.Stream("adversary")
	var total int64
	s.RegisterState("total", func(w sim.StateSink) { w.Int(total) })
	for i := 0; i < 4096; i++ {
		v := r.Int64()
		tm := s.At(sim.Time(v%31), func() { total ^= v; s.Trace(sim.K("attack"), v, total, 0, 0) })
		if i%7 == 0 {
			tm.Cancel()
		}
	}
	if err := s.RunUntilQuiet(); err != nil {
		panic(err)
	}
	return s.Digests()
}

func TestAdv_1_RingWrapReplay(t *testing.T) {
	c01Mark(t)
	a := c01ReplayWork()
	for i := 0; i < 7; i++ {
		if b := c01ReplayWork(); a != b {
			t.Fatalf("digests differ: %v / %v", a, b)
		}
	}
}

func TestAdv_1_ProcessReplay(t *testing.T) {
	c01Mark(t)
	if os.Getenv("ADV_CHILD") == "1" {
		fmt.Println(c01ReplayWork())
		return
	}
	var first []byte
	for i := 0; i < 4; i++ {
		c := exec.Command(os.Args[0], "-test.run=^TestAdv_1_ProcessReplay$")
		c.Env = append(os.Environ(), "ADV_CHILD=1")
		b, e := c.CombinedOutput()
		c01Must(t, e)
		if i > 0 && !bytes.Equal(first, b) {
			t.Fatalf("process outputs differ: %s / %s", first, b)
		}
		first = b
	}
}

func TestAdv_1_TraceToggle(t *testing.T) {
	c01Mark(t)
	var ds [2]sim.Digest
	for i := range ds {
		s := sim.New(sim.Config{Seed: c01Seed, TraceOff: i == 1})
		v := int64(0)
		s.RegisterState("x", func(w sim.StateSink) { w.Int(v) })
		for j := 0; j < 100; j++ {
			s.At(1, func() { v++; s.Trace(sim.K("x"), v, 0, 0, 0) })
		}
		c01Must(t, s.RunUntilQuiet())
		ds[i] = s.StateDigest()
	}
	if ds[0] != ds[1] {
		t.Fatal("trace toggle changed state")
	}
}

func c01StreamAttack(t *testing.T, labels []string, draws int) {
	c01Mark(t)
	for _, label := range labels {
		a, b := sim.NewStream(c01Seed, label), sim.NewStream(c01Seed, label)
		noise := sim.NewStream(c01Seed, "noise")
		for i := 0; i < draws; i++ {
			for j := 0; j < i%19; j++ {
				noise.Uint64()
			}
			if a.Uint64() != b.Uint64() {
				t.Fatalf("stream %q shifted at draw %d", label, i)
			}
		}
	}
}

func TestAdv_2_HostileLabels(t *testing.T) {
	c01StreamAttack(t, []string{"", "\x00", "a/b", "a\x00b", string(bytes.Repeat([]byte("x"), 8192))}, 300)
}

func TestAdv_2_InterleavedConsumption(t *testing.T) {
	c01StreamAttack(t, []string{"up", "down", "conn/999999"}, 10000)
}

func TestAdv_2_RecreatedStreams(t *testing.T) {
	c01Mark(t)
	for _, sd := range []int64{math.MinInt64, -1, 0, math.MaxInt64} {
		a := sim.NewStream(sd, "persist")
		want := a.Uint64()
		for j := 0; j < 200; j++ {
			other := sim.NewStream(sd, fmt.Sprint(j))
			other.Uint64()
			if got := sim.NewStream(sd, "persist").Uint64(); got != want {
				t.Fatalf("seed=%d recreated stream differs", sd)
			}
		}
	}
}

func TestAdv_3_CancelInsideTie(t *testing.T) {
	c01Mark(t)
	s := sim.New(sim.Config{Seed: c01Seed})
	var victim sim.Timer
	var got []int
	s.At(7, func() {
		got = append(got, 1)
		if !victim.Cancel() {
			t.Error("pending victim not cancelled")
		}
		s.At(7, func() { got = append(got, 4) })
	})
	victim = s.At(7, func() { t.Error("cancelled callback fired") })
	s.At(7, func() { got = append(got, 3) })
	c01Must(t, s.RunUntilQuiet())
	if !slices.Equal(got, []int{1, 3, 4}) {
		t.Fatalf("tie order %v", got)
	}
}

func TestAdv_3_BackgroundHandleCopies(t *testing.T) {
	c01Mark(t)
	s := sim.New(sim.Config{Seed: c01Seed})
	for i := 0; i < 10000; i++ {
		a := s.At(sim.MaxTime, func() { t.Error("cancelled timer fired") })
		b := a
		b.MarkBackground()
		a.MarkBackground()
		if !a.Cancel() || b.Cancel() || b.Pending() {
			t.Fatal("copy cancellation inconsistent")
		}
	}
	st := s.Snapshot()
	if st.HeapLen != 0 || st.BackgroundTimers != 0 {
		t.Fatalf("cancel residue %+v", st)
	}
	c01Must(t, s.RunUntilQuiet())
}

func TestAdv_3_ExtremeTimeArithmetic(t *testing.T) {
	c01Mark(t)
	vs := []int64{math.MinInt64, math.MinInt64 + 1, -1, 0, 1, math.MaxInt64 - 1, math.MaxInt64}
	clamp := func(v *big.Int) int64 {
		if v.Cmp(big.NewInt(math.MaxInt64)) > 0 {
			return math.MaxInt64
		}
		if v.Cmp(big.NewInt(math.MinInt64)) < 0 {
			return math.MinInt64
		}
		return v.Int64()
	}
	for _, a := range vs {
		for _, b := range vs {
			want := clamp(new(big.Int).Add(big.NewInt(a), big.NewInt(b)))
			if got := int64(sim.Time(a).Add(time.Duration(b))); got != want {
				t.Fatalf("add %d %d=%d want %d", a, b, got, want)
			}
			want = clamp(new(big.Int).Sub(big.NewInt(a), big.NewInt(b)))
			if got := int64(sim.Time(a).Sub(sim.Time(b))); got != want {
				t.Fatalf("sub %d %d=%d want %d", a, b, got, want)
			}
		}
	}
}

func TestAdv_7_BackgroundMasksNoForeground(t *testing.T) {
	c01Mark(t)
	s := sim.New(sim.Config{Seed: c01Seed})
	for i := 0; i < 100; i++ {
		s.At(100, func() {}).MarkBackground()
	}
	tm := s.At(100, func() {})
	if s.CheckQuiesce() == nil {
		t.Fatal("foreground timer hidden among background timers")
	}
	tm.Cancel()
	c01Must(t, s.CheckQuiesce())
}

func c01ShrinkCheck(t *testing.T, in []sim.Fault, p func([]sim.Fault) bool) {
	t.Helper()
	orig := slices.Clone(in)
	if !p(in) {
		t.Fatal("invalid attack: initial schedule does not fail")
	}
	out := sim.Shrink(in, p)
	if !p(out) {
		t.Fatalf("shrunk schedule no longer fails: %v", out)
	}
	for i := range out {
		c := slices.Delete(slices.Clone(out), i, i+1)
		if p(c) {
			t.Fatalf("removing fault %d still fails: %v", i, out)
		}
	}
	if !reflect.DeepEqual(orig, in) {
		t.Fatal("shrinker mutated caller input")
	}
}

func TestAdv_9_NonmonotonePredicate(t *testing.T) {
	c01Mark(t)
	in := make([]sim.Fault, 25)
	for i := range in {
		in[i].Target = int64(i)
	}
	c01ShrinkCheck(t, in, func(f []sim.Fault) bool {
		sum := int64(0)
		for _, x := range f {
			sum += x.Target
		}
		return len(f) > 0 && (sum%7 == 6 || len(f) == 25)
	})
}

func TestAdv_9_OrderedDuplicateTriggers(t *testing.T) {
	c01Mark(t)
	in := []sim.Fault{{Target: 9}, {Target: 2}, {Target: 9}, {Target: 3}, {Target: 9}, {Target: 2}, {Target: 3}}
	c01ShrinkCheck(t, in, func(fs []sim.Fault) bool {
		stage := 0
		for _, f := range fs {
			if f.Target == []int64{9, 2, 3}[stage] {
				stage++
				if stage == 3 {
					return true
				}
			}
		}
		return false
	})
}

func TestAdv_9_EmptyAndMaximumSchedule(t *testing.T) {
	c01Mark(t)
	c01ShrinkCheck(t, []sim.Fault{}, func([]sim.Fault) bool { return true })
	in := make([]sim.Fault, sim.MaxScheduleLen)
	for i := range in {
		in[i].Target = int64(i)
	}
	c01ShrinkCheck(t, in, func(fs []sim.Fault) bool {
		for _, f := range fs {
			if f.Target == 513 {
				return true
			}
		}
		return false
	})
}

func c01ProductAST(t *testing.T, visit func(string, *ast.File)) {
	t.Helper()
	for _, dir := range []string{"internal/sim", "internal/obs"} {
		err := filepath.WalkDir(filepath.Join("../..", dir), func(p string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if d.IsDir() {
				if d.Name() == "simtest" || d.Name() == "obstest" || d.Name() == "csvflush" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			f, e := parser.ParseFile(token.NewFileSet(), p, nil, 0)
			if e != nil {
				return e
			}
			visit(p, f)
			return nil
		})
		c01Must(t, err)
	}
}

func TestAdv_10_AliasedEntropyAndClock(t *testing.T) {
	c01Mark(t)
	c01ProductAST(t, func(p string, f *ast.File) {
		imports := map[string]string{}
		for _, im := range f.Imports {
			path, _ := strconv.Unquote(im.Path.Value)
			name := filepath.Base(path)
			if im.Name != nil {
				name = im.Name.Name
			}
			imports[name] = path
			if path == "crypto/rand" || path == "os" || path == "net" || path == "runtime" {
				t.Errorf("process/entropy import %s in %s", path, p)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			path := imports[id.Name]
			if path == "time" {
				switch sel.Sel.Name {
				case "Now", "Since", "Until", "Sleep", "After", "AfterFunc", "NewTimer", "NewTicker", "Tick":
					t.Errorf("wall clock reference %s.%s in %s", id.Name, sel.Sel.Name, p)
				}
			}
			if path == "math/rand" || path == "math/rand/v2" {
				switch sel.Sel.Name {
				case "New", "NewPCG", "NewSource", "Rand":
				default:
					t.Errorf("global random reference %s.%s in %s", id.Name, sel.Sel.Name, p)
				}
			}
			return true
		})
	})
}

func TestAdv_10_ConcurrencyAndFloatingSyntax(t *testing.T) {
	c01Mark(t)
	c01ProductAST(t, func(p string, f *ast.File) {
		ast.Inspect(f, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.GoStmt, *ast.ChanType, *ast.SendStmt, *ast.SelectStmt:
				t.Errorf("concurrency syntax %T in %s", v, p)
			case *ast.BasicLit:
				if v.Kind == token.FLOAT || v.Kind == token.IMAG {
					t.Errorf("floating literal %s in %s", v.Value, p)
				}
			case *ast.Ident:
				if v.Name == "float32" || v.Name == "float64" || v.Name == "complex64" || v.Name == "complex128" {
					t.Errorf("floating type %s in %s", v.Name, p)
				}
			}
			return true
		})
	})
}

func TestTierRAdvC01_10_DeterminismLintNoAllowances(t *testing.T) {
	c01Mark(t)
	if os.Getenv("MG_TIER_R") != "1" {
		t.Skip("tier R: set MG_TIER_R=1 (runs /usr/local/go/bin/go run in tools/detlint)")
	}
	root, e := filepath.Abs("../..")
	c01Must(t, e)
	c := exec.Command("/usr/local/go/bin/go", "run", ".", "-root", root, "internal/sim", "internal/sim/simnet", "internal/sim/simtoy", "internal/obs")
	c.Dir = filepath.Join(root, "tools/detlint")
	b, e := c.CombinedOutput()
	if e != nil {
		t.Fatalf("determinism lint without allowances: %v\n%s", e, b)
	}
	t.Log(string(b))
}
