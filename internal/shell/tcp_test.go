package shell

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/config/certtest"
	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/csvflush"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
)

const wait = 10 * time.Second

func listen(t *testing.T) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func dial(t *testing.T, s *Shell) net.Conn {
	t.Helper()
	c, err := net.DialTimeout("tcp", s.Addr().String(), wait)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetDeadline(time.Now().Add(wait))
	t.Cleanup(func() { c.Close() })
	return c
}

func get(t *testing.T, s *Shell, path string) (int, string, []byte) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + s.AdminAddr().String() + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header.Get("Content-Type"), body
}

func TestAdminAnswersWhileTheEngineIsBlocked(t *testing.T) {
	core := newCore()
	entered, release := make(chan struct{}), make(chan struct{})
	core.onStep = func(_ proxy.Instant, ev proxy.Event) []proxy.Action {
		if ev.Kind == proxy.EvClientData {
			close(entered)
			<-release
		}
		return nil
	}
	cfg := testConfig(listen(t))
	cfg.AdminListener = listen(t)
	s := startShell(t, core, cfg)
	defer closeShell(t, s)
	defer close(release)
	deadline := time.Now().Add(wait)
	for s.pub.Load() == nil {
		if time.Now().After(deadline) {
			t.Fatal("no first publish")
		}
		time.Sleep(time.Millisecond)
	}
	c := dial(t, s)
	if _, err := c.Write(sslRequest()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(wait):
		t.Fatal("the engine never blocked")
	}
	code, ctype, body := get(t, s, "/healthz")
	var h struct {
		State  string `json:"state"`
		Checks []struct {
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(body, &h); err != nil || code != http.StatusOK || ctype != "application/json" {
		t.Fatalf("/healthz %d %q %s %v", code, ctype, body, err)
	}
	if h.State != "ok" || len(h.Checks) != 2 || h.Checks[0].Name != "collector" || h.Checks[1].Name != "listener" || h.Checks[1].State != "ok" {
		t.Fatalf("/healthz body %s", body)
	}
	code, ctype, body = get(t, s, "/stats")
	var doc struct {
		Instance string `json:"instance"`
		Seq      int64  `json:"seq"`
	}
	if err := json.Unmarshal(body, &doc); err != nil || code != http.StatusOK || ctype != "application/json" || doc.Instance != "gw-test" {
		t.Fatalf("/stats %d %q %s %v", code, ctype, body, err)
	}
	code, ctype, body = get(t, s, "/metrics")
	if code != http.StatusOK || ctype != PromContentType || !bytes.Contains(body, []byte("mg_obs_publishes")) {
		t.Fatalf("/metrics %d %q %s", code, ctype, body)
	}
	select {
	case <-entered:
	default:
		t.Fatal("unreachable")
	}
	if n := s.Snapshot().AdminRequests; n != 3 {
		t.Fatalf("admin requests %d, want 3", n)
	}
}

func TestAdminRefusesOtherMethods(t *testing.T) {
	cfg := testConfig(listen(t))
	cfg.AdminListener = listen(t)
	s := startShell(t, newCore(), cfg)
	defer closeShell(t, s)
	resp, err := http.Post("http://"+s.AdminAddr().String()+"/healthz", "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestListenerDownFailsHealthAndEndsRun(t *testing.T) {
	ln := listen(t)
	cfg := testConfig(ln)
	cfg.AdminListener = listen(t)
	s, err := New(newCore(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	ran := make(chan error, 1)
	go func() { ran <- s.Run(context.Background()) }()
	deadline := time.Now().Add(wait)
	for s.pub.Load() == nil || !s.listenerUp.Load() {
		if time.Now().After(deadline) {
			t.Fatal("not started")
		}
		time.Sleep(time.Millisecond)
	}
	if code, _, _ := get(t, s, "/healthz"); code != http.StatusOK {
		t.Fatalf("healthz %d before the listener went down", code)
	}
	_ = ln.Close()
	select {
	case err := <-ran:
		if !errors.Is(err, ErrListenerDown) {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(wait):
		t.Fatal("Run did not return after the listener went down")
	}
	h := s.Health()
	if h.State != obs.StateFailed || h.Checks[len(h.Checks)-1].Name != CheckListener {
		t.Fatalf("health %+v", h)
	}
	if body := h.AppendJSON(nil); !bytes.Contains(body, []byte(`"state":"failed"`)) {
		t.Fatalf("health body %s", body)
	}
	wantGoroutines(t, s, 0)
}

func TestListenerDownAnswers503(t *testing.T) {
	ln := listen(t)
	cfg := testConfig(ln)
	cfg.AdminListener = listen(t)
	s := startShell(t, newCore(), cfg)
	defer closeShell(t, s)
	_ = ln.Close()
	<-s.ListenerLost()
	code, _, body := get(t, s, "/healthz")
	if code != http.StatusServiceUnavailable || !bytes.Contains(body, []byte(`{"name":"listener","state":"failed"`)) {
		t.Fatalf("healthz %d %s", code, body)
	}
}

func TestRunStopsOnCancelAndFlushesTheCSVFiles(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(listen(t))
	cfg.Obs.CSVDir = dir
	var shellSeen func() proxy.ShellSnapshot
	cfg.Sources = func(c *obs.Collector, sh func() proxy.ShellSnapshot) error {
		shellSeen = sh
		return nil
	}
	s, err := New(newCore(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if shellSeen == nil || shellSeen().MailboxCap != mailboxCap {
		t.Fatal("the sources hook did not get the shell snapshot")
	}
	ctx, cancel := context.WithCancel(context.Background())
	ran := make(chan error, 1)
	go func() { ran <- s.Run(ctx) }()
	c, err := net.DialTimeout("tcp", s.Addr().String(), wait)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	cancel()
	select {
	case err := <-ran:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(wait):
		t.Fatal("Run did not stop")
	}
	run := s.CSVRunDir()
	if !strings.HasPrefix(run, filepath.Join(dir, "gw-test")+string(filepath.Separator)) {
		t.Fatalf("run dir %q", run)
	}
	for _, f := range []string{csvflush.MetricsFile, csvflush.RunFile} {
		st, err := os.Stat(filepath.Join(run, f))
		if err != nil || st.Size() == 0 {
			t.Fatalf("%s: %v", f, err)
		}
	}
	runCSV, _ := os.ReadFile(filepath.Join(run, csvflush.RunFile))
	if !bytes.Contains(runCSV, []byte(",R,")) {
		t.Fatalf("run.csv does not name tier R:\n%s", runCSV)
	}
	wantGoroutines(t, s, 0)
}

func TestSmuggledPlaintextAfterTheTLSRequestClosesTheConnection(t *testing.T) {
	ca := certtest.NewCA(t, "shell test CA")
	core := newCore()
	core.onStep = tlsDecider
	cfg := testConfig(listen(t))
	cfg.ClientTLS = &tls.Config{Certificates: []tls.Certificate{ca.Issue(t, "localhost").TLS}, MinVersion: tls.VersionTLS12}
	s := startShell(t, core, cfg)
	defer closeShell(t, s)
	c := dial(t, s)
	if _, err := c.Write(append(sslRequest(), startupMessage("alice", "app")...)); err != nil {
		t.Fatal(err)
	}
	rest, err := io.ReadAll(c)
	if err != nil || len(rest) == 0 || rest[0] != 'S' {
		t.Fatalf("read %q %v", rest, err)
	}
	res := core.waitKind(t, proxy.EvClientTLS, 1, wait)
	if res[0].ev.TLS.Outcome != pgwire.HandshakeViolation {
		t.Fatalf("handshake outcome %v, want %v", res[0].ev.TLS.Outcome, pgwire.HandshakeViolation)
	}
	data := core.of(proxy.EvClientData)
	if len(data) != 1 || len(data[0].ev.Data) != proxy.FirstClientRead {
		t.Fatalf("data events %+v: the smuggled bytes reached the engine", data)
	}
	if s.Snapshot().HandshakesFailed != 1 {
		t.Fatalf("snapshot %+v", s.Snapshot())
	}
}

func clientTLSRun(t *testing.T, client *tls.Config) (*scriptCore, error, []byte) {
	ca := certtest.NewCA(t, "shell test CA")
	core := newCore()
	core.onStep = tlsDecider
	cfg := testConfig(listen(t))
	cfg.ClientTLS = &tls.Config{Certificates: []tls.Certificate{ca.Issue(t, "localhost").TLS}, MinVersion: tls.VersionTLS12}
	s := startShell(t, core, cfg)
	defer closeShell(t, s)
	c := dial(t, s)
	if _, err := c.Write(sslRequest()); err != nil {
		t.Fatal(err)
	}
	answer := make([]byte, 1)
	if _, err := io.ReadFull(c, answer); err != nil || answer[0] != 'S' {
		t.Fatalf("answer %q %v", answer, err)
	}
	client.RootCAs, client.ServerName = ca.Pool(), "localhost"
	tc := tls.Client(c, client)
	if err := tc.Handshake(); err != nil {
		core.waitKind(t, proxy.EvClientTLS, 1, wait)
		return core, err, nil
	}
	if _, err := tc.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, len("echo:ping"))
	if _, err := io.ReadFull(tc, reply); err != nil {
		t.Fatal(err)
	}
	core.waitKind(t, proxy.EvClientWrote, 2, wait)
	return core, nil, reply
}

func TestClientTLSLeg(t *testing.T) {
	for _, c := range []struct {
		name    string
		max     uint16
		version pgwire.TLSVersion
	}{{"1.3", 0, pgwire.TLSVersion13}, {"1.2", tls.VersionTLS12, pgwire.TLSVersion12}} {
		t.Run(c.name, func(t *testing.T) {
			core, err, reply := clientTLSRun(t, &tls.Config{MaxVersion: c.max})
			if err != nil {
				t.Fatal(err)
			}
			res := core.of(proxy.EvClientTLS)
			if len(res) != 1 || res[0].ev.TLS != (pgwire.HandshakeResult{Outcome: pgwire.HandshakeCompleted, Version: c.version}) {
				t.Fatalf("handshake events %+v", res)
			}
			if string(reply) != "echo:ping" {
				t.Fatalf("reply %q", reply)
			}
			var got []byte
			for _, d := range core.of(proxy.EvClientData)[1:] {
				got = append(got, d.ev.Data...)
			}
			if string(got) != "ping" {
				t.Fatalf("plaintext after the handshake %q", got)
			}
		})
	}
	t.Run("1.1 refused", func(t *testing.T) {
		core, err, _ := clientTLSRun(t, &tls.Config{MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS11})
		if err == nil {
			t.Fatal("a TLS 1.1 client completed the handshake")
		}
		res := core.of(proxy.EvClientTLS)
		if len(res) != 1 || res[0].ev.TLS.Outcome != pgwire.HandshakeFailed {
			t.Fatalf("handshake events %+v", res)
		}
	})
}

type fakeDB struct {
	ln   net.Listener
	got  chan []byte
	cert tls.Certificate
	tls  bool
}

func startFakeDB(t *testing.T, cert tls.Certificate, useTLS bool) *fakeDB {
	f := &fakeDB{ln: listen(t), got: make(chan []byte, 1), cert: cert, tls: useTLS}
	t.Cleanup(func() { f.ln.Close() })
	go f.serve()
	return f
}

func (f *fakeDB) serve() {
	c, err := f.ln.Accept()
	if err != nil {
		return
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(wait))
	first := make([]byte, 8)
	if _, err := io.ReadFull(c, first); err != nil {
		f.got <- nil
		return
	}
	if !f.tls {
		f.got <- first
		return
	}
	if !bytes.Equal(first, sslRequest()) {
		f.got <- first
		return
	}
	if _, err := c.Write([]byte{'S'}); err != nil {
		return
	}
	tc := tls.Server(c, &tls.Config{Certificates: []tls.Certificate{f.cert}})
	if err := tc.Handshake(); err != nil {
		f.got <- nil
		return
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(tc, buf); err != nil {
		f.got <- nil
		return
	}
	f.got <- buf
}

func dbScript(tlsFirst bool) func(proxy.Instant) []proxy.Action {
	return func(proxy.Instant) []proxy.Action {
		return []proxy.Action{{Kind: proxy.ActDBOpen, Conn: 1, TLSFirst: tlsFirst}}
	}
}

func dbStep(_ proxy.Instant, ev proxy.Event) []proxy.Action {
	switch {
	case ev.Kind == proxy.EvDBOpened && ev.Conn == 1:
		return nil
	case ev.Kind == proxy.EvDBData && bytes.Equal(ev.Data, []byte{'S'}):
		return []proxy.Action{act(proxy.ActDBStartTLS, ev.Conn)}
	case ev.Kind == proxy.EvDBTLS && ev.TLS.Outcome == pgwire.HandshakeCompleted:
		return []proxy.Action{act(proxy.ActDBResume, ev.Conn), writeAct(proxy.ActDBWrite, ev.Conn, []byte("hello"))}
	case ev.Kind == proxy.EvDBTLS:
		return []proxy.Action{act(proxy.ActDBClose, ev.Conn)}
	}
	return nil
}

func TestDatabaseTLSModes(t *testing.T) {
	dbCA, otherCA := certtest.NewCA(t, "database CA"), certtest.NewCA(t, "unrelated CA")
	leaf := dbCA.Issue(t, "db.test")
	cases := []struct {
		name    string
		backend *tls.Config
		outcome pgwire.HandshakeOutcome
		got     string
	}{
		{"require with an unknown CA", &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}, pgwire.HandshakeCompleted, "hello"},
		{"verify-full with the wrong CA", &tls.Config{RootCAs: otherCA.Pool(), ServerName: "db.test", MinVersion: tls.VersionTLS12}, pgwire.HandshakeFailedVerification, ""},
		{"verify-full with the wrong name", &tls.Config{RootCAs: dbCA.Pool(), ServerName: "other.test", MinVersion: tls.VersionTLS12}, pgwire.HandshakeFailedVerification, ""},
		{"verify-full with the right CA", &tls.Config{RootCAs: dbCA.Pool(), ServerName: "db.test", MinVersion: tls.VersionTLS12}, pgwire.HandshakeCompleted, "hello"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := startFakeDB(t, leaf.TLS, true)
			core := newCore()
			core.onStart = dbScript(true)
			core.onStep = func(now proxy.Instant, ev proxy.Event) []proxy.Action {
				if ev.Kind == proxy.EvDBOpened {
					return []proxy.Action{writeAct(proxy.ActDBWrite, ev.Conn, sslRequest())}
				}
				return dbStep(now, ev)
			}
			cfg := testConfig(listen(t))
			cfg.DBAddress, cfg.BackendTLS = db.ln.Addr().String(), c.backend
			s := startShell(t, core, cfg)
			defer closeShell(t, s)
			res := core.waitKind(t, proxy.EvDBTLS, 1, wait)
			if res[0].ev.TLS.Outcome != c.outcome {
				t.Fatalf("outcome %v, want %v", res[0].ev.TLS.Outcome, c.outcome)
			}
			select {
			case got := <-db.got:
				if string(got) != c.got {
					t.Fatalf("the database got %q, want %q", got, c.got)
				}
			case <-time.After(wait):
				t.Fatal("the fake database saw nothing")
			}
			first := core.of(proxy.EvDBData)
			if len(first) == 0 || len(first[0].ev.Data) != proxy.FirstDBTLSRead {
				t.Fatalf("first database read %+v, want exactly %d byte", first, proxy.FirstDBTLSRead)
			}
		})
	}
	t.Run("disable never sends TLS", func(t *testing.T) {
		db := startFakeDB(t, leaf.TLS, false)
		core := newCore()
		core.onStart = dbScript(false)
		core.onStep = func(_ proxy.Instant, ev proxy.Event) []proxy.Action {
			if ev.Kind == proxy.EvDBOpened {
				return []proxy.Action{writeAct(proxy.ActDBWrite, ev.Conn, startupMessage("gateway", "app")[:8])}
			}
			return nil
		}
		cfg := testConfig(listen(t))
		cfg.DBAddress = db.ln.Addr().String()
		s := startShell(t, core, cfg)
		defer closeShell(t, s)
		select {
		case got := <-db.got:
			if !bytes.Equal(got, startupMessage("gateway", "app")[:8]) {
				t.Fatalf("the database got %q", got)
			}
		case <-time.After(wait):
			t.Fatal("the fake database saw nothing")
		}
		core.waitKind(t, proxy.EvDBClosed, 1, wait)
		if len(core.of(proxy.EvDBTLS)) != 0 {
			t.Fatal("a TLS event without a TLS request")
		}
	})
	t.Run("start-tls without a backend TLS config fails", func(t *testing.T) {
		db := startFakeDB(t, leaf.TLS, true)
		core := newCore()
		core.onStart = dbScript(true)
		core.onStep = func(now proxy.Instant, ev proxy.Event) []proxy.Action {
			if ev.Kind == proxy.EvDBOpened {
				return []proxy.Action{writeAct(proxy.ActDBWrite, ev.Conn, sslRequest())}
			}
			return dbStep(now, ev)
		}
		cfg := testConfig(listen(t))
		cfg.DBAddress = db.ln.Addr().String()
		s := startShell(t, core, cfg)
		defer closeShell(t, s)
		res := core.waitKind(t, proxy.EvDBTLS, 1, wait)
		if res[0].ev.TLS.Outcome != pgwire.HandshakeFailed {
			t.Fatalf("outcome %v", res[0].ev.TLS.Outcome)
		}
	})
}

func TestNewRefusesBadConfigs(t *testing.T) {
	ln := listen(t)
	defer ln.Close()
	for _, c := range []struct {
		name string
		core Core
		edit func(*Config)
	}{
		{"no core", nil, func(*Config) {}},
		{"no listener", newCore(), func(c *Config) { c.Listener = nil }},
		{"mailbox too large", newCore(), func(c *Config) { c.MailboxCap = maxMailboxCap + 1 }},
		{"negative mailbox", newCore(), func(c *Config) { c.MailboxCap = -1 }},
		{"no publish interval", newCore(), func(c *Config) { c.PublishInterval = 0 }},
		{"bad health rule", newCore(), func(c *Config) { c.HealthRules = []obs.Rule{{Name: "Bad Name"}} }},
		{"failing sources", newCore(), func(c *Config) {
			c.Sources = func(*obs.Collector, func() proxy.ShellSnapshot) error { return errRefused }
		}},
		{"bad csv run id", newCore(), func(c *Config) { c.Obs.CSVDir, c.Obs.Instance = t.TempDir(), "../x" }},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg := testConfig(ln)
			c.edit(&cfg)
			if _, err := New(c.core, cfg); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestCloseWithoutStartAndStartTwice(t *testing.T) {
	s, err := New(newCore(), testConfig(listen(t)))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(); !errors.Is(err, ErrStarted) {
		t.Fatalf("Start after Close: %v", err)
	}
	s = startShell(t, newCore(), testConfig(listen(t)))
	if err := s.Start(); !errors.Is(err, ErrStarted) {
		t.Fatalf("second Start: %v", err)
	}
	closeShell(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}
