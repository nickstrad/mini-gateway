// Package report holds the pieces shared by the gate tools: findings,
// output formatting, allowlist parsing and function naming.
package report

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/token"
	"io"
	"os"
	"sort"
	"strings"
)

// Finding is one problem at a source position.
type Finding struct {
	Path string
	Line int
	Col  int
	Rule string
	Msg  string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d:%d: %s: %s", f.Path, f.Line, f.Col, f.Rule, f.Msg)
}

// Sort orders findings by path, line, column, rule, message.
func Sort(fs []Finding) {
	sort.Slice(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Col != b.Col {
			return a.Col < b.Col
		}
		if a.Rule != b.Rule {
			return a.Rule < b.Rule
		}
		return a.Msg < b.Msg
	})
}

// Print writes sorted findings one per line.
func Print(w io.Writer, fs []Finding) {
	Sort(fs)
	for _, f := range fs {
		fmt.Fprintln(w, f.String())
	}
}

// ExitCode maps a run outcome to the tool exit status.
func ExitCode(fs []Finding, err error) int {
	if err != nil {
		return 2
	}
	if len(fs) > 0 {
		return 1
	}
	return 0
}

// Entry is one allowlist line.
type Entry struct {
	Line   int
	Fields []string // file, func, [rule,] justification
	Used   bool
}

// ReadAllow parses a tab-separated allowlist with n fields per line; the last
// field is the justification and must be non-empty. Blank lines and lines
// starting with '#' are ignored. Malformed lines come back as findings with
// rule allow-format.
func ReadAllow(path string, n int) ([]*Entry, []Finding, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	var entries []*Entry
	var bad []Finding
	sc := bufio.NewScanner(f)
	ln := 0
	for sc.Scan() {
		ln++
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		parts := strings.SplitN(line, "\t", n)
		if len(parts) != n {
			bad = append(bad, Finding{path, ln, 1, "allow-format", fmt.Sprintf("want %d tab-separated fields, got %d", n, len(parts))})
			continue
		}
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		empty := false
		for i, p := range parts {
			if p == "" {
				what := "field"
				if i == n-1 {
					what = "justification"
				}
				bad = append(bad, Finding{path, ln, 1, "allow-format", fmt.Sprintf("empty %s (field %d)", what, i+1)})
				empty = true
				break
			}
		}
		if empty {
			continue
		}
		entries = append(entries, &Entry{Line: ln, Fields: parts})
	}
	if err := sc.Err(); err != nil {
		return nil, nil, err
	}
	return entries, bad, nil
}

// Stale reports allowlist entries that matched nothing.
func Stale(path string, entries []*Entry) []Finding {
	var out []Finding
	for _, e := range entries {
		if !e.Used {
			out = append(out, Finding{path, e.Line, 1, "stale-allow", "allowlist entry matches nothing: " + strings.Join(e.Fields[:len(e.Fields)-1], " ")})
		}
	}
	return out
}

// FuncName returns Func or Type.Method for a declaration; pointer receivers
// and type parameters are dropped, so (*T[K]).M becomes T.M.
func FuncName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	t := fd.Recv.List[0].Type
	for {
		switch x := t.(type) {
		case *ast.StarExpr:
			t = x.X
			continue
		case *ast.ParenExpr:
			t = x.X
			continue
		case *ast.IndexExpr:
			t = x.X
			continue
		case *ast.IndexListExpr:
			t = x.X
			continue
		case *ast.Ident:
			return x.Name + "." + fd.Name.Name
		}
		return "?." + fd.Name.Name
	}
}

// Enclosing returns the name of the top-level function declaration in f
// containing pos, or "-" when pos is outside every function. Function
// literals are attributed to their enclosing declaration.
func Enclosing(f *ast.File, pos token.Pos) string {
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Pos() <= pos && pos < fd.End() {
			return FuncName(fd)
		}
	}
	return "-"
}

var nonGoSource = map[string]bool{
	".c": true, ".cc": true, ".cxx": true, ".cpp": true, ".m": true,
	".h": true, ".hh": true, ".hpp": true, ".hxx": true,
	".f": true, ".F": true, ".for": true, ".f90": true,
	".s": true, ".S": true, ".sx": true,
	".swig": true, ".swigcxx": true, ".syso": true,
}

// NonGoSource reports whether the go tool compiles or links a file with
// this name's extension into a package (assembly, cgo, Fortran, SWIG and
// .syso objects).
func NonGoSource(name string) bool {
	i := strings.LastIndexByte(name, '.')
	return i >= 0 && nonGoSource[name[i:]]
}

// SkipDir reports whether a directory name is excluded from tree walks.
func SkipDir(name string) bool {
	switch name {
	case "agent_work", ".git", "testdata", "vendor", "tools":
		return true
	}
	return false
}
