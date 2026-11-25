package adv_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func productAST(t *testing.T, visit func(string, *ast.File)) {
	t.Helper()
	for _, dir := range []string{"internal/sim", "internal/obs"} {
		err := filepath.WalkDir(filepath.Join("../../..", dir), func(p string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if d.IsDir() {
				if d.Name() == "simtest" || d.Name() == "obstest" || d.Name() == "csvflush" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			f, e := parser.ParseFile(token.NewFileSet(), p, nil, 0)
			if e != nil {
				return e
			}
			visit(p, f)
			return nil
		})
		must(t, err)
	}
}
func TestAdv_10_AliasedEntropyAndClock(t *testing.T) {
	mark(t)
	productAST(t, func(p string, f *ast.File) {
		imports := map[string]string{}
		for _, im := range f.Imports {
			path, _ := strconv.Unquote(im.Path.Value)
			name := filepath.Base(path)
			if im.Name != nil {
				name = im.Name.Name
			}
			imports[name] = path
			if path == "crypto/rand" || path == "os" || path == "net" || path == "runtime" {
				t.Errorf("process/entropy import %s in %s", path, p)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			path := imports[id.Name]
			if path == "time" {
				switch sel.Sel.Name {
				case "Now", "Since", "Until", "Sleep", "After", "AfterFunc", "NewTimer", "NewTicker", "Tick":
					t.Errorf("wall clock reference %s.%s in %s", id.Name, sel.Sel.Name, p)
				}
			}
			if path == "math/rand" || path == "math/rand/v2" {
				switch sel.Sel.Name {
				case "New", "NewPCG", "NewSource", "Rand":
				default:
					t.Errorf("global random reference %s.%s in %s", id.Name, sel.Sel.Name, p)
				}
			}
			return true
		})
	})
}
func TestAdv_10_ConcurrencyAndFloatingSyntax(t *testing.T) {
	mark(t)
	productAST(t, func(p string, f *ast.File) {
		ast.Inspect(f, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.GoStmt, *ast.ChanType, *ast.SendStmt, *ast.SelectStmt:
				t.Errorf("concurrency syntax %T in %s", v, p)
			case *ast.BasicLit:
				if v.Kind == token.FLOAT || v.Kind == token.IMAG {
					t.Errorf("floating literal %s in %s", v.Value, p)
				}
			case *ast.Ident:
				if v.Name == "float32" || v.Name == "float64" || v.Name == "complex64" || v.Name == "complex128" {
					t.Errorf("floating type %s in %s", v.Name, p)
				}
			}
			return true
		})
	})
}
func TestAdv_10_DeterminismLintNoAllowances(t *testing.T) {
	mark(t)
	root, e := filepath.Abs("../../..")
	must(t, e)
	c := exec.Command("/usr/local/go/bin/go", "run", ".", "-root", root, "internal/sim", "internal/sim/simnet", "internal/sim/simtoy", "internal/obs")
	c.Dir = filepath.Join(root, "tools/detlint")
	b, e := c.CombinedOutput()
	if e != nil {
		t.Fatalf("determinism lint without allowances: %v\n%s", e, b)
	}
	t.Log(string(b))
}
