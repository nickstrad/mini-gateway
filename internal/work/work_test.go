package work

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkImportsOnlyTheStandardLibrary(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no Go files: %v", err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		af, err := parser.ParseFile(fset, f, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, im := range af.Imports {
			path := strings.Trim(im.Path.Value, `"`)
			first := strings.SplitN(path, "/", 2)[0]
			if strings.Contains(first, ".") {
				t.Errorf("%s imports %s: internal/work may import only the standard library", f, path)
			}
			if strings.HasPrefix(path, "github.com/nickstrad/mini-gateway") {
				t.Errorf("%s imports %s: internal/work may not import other packages of this module", f, path)
			}
		}
		if !strings.HasSuffix(f, "_test.go") && (strings.Contains(string(src), "pgproto3") || strings.Contains(string(src), "pgwire")) {
			t.Errorf("%s names a Postgres package", f)
		}
	}
}

func TestNamesCoverEveryValue(t *testing.T) {
	seen := map[string]bool{}
	for c := ClassNone; c < NumClasses; c++ {
		if c.String() == "invalid" || seen["c"+c.String()] {
			t.Errorf("class %d has no unique name", c)
		}
		seen["c"+c.String()] = true
	}
	for r := RejectNone; r < NumRejectReasons; r++ {
		if r.String() == "invalid" || seen["r"+r.String()] {
			t.Errorf("reason %d has no unique name", r)
		}
		seen["r"+r.String()] = true
	}
	for e := ErrNone; e < NumErrorClasses; e++ {
		if e.String() == "invalid" {
			t.Errorf("error class %d has no name", e)
		}
	}
	for r := RefusalNone; r < NumRefusals; r++ {
		if r.String() == "invalid" {
			t.Errorf("refusal %d has no name", r)
		}
	}
	if NumClasses.String() != "invalid" || Tri(9).String() != "invalid" || Release(9).String() != "invalid" {
		t.Error("out-of-range values must print invalid")
	}
}

func TestClassForwards(t *testing.T) {
	if !ClassStartLease.Forwards() || !ClassContinueLease.Forwards() || ClassReject.Forwards() || ClassAnswerLocally.Forwards() {
		t.Error("Forwards is wrong")
	}
}

func TestShareKeyEqual(t *testing.T) {
	a := ShareKey{Tenant: "t", Text: []byte("SELECT 1")}
	if !a.Equal(ShareKey{Tenant: "t", Text: []byte("SELECT 1")}) || a.Equal(ShareKey{Text: []byte("SELECT 1")}) || a.Equal(ShareKey{Tenant: "t", Text: []byte("SELECT  1")}) {
		t.Error("ShareKey.Equal is wrong")
	}
}
