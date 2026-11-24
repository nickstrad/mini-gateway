package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"detlint/internal/report"
)

func short(fs []report.Finding) []string {
	report.Sort(fs)
	var out []string
	for _, f := range fs {
		out = append(out, fmt.Sprintf("%s:%d: %s", f.Path, f.Line, f.Rule))
	}
	return out
}

func TestImportRules(t *testing.T) {
	fs, files, err := run("testdata/mod", "testdata/rules.txt")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"internal/other/o.go:4: third-party",
		"internal/other/o.go:5: third-party",
		"internal/other/o.go:6: third-party",
		"internal/other/o.go:7: only",
		"internal/other/o_test.go:3: third-party",
		"internal/pool/pool.go:5: deny",
		"internal/pool/pool.go:6: deny",
		"internal/pool/pool.go:7: deny",
		"internal/pool/pool.go:7: only",
		"internal/pool/pool_test.go:4: deny",
		"internal/pool/tagged.go:5: deny",
		"internal/pool/testdata/hidden/h.go:3: deny",
		"internal/proxy/p.go:5: deny",
		"internal/proxy/reach.go:3: testdata-import",
		"internal/tools:1: layout",
		"sub/go.mod:1: nested-module",
	}
	if got := short(fs); !slices.Equal(got, want) {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if files != 13 {
		t.Errorf("checked %d files, want 13", files)
	}
}

func TestPatterns(t *testing.T) {
	const m = "example.test/gw"
	cases := []struct {
		words []string
		path  string
		want  bool
	}{
		{[]string{"internal/pool"}, m + "/internal/pool", true},
		{[]string{"internal/pool"}, m + "/internal/pool/sub", false},
		{[]string{"internal/pool"}, m + "/internal/pooled", false},
		{[]string{"internal/pool/..."}, m + "/internal/pool", true},
		{[]string{"internal/pool/..."}, m + "/internal/pool/sub", true},
		{[]string{"internal/pool/..."}, m + "/internal/pooled", false},
		{[]string{"internal/obs/...", "!internal/obs"}, m + "/internal/obs", false},
		{[]string{"internal/obs/...", "!internal/obs"}, m + "/internal/obs/csv", true},
		{[]string{"."}, m, true},
		{[]string{"."}, m + "/cmd", false},
		{[]string{"..."}, m + "/cmd/x", true},
		{[]string{"github.com/a/b"}, "github.com/a/b", true},
		{[]string{"github.com/a/b"}, "github.com/a/b/c", false},
		{[]string{"github.com/a/b/..."}, "github.com/a/b/c", true},
		{[]string{"internal/pool"}, "internal/pool", false},
	}
	for _, c := range cases {
		if got := parseSet(c.words, m).match(c.path); got != c.want {
			t.Errorf("%q match %q = %v, want %v", c.words, c.path, got, c.want)
		}
	}
}

func writeTree(t *testing.T, gomod, rules string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	rp := filepath.Join(t.TempDir(), "rules.txt")
	if err := os.WriteFile(rp, []byte(rules), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, rp
}

func TestGoMod(t *testing.T) {
	const head = "module example.test/gw\n\ngo 1.26\n"
	const withPgx = "module example.test/gw\nrequire github.com/jackc/pgx/v5\n"
	const noReq = "module example.test/gw\n"
	cases := []struct {
		name  string
		gomod string
		rules string
		want  []string
	}{
		{"no requirements", head, withPgx, nil},
		{"no requirements, none allowed", head, noReq, nil},
		{"the one requirement", head + "\nrequire github.com/jackc/pgx/v5 v5.13.0\n", withPgx, nil},
		{"toolchain line passes", head + "toolchain go1.26.8\n", withPgx, nil},
		{"the one requirement, none allowed", head + "\nrequire github.com/jackc/pgx/v5 v5.13.0\n", noReq, []string{"go.mod:5: gomod"}},
		{"a second requirement", head + "\nrequire (\n\tgithub.com/google/uuid v1.6.0\n\tgithub.com/jackc/pgx/v5 v5.13.0\n)\n", withPgx, []string{"go.mod:6: gomod"}},
		{"indirect beside the direct one", head + "\nrequire github.com/jackc/pgx/v5 v5.13.0\n\nrequire (\n\tgolang.org/x/text v0.29.0 // indirect\n)\n", withPgx, nil},
		{"indirect alone", head + "\nrequire golang.org/x/text v0.29.0 // indirect\n", withPgx, []string{"go.mod:5: gomod"}},
		{"replace line", head + "\nreplace github.com/jackc/pgx/v5 => ../pgx\n", withPgx, []string{"go.mod:5: gomod"}},
		{"replace block", head + "\nreplace (\n\tgithub.com/jackc/pgx/v5 => ../pgx\n)\n", withPgx, []string{"go.mod:5: gomod"}},
		{"tool directive", head + "\ntool example.org/x/cmd/y\n", withPgx, []string{"go.mod:5: gomod"}},
		{"wrong module", "module example.test/other\n\ngo 1.26\n", withPgx, []string{"go.mod:1: gomod"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, rp := writeTree(t, c.gomod, c.rules)
			fs, _, err := run(root, rp)
			if err != nil {
				t.Fatal(err)
			}
			if got := short(fs); !slices.Equal(got, c.want) {
				t.Errorf("got %q want %q (%v)", got, c.want, fs)
			}
		})
	}
}

func TestUnparsableFiles(t *testing.T) {
	root, rp := writeTree(t, "module example.test/gw\n\ngo 1.26\n", "module example.test/gw\n")
	for name, src := range map[string]string{
		"broken.go": "package x\n\nimport _ \"example.org/seen\"\n\nfunc broken( {\n",
		"nopkg.go":  "import _ \"example.org/unseen\"\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fs, _, err := run(root, rp)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"broken.go:3: third-party", "nopkg.go:1: parse"}
	if got := short(fs); !slices.Equal(got, want) {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestWorkspaceAndVendor(t *testing.T) {
	root, rp := writeTree(t, "module example.test/gw\n\ngo 1.26\n", "module example.test/gw\n")
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "vendor"), 0o755); err != nil {
		t.Fatal(err)
	}
	fs, _, err := run(root, rp)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"go.work:1: gomod", "vendor:1: gomod"}
	if got := short(fs); !slices.Equal(got, want) {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestBadRulesAreErrors(t *testing.T) {
	for _, rules := range []string{
		"",
		"deny a : b\n",
		"module m\nmodule n\n",
		"module m\ndeny a b\n",
		"module m\ndeny : b\n",
		"module m\ndeny a :\n",
		"module m\nonly !a : b\n",
		"module m\nforbid a : b\n",
		"module m\nrequire\n",
	} {
		root, rp := writeTree(t, "module m\n", rules)
		if _, _, err := run(root, rp); err == nil {
			t.Errorf("rules %q: no error", rules)
		}
	}
}

func TestExitCodes(t *testing.T) {
	clean, rp := writeTree(t, "module example.test/gw\n\ngo 1.26\n", "module example.test/gw\n")
	cases := []struct {
		args []string
		want int
	}{
		{[]string{"-root", clean, "-rules", rp}, 0},
		{[]string{"-root", "testdata/mod", "-rules", "testdata/rules.txt"}, 1},
		{[]string{"-root", clean}, 2},
		{[]string{"-rules", rp}, 2},
		{[]string{"-root", clean, "-rules", rp, "extra"}, 2},
		{[]string{"-root", clean, "-rules", "testdata/missing.txt"}, 2},
		{[]string{"-root", "testdata/missing", "-rules", rp}, 2},
	}
	for _, c := range cases {
		if got := mainArgs(c.args); got != c.want {
			t.Errorf("mainArgs(%q) = %d, want %d", c.args, got, c.want)
		}
	}
}
