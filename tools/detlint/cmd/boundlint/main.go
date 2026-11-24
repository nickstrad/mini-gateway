// Command boundlint is the bounded-channel lint for the goroutine shell: every channel
// has a named capacity, and nothing grows without a length check.
//
//	boundlint [-allow file] -root <module root> <pkg-dir>...
//
// The rules and their limits are in README-tools.txt (section 7).
// Exit status: 0 clean, 1 findings, 2 usage or internal error.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/build"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/scanner"
	"go/token"
	"go/types"
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
	fl := flag.NewFlagSet("boundlint", flag.ContinueOnError)
	root := fl.String("root", "", "module root directory (contains go.mod)")
	allow := fl.String("allow", "", "allowlist file: path<TAB>Func<TAB>rule<TAB>justification")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if *root == "" || fl.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: boundlint [-allow file] -root <module root> <pkg-dir>...")
		return 2
	}
	res, err := run(*root, *allow, fl.Args())
	if err != nil {
		fmt.Fprintln(os.Stderr, "boundlint:", err)
		return 2
	}
	report.Print(os.Stdout, res.findings)
	fmt.Fprintf(os.Stderr, "boundlint: checked %d files in %d packages\n", res.files, res.pkgs)
	return report.ExitCode(res.findings, nil)
}

type result struct {
	findings []report.Finding
	files    int
	pkgs     int
}

// allowable lists the rules an allowlist line may name. A channel capacity that is not a
// named constant or a config field has no exemption: name the number instead.
var allowable = map[string]bool{"unbuffered": true, "unbounded-growth": true}

func init() {
	build.Default.CgoEnabled = false
}

func readModulePath(root string) (string, error) {
	f, err := os.Open(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		words := strings.Fields(sc.Text())
		if len(words) == 2 && words[0] == "module" {
			return strings.Trim(words[1], "\""), nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", errors.New("no module line in go.mod")
}

// expand turns the package arguments into directories relative to root. "dir/..."
// recurses; missing directories are skipped.
func expand(root string, args []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	add := func(rel string) {
		rel = filepath.ToSlash(filepath.Clean(rel))
		if !seen[rel] {
			seen[rel] = true
			out = append(out, rel)
		}
	}
	for _, a := range args {
		base, tree := strings.CutSuffix(a, "/...")
		if a == "..." {
			base, tree = ".", true
		}
		start := filepath.Join(root, base)
		if st, err := os.Stat(start); err != nil || !st.IsDir() {
			continue
		}
		if !tree {
			add(base)
			continue
		}
		err := filepath.WalkDir(start, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				return nil
			}
			name := d.Name()
			if p != start && (report.SkipDir(name) || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			if p != start {
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

type loader struct {
	fset    *token.FileSet
	root    string
	module  string
	std     types.ImporterFrom
	cache   map[string]*types.Package
	loading map[string]bool
}

func (l *loader) Import(path string) (*types.Package, error) {
	return l.ImportFrom(path, l.root, 0)
}

func (l *loader) ImportFrom(path, dir string, mode types.ImportMode) (*types.Package, error) {
	rel := ""
	switch {
	case path == l.module:
		rel = "."
	case strings.HasPrefix(path, l.module+"/"):
		rel = strings.TrimPrefix(path, l.module+"/")
	default:
		return l.std.ImportFrom(path, dir, mode)
	}
	if p, ok := l.cache[path]; ok {
		return p, nil
	}
	if l.loading[path] {
		return nil, fmt.Errorf("import cycle through %s", path)
	}
	l.loading[path] = true
	defer delete(l.loading, path)
	files, _, err := l.parse(rel)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no Go files for %s", path)
	}
	conf := types.Config{Importer: l, Error: func(error) {}}
	pkg, _ := conf.Check(path, l.fset, files, nil)
	l.cache[path] = pkg
	return pkg, nil
}

// parse reads the non-test Go files of one directory that the default build selects, and
// names the non-test Go files it does not select.
func (l *loader) parse(rel string) (files []*ast.File, excluded []string, err error) {
	dir := filepath.Join(l.root, filepath.FromSlash(rel))
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		ok, merr := build.Default.MatchFile(dir, name)
		if merr != nil || !ok {
			excluded = append(excluded, name)
			continue
		}
		f, perr := parser.ParseFile(l.fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if perr != nil {
			return nil, nil, perr
		}
		files = append(files, f)
	}
	return files, excluded, nil
}

func run(root, allowPath string, args []string) (*result, error) {
	module, err := readModulePath(root)
	if err != nil {
		return nil, err
	}
	res := &result{}
	var entries []*report.Entry
	if allowPath != "" {
		var bad []report.Finding
		entries, bad, err = report.ReadAllow(allowPath, 4)
		if err != nil {
			return nil, err
		}
		res.findings = append(res.findings, bad...)
		for _, e := range entries {
			if !allowable[e.Fields[2]] {
				res.findings = append(res.findings, report.Finding{Path: allowPath, Line: e.Line, Col: 1, Rule: "allow-format", Msg: "rule " + e.Fields[2] + " cannot be allowlisted (only unbuffered and unbounded-growth can)"})
				e.Used = true
			}
		}
	}
	dirs, err := expand(root, args)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	l := &loader{fset: fset, root: root, module: module, cache: map[string]*types.Package{}, loading: map[string]bool{},
		std: importer.ForCompiler(fset, "source", nil).(types.ImporterFrom)}
	seen := map[string]bool{}
	for _, rel := range dirs {
		add := func(pos token.Pos, name, ruleID, msg string) {
			p := fset.Position(pos)
			path := rel + "/" + name
			if pos.IsValid() {
				if r, err := filepath.Rel(root, p.Filename); err == nil {
					path = filepath.ToSlash(r)
				}
			} else {
				p.Line, p.Column = 1, 1
			}
			key := fmt.Sprintf("%s:%d:%d:%s", path, p.Line, p.Column, ruleID)
			if seen[key] {
				return
			}
			seen[key] = true
			res.findings = append(res.findings, report.Finding{Path: path, Line: p.Line, Col: p.Column, Rule: ruleID, Msg: msg})
		}
		files, excluded, err := l.parse(rel)
		if err != nil {
			var list scanner.ErrorList
			if errors.As(err, &list) {
				res.findings = append(res.findings, report.Finding{Path: rel, Line: 1, Col: 1, Rule: "parse", Msg: err.Error()})
				continue
			}
			return nil, err
		}
		for _, name := range excluded {
			add(token.NoPos, name, "buildtag", "the default build leaves this file out, so its channels cannot be checked")
		}
		if len(files) == 0 {
			continue
		}
		res.pkgs++
		res.files += len(files)
		info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{},
			Defs: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
		path := module
		if rel != "." {
			path = module + "/" + rel
		}
		conf := types.Config{Importer: l, Error: func(e error) {
			var te types.Error
			if errors.As(e, &te) {
				add(te.Pos, "", "typecheck", te.Msg)
			}
		}}
		tpkg, _ := conf.Check(path, fset, files, info)
		decls := map[*types.Func]*ast.FuncDecl{}
		for _, f := range files {
			for _, d := range f.Decls {
				if fd, ok := d.(*ast.FuncDecl); ok && fd.Body != nil {
					if fn, ok := info.Defs[fd.Name].(*types.Func); ok {
						decls[fn] = fd
					}
				}
			}
		}
		where := func(pos token.Pos) string {
			p := fset.Position(pos)
			return fmt.Sprintf("%s:%d", filepath.Base(p.Filename), p.Line)
		}
		for _, f := range files {
			c := &checker{info: info, file: f, pkg: tpkg, decls: decls, where: where}
			c.report = func(pos token.Pos, ruleID, msg string) {
				if allowable[ruleID] {
					p := fset.Position(pos)
					r, _ := filepath.Rel(root, p.Filename)
					fn := report.Enclosing(f, pos)
					hit := false
					for _, e := range entries {
						if e.Fields[0] == filepath.ToSlash(r) && e.Fields[1] == fn && e.Fields[2] == ruleID {
							e.Used = true
							hit = true
						}
					}
					if hit {
						return
					}
				}
				add(pos, "", ruleID, msg)
			}
			c.checkFile()
		}
	}
	if allowPath != "" {
		res.findings = append(res.findings, report.Stale(allowPath, entries)...)
	}
	return res, nil
}

type checker struct {
	info   *types.Info
	file   *ast.File
	pkg    *types.Package
	decls  map[*types.Func]*ast.FuncDecl
	where  func(token.Pos) string
	report func(pos token.Pos, ruleID, msg string)
}

func unparen(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			return e
		}
		e = p.X
	}
}

func (c *checker) builtin(fun ast.Expr, name string) bool {
	id, ok := unparen(fun).(*ast.Ident)
	if !ok {
		return false
	}
	b, ok := c.info.Uses[id].(*types.Builtin)
	return ok && b.Name() == name
}

func (c *checker) checkFile() {
	for _, d := range c.file.Decls {
		ast.Inspect(d, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				c.checkMake(x)
			case *ast.ForStmt:
				if x.Cond == nil {
					c.checkLoop(d, x.Body)
				}
			case *ast.RangeStmt:
				if t := c.info.TypeOf(x.X); t != nil {
					if _, ok := t.Underlying().(*types.Chan); ok {
						c.checkLoop(d, x.Body)
					}
				}
			}
			return true
		})
	}
}

func (c *checker) checkMake(call *ast.CallExpr) {
	if !c.builtin(call.Fun, "make") || len(call.Args) == 0 {
		return
	}
	t := c.info.TypeOf(call.Args[0])
	if t == nil {
		return
	}
	if _, ok := t.Underlying().(*types.Chan); !ok {
		return
	}
	if len(call.Args) == 1 {
		c.report(call.Pos(), "unbuffered", "make(chan) with no capacity: give it a named capacity, or allowlist it with the reason a rendezvous is wanted")
		return
	}
	arg := unparen(call.Args[1])
	if tv, ok := c.info.Types[arg]; ok && tv.Value != nil && tv.Value.Kind() == constant.Int && constant.Sign(tv.Value) == 0 {
		c.report(call.Pos(), "unbuffered", "make(chan) with capacity zero: give it a named capacity, or allowlist it with the reason a rendezvous is wanted")
		return
	}
	if c.namedConst(arg) || c.configField(arg) {
		return
	}
	c.report(arg.Pos(), "chan-cap", "channel capacity "+types.ExprString(arg)+" is neither a named constant nor a field of a config struct")
}

func (c *checker) namedConst(e ast.Expr) bool {
	var id *ast.Ident
	switch x := e.(type) {
	case *ast.Ident:
		id = x
	case *ast.SelectorExpr:
		if _, isSel := c.info.Selections[x]; isSel {
			return false
		}
		id = x.Sel
	default:
		return false
	}
	_, ok := c.info.Uses[id].(*types.Const)
	return ok
}

// configField reports whether e selects a field of a struct type whose name contains
// "config" (any case), or of a struct type declared in a package named config.
func (c *checker) configField(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	s, ok := c.info.Selections[sel]
	if !ok || s.Kind() != types.FieldVal {
		return false
	}
	t := s.Recv()
	if p, ok := t.Underlying().(*types.Pointer); ok {
		t = p.Elem()
	}
	named, ok := types.Unalias(t).(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	if strings.Contains(strings.ToLower(obj.Name()), "config") {
		return true
	}
	return obj.Pkg() != nil && obj.Pkg().Name() == "config"
}

func (c *checker) field(e ast.Expr) (*ast.SelectorExpr, bool) {
	sel, ok := unparen(e).(*ast.SelectorExpr)
	if !ok {
		return nil, false
	}
	s, ok := c.info.Selections[sel]
	return sel, ok && s.Kind() == types.FieldVal
}

// deref removes parentheses and pointer dereferences, and says whether it removed a
// dereference.
func deref(e ast.Expr) (ast.Expr, bool) {
	star := false
	for {
		switch x := e.(type) {
		case *ast.ParenExpr:
			e = x.X
			continue
		case *ast.StarExpr:
			e, star = x.X, true
			continue
		}
		return e, star
	}
}

// growth is one place where something that outlives a loop iteration gets longer.
type growth struct {
	pos  token.Pos
	text string       // the target as written, without outer parentheses
	what string       // "x.f is appended to", ...
	obj  types.Object // the struct field or package variable; nil when it is not known
}

// fresh returns the local variables of body that are certainly not an alias of anything
// else: declared in body by `:=` or `var`, and every value ever assigned to them is a
// make(...) call or a composite literal (or nothing: a nil map).
func (c *checker) fresh(body ast.Node) map[types.Object]bool {
	declared := map[types.Object]bool{}
	tainted := map[types.Object]bool{}
	own := func(e ast.Expr) bool {
		switch x := unparen(e).(type) {
		case *ast.CompositeLit:
			return true
		case *ast.CallExpr:
			return c.builtin(x.Fun, "make")
		}
		return false
	}
	obj := func(e ast.Expr) types.Object {
		id, ok := unparen(e).(*ast.Ident)
		if !ok {
			return nil
		}
		if o := c.info.Defs[id]; o != nil {
			declared[o] = true
			return o
		}
		return c.info.Uses[id]
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range x.Lhs {
				o := obj(lhs)
				if o == nil {
					continue
				}
				if len(x.Lhs) != len(x.Rhs) || !own(x.Rhs[i]) {
					tainted[o] = true
				}
			}
		case *ast.ValueSpec:
			for i, id := range x.Names {
				o := c.info.Defs[id]
				if o == nil {
					continue
				}
				declared[o] = true
				if len(x.Values) != 0 && (len(x.Values) != len(x.Names) || !own(x.Values[i])) {
					tainted[o] = true
				}
			}
		case *ast.UnaryExpr:
			if x.Op == token.AND {
				if id, ok := unparen(x.X).(*ast.Ident); ok {
					if o := c.info.Uses[id]; o != nil {
						tainted[o] = true
					}
				}
			}
		}
		return true
	})
	for o := range tainted {
		delete(declared, o)
	}
	return declared
}

// target decides whether growing e (the left side of an append assignment, or the map of
// an insertion when isMap) can outlive the loop iteration. It can when e is
//   - a struct field, reached directly or through a dereference;
//   - anything reached through a pointer dereference (*p, whatever p is);
//   - a package-level variable;
//   - for a map: any variable that is not in fresh (a parameter, a copy of a field, a
//     function result: a map value is a reference, so it may be an alias of a field).
//
// A local slice variable is not a target: appending to a copy of a slice header does not
// lengthen the original.
func (c *checker) target(e ast.Expr, isMap bool, fresh map[types.Object]bool) (what string, obj types.Object, ok bool) {
	base, star := deref(e)
	if sel, isField := c.field(base); isField {
		return "", c.info.Selections[sel].Obj(), true
	}
	if star {
		return " (through a pointer)", nil, true
	}
	id, isIdent := base.(*ast.Ident)
	if !isIdent {
		return "", nil, false
	}
	v, isVar := c.info.Uses[id].(*types.Var)
	if !isVar {
		return "", nil, false
	}
	if c.pkg != nil && v.Parent() == c.pkg.Scope() {
		return " (a package variable)", v, true
	}
	if isMap && !fresh[v] {
		return " (a map this function did not make, so it may be a struct field under another name)", nil, true
	}
	return "", nil, false
}

// growths lists the growth sites directly in body (function literals included) and the
// calls in body to functions and methods declared in this package.
func (c *checker) growths(body ast.Node, fresh map[types.Object]bool) (sites []growth, calls []*ast.CallExpr) {
	mapOf := func(e ast.Expr) (ast.Expr, bool) {
		ix, ok := unparen(e).(*ast.IndexExpr)
		if !ok {
			return nil, false
		}
		t := c.info.TypeOf(ix.X)
		if t == nil {
			return nil, false
		}
		if _, isMap := t.Underlying().(*types.Map); !isMap {
			return nil, false
		}
		return ix.X, true
	}
	add := func(e ast.Expr, isMap bool, how string) {
		what, obj, ok := c.target(e, isMap, fresh)
		if !ok {
			return
		}
		text := types.ExprString(unparen(e))
		sites = append(sites, growth{pos: e.Pos(), text: text, what: text + " " + how + what, obj: obj})
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range x.Lhs {
				if m, ok := mapOf(lhs); ok {
					add(m, true, "gets a map entry")
					continue
				}
				if len(x.Lhs) != len(x.Rhs) {
					continue
				}
				call, ok := unparen(x.Rhs[i]).(*ast.CallExpr)
				if !ok || !c.builtin(call.Fun, "append") {
					continue
				}
				add(lhs, false, "is appended to")
			}
		case *ast.IncDecStmt:
			if m, ok := mapOf(x.X); ok {
				add(m, true, "gets a map entry")
			}
		case *ast.CallExpr:
			if c.callee(x) != nil {
				calls = append(calls, x)
			}
		}
		return true
	})
	return sites, calls
}

// callee returns the declaration a call statically names, when it is a function or
// method declared in this package; nil otherwise (builtins, conversions, other packages,
// interface methods, function values).
func (c *checker) callee(call *ast.CallExpr) *types.Func {
	fun := unparen(call.Fun)
	for {
		switch x := fun.(type) {
		case *ast.IndexExpr:
			fun = unparen(x.X)
			continue
		case *ast.IndexListExpr:
			fun = unparen(x.X)
			continue
		}
		break
	}
	var id *ast.Ident
	switch x := fun.(type) {
	case *ast.Ident:
		id = x
	case *ast.SelectorExpr:
		id = x.Sel
	default:
		return nil
	}
	fn, ok := c.info.Uses[id].(*types.Func)
	if !ok {
		return nil
	}
	fn = fn.Origin()
	if c.decls[fn] == nil {
		return nil
	}
	return fn
}

// reach lists the growth sites of fn and of every function of this package it calls,
// directly or not, that the function holding the site does not length-check itself.
func (c *checker) reach(fn *types.Func, seen map[*types.Func]bool, out *[]growth) {
	if seen[fn] {
		return
	}
	seen[fn] = true
	fd := c.decls[fn]
	sites, calls := c.growths(fd.Body, c.fresh(fd))
	for _, s := range sites {
		if !c.lenChecked(fd, s.text) {
			*out = append(*out, s)
		}
	}
	for _, call := range calls {
		c.reach(c.callee(call), seen, out)
	}
}

// checkLoop looks through the body of an unbounded loop for growth of something that
// outlives an iteration, in the body itself and in the functions of this package it calls.
func (c *checker) checkLoop(decl ast.Decl, body *ast.BlockStmt) {
	sites, calls := c.growths(body, c.fresh(decl))
	for _, s := range sites {
		if c.lenChecked(decl, s.text) {
			continue
		}
		c.report(s.pos, "unbounded-growth", fmt.Sprintf("%s inside an unbounded loop, and this function never compares len(%s) with anything", s.what, s.text))
	}
	for _, call := range calls {
		fn := c.callee(call)
		var far []growth
		c.reach(fn, map[*types.Func]bool{}, &far)
		for _, s := range far {
			if s.obj != nil && c.lenCheckedObj(decl, s.obj) {
				continue
			}
			c.report(call.Pos(), "unbounded-growth", fmt.Sprintf("the call of %s inside an unbounded loop reaches %s: %s, and neither that function nor this one compares its length with anything", fn.Name(), c.where(s.pos), s.what))
			break
		}
	}
}

// lenChecked reports whether the declaration holds a comparison one side of which is
// len(<text>).
func (c *checker) lenChecked(decl ast.Node, text string) bool {
	return c.compares(decl, func(arg ast.Expr) bool { return types.ExprString(unparen(arg)) == text })
}

// lenCheckedObj reports whether the declaration holds a comparison one side of which is
// len(x), x being the struct field or variable obj however it is spelled.
func (c *checker) lenCheckedObj(decl ast.Node, obj types.Object) bool {
	return c.compares(decl, func(arg ast.Expr) bool {
		base, _ := deref(arg)
		if sel, ok := c.field(base); ok {
			return c.info.Selections[sel].Obj() == obj
		}
		id, ok := base.(*ast.Ident)
		return ok && c.info.Uses[id] == obj
	})
}

func (c *checker) compares(decl ast.Node, match func(ast.Expr) bool) bool {
	found := false
	isLen := func(e ast.Expr) bool {
		call, ok := unparen(e).(*ast.CallExpr)
		return ok && len(call.Args) == 1 && c.builtin(call.Fun, "len") && match(call.Args[0])
	}
	ast.Inspect(decl, func(n ast.Node) bool {
		if b, ok := n.(*ast.BinaryExpr); ok {
			switch b.Op {
			case token.LSS, token.LEQ, token.GTR, token.GEQ, token.EQL, token.NEQ:
				if isLen(b.X) || isLen(b.Y) {
					found = true
				}
			}
		}
		return !found
	})
	return found
}
