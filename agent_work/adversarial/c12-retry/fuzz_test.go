package adv_test

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/retry"
	"github.com/nickstrad/mini-gateway/internal/work"
)

func body(t *testing.T, b []byte, name string) string {
	t.Helper()
	f, e := parser.ParseFile(token.NewFileSet(), "source.go", b, 0)
	must(t, e)
	for _, d := range f.Decls {
		if f, ok := d.(*ast.FuncDecl); ok && f.Name.Name == name {
			var out bytes.Buffer
			must(t, format.Node(&out, token.NewFileSet(), f))
			return out.String()
		}
	}
	t.Fatalf("missing %s", name)
	return ""
}
func TestAdv_15_DecoderHostilePrefixesAndHugeSuffix(t *testing.T) {
	begin(t)
	local, e := os.ReadFile("decoder_snapshot_test.go")
	must(t, e)
	if body(t, local, "decodeOps") != body(t, source(t, "internal/retry/retry_test.go"), "decodeOps") {
		t.Fatal("decoder implementation changed: refresh extraction before interpreting this attack")
	}
	rng := rand.New(rand.NewSource(seed))
	prefix := make([]byte, 768)
	rng.Read(prefix)
	want := decodeOps(prefix)
	for _, size := range []int{0, 1, 2, 3, 767, 768, 769, 1 << 20} {
		b := make([]byte, size)
		copy(b, prefix)
		got := decodeOps(b)
		if len(got) != min(size/3, 256) {
			t.Fatalf("size=%d decoded=%d", size, len(got))
		}
		if size >= 768 && !reflect.DeepEqual(got, want) {
			t.Fatalf("suffix changed decoded prefix, size=%d", size)
		}
	}
	for first := 0; first < 256; first++ {
		for last := 0; last < 256; last++ {
			o := decodeOps([]byte{byte(first), 255, byte(last)})[0]
			if o.dt < 0 || o.dt > 255000000 || o.ttl < 0 || o.ttl > 127500000 || o.f.Attempt < 0 || o.f.Attempt > 1 {
				t.Fatalf("bytes=%d,255,%d yielded %+v", first, last, o)
			}
		}
	}
}
func TestAdv_15_FuzzInputCannotChooseAHashedSeed(t *testing.T) {
	begin(t)
	b := source(t, "internal/retry/retry_test.go")
	ref := source(t, "internal/retry/ref_test.go")
	for _, x := range []string{body(t, b, "FuzzRetryOps"), body(t, b, "decodeOps"), body(t, ref, "runOps")} {
		for _, bad := range []string{"rand.", "hash.", "sha256.", "fnv.", "NewSource(", "NewPCG("} {
			if strings.Contains(x, bad) {
				t.Fatalf("fuzz execution derives randomness: %s", bad)
			}
		}
	}
	if !strings.Contains(body(t, b, "FuzzRetryOps"), "decodeOps(b)") {
		t.Fatal("fuzz input no longer reaches checked decoder")
	}
}
func TestAdv_15_WithdrawalPlantCaughtOnVetoSequences(t *testing.T) {
	begin(t)
	for _, f := range []retry.Failure{{Reached: work.Unknown, Deadline: 999}, {Reached: work.No, InTx: true, Deadline: 999}, {Reached: work.No, BreakerOpen: true, Deadline: 999}, {Reached: work.No, Deadline: 0}} {
		p, e := retry.New(retry.Config{Kind: retry.KindPreForwardOnce, CapTokens: 13, FloorTokens: 7, Plant: retry.PlantWithdrawFirst})
		must(t, e)
		before := p.Snapshot()
		d := p.Decide(0, f)
		s := p.Snapshot()
		if d.Retry || s.Withdrawals == before.Withdrawals || s.Check() == nil {
			t.Fatalf("plant escaped independent veto/ledger oracle: decision=%+v snapshot=%+v", d, s)
		}
	}
}
func TestAdv_15_LongActualFuzzTargetFindsPlant(t *testing.T) {
	if os.Getenv("ADV_LONG") != "1" {
		t.Skip("ADV_LONG=1")
	}
	begin(t)
	dir, e := os.MkdirTemp(".", "fuzz-run-")
	must(t, e)
	defer os.RemoveAll(dir)
	abs, e := filepath.Abs(dir)
	must(t, e)
	build := exec.Command("/usr/local/go/bin/go", "test", "-c", "-o", filepath.Join(abs, "retry.test"), "./internal/retry")
	build.Dir = "../../.."
	out, e := build.CombinedOutput()
	if e != nil {
		t.Fatalf("build target: %v\n%s", e, out)
	}
	c := exec.Command(filepath.Join(abs, "retry.test"), "-test.run=^$", "-test.fuzz=FuzzRetryOps", "-test.fuzztime=2s", "-test.parallel=1", "-test.fuzzcachedir="+filepath.Join(abs, "cache"))
	c.Dir = abs
	c.Env = append(os.Environ(), "MG_FUZZ_PLANT=retry-withdraw-first")
	out, e = c.CombinedOutput()
	if e == nil || !bytes.Contains(out, []byte("withdrawn")) {
		t.Fatalf("actual fuzz target did not identify withdrawal plant: %v\n%s", e, out)
	}
	t.Logf("actual fuzz target detected plant: %s", out)
}
