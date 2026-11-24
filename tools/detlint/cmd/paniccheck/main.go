// Command paniccheck requires every panic call to be allowlisted and forbids
// declaring the name panic.
//
//	paniccheck -root <dir> -allow <file> [dirs...]
//
// Allowlist lines: relative/file.go<TAB>EnclosingFunc<TAB>justification.
// Exit status: 0 clean, 1 findings, 2 usage or internal error.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"detlint/internal/report"
)

func main() {
	os.Exit(mainArgs(os.Args[1:]))
}

func mainArgs(args []string) int {
	fl := flag.NewFlagSet("paniccheck", flag.ContinueOnError)
	root := fl.String("root", "", "module root")
	allow := fl.String("allow", "", "allowlist file")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if *root == "" || *allow == "" {
		fmt.Fprintln(os.Stderr, "usage: paniccheck -root <dir> -allow <allowlist> [dirs...]")
		return 2
	}
	fs, err := run(*root, *allow, fl.Args())
	if err != nil {
		fmt.Fprintln(os.Stderr, "paniccheck:", err)
		return 2
	}
	report.Print(os.Stdout, fs)
	return report.ExitCode(fs, nil)
}

type site struct {
	path, fn string
	pos      token.Position
}

// run finds panic calls in non-test .go files under root/dirs (default
// "internal"; a missing directory has no panics), whatever their build
// constraints, and matches them against the allowlist. Every .go file there,
// test files included, is also searched for declarations of the name panic.
func run(root, allowPath string, dirs []string) ([]report.Finding, error) {
	if len(dirs) == 0 {
		dirs = []string{"internal"}
	}
	entries, out, err := report.ReadAllow(allowPath, 3)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, d := range dirs {
		start := filepath.Join(root, d)
		if _, err := os.Stat(start); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(start, func(p string, de os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if de.IsDir() {
				if p != start && report.SkipDir(de.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(p, ".go") {
				paths = append(paths, p)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(paths)
	fset := token.NewFileSet()
	var sites []site
	for _, p := range paths {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil, err
		}
		rel = filepath.ToSlash(rel)
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			out = append(out, report.Finding{Path: rel, Line: 1, Col: 1, Rule: "parse", Msg: err.Error()})
			continue
		}
		for _, id := range panicDecls(f) {
			pos := fset.Position(id.Pos())
			out = append(out, report.Finding{Path: rel, Line: pos.Line, Col: pos.Column, Rule: "panic-shadow",
				Msg: "declaration of the name panic (it would hide the builtin from this check)"})
		}
		if !strings.HasSuffix(p, "_test.go") {
			sites = append(sites, panicCalls(fset, f, rel)...)
		}
	}
	for _, s := range sites {
		ok := false
		for _, e := range entries {
			if e.Fields[0] == s.path && e.Fields[1] == s.fn {
				e.Used = true
				ok = true
			}
		}
		if !ok {
			out = append(out, report.Finding{Path: s.path, Line: s.pos.Line, Col: s.pos.Column, Rule: "panic",
				Msg: fmt.Sprintf("panic in %s is not in the allowlist (add: %s\\t%s\\t<justification>)", s.fn, s.path, s.fn)})
		}
	}
	out = append(out, report.Stale(allowPath, entries)...)
	return out, nil
}

// panicCalls returns every call whose callee is the bare identifier panic.
func panicCalls(fset *token.FileSet, f *ast.File, rel string) []site {
	var sites []site
	for _, d := range f.Decls {
		name := "-"
		if fd, ok := d.(*ast.FuncDecl); ok {
			name = report.FuncName(fd)
		}
		ast.Inspect(d, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := ast.Unparen(call.Fun).(*ast.Ident); ok && id.Name == "panic" {
				sites = append(sites, site{rel, name, fset.Position(call.Pos())})
			}
			return true
		})
	}
	return sites
}

// panicDecls returns every identifier named panic in a declaring position:
// func and method names, import names, const/var/type specs, fields
// (parameters, results, receivers, struct fields, type parameters), :=
// assignments, range and type-switch variables, and labels.
func panicDecls(f *ast.File) []*ast.Ident {
	var out []*ast.Ident
	add := func(ids ...*ast.Ident) {
		for _, id := range ids {
			if id != nil && id.Name == "panic" {
				out = append(out, id)
			}
		}
	}
	addExprs := func(es ...ast.Expr) {
		for _, e := range es {
			if id, ok := e.(*ast.Ident); ok {
				add(id)
			}
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncDecl:
			add(x.Name)
		case *ast.ImportSpec:
			add(x.Name)
		case *ast.ValueSpec:
			add(x.Names...)
		case *ast.TypeSpec:
			add(x.Name)
		case *ast.Field:
			add(x.Names...)
		case *ast.AssignStmt:
			if x.Tok == token.DEFINE {
				addExprs(x.Lhs...)
			}
		case *ast.RangeStmt:
			if x.Tok == token.DEFINE {
				addExprs(x.Key, x.Value)
			}
		case *ast.LabeledStmt:
			add(x.Label)
		}
		return true
	})
	return out
}
