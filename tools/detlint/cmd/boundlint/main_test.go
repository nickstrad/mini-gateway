package main

import (
	"fmt"
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

var shellFindings = []string{
	"internal/shell/chans.go:25: unbuffered",
	"internal/shell/chans.go:26: unbuffered",
	"internal/shell/chans.go:27: unbuffered",
	"internal/shell/chans.go:28: chan-cap",
	"internal/shell/chans.go:29: chan-cap",
	"internal/shell/chans.go:30: chan-cap",
	"internal/shell/chans.go:31: chan-cap",
	"internal/shell/chans.go:32: chan-cap",
	"internal/shell/chans.go:46: unbuffered",
	"internal/shell/chans.go:51: unbuffered",
	"internal/shell/chans.go:59: chan-cap",
	"internal/shell/chans.go:59: unbuffered",
	"internal/shell/chans.go:63: chan-cap",
	"internal/shell/loops.go:14: unbounded-growth",
	"internal/shell/loops.go:15: unbounded-growth",
	"internal/shell/loops.go:16: unbounded-growth",
	"internal/shell/loops.go:22: unbounded-growth",
	"internal/shell/loops.go:29: unbounded-growth",
	"internal/shell/loops.go:51: unbounded-growth",
	"internal/shell/loops.go:88: unbounded-growth",
	"internal/shell/loops.go:95: unbounded-growth",
	"internal/shell/loops.go:101: unbounded-growth",
	"internal/shell/loops.go:102: unbounded-growth",
	"internal/shell/loops.go:103: unbounded-growth",
	"internal/shell/loops.go:109: unbounded-growth",
	"internal/shell/loops.go:110: unbounded-growth",
	"internal/shell/loops.go:150: unbounded-growth",
	"internal/shell/loops.go:156: unbounded-growth",
	"internal/shell/loops.go:162: unbounded-growth",
	"internal/shell/loops.go:163: unbounded-growth",
	"internal/shell/loops.go:164: unbounded-growth",
	"internal/shell/loops.go:165: unbounded-growth",
	"internal/shell/loops.go:193: unbounded-growth",
}

func TestBoundlint(t *testing.T) {
	cases := []struct {
		name  string
		allow string
		pkgs  []string
		want  []string
	}{
		{name: "shell package with the allowlist", allow: "testdata/allow-ok.txt", pkgs: []string{"internal/shell"}, want: shellFindings},
		{name: "recursion skips testdata and reaches sub", allow: "testdata/allow-ok.txt", pkgs: []string{"internal/shell/..."},
			want: append(slices.Clone(shellFindings), "internal/shell/sub/sub.go:4: chan-cap")},
		{name: "without the allowlist", pkgs: []string{"internal/shell"}, want: func() []string {
			w := append(slices.Clone(shellFindings), "internal/shell/chans.go:43: unbuffered", "internal/shell/loops.go:77: unbounded-growth")
			slices.Sort(w)
			return w
		}()},
		{name: "clean package", pkgs: []string{"internal/clean"}},
		{name: "missing package is skipped", pkgs: []string{"internal/nope", "internal/nope/..."}},
		{name: "file outside the default build", pkgs: []string{"internal/tagged"}, want: []string{"internal/tagged/b.go:1: buildtag"}},
		{name: "type error", pkgs: []string{"internal/typeerr"}, want: []string{"internal/typeerr/a.go:4: chan-cap", "internal/typeerr/a.go:4: typecheck"}},
		{name: "bad allowlist", allow: "testdata/allow-bad.txt", pkgs: []string{"internal/clean", "internal/shell"}, want: func() []string {
			w := append(slices.Clone(shellFindings), "internal/shell/loops.go:77: unbounded-growth",
				"testdata/allow-bad.txt:2: allow-format", "testdata/allow-bad.txt:3: stale-allow",
				"testdata/allow-bad.txt:4: allow-format", "testdata/allow-bad.txt:5: allow-format")
			slices.Sort(w)
			return w
		}()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := run("testdata/mod", c.allow, c.pkgs)
			if err != nil {
				t.Fatal(err)
			}
			got := short(res.findings)
			want := slices.Clone(c.want)
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("got\n%s\nwant\n%s\nfindings: %v", strings.Join(got, "\n"), strings.Join(want, "\n"), res.findings)
			}
		})
	}
}

func TestCounts(t *testing.T) {
	res, err := run("testdata/mod", "", []string{"internal/shell/...", "internal/clean"})
	if err != nil {
		t.Fatal(err)
	}
	if res.pkgs != 3 || res.files != 4 {
		t.Errorf("checked %d files in %d packages, want 4 in 3", res.files, res.pkgs)
	}
}

func TestExitCodes(t *testing.T) {
	cases := []struct {
		args []string
		want int
	}{
		{[]string{"-root", "testdata/mod", "internal/clean"}, 0},
		{[]string{"-root", "testdata/mod", "internal/nope"}, 0},
		{[]string{"-root", "testdata/mod", "internal/shell"}, 1},
		{[]string{"-root", "testdata/mod"}, 2},
		{[]string{"internal/clean"}, 2},
		{[]string{"-root", "testdata/missing", "internal/clean"}, 2},
		{[]string{"-root", "testdata/mod", "-allow", "testdata/missing.txt", "internal/clean"}, 2},
	}
	for _, c := range cases {
		if got := mainArgs(c.args); got != c.want {
			t.Errorf("mainArgs(%q) = %d, want %d", c.args, got, c.want)
		}
	}
}
