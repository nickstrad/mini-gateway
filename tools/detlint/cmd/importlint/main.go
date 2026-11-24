// Command importlint enforces the project's import rules and the go.mod requirement rule.
//
//	importlint -root <module root> -rules <rules file>
//
// The rules file is documented in README-tools.txt (section 6) and at the top of
// tools/lint-rules/imports.txt. Exit status: 0 clean, 1 findings, 2 usage or internal error.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"detlint/internal/report"
)

func main() {
	os.Exit(mainArgs(os.Args[1:]))
}

func mainArgs(args []string) int {
	fl := flag.NewFlagSet("importlint", flag.ContinueOnError)
	root := fl.String("root", "", "module root directory (contains go.mod)")
	rulesPath := fl.String("rules", "", "rules file")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if *root == "" || *rulesPath == "" || fl.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: importlint -root <module root> -rules <rules file>")
		return 2
	}
	fs, files, err := run(*root, *rulesPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "importlint:", err)
		return 2
	}
	report.Print(os.Stdout, fs)
	fmt.Fprintf(os.Stderr, "importlint: checked %d files\n", files)
	return report.ExitCode(fs, nil)
}

// pattern is one package pattern of the rules file. A pattern whose first path element
// contains a dot is a full import path; any other pattern is a directory of this module,
// relative to its root ("." is the root package). A trailing "/..." also matches everything
// below.
type pattern struct {
	path string
	tree bool
}

func parsePattern(s, module string) pattern {
	p := pattern{path: s}
	if rest, ok := strings.CutSuffix(s, "/..."); ok {
		p.path, p.tree = rest, true
	} else if s == "..." {
		p.path, p.tree = ".", true
	}
	first, _, _ := strings.Cut(p.path, "/")
	if !strings.Contains(first, ".") || p.path == "." {
		if p.path == "." {
			p.path = module
		} else {
			p.path = module + "/" + p.path
		}
	}
	return p
}

func (p pattern) match(importPath string) bool {
	if importPath == p.path {
		return true
	}
	return p.tree && strings.HasPrefix(importPath, p.path+"/")
}

// set is a list of patterns with exclusions (written "!pattern" in the rules file).
type set struct {
	in, out []pattern
}

func parseSet(words []string, module string) set {
	var s set
	for _, w := range words {
		if rest, ok := strings.CutPrefix(w, "!"); ok {
			s.out = append(s.out, parsePattern(rest, module))
		} else {
			s.in = append(s.in, parsePattern(w, module))
		}
	}
	return s
}

func (s set) match(importPath string) bool {
	for _, p := range s.out {
		if p.match(importPath) {
			return false
		}
	}
	for _, p := range s.in {
		if p.match(importPath) {
			return true
		}
	}
	return false
}

type rule struct {
	line  int
	left  set
	right set
	text  string
}

type rules struct {
	module  string
	deny    []rule // left: importing packages, right: forbidden imports
	only    []rule // left: imports, right: the only packages that may import them
	require []string
}

// readRules parses the rules file. A malformed file is an error (exit 2), not a finding:
// a gate must not run on rules it could not read.
func readRules(path string) (*rules, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := &rules{}
	type raw struct {
		line        int
		kind        string
		left, right []string
		text        string
	}
	var raws []raw
	sc := bufio.NewScanner(f)
	ln := 0
	for sc.Scan() {
		ln++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		words := strings.Fields(line)
		switch words[0] {
		case "module":
			if len(words) != 2 || r.module != "" {
				return nil, fmt.Errorf("%s:%d: want exactly one line `module <path>`", path, ln)
			}
			r.module = words[1]
		case "require":
			if len(words) != 2 {
				return nil, fmt.Errorf("%s:%d: want `require <module path>`", path, ln)
			}
			r.require = append(r.require, words[1])
		case "deny", "only":
			colon := -1
			for i, w := range words {
				if w == ":" {
					colon = i
					break
				}
			}
			if colon < 2 || colon == len(words)-1 {
				return nil, fmt.Errorf("%s:%d: want `%s <pattern>... : <pattern>...`", path, ln, words[0])
			}
			raws = append(raws, raw{ln, words[0], words[1:colon], words[colon+1:], line})
		default:
			return nil, fmt.Errorf("%s:%d: unknown directive %q", path, ln, words[0])
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if r.module == "" {
		return nil, fmt.Errorf("%s: no `module <path>` line", path)
	}
	for _, x := range raws {
		ru := rule{line: x.line, left: parseSet(x.left, r.module), right: parseSet(x.right, r.module), text: x.text}
		if len(ru.left.in) == 0 || len(ru.right.in) == 0 {
			return nil, fmt.Errorf("%s:%d: each side needs at least one pattern that is not an exclusion", path, x.line)
		}
		if x.kind == "deny" {
			r.deny = append(r.deny, ru)
		} else {
			r.only = append(r.only, ru)
		}
	}
	return r, nil
}

// stdlib reports whether an import path belongs to the standard library: its first
// element has no dot. "C" (cgo) is not accepted.
func stdlib(path string) bool {
	if path == "C" || path == "" {
		return false
	}
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}

// skipDir names the directories below the root that hold no product or test code of the
// root module.
func skipDir(name string) bool {
	return report.SkipDir(name) || name == "_site"
}

func run(root, rulesPath string) ([]report.Finding, int, error) {
	r, err := readRules(rulesPath)
	if err != nil {
		return nil, 0, err
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return nil, 0, err
	}
	out, err := checkGoMod(root, r)
	if err != nil {
		return nil, 0, err
	}
	for _, name := range []string{"go.work", "vendor"} {
		if _, err := os.Lstat(filepath.Join(root, name)); err == nil {
			out = append(out, report.Finding{Path: name, Line: 1, Col: 1, Rule: "gomod", Msg: name + " is present: a workspace or vendor tree can bring in code no check reads"})
		}
	}
	fset := token.NewFileSet()
	files := 0
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if p != root && skipDir(d.Name()) {
				if strings.Contains(rel, "/") && d.Name() != "testdata" && d.Name() != ".git" {
					out = append(out, report.Finding{Path: rel, Line: 1, Col: 1, Rule: "layout", Msg: "a directory named " + d.Name() + " below the top level is skipped by the comment, panic and import checks; rename it"})
				}
				return filepath.SkipDir
			}
			if p != root {
				if _, err := os.Stat(filepath.Join(p, "go.mod")); err == nil {
					out = append(out, report.Finding{Path: rel + "/go.mod", Line: 1, Col: 1, Rule: "nested-module", Msg: "a nested module hides its code from every check; only tools/ and agent_work/ may hold one"})
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		files++
		pkg := r.module
		if dir := filepath.ToSlash(filepath.Dir(rel)); dir != "." {
			pkg = r.module + "/" + dir
		}
		f, perr := parser.ParseFile(fset, p, nil, parser.ImportsOnly)
		if perr != nil && (f == nil || f.Name == nil) {
			out = append(out, report.Finding{Path: rel, Line: 1, Col: 1, Rule: "parse", Msg: perr.Error()})
			return nil
		}
		if perr != nil {
			out = append(out, report.Finding{Path: rel, Line: 1, Col: 1, Rule: "parse", Msg: perr.Error()})
		}
		for _, imp := range f.Imports {
			path, uerr := strconv.Unquote(imp.Path.Value)
			if uerr != nil {
				return fmt.Errorf("%s: bad import literal %s", rel, imp.Path.Value)
			}
			pos := fset.Position(imp.Pos())
			add := func(ruleID, msg string) {
				out = append(out, report.Finding{Path: rel, Line: pos.Line, Col: pos.Column, Rule: ruleID, Msg: msg})
			}
			for _, ru := range r.deny {
				if ru.left.match(pkg) && ru.right.match(path) {
					add("deny", fmt.Sprintf("%q may not be imported here (rules line %d: %s)", path, ru.line, ru.text))
				}
			}
			named := false
			for _, ru := range r.only {
				if !ru.left.match(path) {
					continue
				}
				named = true
				if !ru.right.match(pkg) {
					add("only", fmt.Sprintf("%q may be imported only by the packages of rules line %d: %s", path, ru.line, ru.text))
				}
			}
			local := path == r.module || strings.HasPrefix(path, r.module+"/")
			if !named && !local && !stdlib(path) {
				add("third-party", fmt.Sprintf("%q is outside the standard library and this module, and no `only` rule names it", path))
			}
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return out, files, nil
}

// checkGoMod applies the requirement rule to root/go.mod:
//   - the module line equals the rules file's module line;
//   - every direct requirement (a require line without "// indirect") is named by a
//     `require` line of the rules file;
//   - an "// indirect" requirement is accepted only when at least one direct requirement
//     is present (whether `go mod tidy` would write it is the gate's `go mod tidy -diff`);
//   - no replace, exclude, tool, ignore or godebug directive.
func checkGoMod(root string, r *rules) ([]report.Finding, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, err
	}
	var out []report.Finding
	add := func(line int, msg string) {
		out = append(out, report.Finding{Path: "go.mod", Line: line, Col: 1, Rule: "gomod", Msg: msg})
	}
	allowed := map[string]bool{}
	for _, m := range r.require {
		allowed[m] = true
	}
	type req struct {
		line     int
		path     string
		indirect bool
	}
	var reqs []req
	module := ""
	block := ""
	for i, raw := range strings.Split(string(data), "\n") {
		ln := i + 1
		code, comment, _ := strings.Cut(raw, "//")
		words := strings.Fields(code)
		if len(words) == 0 {
			continue
		}
		if block != "" {
			if words[0] == ")" {
				block = ""
				continue
			}
			words = append([]string{block}, words...)
		} else if len(words) == 2 && words[1] == "(" {
			block = words[0]
			switch block {
			case "require":
			case "replace", "exclude", "tool", "ignore", "godebug", "retract":
				add(ln, "go.mod may not have a "+block+" directive")
			default:
				add(ln, "unexpected go.mod block "+block)
			}
			continue
		}
		switch words[0] {
		case "module":
			if len(words) == 2 {
				module = strings.Trim(words[1], "\"")
			}
		case "go", "toolchain":
		case "require":
			if len(words) != 3 {
				add(ln, "cannot read this require line")
				continue
			}
			reqs = append(reqs, req{ln, strings.Trim(words[1], "\""), strings.TrimSpace(comment) == "indirect"})
		case "replace", "exclude", "tool", "ignore", "godebug", "retract":
			if block == "" {
				add(ln, "go.mod may not have a "+words[0]+" directive")
			}
		default:
			add(ln, "unexpected go.mod line: "+strings.TrimSpace(code))
		}
	}
	if module != r.module {
		add(1, fmt.Sprintf("go.mod declares module %q, the rules file says %q", module, r.module))
	}
	direct := 0
	for _, q := range reqs {
		if q.indirect {
			continue
		}
		direct++
		if !allowed[q.path] {
			want := "none"
			if len(r.require) > 0 {
				want = strings.Join(r.require, ", ")
			}
			add(q.line, fmt.Sprintf("requirement %s is not allowed (allowed direct requirements: %s)", q.path, want))
		}
	}
	for _, q := range reqs {
		if q.indirect && direct == 0 {
			add(q.line, fmt.Sprintf("indirect requirement %s with no direct requirement to force it", q.path))
		}
	}
	return out, nil
}
