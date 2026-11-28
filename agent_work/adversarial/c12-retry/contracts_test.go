package adv_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/config"
	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/csvflush"
	"github.com/nickstrad/mini-gateway/internal/obs/retrysrc"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/retry"
	"github.com/nickstrad/mini-gateway/internal/work"
)

func source(t *testing.T, path string) []byte {
	t.Helper()
	b, e := os.ReadFile(filepath.Join("../../..", path))
	must(t, e)
	return b
}
func TestAdv_10_ErrorRowsRejectUnsafeCombinations(t *testing.T) {
	begin(t)
	allowed := 0
	for id := pgwire.ErrRowID(0); id < pgwire.ErrRowCount; id++ {
		for reached := work.No; reached <= work.Unknown; reached++ {
			for _, tx := range []bool{false, true} {
				m := proxy.MatrixRow{Row: id, Reached: reached, InTx: tx}
				if m.Retried() {
					allowed++
					if id != pgwire.ErrRowLostAfterRead || reached != work.No || tx {
						t.Fatalf("unsafe row=%d reached=%d tx=%v allowed", id, reached, tx)
					}
				}
			}
		}
	}
	if allowed != 1 {
		t.Fatalf("retryable combinations=%d", allowed)
	}
	if !bytes.Equal(source(t, "docs/error-contract.md"), []byte(pgwire.ErrorRowsMarkdown())) {
		t.Fatal("error contract differs from exported rows")
	}
}
func TestAdv_10_MatrixExplicitNeverPaths(t *testing.T) {
	begin(t)
	n := 0
	for _, m := range proxy.FaultMatrixRows() {
		if strings.Contains(m.Client, "no retry") {
			n++
			if m.Retried() {
				t.Errorf("fault %q says client=%q but Retried()=true", m.Fault, m.Client)
			}
		}
	}
	if n < 2 {
		t.Fatalf("missing explicit deadline/breaker prohibitions: %d", n)
	}
}
func TestAdv_10_ActualRetryRowsMatchContract(t *testing.T) {
	r := newRig(t, nil)
	r.query(1001, "UPDATE marks SET n=n+1")
	r.lose(r.take(), 0, true)
	r.connect()
	r.success(r.take(), 'I')
	s := r.e.Snapshot()
	seen := int64(0)
	for id, n := range s.RetryRows {
		seen += n
		if n > 0 && pgwire.ErrRowID(id).ProxyRetry() != pgwire.ProxyRetryWhenNotReached {
			t.Fatalf("retry on forbidden row=%d count=%d", id, n)
		}
	}
	if seen != 1 {
		t.Fatalf("retry rows count=%d", seen)
	}
	r.end()
	if !bytes.Equal(source(t, "docs/fault-matrix.md"), proxy.FaultMatrixMarkdown()) {
		t.Fatal("fault matrix stale")
	}
}

func TestAdv_11_BinaryRejectsPolicyAliases(t *testing.T) {
	begin(t)
	for _, name := range []string{"everything", "retry-everything", "Everything", "pre-forward-once\x00", "2", "PreForwardOnce", "pre-forward-once "} {
		b, _ := json.Marshal(map[string]any{"retry": map[string]any{"policy": name}})
		_, e := config.Parse([]byte(configJSON(string(b))))
		if e == nil || !strings.Contains(e.Error(), "retry.policy") {
			t.Fatalf("policy %q error=%v", name, e)
		}
	}
}
func TestAdv_11_BinaryRejectsPlantInjection(t *testing.T) {
	begin(t)
	for _, key := range []string{"plant", "Plant", "ignore_reached", "everything"} {
		b, _ := json.Marshal(map[string]any{"retry": map[string]any{"policy": "pre-forward-once", key: 1}})
		_, e := config.Parse([]byte(configJSON(string(b))))
		if e == nil || !strings.Contains(e.Error(), "retry."+key) {
			t.Fatalf("plant key %q error=%v", key, e)
		}
	}
}

func capture(t *testing.T, dir string) ([]byte, []obs.InstanceSnapshot) {
	t.Helper()
	p := policy(t, 3, 1)
	c := obs.NewCollector(obs.Config{Instance: "adv", Build: "adversarial", ConfigFingerprint: "fixed"})
	must(t, c.Register(retrysrc.Source(p.Snapshot)))
	clock := int64(0)
	w, e := csvflush.New(csvflush.Config{Dir: dir, Run: csvflush.RunInfo{RunID: "retry", Scenario: "adversarial", Tier: "S", Seed: seed, Instances: 1}, Strict: true, Clock: func() int64 { return clock }})
	must(t, e)
	var snaps []obs.InstanceSnapshot
	for i := 0; i < 31; i++ {
		p.Attempt(retry.Time(i))
		f := eligible()
		f.BreakerOpen = i%7 == 0
		p.Decide(retry.Time(i), f)
		clock = int64(i)
		s, e := c.Publish()
		must(t, e)
		snaps = append(snaps, s)
		must(t, w.Flush(s))
	}
	must(t, w.Close())
	// RoundTrip accepts only the final snapshot per instance.
	must(t, csvflush.RoundTrip(w.RunDir(), snaps[len(snaps)-1]))
	run, err := csvflush.Read(w.RunDir())
	must(t, err)
	for i, snapshot := range snaps {
		var points []obs.Point
		for _, row := range run.Metrics {
			if row.FlushSeq == int64(i) {
				points = append(points, row.Point)
			}
		}
		if !reflect.DeepEqual(points, snapshot.Points) {
			t.Fatalf("flush %d points differ from snapshot: got=%v want=%v", i, points, snapshot.Points)
		}
	}
	var b []byte
	for _, name := range []string{"metrics.csv", "histograms.csv", "run.csv"} {
		x, e := os.ReadFile(filepath.Join(w.RunDir(), name))
		must(t, e)
		b = append(b, x...)
	}
	return b, snaps
}
func TestAdv_14_FlushExhaustionAndFractionalLevels(t *testing.T) {
	begin(t)
	_, ss := capture(t, t.TempDir())
	s := ss[len(ss)-1]
	found := false
	for _, p := range s.Points {
		if p.Metric == "exhaustions" {
			found = true
			if p.Value == 0 {
				t.Fatal("exhaustions not exported")
			}
		}
	}
	if !found {
		t.Fatal("missing exhaustion point")
	}
}
func TestAdv_14_ReplayActionsAndCSVBytes(t *testing.T) {
	run := func() []string {
		r := newRig(t, nil)
		r.query(1001, "UPDATE marks SET n=n+1")
		r.lose(r.take(), 0, true)
		r.connect()
		r.success(r.take(), 'I')
		r.end()
		return r.transcript
	}
	if !reflect.DeepEqual(run(), run()) {
		t.Fatal("same seed produces different driver actions")
	}
	a, _ := capture(t, t.TempDir())
	b, _ := capture(t, t.TempDir())
	if !bytes.Equal(a, b) {
		t.Fatalf("same seed CSV mismatch %x != %x", sha256.Sum256(a), sha256.Sum256(b))
	}
}
func TestAdv_14_CrossProcessReplay(t *testing.T) {
	begin(t)
	if os.Getenv("ADV_REPLAY_CHILD") == "1" {
		b, _ := capture(t, t.TempDir())
		fmt.Printf("REPLAY %x\n", sha256.Sum256(b))
		return
	}
	var hashes []string
	for i := 0; i < 2; i++ {
		c := exec.Command(os.Args[0], "-test.run=^TestAdv_14_CrossProcessReplay$")
		c.Env = append(os.Environ(), "ADV_REPLAY_CHILD=1")
		b, e := c.CombinedOutput()
		must(t, e)
		for _, s := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(s, "REPLAY ") {
				hashes = append(hashes, s)
			}
		}
	}
	if len(hashes) != 2 || hashes[0] != hashes[1] {
		t.Fatalf("cross-process hashes=%v", hashes)
	}
}
