package adv_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func probe(t *testing.T, name string) {
	t.Helper()
	t.Logf("seed=%d", seed)
	root, e := filepath.Abs("../../..")
	if e != nil {
		t.Fatal(e)
	}
	src := filepath.Join(root, "agent_work/adversarial/c07-pool/fuzz_probe.go.txt")
	overlay := map[string]any{"Replace": map[string]string{filepath.Join(root, "internal/pool/adv_probe_test.go"): src}}
	b, e := json.Marshal(overlay)
	if e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(scratch(t), "overlay.json")
	if e = os.WriteFile(file, b, 0644); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/local/go/bin/go", "test", "-overlay", file, "-count=1", "-run", "^"+name+"$", "./internal/pool")
	cmd.Dir = root
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("%s: %v\n%s", name, e, out)
	}
}
func TestAdv_15_DecoderAllBytesAndSizes(t *testing.T) { probe(t, "TestAdvProbeDecoder") }
func TestAdv_15_ConstantSeedReplay(t *testing.T)      { probe(t, "TestAdvProbeConstantSeed") }
func TestAdv_15_IndependentPlantWitness(t *testing.T) { probe(t, "TestAdvProbePlant") }
