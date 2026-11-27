package adv_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Overlay adds independent probes beside the private decoder without changing
// product files, importing nonstandard dependencies, or copying its test cases.
func overlayProbe(t *testing.T, body string) {
	t.Helper()
	t.Logf("seed=%d", seed)
	root, e := filepath.Abs("../../..")
	if e != nil {
		t.Fatal(e)
	}
	d := scratch(t)
	src := filepath.Join(d, "probe_test.go")
	code := "package admission\nimport (\"testing\";\"math/rand\";\"reflect\")\nvar _ = rand.New\nvar _ = reflect.DeepEqual\nfunc TestIndependentFairProbe(t *testing.T){\n" + body + "\n}\n"
	if e = os.WriteFile(src, []byte(code), 0600); e != nil {
		t.Fatal(e)
	}
	m := map[string]any{"Replace": map[string]string{filepath.Join(root, "internal/admission/independent_probe_test.go"): src}}
	b, _ := json.Marshal(m)
	ov := filepath.Join(d, "overlay.json")
	if e = os.WriteFile(ov, b, 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-overlay", ov, "-count=1", "-run", "^TestIndependentFairProbe$", "./internal/admission")
	cmd.Dir = root
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("independent decoder probe: %v\n%s", e, out)
	}
}
func TestAdv_15_EveryByteAndTailLength(t *testing.T) {
	overlayProbe(t, `
for x:=0;x<256;x++ { for n:=0;n<9;n++ { b:=make([]byte,n);for i:=range b{b[i]=byte(x)}; ops:=decodeFairOps(b);if len(ops)!=n/3{t.Fatalf("byte=%d length=%d decoded=%d",x,n,len(ops))};for _,o:=range ops{if o.dt<0||o.dt>2400*Millisecond||o.arg<0||o.arg>255{t.Fatalf("invalid decode %+v",o)}} } }
`)
}
func TestAdv_15_HugeSuffixCannotChangePrefix(t *testing.T) {
	overlayProbe(t, `
r:=rand.New(rand.NewSource(909271));b:=make([]byte,1<<20);r.Read(b);x:=decodeFairOps(b);y:=decodeFairOps(b[:288]);if len(x)!=96||!reflect.DeepEqual(x,y){t.Fatal("suffix beyond 96 operations changes decoding")};for i:=288;i<len(b);i++{b[i]^=255};if !reflect.DeepEqual(x,decodeFairOps(b)){t.Fatal("suffix hashes into decoded operations")}
`)
}
func TestAdv_15_IndependentSeedsFindTurnPlant(t *testing.T) {
	overlayProbe(t, `
r:=rand.New(rand.NewSource(909271));found:=false
for attempt:=0;attempt<3000;attempt++ { b:=make([]byte,96*3);for i:=range b{b[i]=byte(r.Intn(192))};ops:=decodeFairOps(b);if _,e:=runFairOps(RuleFIFO,0,0,ops);e!=nil{t.Fatalf("unplanted seed=909271 attempt=%d: %v",attempt,e)};if _,e:=runFairOps(RuleFIFO,0,FairPlantUnpopKeepsTurn,ops);e!=nil{t.Logf("seed=909271 attempt=%d detected: %v",attempt,e);found=true;break}}
if !found{t.Fatal("turn plant survived 3000 independent schedules, seed=909271")}
`)
}

func TestAdv_13_RemovingInvariantMakesProbeFail(t *testing.T) {
	if os.Getenv("ADV_LONG") != "1" {
		t.Skip("ADV_LONG=1: compile mutation overlay")
	}
	root, _ := filepath.Abs("../../..")
	p := filepath.Join(root, "internal/proxy/engine.go")
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	old := "\tif err := e.fairnessCheck(); err != nil {\n\t\treturn err\n\t}\n"
	if strings.Count(string(b), old) != 1 {
		t.Fatal("cannot identify fairness block")
	}
	d := scratch(t)
	replacement := filepath.Join(d, "engine.go")
	os.WriteFile(replacement, []byte(strings.Replace(string(b), old, "", 1)), 0600)
	ov := filepath.Join(d, "overlay.json")
	j, _ := json.Marshal(map[string]any{"Replace": map[string]string{p: replacement}})
	os.WriteFile(ov, j, 0600)
	cmd := exec.Command("go", "test", "-overlay", ov, "-count=1", "-run", "^TestAdv_13_ExtraFairLeaseDetected$", "./agent_work/adversarial/c09-fairness")
	cmd.Dir = root
	out, e := cmd.CombinedOutput()
	if e == nil || !strings.Contains(string(out), "engine accepted extra fair leases") {
		t.Fatalf("mutation did not produce intended failure: %v\n%s", e, out)
	}
}
