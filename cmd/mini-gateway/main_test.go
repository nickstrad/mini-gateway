package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/config"
	"github.com/nickstrad/mini-gateway/internal/config/certtest"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func neverCalled(t *testing.T) contextFunc {
	return func() (context.Context, context.CancelFunc) {
		t.Fatal("the server was started")
		return nil, nil
	}
}

func runCmd(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(args, strings.NewReader(stdin), &out, &errOut, neverCalled(t))
	return code, out.String(), errOut.String()
}

// TestVerifierMode proves -verifier derives valid SCRAM-SHA-256 verifiers from
// both a flag value and stdin ("-").
func TestVerifierMode(t *testing.T) {
	for _, c := range []struct {
		args  []string
		stdin string
		iters int
	}{
		{[]string{"-verifier", "-password", "s3cret"}, "", 4096},
		{[]string{"-verifier", "-password", "s3cret", "-iterations", "8192"}, "", 8192},
		{[]string{"-verifier", "-password", "-"}, "s3cret\n", 4096},
	} {
		code, out, errOut := runCmd(t, c.stdin, c.args...)
		if code != exitOK {
			t.Fatalf("%v: exit %d: %s", c.args, code, errOut)
		}
		v, err := pgwire.ParseVerifier(strings.TrimSpace(out))
		if err != nil || v.Iterations != c.iters || len(v.Salt) != pgwire.ScramSaltLen {
			t.Fatalf("%v: %q: %+v %v", c.args, out, v, err)
		}
		again, err := pgwire.NewVerifier("s3cret", v.Salt, v.Iterations)
		if err != nil || again.String() != strings.TrimSpace(out) {
			t.Fatalf("%v: the verifier is not the password's", c.args)
		}
	}
	_, first, _ := runCmd(t, "", "-verifier", "-password", "x")
	_, second, _ := runCmd(t, "", "-verifier", "-password", "x")
	if first == second {
		t.Fatal("two verifiers share a salt")
	}
}

// TestUsageErrorsExitTwo proves invalid flags, missing -config, and bad config files exit 2.
func TestUsageErrorsExitTwo(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"-verifier"},
		{"-verifier", "-password", "x", "-iterations", "0"},
		{"-nope"},
		{"-config", "x.json", "extra"},
		{"-config", filepath.Join(t.TempDir(), "absent.json")},
		{"-check"},
	} {
		if code, _, _ := runCmd(t, "", args...); code != exitUsage {
			t.Errorf("%v: exit %d, want %d", args, code, exitUsage)
		}
	}
	if code, _, _ := runCmd(t, "", "-h"); code != exitOK {
		t.Errorf("-h: exit %d", code)
	}
}

func exampleConfig(t *testing.T, edit func(string) string) string {
	t.Helper()
	dir := t.TempDir()
	b, err := os.ReadFile(filepath.Join("..", "..", "internal", "config", "testdata", "example.json"))
	if err != nil {
		t.Fatal(err)
	}
	js := string(b)
	if edit != nil {
		js = edit(js)
	}
	path := filepath.Join(dir, "gateway.json")
	certtest.WriteFile(t, path, []byte(js))
	certtest.NewCA(t, "cmd test CA").Issue(t, "localhost").WriteFiles(t, dir, "server.crt", "server.key")
	return path
}

// TestCheckPrintsTheFingerprint proves -check validates a config and prints its fingerprint without serving.
func TestCheckPrintsTheFingerprint(t *testing.T) {
	path := exampleConfig(t, nil)
	code, out, errOut := runCmd(t, "", "-config", path, "-check")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	f, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "fingerprint "+f.Fingerprint()) {
		t.Fatalf("output %q, want fingerprint %s", out, f.Fingerprint())
	}
	bad := exampleConfig(t, func(s string) string { return strings.Replace(s, `"reset": "always"`, `"reset": "never"`, 1) })
	code, _, errOut = runCmd(t, "", "-config", bad, "-check")
	if code != exitUsage || !strings.Contains(errOut, "lease.reset") {
		t.Fatalf("bad config: exit %d: %s", code, errOut)
	}
}

func ephemeral(s string) string {
	s = strings.Replace(s, `"listen": "127.0.0.1:6432"`, `"listen": "127.0.0.1:0"`, 1)
	s = strings.Replace(s, `"admin_listen": "127.0.0.1:6433"`, `"admin_listen": "127.0.0.1:0"`, 1)
	return strings.Replace(s, `"address": "127.0.0.1:5432"`, `"address": "127.0.0.1:1"`, 1)
}

var startLine = regexp.MustCompile(`listening on (\S+), admin on (\S+), config fingerprint ([0-9a-f]{16})`)

// TestServeStartsAndStopsCleanly proves serve binds listeners, serves admin endpoints, and exits 0 on cancel.
func TestServeStartsAndStopsCleanly(t *testing.T) {
	path := exampleConfig(t, ephemeral)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out, errOut syncBuffer
	done := make(chan int, 1)
	go func() {
		done <- run([]string{"-config", path}, strings.NewReader(""), &out, &errOut, func() (context.Context, context.CancelFunc) {
			return ctx, func() {}
		})
	}()
	var m []string
	deadline := time.Now().Add(10 * time.Second)
	for m = startLine.FindStringSubmatch(out.String()); m == nil; m = startLine.FindStringSubmatch(out.String()) {
		select {
		case code := <-done:
			t.Fatalf("exited %d before serving; stdout %q stderr %q", code, out.String(), errOut.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("no start line; stdout %q stderr %q", out.String(), errOut.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	c, err := net.DialTimeout("tcp", m[1], 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + m[2] + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Contains(body, []byte(`"name":"listener","state":"ok"`)) {
		t.Fatalf("healthz %d %s", resp.StatusCode, body)
	}
	cancel()
	select {
	case code := <-done:
		if code != exitOK {
			t.Fatalf("exit %d: %s", code, errOut.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("did not stop")
	}
}

// TestListenFailureExitsOne proves an occupied listener address causes a runtime exit 1.
func TestListenFailureExitsOne(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	path := exampleConfig(t, func(s string) string {
		return strings.Replace(ephemeral(s), `"listen": "127.0.0.1:0"`, `"listen": "`+busy.Addr().String()+`"`, 1)
	})
	var out, errOut bytes.Buffer
	code := run([]string{"-config", path}, strings.NewReader(""), &out, &errOut, func() (context.Context, context.CancelFunc) {
		return context.WithCancel(context.Background())
	})
	if code != exitRuntime || !strings.Contains(errOut.String(), "listen") {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
}

// TestSignalsEndTheRunContext proves signalContext cancels cleanly on SIGINT/SIGTERM.
func TestSignalsEndTheRunContext(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT} {
		ctx, stop := signalContext()
		if err := syscall.Kill(os.Getpid(), sig); err != nil {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
			t.Fatalf("%v did not end the context", sig)
		}
		stop()
	}
}

// TestCryptoRandomVaries proves cryptoRandom draws distinct values for BackendKeyData issuance.
func TestCryptoRandomVaries(t *testing.T) {
	r := cryptoRandom{}
	if r.Uint64() == r.Uint64() && r.Uint64() == r.Uint64() {
		t.Fatal("the random source repeats")
	}
	if build() == "" {
		t.Fatal("empty build string")
	}
}
