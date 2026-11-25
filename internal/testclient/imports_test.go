package testclient_test

import (
	"bufio"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const pgproto3Path = "github.com/jackc/pgx/v5/pgproto3"

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	for {
		if b, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil && strings.Contains(string(b), "module github.com/nickstrad/mini-gateway\n") {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repository root not found")
		}
		dir = parent
	}
}

func pgproto3Allowed(t *testing.T, root string) []string {
	t.Helper()
	f, err := os.Open(filepath.Join(root, "tools", "lint-rules", "imports.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		left, right, ok := strings.Cut(sc.Text(), ":")
		if !ok || strings.TrimSpace(left) != "only "+pgproto3Path {
			continue
		}
		var dirs []string
		for _, p := range strings.Fields(right) {
			dirs = append(dirs, strings.TrimSuffix(p, "/...")+"/")
		}
		return dirs
	}
	t.Fatal("imports.txt has no `only` rule for pgproto3")
	return nil
}

func pgproto3Importers(t *testing.T, root string) []string {
	t.Helper()
	var found []string
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == ".git" || rel == "tools" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == pgproto3Path || strings.HasPrefix(p, pgproto3Path+"/") {
				found = append(found, rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// Proves narrowed Claim 11: every Go file outside tools/ (including tests,
// nested modules, and testdata) imports pgproto3 only under the directories
// permitted by tools/lint-rules/imports.txt.
func TestPgproto3IsImportedOnlyWhereTheRulesAllowInEveryGoFileOutsideTools(t *testing.T) {
	root := repoRoot(t)
	allowed := pgproto3Allowed(t, root)
	files := pgproto3Importers(t, root)
	if len(files) == 0 {
		t.Fatal("no file imports pgproto3: the scan is broken")
	}
	for _, f := range files {
		ok := false
		for _, dir := range allowed {
			ok = ok || strings.HasPrefix(f, dir)
		}
		if !ok {
			t.Errorf("%s imports pgproto3; allowed only under %v", f, allowed)
		}
	}
	t.Logf("pgproto3 importers: %v", files)
}
