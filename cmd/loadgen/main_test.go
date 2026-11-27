package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/loadgen"
	"github.com/nickstrad/mini-gateway/internal/loadgen/loadgentest"
)

func background() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}

func runCmd(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, strings.NewReader("pw\n"), &out, &errb, background)
	return code, out.String(), errb.String()
}

func TestTheSeedIsPrintedFirstAndAServedRunExitsZero(t *testing.T) {
	f := loadgentest.Start(t, loadgentest.Options{})
	code, out, errs := runCmd(t, "-addr", f.Addr(), "-rate", "50", "-duration", "2500ms", "-conns", "2", "-tenants", "2",
		"-password", "-", "-late-after", "250ms", "-seed", "123")
	if code != exitOK {
		t.Fatalf("exit %d\n%s\n%s", code, out, errs)
	}
	first := strings.SplitN(out, "\n", 2)[0]
	if !strings.HasPrefix(first, "loadgen: seed 123 ") || !strings.Contains(first, "-seed 123") {
		t.Fatalf("first line %q", first)
	}
	if strings.Contains(out, "SHORTFALL") || !strings.Contains(out, "intended 125 of 125 planned; sent 125; could not send 0; late 0") {
		t.Fatalf("summary:\n%s", out)
	}
	if !regexp.MustCompile(`(?m)^t=\d+\.\ds sent \d+/\d+ completed`).MatchString(out) {
		t.Fatalf("no status line:\n%s", out)
	}
}

func TestARandomSeedIsPrintedSoTheRunCanBeRepeated(t *testing.T) {
	_, out, _ := runCmd(t, "-plan", "3")
	m := regexp.MustCompile(`plan: seed (-?\d+) `).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no seed in %q", out)
	}
	_, again, _ := runCmd(t, "-plan", "3", "-seed", m[1])
	if again != out {
		t.Fatalf("repeating the printed seed gave another plan:\n%s\n%s", out, again)
	}
}

func TestAThrottledServerPrintsShortfallAndExitsThree(t *testing.T) {
	f := loadgentest.Start(t, loadgentest.Options{Delay: 50 * time.Millisecond})
	code, out, _ := runCmd(t, "-addr", f.Addr(), "-rate", "400", "-duration", "1s", "-conns", "1", "-tenants", "1",
		"-max-outstanding", "2", "-drain", "1s", "-seed", "5", "-quiet")
	if code != exitShortfall {
		t.Fatalf("exit %d\n%s", code, out)
	}
	m := regexp.MustCompile(`SHORTFALL: achieved [0-9.]+/s of target 400/s: (\d+) late, (\d+) could not send`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no SHORTFALL line:\n%s", out)
	}
	late, _ := strconv.Atoi(m[1])
	cns, _ := strconv.Atoi(m[2])
	if late+cns == 0 {
		t.Fatalf("shortfall with nothing counted:\n%s", out)
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{{"-rate", "0"}, {"-profile", "x"}, {"-conns", "1", "-tenants", "2"}, {"extra"},
		{"-tls", "bogus"}, {"-drain", "-1s"}, {"-user-prefix", ""}} {
		if code, _, _ := runCmd(t, args...); code != exitUsage {
			t.Errorf("%v: exit %d, want %d", args, code, exitUsage)
		}
	}
	if code, _, _ := runCmd(t, "-h"); code != exitOK {
		t.Error("-h")
	}
}

func TestNoServerIsARuntimeFailureWithConservedNumbers(t *testing.T) {
	code, out, errs := runCmd(t, "-addr", "127.0.0.1:1", "-rate", "10", "-duration", "1s", "-conns", "1", "-tenants", "1", "-seed", "1")
	if code != exitRuntime || !strings.Contains(errs, "no connection") || !strings.Contains(out, "loadgen summary") {
		t.Fatalf("exit %d\n%s\n%s", code, out, errs)
	}
}

func TestTheSameSeedGivesTheSamePlanAcrossProcesses(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := filepath.Join(t.TempDir(), "loadgen")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	args := []string{"-plan", "500", "-seed", "777", "-profile", "errors", "-tenants", "3", "-rate", "250"}
	var outs [2][]byte
	for i := range outs {
		b, err := exec.Command(bin, args...).Output()
		if err != nil {
			t.Fatal(err)
		}
		outs[i] = b
	}
	if !bytes.Equal(outs[0], outs[1]) {
		t.Fatal("two processes printed different plans for one seed")
	}
	p := loadgen.Plan{Seed: 777, Rate: 250, DurationNs: int64(10 * time.Second), Tenants: 3, Keys: loadgen.DefaultKeys, Profile: loadgen.ProfileErrors}
	want := fmt.Sprintf("digest %016x", p.Digest(500))
	if !strings.HasSuffix(strings.TrimSpace(string(outs[0])), want) {
		t.Fatalf("the process digest differs from the in-process one (%s):\n%s", want, outs[0][len(outs[0])-40:])
	}
	f := loadgentest.Start(t, loadgentest.Options{})
	var seen [2]map[string][]string
	for i := range seen {
		g := loadgentest.Start(t, loadgentest.Options{})
		if i == 0 {
			g = f
		}
		cmd := exec.Command(bin, "-addr", g.Addr(), "-seed", "777", "-profile", "errors", "-tenants", "3", "-conns", "3",
			"-rate", "250", "-duration", "1s", "-late-after", "500ms", "-quiet")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("run %d: %v\n%s", i, err, out)
		}
		seen[i] = map[string][]string{}
		for ten := 0; ten < 3; ten++ {
			u := loadgen.TenantUser("t", ten)
			seen[i][u] = g.Statements(u)
		}
	}
	for u := range seen[0] {
		if strings.Join(seen[0][u], "\n") != strings.Join(seen[1][u], "\n") || len(seen[0][u]) == 0 {
			t.Fatalf("tenant %s: the two processes sent different statements", u)
		}
	}
}
