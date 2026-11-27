package simrun_test

import (
	"errors"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/nickstrad/mini-gateway/"

var adapters = []string{"internal/obs/csvflush", "internal/obs/jsonstats", "internal/obs/logline", "internal/obs/promtext"}

var rawExportSide = []string{
	"internal/sim", "internal/testclient", "internal/randdist",
	"internal/obs/simsrc", "internal/obs/wiresrc", "internal/obs/clssrc", "internal/obs/enginesrc", "internal/obs/obstest",
	"internal/pool", "internal/admission", "internal/coalesce", "internal/retry", "internal/work", "internal/pgwire", "internal/proxy",
}

var adapterForbidden = []string{
	"internal/sim", "internal/testclient",
	"internal/obs/simsrc", "internal/obs/wiresrc", "internal/obs/clssrc", "internal/obs/enginesrc",
	"internal/pool", "internal/admission", "internal/coalesce", "internal/retry", "internal/work", "internal/pgwire", "internal/proxy",
}

func importsUnder(t *testing.T, dir string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			out[path] = append(out[path], strings.TrimPrefix(p, module))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func hasPrefixAny(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if s == p || strings.HasPrefix(s, p+"/") {
			return true
		}
	}
	return false
}

// TestOneRawExportImportRule parses every Go file in the collector, simulator,
// mapping, and adapter trees to prove the one-direction dependency boundary holds.
func TestOneRawExportImportRule(t *testing.T) {
	root := repoRoot(t)
	forbidden := append(append([]string{}, adapters...), "internal/obs/simrun", "internal/obs/catalog")
	for _, dir := range rawExportSide {
		if _, err := os.Stat(filepath.Join(root, dir)); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		for file, imps := range importsUnder(t, filepath.Join(root, dir)) {
			for _, imp := range imps {
				if hasPrefixAny(imp, forbidden) {
					t.Errorf("%s imports %s", file, imp)
				}
			}
		}
	}
	for file, imps := range importsUnder(t, filepath.Join(root, "internal/obs")) {
		if filepath.Dir(file) != filepath.Join(root, "internal/obs") {
			continue
		}
		for _, imp := range imps {
			if hasPrefixAny(imp, forbidden) {
				t.Errorf("the collector package imports %s (%s)", imp, file)
			}
		}
	}
	for _, a := range adapters {
		for file, imps := range importsUnder(t, filepath.Join(root, a)) {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			for _, imp := range imps {
				if hasPrefixAny(imp, adapterForbidden) {
					t.Errorf("adapter file %s reads a component directly: %s", file, imp)
				}
			}
		}
	}
}
