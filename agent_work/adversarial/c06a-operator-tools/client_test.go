package adv_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/config"
	lg "github.com/nickstrad/mini-gateway/internal/loadgen"
	"github.com/nickstrad/mini-gateway/internal/loadgen/live"
	"github.com/nickstrad/mini-gateway/internal/mgctl"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
)

func frame(k byte, b []byte) []byte {
	r := []byte{k, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(r[1:], uint32(len(b)+4))
	return append(r, b...)
}
func ready() []byte { return frame('Z', []byte{'I'}) }
func errorFrame(sev, code, msg string) []byte {
	return frame('E', []byte("S"+sev+"\x00C"+code+"\x00M"+msg+"\x00\x00"))
}
func loginBytes() []byte { return append(frame('R', []byte{0, 0, 0, 0}), ready()...) }

type rwFixture struct {
	*bytes.Reader
	written bytes.Buffer
}

func (r *rwFixture) Write(b []byte) (int, error) { return r.written.Write(b) }
func (r *rwFixture) Close() error                { return nil }
func envBuf() (mgctl.Env, *bytes.Buffer, *bytes.Buffer) {
	out, errb := new(bytes.Buffer), new(bytes.Buffer)
	return mgctl.Env{Stdout: out, Stderr: errb, Stdin: strings.NewReader(""), Salt: func(b []byte) {
		for i := range b {
			b[i] = byte(i + 13)
		}
	}}, out, errb
}
func queryFixture(t *testing.T, b []byte) (int, string, string) {
	t.Helper()
	t.Logf("seed=%d", seed)
	e, o, er := envBuf()
	e.Dial = func() (io.ReadWriteCloser, error) {
		return &rwFixture{Reader: bytes.NewReader(append(loginBytes(), b...))}, nil
	}
	rc := mgctl.Query(e, mgctl.Credentials{User: "t0", Database: "app"}, "SELECT 1")
	return rc, o.String(), er.String()
}
func TestAdv_6_LiveSQLStateClassification(t *testing.T) {
	t.Logf("seed=%d", seed)
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, e := ln.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(15 * time.Second))
		var h [4]byte
		if _, e = io.ReadFull(c, h[:]); e != nil {
			return
		}
		n := binary.BigEndian.Uint32(h[:])
		if n > 65536 {
			return
		}
		io.CopyN(io.Discard, c, int64(n)-4)
		c.Write(loginBytes())
		codes := []string{"22012", "23505", "42P01", "0A000"}
		messages := []string{"division by zero", "duplicate key", "missing relation", pgwire.ErrorRows[pgwire.ErrRowUnsupportedFeature].Message}
		for i := 0; ; i++ {
			var h [5]byte
			if _, e = io.ReadFull(c, h[:]); e != nil {
				return
			}
			n := binary.BigEndian.Uint32(h[1:])
			if n > 65536 {
				return
			}
			io.CopyN(io.Discard, c, int64(n)-4)
			if h[0] != 'Q' {
				return
			}
			c.Write(append(errorFrame("ERROR", codes[i%4], messages[i%4]), ready()...))
		}
	}()
	p := plan()
	p.Rate = 24
	p.DurationNs = 1e9
	r, err := live.Run(context.Background(), live.Options{Addr: ln.Addr().String(), Plan: p, UserPrefix: "t", Database: "app", Tracker: lg.TrackerConfig{Conns: 1}, Drain: 5 * time.Second})
	<-done
	if err != nil {
		t.Fatal(err)
	}
	s := r.Snapshot
	if s.Completed[lg.OutcomeRefused] != 6 || s.Completed[lg.OutcomeDBError] != 18 || len(s.ErrorsByClass) != 4 {
		t.Fatalf("expected 6 refused,18 db-error,4 classes: %+v", s)
	}
	for _, c := range s.ErrorsByClass {
		if c.Count != 6 {
			t.Fatalf("class count %+v expected 6", c)
		}
	}
}
func configDiff(t *testing.T, variants []string) {
	t.Helper()
	t.Logf("seed=%d", seed)
	for i, s := range variants {
		f := filepath.Join(t.TempDir(), "case.json")
		if err := os.WriteFile(f, []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
		_, want := config.Load(f)
		e, _, er := envBuf()
		rc := mgctl.ConfigCheck(e, f)
		if (rc == 0) != (want == nil) {
			t.Fatalf("case %d gateway=%v mgctl=%d %s", i, want, rc, er)
		}
		if want != nil && !strings.Contains(er.String(), want.Error()) {
			t.Fatalf("case %d different diagnostic: %s vs %v", i, er, want)
		}
	}
}
func TestAdv_7_ConfigGrammarDifferential(t *testing.T) {
	base := read(t, "internal/config/testdata/example.json")
	configDiff(t, []string{base + "\n{}", base + "\x00", strings.Replace(base, "{", "{\"unknown\":true,", 1), "[]", "null", base})
}
func TestAdv_7_ConfigDuplicateFieldsDifferential(t *testing.T) {
	base := read(t, "internal/config/testdata/example.json")
	var variants []string
	for _, v := range []string{"null", "0", "-1", "9223372036854775808", "{}", "[]", "\"127.0.0.1:0\""} {
		variants = append(variants, strings.Replace(base, "{", "{\"listen\":"+v+",\"listen\":"+v+",", 1))
	}
	configDiff(t, variants)
}

type readAdmin struct{ paths []string }

func (a *readAdmin) Get(p string) (int, []byte, error) {
	a.paths = append(a.paths, p)
	switch p {
	case "/healthz":
		return 200, []byte(`{"state":"ok"}`), nil
	case "/stats":
		return 200, []byte(`{"instance":"adversary","points":[]}`), nil
	case "/metrics":
		return 200, []byte("# harmless\n"), nil
	}
	return 404, nil, fmt.Errorf("unexpected path %s", p)
}
func TestAdv_7_AdminReadsNeverDial(t *testing.T) {
	t.Logf("seed=%d", seed)
	e, _, _ := envBuf()
	a := new(readAdmin)
	e.Admin = a
	e.Dial = func() (io.ReadWriteCloser, error) { t.Fatal("admin command dialed database"); return nil, nil }
	mgctl.Health(e)
	mgctl.Metrics(e)
	mgctl.ConfigShow(e, "")
	mgctl.Tenants(e, "")
	if len(a.paths) != 4 {
		t.Fatalf("expected four read paths, observed %v", a.paths)
	}
	for _, p := range a.paths {
		if p != "/healthz" && p != "/stats" && p != "/metrics" {
			t.Fatal(p)
		}
	}
}
func verifyLogin(t *testing.T, password, text string) {
	t.Helper()
	v, err := pgwire.ParseVerifier(strings.TrimSpace(text))
	if err != nil {
		t.Fatal(err)
	}
	s := pgwire.NewScramServer(v, "server-nonce", false)
	c := pgwire.NewScramClient("", password, "client-nonce", pgwire.GS2Header(false, false))
	first, cause := s.First(pgwire.ScramMechanism, c.First())
	if cause != pgwire.AuthCauseNone {
		t.Fatal(cause)
	}
	last, cause := c.Final(first)
	if cause != pgwire.AuthCauseNone {
		t.Fatal(cause)
	}
	reply, cause := s.Final(last)
	if cause != pgwire.AuthCauseNone || c.Verify(reply) != pgwire.AuthCauseNone {
		t.Fatalf("verifier does not authenticate supplied password: %v", cause)
	}
}
func TestAdv_8_LongStdinPassword(t *testing.T) {
	t.Logf("seed=%d", seed)
	pw := strings.Repeat("abcdefgh", 129)
	e, o, er := envBuf()
	e.Stdin = strings.NewReader(pw + "\n")
	rc := mgctl.Verifier(e, "-", 4096)
	if rc != 0 {
		t.Logf("oversized password explicitly refused: %s", er)
		return
	}
	verifyLogin(t, pw, o.String())
}
func TestAdv_8_UnicodeAndWhitespaceSecrets(t *testing.T) {
	t.Logf("seed=%d", seed)
	for _, pw := range []string{"　é雪☃ pass ", "trailing\tsecret", "comma,equals=secret"} {
		e, o, er := envBuf()
		if rc := mgctl.Verifier(e, pw, 4096); rc != 0 {
			t.Fatalf("rc=%d %s", rc, er)
		}
		if strings.Contains(o.String()+er.String(), pw) {
			t.Fatal("plaintext secret printed")
		}
		verifyLogin(t, pw, o.String())
	}
}
func TestAdv_8_InvalidIterationDiagnostics(t *testing.T) {
	t.Logf("seed=%d", seed)
	for _, n := range []int{-1, 0, pgwire.MaxScramIterations + 1, 2147483647} {
		e, o, er := envBuf()
		pw := "private-606013"
		if rc := mgctl.Verifier(e, pw, n); rc == 0 {
			t.Fatalf("accepted iterations %d", n)
		}
		if strings.Contains(o.String()+er.String(), pw) {
			t.Fatalf("password in diagnostic at %d", n)
		}
	}
}
func TestAdv_9_FatalWithoutReady(t *testing.T) {
	for _, id := range []pgwire.ErrRowID{pgwire.ErrRowTLSRequired, pgwire.ErrRowLostInTransaction, pgwire.ErrRowIdleInTransaction} {
		r := pgwire.ErrorRows[id]
		rc, out, er := queryFixture(t, errorFrame("FATAL", r.Code, r.Message))
		if rc != mgctl.ExitAnswered || !strings.Contains(out, r.Code) || !strings.Contains(out, r.ID) {
			t.Fatalf("fatal row %s rc=%d output=%s error=%s", r.ID, rc, out, er)
		}
	}
}
func TestAdv_9_RowsBeforeLaterError(t *testing.T) {
	b := rowReply("first-result")
	b = append(b, errorFrame("ERROR", "22012", "division by zero")...)
	b = append(b, ready()...)
	rc, out, er := queryFixture(t, b)
	if rc != mgctl.ExitAnswered || !strings.Contains(out, "22012") || !strings.Contains(out, "pass-through") {
		t.Fatalf("error after rows rc=%d out=%s err=%s", rc, out, er)
	}
}
func rowReply(value string) []byte {
	desc := []byte{0, 1, 'v', 0}
	desc = append(desc, make([]byte, 18)...)
	b := frame('T', desc)
	row := []byte{0, 1, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(row[2:], uint32(len(value)))
	row = append(row, []byte(value)...)
	b = append(b, frame('D', row)...)
	return append(b, frame('C', []byte("SELECT 1\x00"))...)
}
func TestAdv_9_MultipleResultSets(t *testing.T) {
	b := append(rowReply("first-result"), rowReply("second-result")...)
	b = append(b, ready()...)
	rc, out, er := queryFixture(t, b)
	if rc != 0 || !strings.Contains(out, "first-result") || !strings.Contains(out, "second-result") {
		t.Fatalf("multi-result rc=%d out=%s err=%s", rc, out, er)
	}
}
