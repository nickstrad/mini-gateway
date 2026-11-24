package main

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"path/filepath"
	"strconv"
	"strings"

	"detlint/internal/report"
)

type config struct {
	root         string
	tests        bool
	allow        string
	allowImports []string
	pkgs         []string
}

type result struct {
	findings []report.Finding
	files    int
	pkgs     int
}

// run checks the configured packages and returns findings after allowlist
// filtering. An error means the run itself could not proceed (exit 2).
func run(cfg config) (result, error) {
	var res result
	root, err := filepath.Abs(cfg.root)
	if err != nil {
		return res, err
	}
	modPath, err := readModulePath(root)
	if err != nil {
		return res, fmt.Errorf("reading module path: %w", err)
	}
	dirs, err := expand(root, cfg.pkgs)
	if err != nil {
		return res, err
	}
	fset := token.NewFileSet()
	l := &loader{fset: fset, root: root, modPath: modPath,
		cache: map[string]*types.Package{}, errs: map[string]error{}, loading: map[string]bool{}}
	lc := &lintCtx{modPath: modPath, imports: importAllow(cfg.allowImports)}
	var raw []rawFinding
	for _, rel := range dirs {
		dir := filepath.Join(root, rel)
		files, xtest, bt, err := scanChecked(fset, root, dir, cfg.tests)
		if err != nil {
			raw = append(raw, rawFinding{report.Finding{Path: filepath.ToSlash(rel), Line: 1, Col: 1, Rule: "parse", Msg: err.Error()}, "-"})
			continue
		}
		for _, f := range bt {
			raw = append(raw, rawFinding{f, "-"})
		}
		if len(files) == 0 && len(xtest) == 0 {
			continue
		}
		path := l.importPath(rel)
		var under *types.Package
		if len(files) > 0 {
			res.pkgs++
			res.files += len(files)
			var pf []rawFinding
			under, pf = checkAndLint(l, lc, root, path, files)
			raw = append(raw, pf...)
		}
		if len(xtest) > 0 {
			res.pkgs++
			res.files += len(xtest)
			if under != nil {
				l.override = map[string]*types.Package{path: under}
			}
			_, pf := checkAndLint(l, lc, root, path+"_test", xtest)
			l.override = nil
			raw = append(raw, pf...)
		}
	}
	res.findings = dedupe(raw)
	if cfg.allow != "" {
		res.findings, err = applyAllow(cfg.allow, res.findings, raw)
		if err != nil {
			return res, err
		}
	}
	report.Sort(res.findings)
	return res, nil
}

type rawFinding struct {
	report.Finding
	fn string
}

// dedupe keeps one finding per (path, line, rule), the leftmost.
func dedupe(raw []rawFinding) []report.Finding {
	fs := make([]report.Finding, 0, len(raw))
	for _, r := range raw {
		fs = append(fs, r.Finding)
	}
	report.Sort(fs)
	type key struct {
		path string
		line int
		rule string
	}
	seen := map[key]bool{}
	out := fs[:0]
	for _, f := range fs {
		k := key{f.Path, f.Line, f.Rule}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, f)
	}
	return out
}

func applyAllow(path string, fs []report.Finding, raw []rawFinding) ([]report.Finding, error) {
	entries, bad, err := report.ReadAllow(path, 4)
	if err != nil {
		return nil, fmt.Errorf("reading allowlist: %w", err)
	}
	fnOf := map[report.Finding]string{}
	for _, r := range raw {
		fnOf[r.Finding] = r.fn
	}
	var out []report.Finding
	for _, f := range fs {
		allowed := false
		for _, e := range entries {
			if e.Fields[0] == f.Path && e.Fields[1] == fnOf[f] && e.Fields[2] == f.Rule {
				e.Used = true
				allowed = true
			}
		}
		if !allowed {
			out = append(out, f)
		}
	}
	out = append(out, bad...)
	out = append(out, report.Stale(path, entries)...)
	return out, nil
}

// checkAndLint type-checks one package and lints its files. Type errors are
// findings; the package is returned for use by an external test package.
func checkAndLint(l *loader, lc *lintCtx, root, path string, files []*ast.File) (*types.Package, []rawFinding) {
	var out []rawFinding
	relPath := func(pos token.Pos) (string, int, int) {
		p := l.fset.Position(pos)
		rel, err := filepath.Rel(root, p.Filename)
		if err != nil {
			rel = p.Filename
		}
		return filepath.ToSlash(rel), p.Line, p.Column
	}
	fileOf := func(pos token.Pos) *ast.File {
		for _, f := range files {
			if f.FileStart <= pos && pos <= f.FileEnd {
				return f
			}
		}
		return nil
	}
	add := func(pos token.Pos, rule, msg string) {
		p, line, col := relPath(pos)
		fn := "-"
		if f := fileOf(pos); f != nil {
			fn = report.Enclosing(f, pos)
		}
		out = append(out, rawFinding{report.Finding{Path: p, Line: line, Col: col, Rule: rule, Msg: msg}, fn})
	}
	info := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	typeErr := false
	conf := types.Config{Importer: l, Error: func(err error) {
		typeErr = true
		if te, ok := err.(types.Error); ok {
			add(te.Pos, "typecheck", te.Msg)
			return
		}
		out = append(out, rawFinding{report.Finding{Path: path, Line: 1, Col: 1, Rule: "typecheck", Msg: err.Error()}, "-"})
	}}
	pkg, err := conf.Check(path, l.fset, files, info)
	if err != nil && !typeErr {
		out = append(out, rawFinding{report.Finding{Path: path, Line: 1, Col: 1, Rule: "typecheck", Msg: err.Error()}, "-"})
	}
	for _, f := range files {
		isTest := strings.HasSuffix(l.fset.Position(f.FileStart).Filename, "_test.go")
		lintFile(f, isTest, lc, info, pkg, add)
	}
	return pkg, out
}

var wallclock = set("Now", "Since", "Until", "Sleep", "After", "AfterFunc", "Tick", "NewTimer", "NewTicker")
var ctxTimer = set("WithTimeout", "WithDeadline", "WithTimeoutCause", "WithDeadlineCause")
var randCtors = set("New", "NewPCG", "NewChaCha8", "NewSource", "NewZipf")
var osProc = set("Getpid", "Getppid", "Hostname", "Getenv", "LookupEnv", "Environ", "Args", "Getwd")
var runtimeProc = set("NumGoroutine", "NumCPU", "GOMAXPROCS", "Stack", "Caller", "Callers")
var reflectPtr = set("Pointer", "UnsafePointer", "UnsafeAddr")
var mapsIter = set("Keys", "Values", "All")

func set(xs ...string) map[string]bool {
	m := map[string]bool{}
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// stdAllowed is the import allowlist for non-test files of checked packages
// (besides the root module's own packages).
var stdAllowed = []string{
	"bufio", "bytes", "cmp", "container/heap", "container/list", "container/ring",
	"crypto/sha256", "crypto/sha512", "crypto/subtle", "embed",
	"encoding/binary", "encoding/hex", "encoding/base64", "errors", "fmt",
	"hash", "hash/crc32", "hash/crc64", "hash/fnv", "io", "iter", "maps",
	"math", "math/bits", "math/rand/v2", "slices", "sort", "strconv", "strings",
	"time", "unicode", "unicode/utf8", "unicode/utf16",
}

func importAllow(extra []string) map[string]bool {
	m := set(stdAllowed...)
	for _, e := range extra {
		m[e] = true
	}
	return m
}

// lintCtx is the per-run configuration the file rules need.
type lintCtx struct {
	modPath string
	imports map[string]bool
}

func (lc *lintCtx) importOK(path string) bool {
	return lc.imports[path] || path == lc.modPath || strings.HasPrefix(path, lc.modPath+"/")
}

// testImport maps an import path to its rule for _test.go files, which are
// not subject to the import allowlist.
func testImport(path string) (string, bool) {
	switch path {
	case "crypto/rand":
		return "cryptorand", true
	case "hash/maphash", "unsafe":
		return "procstate", true
	case "net", "os/exec", "os/signal", "syscall", "plugin":
		return "realio", true
	}
	if strings.HasPrefix(path, "net/http") || strings.HasPrefix(path, "syscall/") {
		return "realio", true
	}
	return "", false
}

func isFloatType(t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Info()&(types.IsFloat|types.IsComplex) != 0
}

func isFloat(t types.Type) bool {
	return t != nil && isFloatType(t)
}

func isChan(t types.Type) bool {
	return underIs(t, func(u types.Type) bool { _, ok := u.(*types.Chan); return ok })
}

func isMap(t types.Type) bool {
	return underIs(t, func(u types.Type) bool { _, ok := u.(*types.Map); return ok })
}

// underIs applies pred to t's underlying type; for a type parameter it
// reports true if any type term of the constraint's type set satisfies pred.
func underIs(t types.Type, pred func(types.Type) bool) bool {
	if t == nil {
		return false
	}
	if tp, ok := t.(*types.TypeParam); ok {
		return termsHave(tp.Constraint(), pred, map[*types.Interface]bool{})
	}
	return pred(t.Underlying())
}

// termsHave reports whether a type term of constraint t (following embedded
// interfaces and unions at any depth) has an underlying type satisfying
// pred. A constraint without type terms (any, comparable, method sets) has
// none, so it reports false.
func termsHave(t types.Type, pred func(types.Type) bool, seen map[*types.Interface]bool) bool {
	iface, ok := t.Underlying().(*types.Interface)
	if !ok {
		return pred(t.Underlying())
	}
	if seen[iface] {
		return false
	}
	seen[iface] = true
	for i := 0; i < iface.NumEmbeddeds(); i++ {
		switch e := iface.EmbeddedType(i).(type) {
		case *types.Union:
			for j := 0; j < e.Len(); j++ {
				if termsHave(e.Term(j).Type(), pred, seen) {
					return true
				}
			}
		default:
			if termsHave(e, pred, seen) {
				return true
			}
		}
	}
	return false
}

// pkgFunc returns (pkgpath, name, true) if obj is a package-level function.
func pkgFunc(obj types.Object) (string, string, bool) {
	fn, ok := obj.(*types.Func)
	if !ok || fn.Pkg() == nil {
		return "", "", false
	}
	if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
		return "", "", false
	}
	return fn.Pkg().Path(), fn.Name(), true
}

// qualName renders pkg.Name or pkg.Type.Method for messages.
func qualName(obj types.Object) string {
	if fn, ok := obj.(*types.Func); ok {
		if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
			t := sig.Recv().Type()
			if p, ok := t.(*types.Pointer); ok {
				t = p.Elem()
			}
			if n, ok := t.(*types.Named); ok {
				return fn.Pkg().Path() + "." + n.Obj().Name() + "." + fn.Name()
			}
		}
	}
	return obj.Pkg().Path() + "." + obj.Name()
}

// calleeObj resolves the object a call's Fun refers to, if it is a name.
func calleeObj(info *types.Info, fun ast.Expr) types.Object {
	switch f := ast.Unparen(fun).(type) {
	case *ast.Ident:
		return info.Uses[f]
	case *ast.SelectorExpr:
		return info.Uses[f.Sel]
	case *ast.IndexExpr:
		return calleeObj(info, f.X)
	case *ast.IndexListExpr:
		return calleeObj(info, f.X)
	}
	return nil
}

func lintFile(f *ast.File, isTest bool, lc *lintCtx, info *types.Info, pkg *types.Package, add func(token.Pos, string, string)) {
	for _, im := range f.Imports {
		p, err := strconv.Unquote(im.Path.Value)
		if err != nil {
			p = im.Path.Value
		}
		if isTest {
			if rule, ok := testImport(p); ok {
				add(im.Pos(), rule, "import of "+p)
			}
			continue
		}
		if !lc.importOK(p) {
			add(im.Pos(), "import", "import of "+p+" is not on the import allowlist")
		}
	}
	var stack []ast.Node
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		parent := ast.Node(nil)
		if len(stack) > 0 {
			parent = stack[len(stack)-1]
		}
		lintNode(n, parent, stack, info, pkg, add)
		stack = append(stack, n)
		return true
	})
}

func lintNode(n, parent ast.Node, stack []ast.Node, info *types.Info, pkg *types.Package, add func(token.Pos, string, string)) {
	if e, ok := n.(ast.Expr); ok {
		if tv, ok := info.Types[e]; ok && isFloat(tv.Type) {
			add(e.Pos(), "float", "floating-point expression of type "+tv.Type.String())
		}
	}
	switch x := n.(type) {
	case *ast.Ident:
		if obj := info.Defs[x]; obj != nil {
			switch o := obj.(type) {
			case *types.Var:
				if isFloat(o.Type()) {
					add(x.Pos(), "float", "floating-point variable "+x.Name)
				}
				if isChan(o.Type()) {
					add(x.Pos(), "concurrency", "channel variable "+x.Name)
				}
			case *types.TypeName:
				if tp, ok := o.Type().(*types.TypeParam); ok && underIs(tp, isFloatType) {
					add(x.Pos(), "float", "type parameter "+x.Name+" admits floating-point or complex types ("+tp.Constraint().String()+")")
				}
			}
		}
		if obj := info.Uses[x]; obj != nil {
			lintUse(x, obj, parent, stack, info, pkg, add)
		}
	case *ast.GoStmt:
		add(x.Pos(), "concurrency", "go statement")
	case *ast.SelectStmt:
		add(x.Pos(), "concurrency", "select statement")
	case *ast.ChanType:
		add(x.Pos(), "concurrency", "channel type")
	case *ast.SendStmt:
		add(x.Pos(), "concurrency", "channel send")
	case *ast.UnaryExpr:
		if x.Op == token.ARROW {
			add(x.Pos(), "concurrency", "channel receive")
		}
	case *ast.RangeStmt:
		t := info.TypeOf(x.X)
		switch {
		case isChan(t):
			add(x.Pos(), "concurrency", "range over channel")
		case isMap(t):
			if !orderInsensitive(x, parent, stack, info) {
				add(x.Pos(), "maprange", "range over map "+types.ExprString(x.X)+" has nondeterministic order")
			}
		}
	case *ast.CallExpr:
		lintPtrFmt(x, info, add)
	}
}

func lintUse(id *ast.Ident, obj types.Object, parent ast.Node, stack []ast.Node, info *types.Info, pkg *types.Package, add func(token.Pos, string, string)) {
	if b, ok := obj.(*types.Builtin); ok {
		if b.Name() == "close" {
			add(id.Pos(), "concurrency", "close of channel")
		}
		return
	}
	if v, ok := obj.(*types.Var); ok && isChan(v.Type()) {
		add(id.Pos(), "concurrency", "use of channel "+id.Name)
	}
	if obj.Pkg() == nil || obj.Pkg() == pkg {
		return
	}
	path, name := obj.Pkg().Path(), obj.Name()
	fpath, fname, isPkgFunc := pkgFunc(obj)
	switch path {
	case "fmt":
		if _, ok := obj.(*types.Func); ok && !directCallee(id, parent, stack) {
			add(id.Pos(), "ptrfmt", qualName(obj)+" used as a value; its arguments cannot be checked")
		}
	case "time":
		if isPkgFunc && wallclock[fname] {
			add(id.Pos(), "wallclock", "use of time."+fname)
		}
	case "math/rand", "math/rand/v2":
		if isPkgFunc && !randCtors[fname] {
			add(id.Pos(), "globalrand", "use of global-source "+fpath+"."+fname)
		}
	case "sync", "sync/atomic":
		add(id.Pos(), "concurrency", "use of "+qualName(obj))
	case "os":
		if osProc[name] {
			add(id.Pos(), "procstate", "use of os."+name)
		} else {
			add(id.Pos(), "realio", "use of os."+name)
		}
	case "context":
		if isPkgFunc && ctxTimer[fname] {
			add(id.Pos(), "wallclock", "use of context."+fname+" (wall-clock deadline)")
		}
	case "runtime":
		if isPkgFunc && runtimeProc[fname] {
			add(id.Pos(), "procstate", "use of runtime."+fname)
		}
	case "reflect":
		if fn, ok := obj.(*types.Func); ok && reflectPtr[name] {
			if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
				add(id.Pos(), "procstate", "use of reflect.Value."+name)
			}
		}
	case "maps":
		if isPkgFunc && mapsIter[fname] && !wrappedInSorted(id, parent, stack, info) {
			add(id.Pos(), "maprange", "maps."+fname+" iteration order is nondeterministic; wrap directly in slices.Sorted")
		}
	}
}

// calleeChain finds the call whose Fun is the use id (possibly as the Sel of
// a selector, possibly parenthesized). It returns the index in stack of that
// call, or -1.
func calleeChain(id *ast.Ident, parent ast.Node, stack []ast.Node) int {
	i := len(stack) - 1
	var fun ast.Node = id
	if sel, ok := parent.(*ast.SelectorExpr); ok && sel.Sel == id {
		fun = sel
		i--
	}
	for i >= 0 {
		if p, ok := stack[i].(*ast.ParenExpr); ok && p.X == fun {
			fun = p
			i--
			continue
		}
		break
	}
	if i < 0 {
		return -1
	}
	if call, ok := stack[i].(*ast.CallExpr); ok && call.Fun == fun {
		return i
	}
	return -1
}

func directCallee(id *ast.Ident, parent ast.Node, stack []ast.Node) bool {
	return calleeChain(id, parent, stack) >= 0
}

// wrappedInSorted reports whether the use id of maps.Keys/Values/All is the
// callee of a call that is itself the first argument of slices.Sorted.
func wrappedInSorted(id *ast.Ident, parent ast.Node, stack []ast.Node, info *types.Info) bool {
	i := calleeChain(id, parent, stack)
	if i < 1 {
		return false
	}
	call := stack[i].(*ast.CallExpr)
	outer, ok := stack[i-1].(*ast.CallExpr)
	if !ok || len(outer.Args) != 1 || outer.Args[0] != call {
		return false
	}
	p, n, ok := pkgFunc(calleeObj(info, outer.Fun))
	return ok && p == "slices" && n == "Sorted"
}

// orderInsensitive accepts exactly two shapes of range-over-map loop: an
// empty body, or a body whose every statement is `s = append(s, k)` /
// `s = append(s, k, v)` (k, v the range key and value identifiers, s one
// slice variable) immediately followed, as the next statement of the same
// block, by slices.Sort(s), sort.Strings(s) or sort.Ints(s).
func orderInsensitive(rs *ast.RangeStmt, parent ast.Node, stack []ast.Node, info *types.Info) bool {
	// `for k = range m` assigns to a variable that outlives the loop: its
	// final value is the last key visited, which is map order, whatever the
	// body does (an empty body included). `for range m` and `for _ = range m`
	// assign nothing.
	if rs.Tok == token.ASSIGN {
		for _, e := range []ast.Expr{rs.Key, rs.Value} {
			if e == nil {
				continue
			}
			if id, ok := ast.Unparen(e).(*ast.Ident); !ok || id.Name != "_" {
				return false
			}
		}
	}
	if len(rs.Body.List) == 0 {
		return true
	}
	objOf := func(e ast.Expr) types.Object {
		id, ok := ast.Unparen(e).(*ast.Ident)
		if !ok || id.Name == "_" {
			return nil
		}
		if o := info.Defs[id]; o != nil {
			return o
		}
		return info.Uses[id]
	}
	iterVars := map[types.Object]bool{}
	for _, e := range []ast.Expr{rs.Key, rs.Value} {
		if e != nil {
			if o := objOf(e); o != nil {
				iterVars[o] = true
			}
		}
	}
	var dst types.Object
	for _, st := range rs.Body.List {
		as, ok := st.(*ast.AssignStmt)
		if !ok || as.Tok != token.ASSIGN || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return false
		}
		call, ok := ast.Unparen(as.Rhs[0]).(*ast.CallExpr)
		if !ok || call.Ellipsis.IsValid() || len(call.Args) < 2 {
			return false
		}
		if b, ok := calleeObj(info, call.Fun).(*types.Builtin); !ok || b.Name() != "append" {
			return false
		}
		d := objOf(as.Lhs[0])
		if d == nil || objOf(call.Args[0]) != d || (dst != nil && d != dst) {
			return false
		}
		dst = d
		for _, a := range call.Args[1:] {
			if o := objOf(a); o == nil || !iterVars[o] {
				return false
			}
		}
	}
	var self ast.Stmt = rs
	if ls, ok := parent.(*ast.LabeledStmt); ok && ls.Stmt == rs {
		self = ls
		if len(stack) < 2 {
			return false
		}
		parent = stack[len(stack)-2]
	}
	var list []ast.Stmt
	switch b := parent.(type) {
	case *ast.BlockStmt:
		list = b.List
	case *ast.CaseClause:
		list = b.Body
	case *ast.CommClause:
		list = b.Body
	default:
		return false
	}
	next := -1
	for i, st := range list {
		if st == self && i+1 < len(list) {
			next = i + 1
		}
	}
	if next < 0 {
		return false
	}
	es, ok := list[next].(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := ast.Unparen(es.X).(*ast.CallExpr)
	if !ok || len(call.Args) != 1 || call.Ellipsis.IsValid() {
		return false
	}
	p, name, ok := pkgFunc(calleeObj(info, call.Fun))
	if !ok || !((p == "slices" && name == "Sort") || (p == "sort" && (name == "Strings" || name == "Ints"))) {
		return false
	}
	return objOf(call.Args[0]) == dst
}

// lintPtrFmt flags, in a call of a fmt function or method, every argument
// bound to an `any` parameter whose static type can print an address, and a
// %p verb in any constant string argument of a call to a function or method
// whose name ends in "f". The fmt scanning functions are exempt (their
// operands are pointers by necessity and are written to, not printed).
func lintPtrFmt(call *ast.CallExpr, info *types.Info, add func(token.Pos, string, string)) {
	fn, ok := calleeObj(info, call.Fun).(*types.Func)
	if !ok {
		return
	}
	if fn.Pkg() != nil && fn.Pkg().Path() == "fmt" && !strings.Contains(strings.ToLower(fn.Name()), "scan") {
		lintFmtArgs(call, fn, info, add)
	}
	if !strings.HasSuffix(fn.Name(), "f") {
		return
	}
	for _, a := range call.Args {
		tv, ok := info.Types[a]
		if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
			continue
		}
		if hasPtrVerb(constant.StringVal(tv.Value)) {
			add(a.Pos(), "ptrfmt", "%p verb in format string of "+fn.Name())
		}
	}
}

func lintFmtArgs(call *ast.CallExpr, fn *types.Func, info *types.Info, add func(token.Pos, string, string)) {
	sig, ok := fn.Type().(*types.Signature)
	if !ok {
		return
	}
	params := sig.Params()
	paramType := func(i int) types.Type {
		n := params.Len()
		if n == 0 {
			return nil
		}
		if sig.Variadic() && i >= n-1 {
			if call.Ellipsis.IsValid() {
				return params.At(n - 1).Type()
			}
			return params.At(n - 1).Type().(*types.Slice).Elem()
		}
		if i < n {
			return params.At(i).Type()
		}
		return nil
	}
	type arg struct {
		pos token.Pos
		t   types.Type
	}
	var args []arg
	if len(call.Args) == 1 {
		if tup, ok := info.TypeOf(call.Args[0]).(*types.Tuple); ok {
			for i := 0; i < tup.Len(); i++ {
				args = append(args, arg{call.Args[0].Pos(), tup.At(i).Type()})
			}
		}
	}
	if args == nil {
		for _, a := range call.Args {
			args = append(args, arg{a.Pos(), info.TypeOf(a)})
		}
	}
	for i, a := range args {
		pt := paramType(i)
		if pt == nil || a.t == nil {
			continue
		}
		if iface, ok := pt.Underlying().(*types.Interface); !ok || !iface.Empty() {
			continue
		}
		if canPrintAddr(a.t, map[types.Type]bool{}) {
			add(a.pos, "ptrfmt", "argument of type "+a.t.String()+" to "+qualName(fn)+" can print an address")
		}
	}
}

// canPrintAddr reports whether fmt may render a value of static type t as a
// memory address: a pointer, channel, func, unsafe.Pointer or uintptr, or a
// composite (map, slice, array, struct) containing one at any depth. An
// interface type reports false: its dynamic type is unknown statically. A
// type parameter reports true if any type term of its constraint does.
func canPrintAddr(t types.Type, seen map[types.Type]bool) bool {
	t = types.Unalias(t)
	switch x := t.(type) {
	case *types.TypeParam:
		return termsHave(x.Constraint(), func(u types.Type) bool { return canPrintAddr(u, seen) }, map[*types.Interface]bool{})
	case *types.Named:
		if seen[x] {
			return false
		}
		seen[x] = true
		return canPrintAddr(x.Underlying(), seen)
	case *types.Tuple:
		for i := 0; i < x.Len(); i++ {
			if canPrintAddr(x.At(i).Type(), seen) {
				return true
			}
		}
		return false
	}
	switch u := t.Underlying().(type) {
	case *types.Basic:
		return u.Kind() == types.Uintptr || u.Kind() == types.UnsafePointer
	case *types.Pointer, *types.Chan, *types.Signature:
		return true
	case *types.Map:
		return canPrintAddr(u.Key(), seen) || canPrintAddr(u.Elem(), seen)
	case *types.Slice:
		return canPrintAddr(u.Elem(), seen)
	case *types.Array:
		return canPrintAddr(u.Elem(), seen)
	case *types.Struct:
		for i := 0; i < u.NumFields(); i++ {
			if canPrintAddr(u.Field(i).Type(), seen) {
				return true
			}
		}
	}
	return false
}

func hasPtrVerb(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			continue
		}
		i++
		if i < len(s) && s[i] == '%' {
			continue
		}
		for i < len(s) && strings.IndexByte("+-# 0", s[i]) >= 0 {
			i++
		}
		skipArg := func() {
			if i < len(s) && s[i] == '[' {
				for i < len(s) && s[i] != ']' {
					i++
				}
				i++
			}
		}
		skipNum := func() {
			if i < len(s) && s[i] == '*' {
				i++
				return
			}
			for i < len(s) && s[i] >= '0' && s[i] <= '9' {
				i++
			}
		}
		skipArg()
		skipNum()
		if i < len(s) && s[i] == '.' {
			i++
			skipArg()
			skipNum()
		}
		skipArg()
		if i < len(s) && s[i] == 'p' {
			return true
		}
		i--
	}
	return false
}
