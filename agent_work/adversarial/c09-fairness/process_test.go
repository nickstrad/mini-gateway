package adv_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestReplayWorker(t *testing.T) {
	d := os.Getenv("C09_REPLAY_OUTPUT")
	if d == "" {
		t.Skip("child only")
	}
	s := spec(3, 13*time.Millisecond, 14)
	r := run(t, s, seed, "")
	b, e := json.Marshal(struct {
		Clients any
		Engine  any
	}{r.Clients, r.Engine})
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(d, b, 0600); e != nil {
		t.Fatal(e)
	}
}
func TestAdv_14_FreshProcessesReplay(t *testing.T) {
	if os.Getenv("ADV_LONG") != "1" {
		t.Skip("ADV_LONG=1: cross-process replay campaign")
	}
	t.Logf("seed=%d", seed)
	d := scratch(t)
	var prior []byte
	for _, name := range []string{"one", "two", "three"} {
		p := filepath.Join(d, name)
		cmd := exec.Command(os.Args[0], "-test.run=^TestReplayWorker$", "-test.count=1")
		cmd.Env = append(os.Environ(), "C09_REPLAY_OUTPUT="+p)
		out, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("child %s: %v %s", name, e, out)
		}
		b, e := os.ReadFile(p)
		if e != nil {
			t.Fatal(e)
		}
		if prior != nil && !bytes.Equal(prior, b) {
			t.Fatalf("fresh process %s changed replies or engine snapshot", name)
		}
		prior = b
	}
}
