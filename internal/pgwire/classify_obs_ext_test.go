package pgwire_test

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
)

func corpusTexts(t *testing.T) []string {
	t.Helper()
	f, err := os.Open("testdata/classifier-corpus.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		_, text, ok := strings.Cut(line, "\t")
		if !ok {
			t.Fatalf("corpus line without a tab: %q", line)
		}
		if strings.HasPrefix(text, `"`) {
			if text, err = strconv.Unquote(text); err != nil {
				t.Fatal(err)
			}
		}
		out = append(out, text)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestClassifierSafeReadCorpusLeavesTheFakeUnchanged(t *testing.T) {
	s := sim.New(sim.Config{Seed: 1, Scenario: t.Name()})
	n := simnet.New(s)
	srv, err := fakepg.New(n, fakepg.Config{Rows: []fakepg.Row{{Key: "a", Value: "1"}, {Key: "b", Value: "2"}}})
	if err != nil {
		t.Fatal(err)
	}
	p := workload.NewProbe(n, srv.Addr(), simnet.DefaultOptions())
	p.Startup("app", "app")
	if err := s.RunFor(time.Millisecond); err != nil {
		t.Fatal(err)
	}
	ran := 0
	for _, q := range corpusTexts(t) {
		if !pgwire.ClassifyQuery([]byte(q), false).SafeRead {
			continue
		}
		p.Query(q)
		ran++
		if err := s.RunFor(time.Second); err != nil {
			t.Fatal(err)
		}
	}
	p.Terminate()
	p.Close()
	if err := s.RunUntilQuiet(); err != nil {
		t.Fatal(err)
	}
	snap := srv.Snapshot()
	if ran < 50 || snap.StatementsStarted < int64(ran) {
		t.Fatalf("ran %d safe-read statements, the fake started %d", ran, snap.StatementsStarted)
	}
	if snap.WritesApplied != 0 || len(srv.WriteLog()) != 0 {
		t.Fatalf("safe-read statements changed the fake: writes %d, log %d", snap.WritesApplied, len(srv.WriteLog()))
	}
	rows := srv.Rows()
	if len(rows) != 2 || rows[0].Value != "1" || rows[1].Value != "2" {
		t.Fatalf("table changed: %+v", rows)
	}
}
