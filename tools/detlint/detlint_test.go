package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// short renders a finding as path:line: rule for comparison.
func shortFindings(t *testing.T, cfg config) []string {
	t.Helper()
	res, err := run(cfg)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	var out []string
	for _, f := range res.findings {
		out = append(out, fmt.Sprintf("%s:%d: %s", f.Path, f.Line, f.Rule))
	}
	return out
}

func TestDetlint(t *testing.T) {
	cases := []struct {
		name        string
		pkgs        []string
		tests       bool
		allow       string
		allowImport []string
		want        []string
	}{
		// wallclock
		{name: "time aliased import", pkgs: []string{"wallclock/alias"},
			want: []string{"wallclock/alias/a.go:6: wallclock"}},
		{name: "time dot import", pkgs: []string{"wallclock/dot"},
			want: []string{"wallclock/dot/a.go:6: wallclock"}},
		{name: "time.Now method value", pkgs: []string{"wallclock/methodvalue"},
			want: []string{"wallclock/methodvalue/a.go:6: wallclock"}},
		{name: "time.Now passed as argument", pkgs: []string{"wallclock/funcarg"},
			want: []string{"wallclock/funcarg/a.go:10: wallclock"}},
		{name: "context.WithTimeout", pkgs: []string{"wallclock/ctxtimeout"},
			want: []string{"wallclock/ctxtimeout/a.go:4: import", "wallclock/ctxtimeout/a.go:9: wallclock"}},
		{name: "time.Duration arithmetic passes", pkgs: []string{"clean/duration"}},
		{name: "local module import resolves", pkgs: []string{"clean/localimport"}},

		// globalrand / cryptorand
		{name: "global rand via aliased math/rand/v2", pkgs: []string{"globalrand/aliasv2"},
			want: []string{"globalrand/aliasv2/a.go:6: globalrand", "globalrand/aliasv2/a.go:10: globalrand"}},
		{name: "global rand math/rand v1", pkgs: []string{"globalrand/v1"},
			want: []string{"globalrand/v1/a.go:3: import", "globalrand/v1/a.go:6: globalrand"}},
		{name: "seeded *rand.Rand methods pass", pkgs: []string{"clean/seeded"}},
		{name: "crypto/rand", pkgs: []string{"cryptorand"},
			want: []string{"cryptorand/a.go:3: import"}},

		// concurrency
		{name: "select", pkgs: []string{"concurrency/selectstmt"},
			want: []string{"concurrency/selectstmt/a.go:4: concurrency"}},
		{name: "go statement", pkgs: []string{"concurrency/gostmt"},
			want: []string{"concurrency/gostmt/a.go:6: concurrency"}},
		{name: "channel make send receive close", pkgs: []string{"concurrency/channel"},
			want: []string{
				"concurrency/channel/a.go:4: concurrency",
				"concurrency/channel/a.go:5: concurrency",
				"concurrency/channel/a.go:6: concurrency",
				"concurrency/channel/a.go:7: concurrency",
			}},
		{name: "sync/atomic", pkgs: []string{"concurrency/atomic"},
			want: []string{"concurrency/atomic/a.go:3: import", "concurrency/atomic/a.go:5: concurrency", "concurrency/atomic/a.go:8: concurrency"}},
		{name: "sync.Mutex", pkgs: []string{"concurrency/mutex"},
			want: []string{
				"concurrency/mutex/a.go:3: import",
				"concurrency/mutex/a.go:6: concurrency",
				"concurrency/mutex/a.go:11: concurrency",
				"concurrency/mutex/a.go:13: concurrency",
			}},

		// procstate
		{name: "hash/maphash", pkgs: []string{"procstate/maphash"},
			want: []string{"procstate/maphash/a.go:3: import"}},
		{name: "os.Getpid", pkgs: []string{"procstate/getpid"},
			want: []string{"procstate/getpid/a.go:3: import", "procstate/getpid/a.go:6: procstate"}},
		{name: "unsafe runtime reflect", pkgs: []string{"procstate/misc"},
			want: []string{
				"procstate/misc/a.go:4: import",
				"procstate/misc/a.go:5: import",
				"procstate/misc/a.go:6: import",
				"procstate/misc/a.go:10: procstate",
				"procstate/misc/a.go:14: procstate",
			}},

		// realio
		{name: "os file function", pkgs: []string{"realio/osfile"},
			want: []string{"realio/osfile/a.go:3: import", "realio/osfile/a.go:6: realio"}},
		{name: "net import", pkgs: []string{"realio/netimport"},
			want: []string{"realio/netimport/a.go:3: import"}},

		// ptrfmt
		{name: "%p in format", pkgs: []string{"ptrfmt/pos"},
			want: []string{"ptrfmt/pos/a.go:11: ptrfmt", "ptrfmt/pos/a.go:15: ptrfmt"}},
		{name: "no %p passes", pkgs: []string{"ptrfmt/clean"}},
		{name: "address-printing argument types and fmt values", pkgs: []string{"ptrfmt/types"},
			want: []string{
				"ptrfmt/types/a.go:15: ptrfmt",
				"ptrfmt/types/a.go:17: ptrfmt",
				"ptrfmt/types/a.go:19: ptrfmt",
				"ptrfmt/types/a.go:21: ptrfmt",
				"ptrfmt/types/a.go:23: ptrfmt",
				"ptrfmt/types/a.go:27: ptrfmt",
				"ptrfmt/types/a.go:30: ptrfmt",
				"ptrfmt/types/a.go:32: ptrfmt",
				"ptrfmt/types/a.go:34: ptrfmt",
				"ptrfmt/types/a.go:38: ptrfmt",
			}},

		// maprange
		{name: "map range feeding output", pkgs: []string{"maprange/output"},
			want: []string{"maprange/output/a.go:7: maprange", "maprange/output/a.go:15: maprange"}},
		{name: "collect without sort", pkgs: []string{"maprange/unsortedcollect"},
			want: []string{"maprange/unsortedcollect/a.go:5: maprange"}},
		{name: "maps.Keys iteration", pkgs: []string{"maprange/mapskeys"},
			want: []string{"maprange/mapskeys/a.go:6: maprange"}},
		{name: "range assigning to an outer variable leaks the last key", pkgs: []string{"maprange/outervar"},
			want: []string{
				"maprange/outervar/a.go:8: maprange",
				"maprange/outervar/a.go:17: maprange",
				"maprange/outervar/a.go:24: maprange",
				"maprange/outervar/a.go:33: maprange",
			}},
		{name: "collect and sort passes", pkgs: []string{"clean/sorted"}},
		{name: "comparator sorts do not accept a map range", pkgs: []string{"maprange/comparator"},
			want: []string{
				"maprange/comparator/a.go:18: maprange",
				"maprange/comparator/a.go:27: maprange",
				"maprange/comparator/a.go:36: maprange",
				"maprange/comparator/a.go:44: maprange",
			}},
		{name: "sort not immediately after, two slices, other slice, func literal", pkgs: []string{"maprange/notimmediate"},
			want: []string{
				"maprange/notimmediate/a.go:10: maprange",
				"maprange/notimmediate/a.go:21: maprange",
				"maprange/notimmediate/a.go:32: maprange",
				"maprange/notimmediate/a.go:42: maprange",
			}},

		// float
		{name: "float in a decision", pkgs: []string{"float/decision"},
			want: []string{"float/decision/a.go:4: float", "float/decision/a.go:5: float"}},
		{name: "float field", pkgs: []string{"float/field"},
			want: []string{"float/field/a.go:4: float", "float/field/a.go:8: float"}},
		{name: "type parameters admitting floats", pkgs: []string{"float/typeparam"},
			want: []string{
				"float/typeparam/a.go:5: float",
				"float/typeparam/a.go:7: float",
				"float/typeparam/a.go:9: float",
				"float/typeparam/a.go:11: float",
				"float/typeparam/a.go:19: float",
			}},
		{name: "integer, comparable and any type parameters pass", pkgs: []string{"clean/generic"}},

		// import allowlist
		{name: "blank import outside the allowlist", pkgs: []string{"import/blank"},
			want: []string{"import/blank/a.go:4: import"}},
		{name: "dot import outside the allowlist", pkgs: []string{"import/dot"},
			want: []string{"import/dot/a.go:3: import"}},
		{name: "import finding allowlisted with func -", pkgs: []string{"import/blank"}, allow: "testdata/allow-import.txt"},
		{name: "-allow-import extends the list", pkgs: []string{"import/..."}, allowImport: []string{"os", "path/filepath"}},

		// buildtag
		{name: "go:build file is reported and still linted", pkgs: []string{"buildtag/constraint"},
			want: []string{"buildtag/constraint/a.go:1: buildtag", "buildtag/constraint/a.go:7: wallclock"}},
		{name: "GOOS/GOARCH file name suffixes, test file included", pkgs: []string{"buildtag/suffix"},
			want: []string{
				"buildtag/suffix/a_linux.go:1: buildtag",
				"buildtag/suffix/b_amd64.go:1: buildtag",
				"buildtag/suffix/c_windows_arm64.go:1: buildtag",
				"buildtag/suffix/d_darwin_test.go:1: buildtag",
			}},
		{name: "non-Go source files", pkgs: []string{"buildtag/nongo"},
			want: []string{
				"buildtag/nongo/x.s:1: buildtag",
				"buildtag/nongo/y.c:1: buildtag",
				"buildtag/nongo/z.syso:1: buildtag",
			}},
		{name: "import C", pkgs: []string{"buildtag/cgo"},
			want: []string{"buildtag/cgo/a.go:3: buildtag", "buildtag/cgo/a.go:3: import", "buildtag/cgo/a.go:3: typecheck"}},
		{name: "underscore and dot files", pkgs: []string{"buildtag/invisible"},
			want: []string{"buildtag/invisible/.c.go:1: buildtag", "buildtag/invisible/_b.go:1: buildtag"}},
		{name: "constraint in a test file without -tests", pkgs: []string{"buildtag/testtag"},
			want: []string{"buildtag/testtag/a_test.go:1: buildtag"}},
		{name: "os-like names that are not suffixes pass", pkgs: []string{"clean/names"}},

		// allowlist
		{name: "unallowed finding", pkgs: []string{"allowed"},
			want: []string{"allowed/a.go:5: maprange"}},
		{name: "allowlisted finding passes", pkgs: []string{"allowed"}, allow: "testdata/allow-ok.txt"},
		{name: "stale allowlist entry fails", pkgs: []string{"allowed"}, allow: "testdata/allow-stale.txt",
			want: []string{"testdata/allow-stale.txt:2: stale-allow"}},
		{name: "empty justification fails", pkgs: []string{"allowed"}, allow: "testdata/allow-empty.txt",
			want: []string{"allowed/a.go:5: maprange", "testdata/allow-empty.txt:1: allow-format"}},

		// loading
		{name: "type error is a finding", pkgs: []string{"typeerr"},
			want: []string{"typeerr/a.go:4: typecheck"}},
		{name: "missing dir skipped", pkgs: []string{"does/not/exist"}},
		{name: "test files skipped by default", pkgs: []string{"withtests"}},
		{name: "test files with -tests", pkgs: []string{"withtests"}, tests: true,
			want: []string{
				"withtests/a_test.go:9: wallclock",
				"withtests/x_test.go:11: concurrency",
				"withtests/x_test.go:12: concurrency",
			}},
		{name: "recursive clean tree", pkgs: []string{"clean/..."}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := shortFindings(t, config{root: "testdata/mod", pkgs: c.pkgs, tests: c.tests, allow: c.allow, allowImports: c.allowImport})
			if !slices.Equal(got, c.want) {
				t.Errorf("findings:\n got %q\nwant %q", got, c.want)
			}
		})
	}
}

// A file with only a "// +build" line is not gofmt-clean (gofmt adds the
// //go:build line), so this fixture is written at test time.
func TestPlusBuildOnly(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":  "module example.com/pb\n\ngo 1.26\n",
		"p/a.go":  "// +build ignore\n\npackage p\n",
		"p/b.go":  "package p\n",
		"q/a.go":  "package q\n\n// +build is only a constraint before the package clause, but any line is reported\n",
		"ok/a.go": "package ok\n\n// build notes, not a constraint\nvar X = 1\n",
		"ok/b.go": "package ok\n\n//go:embed is not a constraint\nvar Y = 2\n",
	}
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		pkg  string
		want []string
	}{
		{"p", []string{"p/a.go:1: buildtag"}},
		{"q", []string{"q/a.go:3: buildtag"}},
		{"ok", nil},
	}
	for _, c := range cases {
		got := shortFindings(t, config{root: root, pkgs: []string{c.pkg}})
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: got %q want %q", c.pkg, got, c.want)
		}
	}
}

func TestDetlintCounts(t *testing.T) {
	res, err := run(config{root: "testdata/mod", pkgs: []string{"clean/...", "nope"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.pkgs != 6 || res.files != 7 {
		t.Errorf("got %d files in %d packages, want 7 in 6", res.files, res.pkgs)
	}
}

func TestDetlintExitCodes(t *testing.T) {
	cases := []struct {
		args []string
		want int
	}{
		{[]string{"-root", "testdata/mod", "clean/..."}, 0},
		{[]string{"-root", "testdata/mod", "wallclock/alias"}, 1},
		{[]string{"-root", "testdata/mod", "import/..."}, 1},
		{[]string{"-root", "testdata/mod", "-allow-import", "os, path/filepath", "import/..."}, 0},
		{[]string{"-root", "testdata/mod"}, 2},
		{[]string{"-root", "testdata/nomodule", "x"}, 2},
	}
	for _, c := range cases {
		if got := mainArgs(c.args); got != c.want {
			t.Errorf("mainArgs(%q) = %d, want %d", c.args, got, c.want)
		}
	}
}

func TestHasPtrVerb(t *testing.T) {
	cases := map[string]bool{
		"%p": true, "%-8p": true, "%[1]p": true, "%#p": true, "x %d %p": true,
		"%%p": false, "%v": false, "%5.2s": false, "p%": false, "": false, "%%%p": true,
	}
	for in, want := range cases {
		if got := hasPtrVerb(in); got != want {
			t.Errorf("hasPtrVerb(%q) = %v, want %v", in, got, want)
		}
	}
}
