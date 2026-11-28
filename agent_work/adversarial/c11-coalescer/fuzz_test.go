package adv_test

import (
	"context"
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A compiler overlay adds a bridge to the unexported decoder; no product file is
// edited. All test vectors below are independent of the builder's corpus.
func bridge(t *testing.T, body string) {
	t.Helper()
	t.Logf("seed=%d", seed)
	root, e := filepath.Abs("../../..")
	if e != nil {
		t.Fatal(e)
	}
	dir := scratch(t)
	src := filepath.Join(dir, "bridge.txt")
	text := "package coalesce\nimport(\"testing\";\"bytes\")\nfunc TestExternalAdversary(t *testing.T){ _=bytes.Equal; " + body + "\n}\n"
	if e = os.WriteFile(src, []byte(text), 0600); e != nil {
		t.Fatal(e)
	}
	v := filepath.Join(root, "internal/coalesce/zz_external_adv_test.go")
	b, _ := json.Marshal(map[string]any{"Replace": map[string]string{v: src}})
	p := filepath.Join(dir, "overlay.json")
	if e = os.WriteFile(p, b, 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/local/go/bin/go", "test", "-overlay", p, "-count=1", "-run", "^TestExternalAdversary$", "./internal/coalesce")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOMAXPROCS=2")
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("decoder/oracle bridge failed: %v\n%s", e, out)
	}
}
func TestAdv_18_HostileDecoderBoundaries(t *testing.T) {
	bridge(t, `for _,n:=range []int{0,1,2,287,288,289,1048576}{for _,v:=range []byte{0,127,255}{b:=bytes.Repeat([]byte{v},n);ops:=decodeOps(b);want:=n/3;if want>96{want=96};if len(ops)!=want{t.Fatalf("n=%d byte=%d decoded=%d want=%d",n,v,len(ops),want)};if n>288{small:=decodeOps(b[:288]);for i:=range small{if small[i]!=ops[i]{t.Fatalf("suffix changed operation %d",i)}}}}}`)
}
func TestAdv_18_NoInputHashSeed(t *testing.T) {
	t.Logf("seed=%d", seed)
	files, e := filepath.Glob("../../../internal/coalesce/*.go")
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, p := range files {
		f, e := parser.ParseFile(token.NewFileSet(), p, nil, parser.ImportsOnly)
		if e != nil {
			t.Fatal(e)
		}
		for _, im := range f.Imports {
			s, _ := strconv.Unquote(im.Path.Value)
			if strings.Contains(s, "/sim") || strings.HasPrefix(s, "hash") || strings.HasPrefix(s, "crypto/") {
				t.Fatalf("core fuzz path gained seed/hash dependency %s in %s", s, p)
			}
		}
		if filepath.Base(p) == "property_test.go" {
			b, _ := os.ReadFile(p)
			found = strings.Contains(string(b), "ops := decodeOps(b)")
		}
	}
	if !found {
		t.Fatal("fuzz target no longer directly decodes input; seed audit needs review")
	}
}
func TestAdv_18_LeaverPlantCaughtByTargetOracle(t *testing.T) {
	bridge(t, `b:=[]byte{0,2,0,0,2,0,0,2,0,7,1,0};clean:=smallConfig(ModeAuto,JoinRelaxed);if _,e:=runOps(clean,decodeOps(b));e!=nil{t.Fatalf("clean oracle rejected witness: %v",e)};clean.Plant=PlantLeaverGetsResult;if _,e:=runOps(clean,decodeOps(b));e==nil{t.Fatal("target oracle failed to detect planted delivery to cancelled follower")}`)
}
