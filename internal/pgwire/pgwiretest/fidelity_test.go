package pgwiretest_test

import (
	"crypto/tls"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire/pgwiretest"
)

const recording = "testdata/startup-postgres17.txt"

func simTranscript(t *testing.T) []string {
	t.Helper()
	d, err := pgwiretest.NewSimFidelity(1)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pgwiretest.Run(d, pgwiretest.StartupScript(pgwiretest.FidelityUser, pgwiretest.FidelityPassword, pgwiretest.FidelityDatabase))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func readRecording(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(recording)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out
}

func comparable(lines []string) []string {
	var out []string
	for _, l := range lines {
		name, _, _ := strings.Cut(l, ":")
		if _, skip := pgwiretest.KnownDifferences[name]; !skip {
			out = append(out, l)
		}
	}
	return out
}

func requireSame(t *testing.T, label string, want, got []string) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("%s: %d steps against %d", label, len(want), len(got))
	}
	want, got = comparable(want), comparable(got)
	if strings.Join(want, "\n") != strings.Join(got, "\n") {
		t.Fatalf("%s differs\nwant:\n%s\ngot:\n%s", label, strings.Join(want, "\n"), strings.Join(got, "\n"))
	}
}

// TestStartupFidelityAgainstRecordedPostgres17 proves fakepg's 12-step startup
// transcript matches the committed Postgres 17 recording outside KnownDifferences.
func TestStartupFidelityAgainstRecordedPostgres17(t *testing.T) {
	requireSame(t, "fake against the Postgres 17 recording", readRecording(t), simTranscript(t))
}

// TestKnownDifferencesAreTheRecordedOnes proves KnownDifferences stays exact
// (only gss-request: real G vs fake N).
func TestKnownDifferencesAreTheRecordedOnes(t *testing.T) {
	rec, sim := readRecording(t), simTranscript(t)
	if len(rec) != len(sim) {
		t.Fatal("step count")
	}
	n := 0
	for i := range rec {
		name, _, _ := strings.Cut(rec[i], ":")
		if _, ok := pgwiretest.KnownDifferences[name]; ok {
			n++
			if rec[i] != "gss-request: G" || sim[i] != "gss-request: N" {
				t.Fatalf("known difference changed: real %q fake %q", rec[i], sim[i])
			}
		}
	}
	if n != len(pgwiretest.KnownDifferences) {
		t.Fatalf("%d known differences found in the recording, %d listed", n, len(pgwiretest.KnownDifferences))
	}
}

// TestSimTranscriptIsDeterministic proves running the simulated startup
// fidelity script twice yields identical transcripts.
func TestSimTranscriptIsDeterministic(t *testing.T) {
	requireSame(t, "two runs", simTranscript(t), simTranscript(t))
}

type realDialer struct {
	targets map[string]string
}

type realConn struct {
	c net.Conn
}

func (d realDialer) Dial(target string) (pgwiretest.Transport, error) {
	c, err := net.DialTimeout("tcp", d.targets[target], 5*time.Second)
	if err != nil {
		return nil, err
	}
	return &realConn{c: c}, nil
}

func (r *realConn) Write(b []byte) error {
	_ = r.c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err := r.c.Write(b)
	return err
}

func (r *realConn) Read(buf []byte) (int, error) {
	_ = r.c.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, err := r.c.Read(buf)
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return n, pgwiretest.ErrTimeout
	}
	return n, err
}

func (r *realConn) StartTLS() error {
	tc := tls.Client(r.c, &tls.Config{InsecureSkipVerify: true})
	_ = tc.SetDeadline(time.Now().Add(5 * time.Second))
	if err := tc.Handshake(); err != nil {
		return err
	}
	r.c = tc
	return nil
}

func (r *realConn) Close() { _ = r.c.Close() }

// TestTierRStartupFidelity runs StartupScript live against real Postgres 17
// containers and proves the fake and committed recording match.
func TestTierRStartupFidelity(t *testing.T) {
	if os.Getenv("MG_TIER_R") != "1" {
		t.Skip("tier R: run scripts/tier-r.sh")
	}
	d := realDialer{targets: map[string]string{pgwiretest.TargetSSLOff: os.Getenv("MG_PG_ADDR"), pgwiretest.TargetSSLOn: os.Getenv("MG_PG_SCRAM_ADDR")}}
	real, err := pgwiretest.Run(d, pgwiretest.StartupScript(pgwiretest.FidelityUser, os.Getenv("MG_PG_SCRAM_PASSWORD"), pgwiretest.FidelityDatabase))
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("MG_TIER_R_UPDATE") == "1" {
		body := "# Startup conversation of Postgres 17 (scripts/tier-r.sh, MG_TIER_R_UPDATE=1); replayed against the fake in CI.\n" + strings.Join(real, "\n") + "\n"
		if err := os.WriteFile(recording, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	requireSame(t, "real Postgres 17 against the fake", real, simTranscript(t))
	requireSame(t, "real Postgres 17 against the recording", readRecording(t), real)
	t.Logf("tier R: startup fidelity: %d steps identical", len(real))
}
