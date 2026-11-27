package adv_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	a "github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/config"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/test/e2e"
)

func root(t *testing.T) string {
	t.Helper()
	p, e := filepath.Abs("../../..")
	if e != nil {
		t.Fatal(e)
	}
	return p
}

// All generated fixtures stay in this attack directory, and are removed by Cleanup.
func scratch(t *testing.T) string {
	t.Helper()
	p, e := os.MkdirTemp(".", ".scratch-")
	if e != nil {
		t.Fatal(e)
	}
	p, e = filepath.Abs(p)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(p) })
	return p
}
func read(t *testing.T, p string) []byte {
	t.Helper()
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func write(t *testing.T, p string, b []byte) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, b, 0644); e != nil {
		t.Fatal(e)
	}
}
func command(t *testing.T, dir string, env []string, name string, args ...string) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, name, args...)
	c.Dir = dir
	c.Env = append(os.Environ(), env...)
	return c.CombinedOutput()
}
func reject(t *testing.T, raw string) {
	t.Helper()
	v, e := pgwire.NewVerifier("test", []byte("abcdefgh12345678"), 4096)
	if e != nil {
		t.Fatal(e)
	}
	base := fmt.Sprintf(`{"database":{"address":"127.0.0.1:5432","name":"test","user":"test","password":"test"},"tenants":[{"user":"test","verifier":%q}],"queue":%s}`, v.String(), raw)
	_, e = config.Parse([]byte(base))
	if e == nil || !strings.Contains(e.Error(), "queue.rule") {
		t.Fatalf("for %s expected queue.rule rejection, got %v", raw, e)
	}
}
func TestAdv_12_EncodedComparisonNames(t *testing.T) {
	start(t)
	for _, s := range []string{`{"rule":"\u0066ifo"}`, `{"rule":"foll\u0079"}`, `{"rule":"FIFO"}`, `{"rule":"folly\u0000"}`} {
		reject(t, s)
	}
}
func TestAdv_12_DuplicateRuleLastWins(t *testing.T) {
	start(t)
	reject(t, `{"rule":"timeout","rule":"fifo"}`)
	reject(t, `{"rule":"control-law","rule":"folly"}`)
}
func TestAdv_12_ComparisonParserHostileCorpus(t *testing.T) {
	start(t)
	for _, s := range []string{"fifo", "folly", "3", "4", " timeout", "timeout\x00", "control-law\n", "TIMEOUT", strings.Repeat("fifo", 10000)} {
		if k, ok := a.ParseProductRule(s); ok {
			t.Fatalf("accepted hostile rule %q as %v", s, k)
		}
	}
	for _, s := range []string{"timeout", "control-law"} {
		if k, ok := a.ParseProductRule(s); !ok || k.ComparisonArm() {
			t.Fatalf("valid rule rejected %q", s)
		}
	}
}

func wireSpec(k a.RuleKind, fragment bool) e2e.Spec {
	s := e2e.Spec{Name: "adv-c08-wire", Tenants: 3, Conns: 1, QueueCap: 5, Duration: 3 * time.Second, Fragment: fragment,
		Admission: a.Config{Rule: k, TenantCapacity: 2, MaxTenants: 3, Detail: true},
		Rows:      []fakepg.Row{{Key: "adv", Value: "v"}}, Service: fakepg.Service{PerKind: []fakepg.KindTime{{Kind: fakepg.KindRead, Time: 190 * time.Millisecond}}}}
	for i := 0; i < 15; i++ {
		s.Clients = append(s.Clients, e2e.Script{Tenant: i % 3, Start: time.Duration(200+i*2) * time.Millisecond, Steps: []e2e.Step{{SQL: "SELECT v FROM kv WHERE k = 'adv'"}}})
	}
	return s
}
func wireRun(t *testing.T, k a.RuleKind, fragment bool, dir string) *e2e.SimRun {
	t.Helper()
	r, e := e2e.RunSim(wireSpec(k, fragment), seed, e2e.SimOptions{Dir: dir})
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func checkReplies(t *testing.T, r *e2e.SimRun) {
	t.Helper()
	var shed, success int64
	for i, c := range r.Clients {
		if len(c.Steps) != 1 {
			t.Fatalf("client %d missing reply: %v", i, c)
		}
		tokens := c.Steps[0]
		if e2e.HasToken(tokens, "E:ERROR:53M01") {
			shed++
			if !e2e.HasToken(tokens, "ZI") {
				t.Fatalf("shed missing idle end marker: %v", tokens)
			}
			if c.Elapsed[0] > 110*time.Millisecond {
				t.Fatalf("shed answered late: client=%d elapsed=%v tokens=%v", i, c.Elapsed[0], tokens)
			}
		} else if e2e.HasToken(tokens, "C:SELECT") {
			success++
		} else {
			t.Fatalf("unexpected reply: %v", tokens)
		}
	}
	a := r.World.Proxy.Engine.AdmissionSnapshot()
	if shed == 0 || success == 0 || shed != a.ShedTotal() {
		t.Fatalf("shed replies=%d successes=%d counted=%d", shed, success, a.ShedTotal())
	}
	if e := a.Check(); e != nil {
		t.Fatal(e)
	}
}
func TestAdv_8_WireTimeoutTenantCollision(t *testing.T) {
	start(t)
	checkReplies(t, wireRun(t, a.RuleTimeout, false, ""))
}
func TestAdv_8_WireControlLawTenantCollision(t *testing.T) {
	start(t)
	r := wireRun(t, a.RuleControlLaw, false, "") // Control law deliberately has no per-request interval bound.
	var sheds int64
	for _, c := range r.Clients {
		if e2e.HasToken(c.Last(), "E:ERROR:53M01") {
			sheds++
			if !e2e.HasToken(c.Last(), "ZI") {
				t.Fatalf("missing end marker %v", c)
			}
		}
	}
	s := r.World.Proxy.Engine.AdmissionSnapshot()
	if sheds == 0 || sheds != s.ShedTotal() || s.Served == 0 {
		t.Fatalf("wire shed=%d stats shed=%d served=%d", sheds, s.ShedTotal(), s.Served)
	}
}
func TestAdv_8_FragmentedShedReplies(t *testing.T) {
	start(t)
	checkReplies(t, wireRun(t, a.RuleTimeout, true, ""))
}

var fixtureOnce sync.Once
var fixtureFiles map[string][]byte

func tableFixture(t *testing.T) string {
	t.Helper()
	fixtureOnce.Do(func() {
		d := scratch(t)
		for _, arm := range e2e.AdmArms() {
			s := e2e.AdmSpec{Scenario: "admission-h2h-l370", Arm: arm, LoadPct: 370, Phases: 1, Clients: 13, TenantCap: 9, TotalCap: 13}
			r, e := e2e.RunAdmission(s, seed, d, "")
			if e != nil {
				t.Fatal(e)
			}
			if e = r.Flush.Check(); e != nil {
				t.Fatal(e)
			}
		}
		fixtureFiles = map[string][]byte{}
		e := filepath.WalkDir(d, func(p string, de os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if de.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(d, p)
			fixtureFiles[rel] = read(t, p)
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
	})
	if len(fixtureFiles) == 0 {
		t.Fatal("table fixture failed to initialize")
	}
	d := scratch(t)
	for name, b := range fixtureFiles {
		write(t, filepath.Join(d, name), b)
	}
	return d
}

func table(t *testing.T, d string) []byte {
	t.Helper()
	b, e := command(t, root(t), []string{"PATH=/usr/local/go/bin:/root/.local/bin:/usr/local/bin:/usr/bin:/bin"}, "bash", "scripts/admission-head-to-head.sh", d)
	if e != nil {
		t.Fatalf("table command failed: %v\n%s", e, b)
	}
	if !bytes.Contains(b, []byte("control-law")) || !bytes.Contains(b, []byte("folly")) {
		t.Fatalf("missing arms in table: %s", b)
	}
	return b
}
func TestAdv_13_SeparateSQLProcesses(t *testing.T) {
	start(t)
	d := tableFixture(t)
	x, y := table(t, d), table(t, d)
	if !bytes.Equal(x, y) {
		t.Fatalf("query changed across processes:\n%s\n%s", x, y)
	}
}
func copyTree(t *testing.T, from, to string, reverse bool) {
	t.Helper()
	e := filepath.WalkDir(from, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(from, p)
		b := read(t, p)
		if reverse && strings.HasSuffix(p, ".csv") {
			lines := bytes.Split(bytes.TrimSuffix(b, []byte("\n")), []byte("\n"))
			for i, j := 1, len(lines)-1; i < j; i, j = i+1, j-1 {
				lines[i], lines[j] = lines[j], lines[i]
			}
			b = append(bytes.Join(lines, []byte("\n")), '\n')
		}
		write(t, filepath.Join(to, rel), b)
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
func TestAdv_13_CSVRowOrder(t *testing.T) {
	start(t)
	d := tableFixture(t)
	other := scratch(t)
	copyTree(t, d, other, true)
	x, y := table(t, d), table(t, other)
	if !bytes.Equal(x, y) {
		t.Fatalf("CSV row order changed aggregate table:\n%s\n%s", x, y)
	}
}
func TestAdv_13_QuotedOutputDirectory(t *testing.T) {
	start(t)
	d := tableFixture(t)
	other := filepath.Join(scratch(t), "operator's runs")
	copyTree(t, d, other, false)
	x, y := table(t, d), table(t, other)
	if !bytes.Equal(x, y) {
		t.Fatal("moving identical flushes changed table")
	}
}
func TestAdv_14_MultiTenantWireReplay(t *testing.T) {
	start(t)
	x, y := wireRun(t, a.RuleControlLaw, false, ""), wireRun(t, a.RuleControlLaw, false, "")
	if x.World.S.Digests() != y.World.S.Digests() || !reflect.DeepEqual(x.Engine, y.Engine) || !reflect.DeepEqual(x.Clients, y.Clients) {
		t.Fatal("same seed changed wire trace, replies or snapshot")
	}
}
func TestAdv_14_FragmentedCSVReplay(t *testing.T) {
	start(t)
	x, y := wireRun(t, a.RuleTimeout, true, scratch(t)), wireRun(t, a.RuleTimeout, true, scratch(t))
	for _, name := range []string{"metrics.csv", "histograms.csv", "run.csv"} {
		if !bytes.Equal(read(t, filepath.Join(x.Flush.RunDir, name)), read(t, filepath.Join(y.Flush.RunDir, name))) {
			t.Fatalf("same seed changed %s", name)
		}
	}
	if x.World.S.Digests() != y.World.S.Digests() {
		t.Fatal("fragmented trace changed")
	}
}
func TestAdv_14_FlushRoundTripAndTamper(t *testing.T) {
	start(t)
	r := wireRun(t, a.RuleTimeout, false, scratch(t))
	if e := r.Flush.Check(); e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(r.Flush.RunDir, "metrics.csv")
	b := read(t, p)
	lines := strings.Split(string(b), "\n")
	changed := false
	for i := len(lines) - 1; i >= 1; i-- {
		if strings.Contains(lines[i], ",admission,served,") {
			j := strings.LastIndex(lines[i], ",")
			lines[i] = lines[i][:j+1] + "999999"
			changed = true
			break
		}
	}
	if !changed {
		t.Fatal("no admission served metric exercised")
	}
	write(t, p, []byte(strings.Join(lines, "\n")))
	if e := r.Flush.Check(); e == nil {
		t.Fatal("flush checker accepted changed served count")
	}
}

// JSON used by the cross-process campaign, kept independent of builder test inputs.
func TestReplayChild(t *testing.T) {
	if os.Getenv("ADV_REPLAY_CHILD") != "1" {
		t.Skip("subprocess helper")
	}
	q := queue(t, cfg(a.RuleTimeout))
	for i := int64(1); i < 1200; i++ {
		q.Push(i*17, fmt.Sprint(i%3), i)
		if i%7 == 0 {
			q.Pop(i * 17)
		}
		drain(q)
	}
	b, e := json.Marshal(q.Snapshot())
	if e != nil {
		t.Fatal(e)
	}
	fmt.Printf("SNAPSHOT:%s\n", b)
}
func TestAdv_14_CrossProcessCoreReplay(t *testing.T) {
	start(t)
	var old []byte
	for i := 0; i < 3; i++ {
		b, e := command(t, ".", []string{"ADV_REPLAY_CHILD=1"}, os.Args[0], "-test.run=^TestReplayChild$")
		if e != nil {
			t.Fatalf("child: %v %s", e, b)
		}
		idx := bytes.Index(b, []byte("SNAPSHOT:"))
		if idx < 0 {
			t.Fatal("missing child snapshot")
		}
		b = bytes.SplitN(b[idx:], []byte("\n"), 2)[0]
		if i > 0 && !bytes.Equal(old, b) {
			t.Fatal("process hash seed changed snapshot")
		}
		old = b
	}
}

func TestAdv_14_LongSeedCampaign(t *testing.T) {
	start(t)
	if os.Getenv("ADV_LONG") != "1" {
		t.Skip("ADV_LONG=1: 32-seed wire replay campaign")
	}
	for s := int64(0); s < 32; s++ {
		for _, k := range []a.RuleKind{a.RuleTimeout, a.RuleControlLaw} {
			t.Logf("scenario seed=%d rule=%s", s, k)
			sp := wireSpec(k, false)
			x, e := e2e.RunSim(sp, s, e2e.SimOptions{})
			if e != nil {
				t.Fatal(s, k, e)
			}
			y, e := e2e.RunSim(sp, s, e2e.SimOptions{})
			if e != nil {
				t.Fatal(s, k, e)
			}
			if x.World.S.Digests() != y.World.S.Digests() || !reflect.DeepEqual(x.Engine, y.Engine) || !reflect.DeepEqual(x.Clients, y.Clients) {
				t.Fatalf("seed=%d rule=%s replay differs", s, k)
			}
		}
	}
}
