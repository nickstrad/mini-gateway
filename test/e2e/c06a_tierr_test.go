package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/csvflush"
	"github.com/nickstrad/mini-gateway/internal/obs/jsonstats"
	"github.com/nickstrad/mini-gateway/internal/proxy/proxysim"
)

const (
	tierRMgctlSteps     = 40
	tierRMgctlKeys      = 5
	tierRMgctlSeed      = 6
	runTargetsTimeout   = 5 * time.Minute
	promPollAttempts    = 60
	promPollPause       = 500 * time.Millisecond
	runTargetsLateAfter = "2s"
)

func buildCmd(t *testing.T, name string) string {
	t.Helper()
	root, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/"+name)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", name, err, out)
	}
	return bin
}

type mgctlBin struct {
	t          *testing.T
	bin        string
	admin      string
	addr       string
	transcript string
}

func (m mgctlBin) run(stdin string, args ...string) (string, int) {
	m.t.Helper()
	full := append([]string{"-admin", m.admin, "-addr", m.addr}, args...)
	cmd := exec.Command(m.bin, full...)
	cmd.Env = append(os.Environ(), "MGCTL_TRANSCRIPT="+m.transcript)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if strings.Contains(errb.String(), "MGCTL_TRANSCRIPT") {
		m.t.Fatalf("transcript: %s", errb.String())
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return out.String() + errb.String(), ee.ExitCode()
	}
	if err != nil {
		m.t.Fatal(err)
	}
	return out.String() + errb.String(), 0
}

func engineCounter(t *testing.T, admin, metric string) int64 {
	t.Helper()
	resp, err := http.Get("http://" + admin + "/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	s, err := jsonstats.Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	return s.Value(obs.ComponentEngine, metric, obs.Labels{})
}

func TestTierRMgctlSeededModelAgainstTheRealBinary(t *testing.T) {
	env := tierR(t)
	bin := buildCmd(t, "mgctl")
	if err := os.MkdirAll(env.RunDir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := mgctlBin{t: t, bin: bin, transcript: filepath.Join(env.RunDir, "tierr-mgctl-transcript.txt")}
	const password = "mgctl-made-password"
	v, code := m.run(password+"\n", "verifier", "-password", "-")
	if code != 0 || strings.Contains(v, password) {
		t.Fatalf("verifier: exit %d %q", code, v)
	}
	local := *env
	local.Verifier = map[string]string{}
	for k, x := range env.Verifier {
		local.Verifier[k] = x
	}
	tenant0 := proxysim.TenantName(0)
	local.Verifier[tenant0] = strings.TrimSpace(v)
	opts := GatewayOpts{Scenario: "tierr-mgctl", ClientTLS: "require", Tenants: 2}
	cfgPath := filepath.Join(t.TempDir(), "gateway.json")
	b, err := json.MarshalIndent(local.gatewayConfig(opts, env.RunDir), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	g := gateway(t, &local, opts)
	m.admin, m.addr = g.Admin, g.Addr
	var rows []string
	for i := 1; i <= tierRMgctlKeys; i++ {
		rows = append(rows, fmt.Sprintf("('m%d', 'v%d')", i, i))
	}
	if err := env.Exec("DELETE FROM kv WHERE k LIKE 'm%'", "INSERT INTO kv VALUES "+strings.Join(rows, ", ")); err != nil {
		t.Fatal(err)
	}
	if out, code := m.run("", "config", "check", cfgPath); code != 0 || !strings.Contains(out, "is valid") {
		t.Fatalf("config check of the running config: %d %s", code, out)
	}
	values := map[string]string{}
	for i := 1; i <= tierRMgctlKeys; i++ {
		values[fmt.Sprintf("m%d", i)] = fmt.Sprintf("v%d", i)
	}
	r := rand.New(rand.NewPCG(tierRMgctlSeed, 1))
	received := engineCounter(t, g.Admin, "requests_received")
	tls := []string{"-tls", "verify-full", "-ca-file", env.CAFile, "-server-name", "localhost"}
	for i := 0; i < tierRMgctlSteps; i++ {
		key := fmt.Sprintf("m%d", 1+r.IntN(tierRMgctlKeys+1))
		var args, want []string
		stdin, wantCode, queries := password+"\n", 0, 0
		switch r.IntN(8) {
		case 0:
			args, want = []string{"health"}, []string{"health: ok", "listener"}
		case 1:
			args, want = []string{"config", "show", "-config", cfgPath}, []string{"matches the running instance: yes"}
		case 2:
			args, want = []string{"tenants", "-config", cfgPath}, []string{"configured tenants", tenant0}
		case 3:
			args, want = []string{"metrics"}, []string{"mg_engine_requests_received_total"}
		case 4:
			queries = 1
			args = append(append([]string{"query", "-user", tenant0, "-password", "-"}, tls...), "SELECT v FROM kv WHERE k = '"+key+"'")
			want = []string{"(0 rows)"}
			if val, ok := values[key]; ok {
				want = []string{val, "(1 rows)", "SELECT 1"}
			}
		case 5:
			queries = 1
			val := "w" + strconv.Itoa(r.IntN(1000))
			args = append(append([]string{"query", "-user", tenant0, "-password", "-"}, tls...), "UPDATE kv SET v = '"+val+"' WHERE k = '"+key+"'")
			want = []string{"UPDATE 0"}
			if _, ok := values[key]; ok {
				values[key] = val
				want = []string{"UPDATE 1"}
			}
		case 6:
			queries, wantCode = 1, 3
			args = append(append([]string{"query", "-user", tenant0, "-password", "-"}, tls...), "SELECT pg_advisory_lock(1)")
			want = []string{"ERROR 0A000", "error-contract row: unsupported-feature"}
		default:
			queries, wantCode = 1, 3
			args = append(append([]string{"query", "-user", tenant0, "-password", "-"}, tls...), "SELECT 1 / 0")
			want = []string{"ERROR 22012: division by zero", "error-contract row: pass-through"}
		}
		before := engineCounter(t, g.Admin, "requests_received")
		out, code := m.run(stdin, args...)
		if code != wantCode {
			t.Fatalf("step %d %v: exit %d, model %d\n%s", i, args, code, wantCode, out)
		}
		for _, w := range want {
			if !strings.Contains(out, w) {
				t.Fatalf("step %d %v: output lacks %q:\n%s", i, args, w, out)
			}
		}
		if strings.Contains(out, password) {
			t.Fatalf("step %d printed the password", i)
		}
		time.Sleep(250 * time.Millisecond)
		after := engineCounter(t, g.Admin, "requests_received")
		if after-before != int64(queries) {
			t.Fatalf("step %d %v: requests_received moved by %d, the model says %d (an admin command must change nothing)", i, args, after-before, queries)
		}
		received += int64(queries)
	}
	if got := engineCounter(t, g.Admin, "requests_received"); got != received {
		t.Fatalf("requests_received %d, model %d", got, received)
	}
	for _, c := range []struct {
		stdin string
		args  []string
		want  string
	}{
		{"wrong\n", append(append([]string{"query", "-user", tenant0, "-password", "-"}, tls...), "SELECT 1"), "error-contract row: auth-failed"},
		{password + "\n", []string{"query", "-user", tenant0, "-password", "-", "-tls", "off", "SELECT 1"}, "error-contract row: tls-required"},
		{password + "\n", append(append([]string{"query", "-user", tenant0, "-password", "-", "-database", "nope"}, tls...), "SELECT 1"), "error-contract row: unknown-database"},
	} {
		out, code := m.run(c.stdin, c.args...)
		if code != 3 || !strings.Contains(out, c.want) {
			t.Fatalf("%v: exit %d\n%s", c.args, code, out)
		}
	}
	if b, err := os.ReadFile(m.transcript); err != nil || !strings.Contains(string(b), "$ mgctl -admin") || strings.Contains(string(b), password) {
		t.Fatalf("transcript %s: %v", m.transcript, err)
	}
}

type devStack struct {
	t   *testing.T
	env []string
	dir string
}

func (d devStack) make(target string, vars ...string) string {
	d.t.Helper()
	root, err := moduleRoot()
	if err != nil {
		d.t.Fatal(err)
	}
	cmd := exec.Command("make", append([]string{"-s", target}, vars...)...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), d.env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		d.t.Fatalf("make %s: %v\n%s", target, err, out)
	}
	return string(out)
}

func docker(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func freePort(t *testing.T) string {
	t.Helper()
	a, err := FreePort()
	if err != nil {
		t.Fatal(err)
	}
	return a[strings.LastIndex(a, ":")+1:]
}

func promValue(t *testing.T, port, query string) (int64, bool) {
	t.Helper()
	resp, err := http.Get("http://127.0.0.1:" + port + "/api/v1/query?query=" + query)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	var doc struct {
		Data struct {
			Result []struct {
				Value []any `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil || len(doc.Data.Result) != 1 || len(doc.Data.Result[0].Value) != 2 {
		return 0, false
	}
	s, _ := doc.Data.Result[0].Value[1].(string)
	v, err := strconv.ParseInt(s, 10, 64)
	return v, err == nil
}

func loadgenCompleted(t *testing.T, dir string) (int64, int64) {
	t.Helper()
	runs, err := filepath.Glob(filepath.Join(dir, "load", "load-simple", "*", "*"))
	if err != nil || len(runs) != 1 {
		t.Fatalf("load tool runs %v: %v", runs, err)
	}
	r, err := csvflush.Read(runs[0])
	if err != nil {
		t.Fatal(err)
	}
	seqs := r.FlushSeqs("loadgen")
	final := r.Snapshot("loadgen", seqs[len(seqs)-1])
	reached := final.Value(obs.ComponentLoadgen, "completed", obs.Labels{Outcome: "ok"}) +
		final.Value(obs.ComponentLoadgen, "completed", obs.Labels{Outcome: "db-error"})
	return reached, final.Value(obs.ComponentLoadgen, "intended", obs.Labels{})
}

func TestTierRRunTargetsUpLoadDown(t *testing.T) {
	tierR(t)
	if _, err := exec.LookPath("make"); err != nil {
		t.Fatal("make is not installed")
	}
	dir := t.TempDir()
	project := fmt.Sprintf("mg-dev-t%d", os.Getpid())
	ports := map[string]string{"MG_DEV_PG_PORT": freePort(t), "MG_DEV_PROM_PORT": freePort(t), "MG_DEV_GW_PORT": freePort(t), "MG_DEV_ADMIN_PORT": freePort(t)}
	d := devStack{t: t, dir: dir, env: []string{"MG_DEV_DIR=" + dir, "MG_DEV_PROJECT=" + project}}
	for k, v := range ports {
		d.env = append(d.env, k+"="+v)
	}
	down := func() { d.make("run-down") }
	t.Cleanup(func() {
		root, _ := moduleRoot()
		cmd := exec.Command("make", "-s", "run-down")
		cmd.Dir, cmd.Env = root, append(os.Environ(), d.env...)
		_ = cmd.Run()
	})
	up := d.make("run-up")
	for _, want := range []string{"The stack is up.", "/targets", "completed/s", "replies/s", "queued"} {
		if !strings.Contains(up, want) {
			t.Fatalf("run-up output lacks %q:\n%s", want, up)
		}
	}
	for _, c := range []string{project + "-postgres", project + "-prometheus"} {
		if !strings.HasPrefix(c, "mg-") || docker(t, "ps", "-q", "--filter", "name=^"+c+"$") == "" {
			t.Fatalf("container %s is not running", c)
		}
	}
	if p := docker(t, "port", project+"-postgres"); !regexp.MustCompile(`^5432/tcp -> 127\.0\.0\.1:\d+$`).MatchString(p) {
		t.Fatalf("Postgres is bound beyond 127.0.0.1: %q", p)
	}
	ss, err := exec.Command("ss", "-Hltn").Output()
	if err != nil {
		t.Fatal(err)
	}
	for name, port := range ports {
		re := regexp.MustCompile(`\s(\S+):` + port + `\s`)
		for _, m := range re.FindAllStringSubmatch(string(ss), -1) {
			if m[1] != "127.0.0.1" {
				t.Fatalf("%s (%s) is listening on %s", name, port, m[1])
			}
		}
		if !re.MatchString(string(ss)) {
			t.Fatalf("%s (%s) is not listening", name, port)
		}
	}
	before, ok := promValue(t, ports["MG_DEV_PROM_PORT"], "mg_engine_requests_completed_total")
	if !ok {
		t.Fatal("Prometheus has no completed-requests series after run-up")
	}
	load := d.make("load", "RATE=40", "DURATION=5s", "PROFILE=simple", "SEED=11", "LOAD_ARGS=-late-after "+runTargetsLateAfter)
	if !strings.Contains(load, "loadgen: seed 11 ") || strings.Contains(load, "SHORTFALL") {
		t.Fatalf("load:\n%s", load)
	}
	want, intended := loadgenCompleted(t, dir)
	if want == 0 || intended != 200 {
		t.Fatalf("the load tool's own count: completed %d intended %d", want, intended)
	}
	var got int64
	for i := 0; i < promPollAttempts; i++ {
		got, ok = promValue(t, ports["MG_DEV_PROM_PORT"], "mg_engine_requests_completed_total")
		if ok && got-before == want {
			break
		}
		time.Sleep(promPollPause)
	}
	if got-before != want {
		t.Fatalf("Prometheus counted %d completed requests during the load, the load tool's CSV flush %d", got-before, want)
	}
	var mounts []string
	for _, c := range []string{project + "-postgres", project + "-prometheus"} {
		mounts = append(mounts, strings.Fields(docker(t, "inspect", "-f", "{{range .Mounts}}{{.Name}} {{end}}", c))...)
	}
	pid, err := os.ReadFile(filepath.Join(dir, "gateway.pid"))
	if err != nil {
		t.Fatal(err)
	}
	gpid, _ := strconv.Atoi(strings.TrimSpace(string(pid)))
	down()
	if out := docker(t, "ps", "-aq", "--filter", "name=^"+project); out != "" {
		t.Fatalf("containers left: %s", out)
	}
	if out := docker(t, "network", "ls", "-q", "--filter", "name=^"+project); out != "" {
		t.Fatalf("networks left: %s", out)
	}
	if out := docker(t, "volume", "ls", "-q", "--filter", "name=^"+project); out != "" {
		t.Fatalf("volumes left: %s", out)
	}
	left := strings.Fields(docker(t, "volume", "ls", "-q"))
	sort.Strings(left)
	for _, v := range mounts {
		if i := sort.SearchStrings(left, v); i < len(left) && left[i] == v {
			t.Fatalf("volume %s of the stack is left behind", v)
		}
	}
	if gpid > 0 && syscall.Kill(gpid, 0) == nil {
		t.Fatalf("the gateway (pid %d) is still running", gpid)
	}
}
