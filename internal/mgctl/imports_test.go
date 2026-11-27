package mgctl_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestTheLogicPackagesImportNoFlagOrHTTPFramework proves internal/mgctl and
// internal/loadgen stay free of CLI flag and HTTP framework dependencies.
func TestTheLogicPackagesImportNoFlagOrHTTPFramework(t *testing.T) {
	banned := func(p string) bool {
		return p == "flag" || p == "net/http" || strings.HasPrefix(p, "net/http/") || strings.HasPrefix(p, "github.com/spf13/") ||
			strings.HasPrefix(p, "github.com/urfave/")
	}
	for _, dir := range []string{".", filepath.Join("..", "loadgen")} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			af, err := parser.ParseFile(token.NewFileSet(), f, src, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			n++
			for _, im := range af.Imports {
				p, _ := strconv.Unquote(im.Path.Value)
				if banned(p) {
					t.Errorf("%s imports %s", f, p)
				}
			}
		}
		if n == 0 {
			t.Fatalf("no Go file read in %s", dir)
		}
	}
}

// TestNoConfigOrCommandSetsTheLoadToolsPlant proves the test-only closed-loop
// plant switch is unreachable from live runners and CLI commands.
func TestNoConfigOrCommandSetsTheLoadToolsPlant(t *testing.T) {
	for _, dir := range []string{filepath.Join("..", "loadgen", "live"), filepath.Join("..", "..", "cmd", "loadgen"), filepath.Join("..", "..", "cmd", "mgctl")} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil || len(files) == 0 {
			t.Fatalf("%s: %v", dir, err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), "Plant") {
				t.Errorf("%s mentions the test-only plant switch", f)
			}
		}
	}
}
