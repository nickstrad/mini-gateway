package adv_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/config"
	"github.com/nickstrad/mini-gateway/internal/obs/csvflush"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/test/e2e"
)

func spec() e2e.Spec {
	s := e2e.Spec{Name: "adv-c06-interleaved", Tenants: 3, Conns: 2, QueueCap: 17, Duration: 12 * time.Second}
	for c := 0; c < 9; c++ {
		s.Clients = append(s.Clients, e2e.Script{Tenant: c % 3, Start: time.Duration(c%4) * time.Millisecond, Steps: []e2e.Step{{SQL: e2e.Tag(c, "BEGIN")}, {SQL: e2e.Tag(c, fmt.Sprintf("SELECT %d", 901+c)), Delay: time.Duration(c%3) * time.Millisecond}, {SQL: e2e.Tag(c, "ROLLBACK")}, {SQL: e2e.Tag(c, "SELECT 987")}}})
	}
	return s
}
func simrun(t *testing.T, s e2e.Spec, seed int64, dir string) *e2e.SimRun {
	t.Helper()
	t.Logf("seed=%d", seed)
	r, err := e2e.RunSim(s, seed, e2e.SimOptions{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if r.Engine.Completed != int64(len(s.Clients)*4) {
		t.Fatalf("only %d completed", r.Engine.Completed)
	}
	return r
}
func TestAdv_11_RoundTripWithQueuedTransactions(t *testing.T) {
	r := simrun(t, spec(), seed, t.TempDir())
	if err := csvflush.RoundTrip(r.Flush.RunDir, r.Final); err != nil {
		t.Fatal(err)
	}
}
func TestAdv_11_ByteIdenticalIndependentFlushes(t *testing.T) {
	a := simrun(t, spec(), seed, t.TempDir())
	b := simrun(t, spec(), seed, t.TempDir())
	files, err := os.ReadDir(a.Flush.RunDir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".csv") {
			continue
		}
		x, err := os.ReadFile(filepath.Join(a.Flush.RunDir, f.Name()))
		if err != nil {
			t.Fatal(err)
		}
		y, err := os.ReadFile(filepath.Join(b.Flush.RunDir, f.Name()))
		if err != nil {
			t.Fatal(err)
		}
		n++
		if !bytes.Equal(x, y) {
			t.Errorf("%s differs on replay", f.Name())
		}
	}
	if n < 3 {
		t.Fatalf("only %d CSV files", n)
	}
}
func TestAdv_11_ConservationFromReloadedFiles(t *testing.T) {
	r := simrun(t, spec(), seed, t.TempDir())
	disk, err := csvflush.Read(r.Flush.RunDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, seq := range disk.FlushSeqs(r.Final.Instance) {
		s := disk.Snapshot(r.Final.Instance, seq)
		vals := map[string]int64{}
		for _, p := range s.Points {
			if p.Component == "engine" {
				vals[p.Metric] += p.Value
			}
		}
		if vals["requests_received"] != vals["requests_accepted"]+vals["requests_rejected"] {
			t.Fatalf("seq=%d received conservation: %v", seq, vals)
		}
		if vals["requests_accepted"] != vals["requests_completed"]+vals["requests_failed"]+vals["requests_shed"]+vals["requests_queued"]+vals["requests_in_flight"] {
			t.Fatalf("seq=%d accepted conservation violated", seq)
		}
	}
}
func TestAdv_12_ConcurrentSchedulesReplay(t *testing.T) {
	for _, n := range []int64{0, -1, 1 << 62, seed} {
		a := simrun(t, spec(), n, "")
		b := simrun(t, spec(), n, "")
		if a.World.S.Digests() != b.World.S.Digests() || !reflect.DeepEqual(a.Engine, b.Engine) {
			t.Fatalf("seed=%d digest/snapshot differs", n)
		}
	}
}
func TestAdv_12_FragmentedReplay(t *testing.T) {
	s := spec()
	s.Fragment = true
	a := simrun(t, s, seed, "")
	b := simrun(t, s, seed, "")
	if a.World.S.Digests() != b.World.S.Digests() {
		t.Fatal("fragmented trace diverged")
	}
}
func TestAdv_12_FreshProcessReplay(t *testing.T) {
	if os.Getenv("ADV_REPLAY_CHILD") == "1" {
		r := simrun(t, spec(), seed, "")
		fmt.Println("ADV-DIGEST", r.World.S.Digests())
		return
	}
	var first string
	for i := 0; i < 3; i++ {
		c := exec.Command(os.Args[0], "-test.run=^TestAdv_12_FreshProcessReplay$", "-test.v")
		c.Env = append(os.Environ(), "ADV_REPLAY_CHILD=1")
		b, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("child: %v %s", err, b)
		}
		var digest string
		for _, l := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(l, "ADV-DIGEST") {
				digest = l
			}
		}
		if digest == "" {
			t.Fatal("no digest")
		}
		if i > 0 && digest != first {
			t.Fatalf("different processes: %s versus %s", digest, first)
		}
		first = digest
	}
}

func validConfig(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile("../../../internal/config/testdata/example.json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err = json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	m["client_tls"] = map[string]any{"mode": "off"}
	return m
}
func invalid(t *testing.T, edit func(map[string]any), key string) {
	t.Helper()
	t.Logf("seed=%d", seed)
	m := validConfig(t)
	b, _ := json.Marshal(m)
	if _, err := config.Parse(b); err != nil {
		t.Fatalf("baseline invalid: %v", err)
	}
	edit(m)
	b, _ = json.Marshal(m)
	_, err := config.Parse(b)
	if err == nil {
		t.Fatalf("accepted invalid %s", key)
	}
	if !strings.Contains(err.Error(), key) {
		t.Fatalf("error omitted %s: %v", key, err)
	}
}
func TestAdv_13_NegativeFleetBudget(t *testing.T) {
	invalid(t, func(m map[string]any) { m["fleet"].(map[string]any)["backend_connections"] = -1 }, "fleet.backend_connections")
}
func TestAdv_13_ExplicitZeroCapacity(t *testing.T) {
	invalid(t, func(m map[string]any) { m["queue"].(map[string]any)["capacity"] = 0 }, "queue.capacity")
}
func TestAdv_13_NestedTimeoutEquality(t *testing.T) {
	invalid(t, func(m map[string]any) { m["timeouts"] = map[string]any{"reset": "5s", "queue_wait": "5s"} }, "timeouts.reset")
}

func TestAdv_14_AllByteValuesAndLengths(t *testing.T) {
	t.Logf("seed=%d", seed)
	for n := 0; n < 1024; n++ {
		b := bytes.Repeat([]byte{byte(n)}, n)
		ops := e2e.DecodeOps(b)
		if len(ops) > 48 || len(ops) != min(n/4, 48) {
			t.Fatalf("n=%d ops=%d", n, len(ops))
		}
		for _, o := range ops {
			if o.Target < 0 || o.Target >= 3 || o.Kind < e2e.OpConnect || int(o.Kind-e2e.OpConnect) >= e2e.NumOps {
				t.Fatalf("n=%d bad op %+v", n, o)
			}
		}
	}
}
func TestAdv_14_OneByteCannotReseedWorld(t *testing.T) {
	rnd := rand.New(rand.NewSource(seed))
	t.Logf("seed=%d", seed)
	b := make([]byte, 192)
	rnd.Read(b)
	orig := e2e.DecodeOps(b)
	for i := range b {
		c := bytes.Clone(b)
		c[i] ^= 0xff
		other := e2e.DecodeOps(c)
		for j := range orig {
			if j != i/4 && orig[j] != other[j] {
				t.Fatalf("byte %d changed op %d: hidden global dependency", i, j)
			}
		}
	}
	if e2e.OpSeed != 1 {
		t.Fatalf("world seed changed: %d", e2e.OpSeed)
	}
}
func TestAdv_14_SeededPlantCampaign(t *testing.T) {
	t.Logf("seed=%d", seed)
	rnd := rand.New(rand.NewSource(seed))
	deadline := time.Now().Add(20 * time.Second)
	for i := 0; i < 2000 && time.Now().Before(deadline); i++ {
		b := make([]byte, 192)
		rnd.Read(b)
		ops := e2e.DecodeOps(b)
		bad := e2e.RunOps(ops, proxy.PlantQueuedVanishKeepsSlot)
		if bad.Failed() {
			good := e2e.RunOps(ops, 0)
			if good.Failed() {
				t.Fatalf("unplanted engine failed input=%x: %s", b, good.String())
			}
			t.Logf("plant caught at sample=%d input=%x", i, b)
			return
		}
	}
	t.Fatal("seeded campaign did not detect plant within 20 seconds")
}
func TestAdv_14_LongUnplantedCampaign(t *testing.T) {
	if os.Getenv("ADV_LONG") != "1" {
		t.Skip("ADV_LONG=1")
	}
	t.Logf("seed=%d", seed)
	rnd := rand.New(rand.NewSource(seed))
	for i := 0; i < 3000; i++ {
		b := make([]byte, 192)
		rnd.Read(b)
		r := e2e.RunOps(e2e.DecodeOps(b), 0)
		if r.Failed() {
			t.Fatalf("sample=%d input=%x: %s", i, b, r.String())
		}
	}
}
