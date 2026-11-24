package main

import (
	"bufio"
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/build/constraint"
	"go/importer"
	"go/parser"
	"go/scanner"
	"go/token"
	"go/types"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"detlint/internal/report"
)

func init() {
	// Pure-Go file selection everywhere: cgo files are skipped, and the
	// source importer never shells out to cgo for net and friends.
	build.Default.CgoEnabled = false
}

var (
	stdOnce sync.Once
	stdImp  types.ImporterFrom
)

// stdImporter type-checks non-module imports (stdlib and module
// dependencies) from source. It is shared across runs as a cache.
func stdImporter() types.ImporterFrom {
	stdOnce.Do(func() {
		stdImp = importer.ForCompiler(token.NewFileSet(), "source", nil).(types.ImporterFrom)
	})
	return stdImp
}

// readModulePath returns the module path declared in root/go.mod.
func readModulePath(root string) (string, error) {
	f, err := os.Open(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if rest, ok := strings.CutPrefix(line, "module"); ok && (rest == "" || rest[0] == ' ' || rest[0] == '\t') {
			p := strings.Trim(strings.TrimSpace(rest), "\"")
			if p != "" {
				return p, nil
			}
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", errors.New("no module line in go.mod")
}

// loader resolves imports of the root module from source and everything else
// through the shared source importer.
type loader struct {
	fset    *token.FileSet
	root    string
	modPath string
	cache   map[string]*types.Package
	errs    map[string]error
	loading map[string]bool
	// override maps an import path to a package to use instead (the package
	// under test, with its in-package test files, for an external test).
	override map[string]*types.Package
}

func (l *loader) Import(path string) (*types.Package, error) {
	return l.ImportFrom(path, l.root, 0)
}

func (l *loader) ImportFrom(path, dir string, mode types.ImportMode) (*types.Package, error) {
	if p, ok := l.override[path]; ok {
		return p, nil
	}
	if rel, ok := l.localRel(path); ok {
		return l.loadLocal(path, rel)
	}
	return stdImporter().ImportFrom(path, dir, mode)
}

func (l *loader) localRel(path string) (string, bool) {
	if path == l.modPath {
		return ".", true
	}
	if rest, ok := strings.CutPrefix(path, l.modPath+"/"); ok {
		return rest, true
	}
	return "", false
}

func (l *loader) importPath(rel string) string {
	if rel == "." || rel == "" {
		return l.modPath
	}
	return l.modPath + "/" + filepath.ToSlash(rel)
}

func (l *loader) loadLocal(path, rel string) (*types.Package, error) {
	if p, ok := l.cache[path]; ok {
		return p, nil
	}
	if err, ok := l.errs[path]; ok {
		return nil, err
	}
	if l.loading[path] {
		return nil, fmt.Errorf("import cycle through %s", path)
	}
	l.loading[path] = true
	defer delete(l.loading, path)
	files, _, err := parseDir(l.fset, filepath.Join(l.root, rel), false)
	if err != nil {
		l.errs[path] = err
		return nil, err
	}
	if len(files) == 0 {
		err := fmt.Errorf("no Go files for %s", path)
		l.errs[path] = err
		return nil, err
	}
	var first error
	conf := types.Config{Importer: l, Error: func(e error) {
		if first == nil {
			first = e
		}
	}}
	pkg, _ := conf.Check(path, l.fset, files, nil)
	if first != nil {
		err := fmt.Errorf("type errors in %s: %v", path, first)
		l.errs[path] = err
		return nil, err
	}
	l.cache[path] = pkg
	return pkg, nil
}

// parseDir parses the .go files of dir that the default build selects. It is
// used for module packages imported by a checked package (which are
// type-checked as the compiler would see them); checked directories go
// through scanChecked instead. With tests it also returns _test.go files;
// files of an external test package come back separately.
func parseDir(fset *token.FileSet, dir string, tests bool) (pkg, xtest []*ast.File, err error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	var names []string
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasPrefix(n, ".") || strings.HasPrefix(n, "_") {
			continue
		}
		if strings.HasSuffix(n, "_test.go") && !tests {
			continue
		}
		ok, err := build.Default.MatchFile(dir, n)
		if err != nil {
			return nil, nil, err
		}
		if ok {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		f, err := parser.ParseFile(fset, filepath.Join(dir, n), nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return nil, nil, err
		}
		if strings.HasSuffix(n, "_test.go") && strings.HasSuffix(f.Name.Name, "_test") {
			xtest = append(xtest, f)
		} else {
			pkg = append(pkg, f)
		}
	}
	return pkg, xtest, nil
}

// nameOnly is a build context that knows no GOOS or GOARCH and reads every
// file as a bare package clause, so MatchFile rejects exactly the file names
// with a GOOS/GOARCH suffix.
var nameOnly = build.Context{
	GOOS:     "detlintnoos",
	GOARCH:   "detlintnoarch",
	Compiler: "gc",
	OpenFile: func(string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("package p\n")), nil
	},
}

// conditionalName reports whether the go tool selects the file by its
// GOOS/GOARCH name suffix.
func conditionalName(dir, name string) (bool, error) {
	ok, err := nameOnly.MatchFile(dir, name)
	return !ok, err
}

// scanChecked reads a checked package directory. Unlike parseDir it drops no
// file: every non-test .go file (and with tests every _test.go file) is
// returned for linting whatever its build constraints, and everything that
// could make the default build differ from what is linted is a [buildtag]
// finding. Files that do not parse are [parse] findings and are skipped. The
// error is for an unreadable directory only.
func scanChecked(fset *token.FileSet, root, dir string, tests bool) (pkg, xtest []*ast.File, fs []report.Finding, err error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, nil, err
	}
	relOf := func(n string) string {
		rel, err := filepath.Rel(root, filepath.Join(dir, n))
		if err != nil {
			return filepath.Join(dir, n)
		}
		return filepath.ToSlash(rel)
	}
	at := func(pos token.Pos, rule, msg string) {
		p := fset.Position(pos)
		rel, err := filepath.Rel(root, p.Filename)
		if err != nil {
			rel = p.Filename
		}
		fs = append(fs, report.Finding{Path: filepath.ToSlash(rel), Line: p.Line, Col: p.Column, Rule: rule, Msg: msg})
	}
	var names []string
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() {
			continue
		}
		isGo := strings.HasSuffix(n, ".go")
		if !isGo && !report.NonGoSource(n) {
			continue
		}
		if strings.HasPrefix(n, "_") || strings.HasPrefix(n, ".") {
			fs = append(fs, report.Finding{Path: relOf(n), Line: 1, Col: 1, Rule: "buildtag", Msg: "file is invisible to the go tool"})
			continue
		}
		if !isGo {
			fs = append(fs, report.Finding{Path: relOf(n), Line: 1, Col: 1, Rule: "buildtag", Msg: "non-Go source file (assembly, cgo, SWIG or object) in a checked package"})
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		path := filepath.Join(dir, n)
		isTest := strings.HasSuffix(n, "_test.go")
		mode := parser.ParseComments | parser.SkipObjectResolution
		if isTest && !tests {
			mode |= parser.ImportsOnly
		}
		f, perr := parser.ParseFile(fset, path, nil, mode)
		if perr != nil {
			line, col := 1, 1
			var el scanner.ErrorList
			if errors.As(perr, &el) && len(el) > 0 {
				line, col = el[0].Pos.Line, el[0].Pos.Column
			}
			fs = append(fs, report.Finding{Path: relOf(n), Line: line, Col: col, Rule: "parse", Msg: perr.Error()})
			continue
		}
		flagged := false
		for _, cg := range f.Comments {
			for _, c := range cg.List {
				if constraint.IsGoBuild(c.Text) || constraint.IsPlusBuild(c.Text) {
					at(c.Pos(), "buildtag", "build constraint: "+strings.TrimSpace(c.Text))
					flagged = true
				}
			}
		}
		cond, err := conditionalName(dir, n)
		if err != nil {
			return nil, nil, nil, err
		}
		if cond {
			fs = append(fs, report.Finding{Path: relOf(n), Line: 1, Col: 1, Rule: "buildtag", Msg: "file name has a GOOS/GOARCH suffix"})
			flagged = true
		}
		for _, im := range f.Imports {
			if im.Path.Value == `"C"` || im.Path.Value == "`C`" {
				at(im.Pos(), "buildtag", `import "C" (cgo)`)
				flagged = true
			}
		}
		if !flagged {
			if ok, err := build.Default.MatchFile(dir, n); err != nil || !ok {
				msg := "file is excluded from the default build"
				if err != nil {
					msg += ": " + err.Error()
				}
				fs = append(fs, report.Finding{Path: relOf(n), Line: 1, Col: 1, Rule: "buildtag", Msg: msg})
			}
		}
		if isTest && !tests {
			continue
		}
		if isTest && strings.HasSuffix(f.Name.Name, "_test") {
			xtest = append(xtest, f)
		} else {
			pkg = append(pkg, f)
		}
	}
	return pkg, xtest, fs, nil
}

// expand turns pkg-dir arguments into a sorted list of existing directories
// relative to root. "dir/..." walks recursively, skipping testdata, vendor
// and hidden or underscore directories. Missing directories are skipped.
func expand(root string, args []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	add := func(rel string) {
		rel = filepath.Clean(rel)
		if !seen[rel] {
			seen[rel] = true
			out = append(out, rel)
		}
	}
	for _, a := range args {
		a = filepath.ToSlash(a)
		recursive := false
		if a == "..." || strings.HasSuffix(a, "/...") {
			recursive = true
			a = strings.TrimSuffix(strings.TrimSuffix(a, "..."), "/")
			if a == "" {
				a = "."
			}
		}
		if filepath.IsAbs(a) {
			return nil, fmt.Errorf("package dir %q must be relative to -root", a)
		}
		abs := filepath.Join(root, a)
		st, err := os.Stat(abs)
		if err != nil || !st.IsDir() {
			continue
		}
		if !recursive {
			add(a)
			continue
		}
		err = filepath.WalkDir(abs, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				return nil
			}
			n := d.Name()
			if p != abs && (n == "testdata" || n == "vendor" || strings.HasPrefix(n, ".") || strings.HasPrefix(n, "_")) {
				return filepath.SkipDir
			}
			if p != abs {
				if _, err := os.Stat(filepath.Join(p, "go.mod")); err == nil {
					return filepath.SkipDir
				}
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			add(rel)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}
