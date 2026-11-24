// Command importcheck reads Go file paths on stdin and checks every import in every file,
// whatever its build tags: product files may import only the standard library and the
// project's own module; _test.go files may also import the paths given with -test-extra.
// It closes the hole a tag-guarded file would leave in a `go list -deps` check.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strconv"
	"strings"
)

func main() {
	module := flag.String("module", "", "module path of the project")
	extra := flag.String("test-extra", "", "comma-separated import paths (and their sub-packages) allowed in _test.go files")
	flag.Parse()
	if *module == "" {
		fmt.Fprintln(os.Stderr, "importcheck: -module is required")
		os.Exit(2)
	}
	var files []string
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		if p := strings.TrimSpace(sc.Text()); p != "" {
			files = append(files, p)
		}
	}
	findings, err := run(files, *module, splitList(*extra))
	if err != nil {
		fmt.Fprintln(os.Stderr, "importcheck:", err)
		os.Exit(2)
	}
	for _, f := range findings {
		fmt.Println(f)
	}
	fmt.Fprintf(os.Stderr, "importcheck: checked %d files\n", len(files))
	if len(findings) > 0 {
		os.Exit(1)
	}
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func within(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+"/")
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

func run(files []string, module string, testExtra []string) ([]string, error) {
	fset := token.NewFileSet()
	var findings []string
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			findings = append(findings, fmt.Sprintf("%s:1:1: import: cannot parse: %v", name, err))
			continue
		}
		isTest := strings.HasSuffix(name, "_test.go")
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return nil, fmt.Errorf("%s: bad import literal %s", name, imp.Path.Value)
			}
			ok := stdlib(path) || within(path, module)
			if !ok && isTest {
				for _, e := range testExtra {
					if within(path, e) {
						ok = true
					}
				}
			}
			if !ok {
				pos := fset.Position(imp.Pos())
				findings = append(findings, fmt.Sprintf("%s:%d:%d: import: %q is outside the standard library and this module", name, pos.Line, pos.Column, path))
			}
		}
	}
	sort.Strings(findings)
	return findings, nil
}
