package adv_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"math/bits"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
)

const seed int64 = 726031

func start(t *testing.T) { t.Helper(); t.Logf("seed=%d", seed) }
func root() string       { p, _ := filepath.Abs("../../.."); return p }
func command(t *testing.T, dir string, env []string, args ...string) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, args[0], args[1:]...)
	c.Dir = dir
	c.Env = append(os.Environ(), append([]string{"PATH=/usr/local/go/bin:" + os.Getenv("PATH"), "GOMAXPROCS=2"}, env...)...)
	return c.CombinedOutput()
}
func must(t *testing.T, args ...string) []byte {
	t.Helper()
	b, e := command(t, root(), nil, args...)
	if e != nil {
		t.Fatalf("%v: %v\n%s", args, e, b)
	}
	return b
}
func TestAdv_1_FrozenImplementation(t *testing.T) {
	start(t)
	old := must(t, "git", "show", "freeze-sim:internal/sim/fault.go")
	now := must(t, "git", "show", "HEAD:internal/sim/fault.go")
	if !bytes.Equal(old, now) {
		t.Fatal("frozen implementation differs")
	}
}
func TestAdv_1_DeletionTieOrder(t *testing.T) {
	start(t)
	in := []sim.Fault{{Target: 1}, {Target: 2}, {Target: 3}, {Target: 4}}
	var seen [][]sim.Fault
	out := sim.Shrink(in, func(s []sim.Fault) bool { seen = append(seen, slices.Clone(s)); return len(s) >= 2 })
	want := []sim.Fault{{Target: 3}, {Target: 4}}
	if !slices.Equal(out, want) || len(seen) != 6 {
		t.Fatalf("out=%v calls=%v", out, seen)
	}
}
func TestAdv_1_ExistingGoldenContracts(t *testing.T) {
	start(t)
	b := must(t, "go", "test", "./internal/sim/simtoy", "./internal/sim/world", "./internal/pgwire", "-run", "Golden", "-count=1", "-v")
	if !bytes.Contains(b, []byte("--- PASS:")) {
		t.Fatalf("no golden exercised: %s", b)
	}
}
func checkMinimal(t *testing.T, in []sim.Fault, p func([]sim.Fault) bool, fl func(sim.FaultKind) sim.FaultFloor) {
	t.Helper()
	out := sim.Minimize(in, p, fl)
	if !p(out) {
		t.Fatalf("seed=%d result stopped failing: %v -> %v", seed, in, out)
	}
	for i, f := range out {
		if p(append(slices.Clone(out[:i]), out[i+1:]...)) {
			t.Fatalf("deletion %d still fails: %v", i, out)
		}
		floor := fl(f.Kind)
		vals := []int64{int64(f.At), f.Arg1, f.Arg2}
		lows := []int64{int64(floor.At), floor.Arg1, floor.Arg2}
		for k, v := range vals {
			if v <= lows[k] {
				continue
			}
			c := slices.Clone(out)
			switch k {
			case 0:
				c[i].At--
			case 1:
				c[i].Arg1--
			case 2:
				c[i].Arg2--
			}
			if p(c) {
				t.Fatalf("field %d fault %d lowerable: %v", k, i, out)
			}
		}
	}
}
func floor(sim.FaultKind) sim.FaultFloor { return sim.FaultFloor{At: -2, Arg1: -2, Arg2: -2} }
func TestAdv_2_CoupledParity(t *testing.T) {
	start(t)
	r := rand.New(rand.NewSource(seed))
	for j := 0; j < 1200; j++ {
		in := make([]sim.Fault, 1+r.Intn(5))
		for i := range in {
			in[i] = sim.Fault{At: sim.Time(r.Intn(12)), Arg1: int64(r.Intn(12)), Arg2: int64(r.Intn(12)), Target: int64(i)}
		}
		p := func(s []sim.Fault) bool {
			var a int64
			for i, f := range s {
				a += (int64(i) + 1) * (int64(f.At)*f.Arg1 + f.Arg2 + f.Target)
			}
			return len(s) > 0 && a%7 == int64(j%7)
		}
		if p(in) {
			checkMinimal(t, in, p, floor)
		}
	}
}
func TestAdv_2_DeletionUnlocksLowering(t *testing.T) {
	start(t)
	in := []sim.Fault{{At: 9, Arg1: 8, Arg2: 7, Target: 1}, {At: 6, Arg1: 5, Arg2: 4, Target: 2}, {At: 3, Arg1: 2, Arg2: 1, Target: 3}}
	p := func(s []sim.Fault) bool {
		var n int64
		for _, f := range s {
			n += int64(f.At) + f.Arg1 - f.Arg2
		}
		return len(s) > 0 && n >= int64(3*len(s))
	}
	checkMinimal(t, in, p, floor)
}
func TestAdv_2_NegativeFloorsAndIdentity(t *testing.T) {
	start(t)
	in := []sim.Fault{{At: math.MaxInt64, Kind: 65535, Target: math.MinInt64, Arg1: math.MaxInt64, Arg2: math.MaxInt64}}
	fl := func(sim.FaultKind) sim.FaultFloor {
		return sim.FaultFloor{At: math.MinInt64, Arg1: math.MinInt64, Arg2: math.MinInt64}
	}
	p := func(s []sim.Fault) bool {
		return len(s) == 1 && s[0].Kind == 65535 && s[0].Target == math.MinInt64 && s[0].At >= -13 && s[0].Arg1 >= -17 && s[0].Arg2 >= -19
	}
	checkMinimal(t, in, p, fl)
}
func TestAdv_3_FullWidthCallBudget(t *testing.T) {
	start(t)
	for _, accept := range []bool{false, true} {
		calls := 0
		out := sim.LowerInts([]int64{math.MaxInt64}, []int64{math.MinInt64}, func([]int64) bool { calls++; return accept })
		if calls > 130 {
			t.Fatalf("calls=%d > 2C=130", calls)
		}
		want := int64(math.MaxInt64)
		if accept {
			want = math.MinInt64
		}
		if out[0] != want {
			t.Fatalf("got %v want %d", out, want)
		}
	}
}
func TestAdv_3_ExhaustiveBooleanLandscape(t *testing.T) {
	start(t)
	for mask := 0; mask < 256; mask++ {
		calls := 0
		p := func(v []int64) bool { calls++; return mask&(1<<uint(v[0])) != 0 }
		out := sim.LowerInts([]int64{7}, nil, p)
		bound := (1 + 7) * (1 + bits.Len64(7))
		if calls > bound {
			t.Fatalf("mask=%d calls=%d bound=%d", mask, calls, bound)
		}
		if out[0] > 0 && mask&(1<<uint(out[0]-1)) != 0 {
			t.Fatalf("mask=%d out=%v not minimal", mask, out)
		}
	}
}
func TestAdv_3_EmptyAndBelowFloorNoCalls(t *testing.T) {
	start(t)
	p := func([]int64) bool { t.Fatal("unexpected predicate call"); return false }
	if out := sim.LowerInts([]int64{-9, 0, 1}, []int64{-8, 0, 2}, p); !slices.Equal(out, []int64{-9, 0, 1}) {
		t.Fatalf("changed %v", out)
	}
	sim.LowerInts(nil, nil, p)
}
func TestAdv_4_MutatingPredicateIsolation(t *testing.T) {
	start(t)
	in := []sim.Fault{{At: 30, Arg1: 20, Arg2: 10, Target: 73}}
	before := slices.Clone(in)
	out := sim.Minimize(in, func(s []sim.Fault) bool {
		ok := len(s) == 1 && s[0].At >= 5
		for i := range s {
			s[i] = sim.Fault{Target: 999}
		}
		return ok
	}, nil)
	if !slices.Equal(in, before) || len(out) != 1 || out[0].Target != 73 || out[0].At != 5 {
		t.Fatalf("in=%v out=%v", in, out)
	}
}
func TestAdv_4_AliasedFloorsAndReturnedStorage(t *testing.T) {
	start(t)
	v := []int64{4, 9, 15}
	before := slices.Clone(v)
	out := sim.LowerInts(v, v[:2], func(s []int64) bool {
		for i := range s {
			s[i] = 999
		}
		return true
	})
	if !slices.Equal(v, before) {
		t.Fatalf("input/floors overwritten: %v", v)
	}
	out[0] = 888
	if !slices.Equal(v, before) {
		t.Fatal("returned storage aliases input")
	}
}
func processDigest() string {
	r := rand.New(rand.NewSource(seed))
	h := sha256.New()
	for j := 0; j < 300; j++ {
		v := []int64{r.Int63n(100), r.Int63n(100), r.Int63n(100)}
		o := sim.LowerInts(v, nil, func(s []int64) bool { return (s[0]*3+s[1]*5+s[2]*7)%11 == 0 })
		fmt.Fprint(h, o)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}
func TestAdv_4_ProcessReplay(t *testing.T) {
	start(t)
	if os.Getenv("ADV_CHILD") == "1" {
		fmt.Println("DIGEST", processDigest())
		return
	}
	var prev string
	for i := 0; i < 3; i++ {
		b, e := command(t, ".", []string{"ADV_CHILD=1"}, os.Args[0], "-test.run=^TestAdv_4_ProcessReplay$")
		if e != nil {
			t.Fatalf("%v: %s", e, b)
		}
		s := string(b)
		if i > 0 && s != prev {
			t.Fatalf("process differs: %s / %s", prev, s)
		}
		prev = s
	}
}
func TestAdv_5_AllKindsRoundTrip(t *testing.T) {
	start(t)
	for base := 0; base < 65536; base += 1024 {
		in := make([]sim.Fault, 1024)
		for i := range in {
			in[i] = sim.Fault{At: sim.Time(math.MinInt64 + int64(i)), Kind: sim.FaultKind(base + i), Target: math.MaxInt64, Arg1: math.MinInt64, Arg2: int64(i)}
		}
		s, e := sim.FormatSchedule(in, nil)
		if e != nil {
			t.Fatal(e)
		}
		out, e := sim.ParseSchedule(s, nil)
		if e != nil || !slices.Equal(in, out) {
			t.Fatalf("base=%d err=%v roundtrip mismatch", base, e)
		}
	}
}
func TestAdv_5_EveryByteCorruptionCanonical(t *testing.T) {
	start(t)
	s := "faults 1\nat=-8ns kind=abc target=17 arg1=-9 arg2=0\n"
	names := []sim.KindName{{Kind: 42, Name: "abc"}}
	for i := range s {
		for b := 0; b < 256; b++ {
			v := []byte(s)
			v[i] = byte(b)
			out, e := sim.ParseSchedule(string(v), names)
			if e != nil {
				if out != nil {
					t.Fatalf("partial result at byte %d", i)
				}
				continue
			}
			back, e := sim.FormatSchedule(out, names)
			if e != nil || back != string(v) {
				t.Fatalf("accepted noncanonical byte %d=%d: %q", i, b, v)
			}
		}
	}
}
func TestAdv_5_UnboundedScheduleClaim(t *testing.T) {
	start(t)
	in := make([]sim.Fault, sim.MaxScheduleLen+1)
	s, e := sim.FormatSchedule(in, nil)
	if e != nil {
		t.Fatalf("claim says every schedule; legal []Fault length=%d rejected: %v", len(in), e)
	}
	out, e := sim.ParseSchedule(s, nil)
	if e != nil || !reflect.DeepEqual(in, out) {
		t.Fatalf("roundtrip err=%v", e)
	}
}
func scratch(t *testing.T) string {
	t.Helper()
	d, e := os.MkdirTemp(".", "scratch-")
	if e != nil {
		t.Fatal(e)
	}
	p, _ := filepath.Abs(d)
	t.Cleanup(func() { os.RemoveAll(p) })
	return p
}
func fakeRun(t *testing.T, output string, exit int) ([]byte, []byte, error) {
	t.Helper()
	d := scratch(t)
	shim := filepath.Join(d, "go-output")
	body := fmt.Sprintf("#!/bin/sh\ncat <<'OUTPUT'\n%s\nOUTPUT\nexit %d\n", output, exit)
	if e := os.WriteFile(shim, []byte(body), 0700); e != nil {
		t.Fatal(e)
	}
	report := filepath.Join(d, "report.tsv")
	b, e := command(t, root(), []string{"FUZZ_GO=" + shim, "FUZZ_REPORT=" + report, "FUZZ_SECONDS=1", "FUZZ_RATE_FLOOR=100"}, "bash", "scripts/fuzz.sh")
	table, _ := os.ReadFile(report)
	return b, table, e
}
func TestAdv_6_RealTSVColumns(t *testing.T) {
	start(t)
	b, table, e := fakeRun(t, "fuzz: elapsed: 4s, execs: 1000 (250/sec)", 0)
	if e != nil {
		t.Fatalf("%v %s", e, b)
	}
	rows := strings.Split(strings.TrimSpace(string(table)), "\n")
	if len(rows) < 2 {
		t.Fatal("no targets reported")
	}
	for i, row := range rows {
		if n := len(strings.Split(row, "\t")); n != 5 {
			t.Fatalf("row %d has %d TSV columns; want 5: %q", i, n, row)
		}
	}
}
func TestAdv_6_GateSeedSweepCounts(t *testing.T) {
	start(t)
	b, table, e := fakeRun(t, "fuzz: elapsed: 2s, execs: 2 (1/sec)", 0)
	if e != nil {
		t.Fatalf("%v %s", e, b)
	}
	// Execute the actual gate's count assignment, with only the report path supplied.
	src, e := os.ReadFile(filepath.Join(root(), "scripts/gate.sh"))
	if e != nil {
		t.Fatal(e)
	}
	var assignment string
	for _, line := range strings.Split(string(src), "\n") {
		if strings.Contains(line, "fuzz_sweeps=$(tail") {
			assignment = line
		}
	}
	if assignment == "" {
		t.Fatal("gate count assignment absent")
	}
	d := scratch(t)
	report := filepath.Join(d, "rates.tsv")
	os.WriteFile(report, table, 0600)
	out, e := command(t, root(), []string{"fuzz_report=" + report}, "bash", "-c", assignment+"\nprintf '%s' \"$fuzz_sweeps\"")
	if e != nil {
		t.Fatalf("%v %s", e, out)
	}
	// The inventory has 11 simulator-dependent targets and one plain target.
	if strings.TrimSpace(string(out)) != "11" {
		t.Fatalf("11 sim targets at 1 exec/s: gate records %q seed sweeps; output:\n%s", out, b)
	}
}
func TestAdv_6_UnreadableAfterProgress(t *testing.T) {
	start(t)
	b, _, e := fakeRun(t, "fuzz: elapsed: 1s, execs: 300 (300/sec)\nfuzz: elapsed: corrupt, execs: unavailable", 0)
	if e == nil {
		t.Fatalf("unreadable final summary accepted using stale progress:\n%s", b)
	}
}
func TestAdv_6_FailedTargetWithGoodRate(t *testing.T) {
	start(t)
	b, _, e := fakeRun(t, "fuzz: elapsed: 1s, execs: 300 (300/sec)\nFAIL", 1)
	if e == nil {
		t.Fatalf("target failure swallowed: %s", b)
	}
}
func TestAdv_6_NoProgressFailsClosed(t *testing.T) {
	start(t)
	b, table, e := fakeRun(t, "fuzz: elapsed: 0s, gathering baseline coverage: 0/900 completed", 0)
	if e == nil || !bytes.Contains(table, []byte("unreadable")) {
		t.Fatalf("no-rate accepted: %v\n%s\n%s", e, b, table)
	}
}
func source(t *testing.T, path string) string {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(root(), path))
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func require(t *testing.T, s string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(s, p) {
			t.Fatalf("audited fixed seed expression missing: %q", p)
		}
	}
}
func TestAdv_7_WorldAndToySeedDataflow(t *testing.T) {
	start(t)
	require(t, source(t, "internal/sim/world/property_test.go"), "r := runSchedule(1, sched, \"\")")
	require(t, source(t, "internal/sim/simtoy/scenario_test.go"), "Config{Seed: 1, Options: BaseOptions(), Faults: sim.ScheduleFromBytes(data, shrinkSpace())")
}
func TestAdv_7_WireAndFakeSeedDataflow(t *testing.T) {
	start(t)
	require(t, source(t, "internal/pgwire/frame_fuzz_test.go"), "testClientConfig(t, ClientTLSMode(mode%3), 1)", "testBackendConfig(BackendTLSMode(mode%3), 1)")
	require(t, source(t, "internal/pgwire/client_script_test.go"), "rand.NewPCG(seed, 99)")
	require(t, source(t, "internal/pgwire/backend_test.go"), "rand.NewPCG(seed, 5)")
	require(t, source(t, "internal/sim/fakepg/helpers_test.go"), "sim.Config{Seed: 1, Scenario: t.Name()}")
}
func TestAdv_7_InventoryAndHiddenHashImports(t *testing.T) {
	start(t)
	count := 0
	for _, dir := range []string{"internal/sim", "internal/sim/world", "internal/sim/simtoy", "internal/sim/fakepg", "internal/pgwire"} {
		deps := must(t, "go", "list", "-deps", "-test", "./"+dir)
		if !bytes.Contains(deps, []byte("github.com/nickstrad/mini-gateway/internal/sim")) {
			t.Fatalf("%s classification lost", dir)
		}
		entries, e := os.ReadDir(filepath.Join(root(), dir))
		if e != nil {
			t.Fatal(e)
		}
		for _, ent := range entries {
			if !strings.HasSuffix(ent.Name(), "_test.go") {
				continue
			}
			s := source(t, dir+"/"+ent.Name())
			n := strings.Count(s, "func Fuzz")
			if n == 0 {
				continue
			}
			count += n
			// Hashes elsewhere in this file belong to golden trace assertions.
			// Inspect fuzz entrypoints themselves; helpers are checked by the two dataflow attacks.
			tree, err := parser.ParseFile(token.NewFileSet(), ent.Name(), s, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, decl := range tree.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || !strings.HasPrefix(fn.Name.Name, "Fuzz") {
					continue
				}
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					id, ok := sel.X.(*ast.Ident)
					if ok && (id.Name == "sha256" || id.Name == "fnv" || id.Name == "crc32" || id.Name == "md5") {
						t.Errorf("hash call in fuzz entrypoint %s/%s", dir, fn.Name.Name)
					}
					return true
				})
			}
		}
	}
	if count != 11 {
		t.Fatalf("unreviewed fuzz inventory: got %d want 11", count)
	}
}
func TestAdv_2_LongSeedCampaign(t *testing.T) {
	if os.Getenv("ADV_LONG") != "1" {
		t.Skip("ADV_LONG=1")
	}
	start(t)
	r := rand.New(rand.NewSource(seed))
	for j := 0; j < 20000; j++ {
		in := []sim.Fault{{At: sim.Time(r.Intn(50)), Arg1: int64(r.Intn(50)), Arg2: int64(r.Intn(50))}, {At: sim.Time(r.Intn(50)), Arg1: int64(r.Intn(50)), Arg2: int64(r.Intn(50))}}
		residue := (int64(in[0].At) + in[0].Arg1*in[1].Arg2) % 13
		p := func(s []sim.Fault) bool {
			if len(s) != 2 {
				return false
			}
			return (int64(s[0].At)+s[0].Arg1*s[1].Arg2)%13 == residue
		}
		checkMinimal(t, in, p, floor)
	}
}
