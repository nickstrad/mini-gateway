package pgwire

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

var cntTallyMethods = map[string]bool{"Bump": true, "Add": true, "Value": true, "Ref": true}

var cntBuiltins = map[string]bool{"len": true, "max": true, "min": true, "make": true, "append": true}

func TestLexLoopsCountTheirSteps(t *testing.T) {
	src, err := os.ReadFile("lex.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range cntLexProblems(t, src) {
		t.Error(p)
	}
}

func TestClassifierLexLoopsCountTheirSteps(t *testing.T) {
	src, err := os.ReadFile("classify.go")
	if err != nil {
		t.Fatal(err)
	}
	f := cntParse(t, "classify.go", src)
	found := false
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "lex" || cntReceiver(fn) != "clsMsg" {
			continue
		}
		found = true
		for _, p := range cntLoopProblems(fn) {
			t.Error(p)
		}
	}
	if !found {
		t.Fatal("classify.go has no (*clsMsg).lex")
	}
}

func TestLexLoopGuardRejectsUncountedWork(t *testing.T) {
	src, err := os.ReadFile("lex.go")
	if err != nil {
		t.Fatal(err)
	}
	plants := map[string][2]string{
		"loop without a count":      {"\tfor k := r.start; k < r.end-1; k++ {\n", "\tfor j := r.start; j < r.end; j++ {\n\t\t_ = l.src[j]\n\t}\n\tfor k := r.start; k < r.end-1; k++ {\n"},
		"call to a scanning helper": {"func (l *Lexer) operator() {\n", "func (l *Lexer) operator() {\n\t_ = lexUnquote(l.src, '+')\n"},
		"call outside the package":  {"func (l *Lexer) operator() {\n", "func (l *Lexer) operator() {\n\t_ = bytes.IndexByte(l.src, '+')\n"},
		"scan without its count":    {"func (l *Lexer) scan() TokenKind {\n\tif clscount.On {\n\t\tl.reads.Bump()\n\t}\n", "func (l *Lexer) scan() TokenKind {\n"},
	}
	for name, p := range plants {
		if strings.Count(string(src), p[0]) != 1 {
			t.Fatalf("%s: plant anchor not found once in lex.go", name)
		}
		planted := strings.Replace(string(src), p[0], p[1], 1)
		if len(cntLexProblems(t, []byte(planted))) == 0 {
			t.Errorf("%s: the guard accepted lex.go with uncounted work planted", name)
		}
	}
}

func cntParse(t *testing.T, name string, src []byte) *ast.File {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), name, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func cntLexProblems(t *testing.T, src []byte) []string {
	t.Helper()
	f := cntParse(t, "lex.go", src)
	decls := map[string]*ast.FuncDecl{}
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok {
			decls[fn.Name.Name] = fn
		}
	}
	var out []string
	for _, fn := range decls {
		if !cntScanner(fn) {
			continue
		}
		out = append(out, cntLoopProblems(fn)...)
		out = append(out, cntCallProblems(fn, decls)...)
		if fn.Name.Name == "scan" && (len(fn.Body.List) == 0 || !cntIsCount(fn.Body.List[0])) {
			out = append(out, "scan does not count the byte it reads first")
		}
	}
	if decls["scan"] == nil {
		out = append(out, "lex.go has no scan")
	}
	return out
}

func cntScanner(fn *ast.FuncDecl) bool {
	if cntReceiver(fn) == "Lexer" {
		return true
	}
	for _, p := range fn.Type.Params.List {
		if cntIsSelector(p.Type, "clscount", "Ref") {
			return true
		}
	}
	return false
}

func cntReceiver(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) != 1 {
		return ""
	}
	typ := fn.Recv.List[0].Type
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	if id, ok := typ.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

func cntLoopProblems(fn *ast.FuncDecl) []string {
	var out []string
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		var body *ast.BlockStmt
		switch loop := n.(type) {
		case *ast.ForStmt:
			body = loop.Body
		case *ast.RangeStmt:
			body = loop.Body
		default:
			return true
		}
		if len(body.List) == 0 || !cntIsCount(body.List[0]) {
			out = append(out, fmt.Sprintf("%s: a loop does not start by counting its step (if clscount.On { ....Bump() })", fn.Name.Name))
		}
		return true
	})
	return out
}

func cntIsCount(s ast.Stmt) bool {
	is, ok := s.(*ast.IfStmt)
	if !ok || is.Init != nil || is.Else != nil || len(is.Body.List) != 1 {
		return false
	}
	if !cntIsSelector(is.Cond, "clscount", "On") {
		return false
	}
	es, ok := is.Body.List[0].(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := es.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Bump"
}

func cntIsSelector(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg
}

func cntCallProblems(fn *ast.FuncDecl, decls map[string]*ast.FuncDecl) []string {
	var out []string
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			callee := decls[fun.Name]
			switch {
			case cntBuiltins[fun.Name], fun.Name == "TokenKind":
			case callee == nil:
				out = append(out, fmt.Sprintf("%s calls %s, which lex.go does not declare", fn.Name.Name, fun.Name))
			case !cntScanner(callee) && cntHasLoop(callee):
				out = append(out, fmt.Sprintf("%s calls %s, which loops without a clscount.Ref to count on", fn.Name.Name, fun.Name))
			}
		case *ast.SelectorExpr:
			callee := decls[fun.Sel.Name]
			switch {
			case cntTallyMethods[fun.Sel.Name]:
			case callee == nil || callee.Recv == nil:
				out = append(out, fmt.Sprintf("%s calls %s, which is not a method declared in lex.go", fn.Name.Name, fun.Sel.Name))
			case !cntScanner(callee) && cntHasLoop(callee):
				out = append(out, fmt.Sprintf("%s calls %s, which loops without a clscount.Ref to count on", fn.Name.Name, fun.Sel.Name))
			}
		default:
			out = append(out, fmt.Sprintf("%s makes a call the guard cannot resolve", fn.Name.Name))
		}
		return true
	})
	return out
}

func cntHasLoop(fn *ast.FuncDecl) bool {
	loop := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			loop = true
		}
		return !loop
	})
	return loop
}
