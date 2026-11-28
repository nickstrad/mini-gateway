package proxy_test

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/work"
)

var (
	matrixOut    = flag.String("fault-matrix-out", "", "write docs/fault-matrix.md into this folder")
	matrixUpdate = flag.Bool("update-fault-matrix", false, "rewrite docs/fault-matrix.md")
)

const matrixDoc = "fault-matrix.md"

// TestFaultMatrixDocumentIsGeneratedFromTheCode proves docs/fault-matrix.md is
// byte-identical to proxy.FaultMatrixMarkdown (or updates it when flagged).
func TestFaultMatrixDocumentIsGeneratedFromTheCode(t *testing.T) {
	want := proxy.FaultMatrixMarkdown()
	root := filepath.Join("..", "..", "docs")
	switch {
	case *matrixOut != "":
		if err := os.WriteFile(filepath.Join(*matrixOut, matrixDoc), want, 0o644); err != nil {
			t.Fatal(err)
		}
	case *matrixUpdate:
		if err := os.WriteFile(filepath.Join(root, matrixDoc), want, 0o644); err != nil {
			t.Fatal(err)
		}
	default:
		got, err := os.ReadFile(filepath.Join(root, matrixDoc))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("docs/%s is stale; run scripts/gen-docs.sh (%v)", matrixDoc, err)
		}
	}
}

// TestEveryFaultMatrixRowStatesRetryAndATestThatExists proves every row in
// FaultMatrixRows names real tests or scenarios and only one row is retried.
func TestEveryFaultMatrixRowStatesRetryAndATestThatExists(t *testing.T) {
	var src bytes.Buffer
	for _, dir := range []string{".", "../pool", "../pgwire", "../shell", "../admission", "../../test/e2e"} {
		files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			src.Write(b)
		}
	}
	names, _ := filepath.Glob(filepath.Join("..", "..", "test", "analysis", "*"))
	scenarios := map[string]bool{}
	for _, n := range names {
		scenarios[filepath.Base(n)] = true
	}
	retried := 0
	for _, m := range proxy.FaultMatrixRows() {
		for _, name := range m.Tests {
			if strings.HasPrefix(name, "Test") {
				if !bytes.Contains(src.Bytes(), []byte("func "+name+"(")) {
					t.Errorf("%s: test %s does not exist", m.Fault, name)
				}
			} else if !scenarios[name] {
				t.Errorf("%s: scenario %s has no analysis folder", m.Fault, name)
			}
		}
		if len(m.Tests) == 0 {
			t.Errorf("%s: no test", m.Fault)
		}
		if !m.Retried() {
			continue
		}
		retried++
		if m.InTx || m.Reached != work.No || m.Row != pgwire.ErrRowLostAfterRead {
			t.Errorf("%s: retried by the proxy, but reached %v in a transaction %v row %s", m.Fault, m.Reached, m.InTx, m.Row.Row().ID)
		}
	}
	if retried != 1 {
		t.Fatalf("%d rows retried by the proxy, want exactly the zero-byte refusal of a lent connection", retried)
	}
	for _, m := range proxy.FaultMatrixRows() {
		if m.InTx && m.Retried() {
			t.Errorf("%s: an in-transaction row is retried", m.Fault)
		}
	}
}

// TestOnlyTheZeroByteRefusalOfALentConnectionIsRetried proves NotRetried and
// RetriedCell agree with Retried and isolate the single retryable matrix row.
func TestOnlyTheZeroByteRefusalOfALentConnectionIsRetried(t *testing.T) {
	var retried []string
	for _, m := range proxy.FaultMatrixRows() {
		if m.Retried() {
			retried = append(retried, m.Fault)
		}
		if m.NotRetried != "" && m.Retried() {
			t.Errorf("%s: names a reason it is not retried and says it is", m.Fault)
		}
		if strings.Contains(m.Client, "no retry") && m.NotRetried == "" {
			t.Errorf("%s: the client cell says no retry but the row names no reason", m.Fault)
		}
		if m.Retried() != strings.HasPrefix(m.RetriedCell(), "yes") {
			t.Errorf("%s: Retried() %v, cell %q", m.Fault, m.Retried(), m.RetriedCell())
		}
	}
	want := "First write of the request refused with zero bytes accepted (a lent connection found dead)"
	if len(retried) != 1 || retried[0] != want {
		t.Fatalf("retried rows %q, want only %q", retried, want)
	}
}
