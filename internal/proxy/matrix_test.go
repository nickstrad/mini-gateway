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
	if retried == 0 {
		t.Fatal("no row is retried by the proxy")
	}
	for _, m := range proxy.FaultMatrixRows() {
		if m.InTx && m.Retried() {
			t.Errorf("%s: an in-transaction row is retried", m.Fault)
		}
	}
}
