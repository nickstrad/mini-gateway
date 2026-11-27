package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/jsonstats"
)

type recorder struct {
	mu      sync.Mutex
	methods []string
	paths   []string
}

func adminServer(t *testing.T) (*httptest.Server, *recorder) {
	t.Helper()
	rec := &recorder{}
	s := obs.InstanceSnapshot{Instance: "gw-1", ConfigFingerprint: "00", Points: []obs.Point{{Component: "engine", Metric: "queue_capacity", Kind: obs.KindGauge, Value: 8}}}
	stats, err := jsonstats.Render(s)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.methods = append(rec.methods, r.Method)
		rec.paths = append(rec.paths, r.URL.Path)
		rec.mu.Unlock()
		switch r.URL.Path {
		case "/healthz":
			_, _ = w.Write(obs.Health{Checks: []obs.CheckResult{{Name: "listener", Reason: "accepting clients"}}}.AppendJSON(nil))
		case "/stats":
			_, _ = w.Write(stats)
		case "/metrics":
			_, _ = w.Write([]byte("mg_engine_queue_capacity 8\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func call(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, strings.NewReader("pw-from-stdin\n"), &out, &errb)
	return code, out.String(), errb.String()
}

func TestNoSubcommandSendsAnythingButAGet(t *testing.T) {
	srv, rec := adminServer(t)
	admin := strings.TrimPrefix(srv.URL, "http://")
	for _, args := range [][]string{{"health"}, {"stats"}, {"stats", "-watch", "-count", "2", "-interval", "1ms"}, {"metrics"},
		{"tenants"}, {"config", "show"}} {
		code, out, errs := call(append([]string{"-admin", admin}, args...)...)
		if code != 0 {
			t.Fatalf("%v: exit %d\n%s%s", args, code, out, errs)
		}
	}
	for i, m := range rec.methods {
		if m != http.MethodGet {
			t.Fatalf("request %d was %s %s", i, m, rec.paths[i])
		}
	}
	for _, p := range rec.paths {
		if !slices.Contains([]string{"/healthz", "/stats", "/metrics"}, p) {
			t.Fatalf("asked for %s", p)
		}
	}
	if len(rec.paths) != 7 {
		t.Fatalf("requests %v", rec.paths)
	}
}

func TestTranscriptLogsEachCommandAndHidesThePassword(t *testing.T) {
	srv, _ := adminServer(t)
	path := filepath.Join(t.TempDir(), "transcript.txt")
	t.Setenv("MGCTL_TRANSCRIPT", path)
	call("-admin", strings.TrimPrefix(srv.URL, "http://"), "health")
	call("verifier", "-password", "hunter2-secret")
	call("-addr", "127.0.0.1:1", "-timeout", "200ms", "query", "-user", "t0", "-password=hunter2-secret", "SELECT 1")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, "hunter2-secret") {
		t.Fatalf("transcript holds the password:\n%s", s)
	}
	for _, want := range []string{"$ mgctl -admin ", "health: ok", "[exit 0]", "$ mgctl verifier -password (password)", "SCRAM-SHA-256$4096:",
		"-password=(password)", "[exit 1]"} {
		if !strings.Contains(s, want) {
			t.Fatalf("transcript lacks %q:\n%s", want, s)
		}
	}
}

func TestUsage(t *testing.T) {
	for _, args := range [][]string{{}, {"bogus"}, {"health", "extra"}, {"config"}, {"config", "check"}, {"config", "nope"},
		{"query", "SELECT 1"}, {"query", "-user", "u"}, {"stats", "-interval", "0s"}, {"query", "-user", "u", "-tls", "x", "SELECT 1"}} {
		if code, _, _ := call(args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	if code, out, _ := call("help"); code != 0 || !strings.Contains(out, "Usage:") {
		t.Fatal("help")
	}
}

func exampleConfig(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "internal", "config", "testdata", "example.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	m["client_tls"] = map[string]any{"mode": "off"}
	return m
}

func mutate(r *rand.Rand, base map[string]any) map[string]any {
	b, _ := json.Marshal(base)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	for k := r.IntN(3); k > 0; k-- {
		section := []string{"pool", "queue", "limits", "timeouts", "lease", "fleet", "obs", "database", "backend_tls", "client_tls", "top"}[r.IntN(11)]
		val := []any{-1, 0, 1, 3, 5000, 100000000, "5s", "0s", "-5s", "bogus", "always", "require", true, nil, []any{}}[r.IntN(15)]
		target := m
		if section != "top" {
			sub, ok := m[section].(map[string]any)
			if !ok {
				continue
			}
			target = sub
		}
		var keys []string
		for key := range target {
			keys = append(keys, key)
		}
		if len(keys) == 0 {
			continue
		}
		slices.Sort(keys)
		key := keys[r.IntN(len(keys))]
		switch r.IntN(4) {
		case 0:
			delete(target, key)
		case 1:
			target[key+"_x"] = val
		default:
			target[key] = val
		}
	}
	return m
}

func TestConfigCheckAgreesWithTheGatewayBinaryOnGeneratedConfigs(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the gateway and mgctl binaries")
	}
	dir := t.TempDir()
	gw, ctl := filepath.Join(dir, "mini-gateway"), filepath.Join(dir, "mgctl")
	for bin, pkg := range map[string]string{gw: "../mini-gateway", ctl: "."} {
		if out, err := exec.Command("go", "build", "-o", bin, pkg).CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", pkg, err, out)
		}
	}
	base := exampleConfig(t)
	r := rand.New(rand.NewPCG(2026, 11))
	valid, invalid := 0, 0
	for i := 0; i < 150; i++ {
		b, _ := json.Marshal(mutate(r, base))
		path := filepath.Join(dir, "c.json")
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
		gOut, gErr, gCode := runBin(t, gw, "-config", path, "-check")
		cOut, cErr, cCode := runBin(t, ctl, "config", "check", path)
		if gCode != cCode || gOut != cOut || strings.TrimPrefix(gErr, "mini-gateway: ") != strings.TrimPrefix(cErr, "mgctl: ") {
			t.Fatalf("config %d %s\ngateway exit %d %q %q\nmgctl   exit %d %q %q", i, b, gCode, gOut, gErr, cCode, cOut, cErr)
		}
		if gCode == 0 {
			valid++
		} else {
			invalid++
		}
	}
	if valid == 0 || invalid == 0 {
		t.Fatalf("valid %d invalid %d: the generator must produce both", valid, invalid)
	}
}

func runBin(t *testing.T, bin string, args ...string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	cmd := exec.Command(bin, args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return out.String(), errb.String(), ee.ExitCode()
	}
	if err != nil {
		t.Fatal(err)
	}
	return out.String(), errb.String(), 0
}
