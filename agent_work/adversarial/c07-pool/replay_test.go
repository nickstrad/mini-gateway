package adv_test

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"testing"
)

func TestAdv_14_CrossProcessReplay(t *testing.T) {
	if os.Getenv("ADV_REPLAY_CHILD") == "1" {
		fmt.Printf("DIGEST:%x\n", sha256.Sum256([]byte(coreReplay(t))))
		return
	}
	t.Logf("seed=%d", seed)
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	var want []byte
	for i := 0; i < 3; i++ {
		cmd := exec.Command(exe, "-test.run=^TestAdv_14_CrossProcessReplay$")
		cmd.Env = append(os.Environ(), "ADV_REPLAY_CHILD=1")
		b, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("child: %v %s", e, b)
		}
		var got []byte
		for _, line := range bytes.Split(b, []byte("\n")) {
			if bytes.HasPrefix(line, []byte("DIGEST:")) {
				got = line
			}
		}
		if got == nil {
			t.Fatal("child missing digest")
		}
		if i == 0 {
			want = got
		} else if !bytes.Equal(want, got) {
			t.Fatalf("process-dependent replay: %s != %s", want, got)
		}
	}
}
