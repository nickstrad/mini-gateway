package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportcheck(t *testing.T) {
	const module = "example.com/proj"
	cases := []struct {
		name, file, src string
		wantFinding     string
	}{
		{"stdlib only passes", "a.go", "package a\nimport (\n\"fmt\"\n\"math/rand/v2\"\n)\nvar _ = fmt.Sprint\nvar _ = rand.New\n", ""},
		{"own module passes", "a.go", "package a\nimport _ \"example.com/proj/internal/env\"\n", ""},
		{"module prefix lookalike fails", "a.go", "package a\nimport _ \"example.com/projx/evil\"\n", "example.com/projx/evil"},
		{"rapid in product file fails", "a.go", "package a\nimport _ \"pgregory.net/rapid\"\n", "pgregory.net/rapid"},
		{"rapid in test file passes", "a_test.go", "package a\nimport _ \"pgregory.net/rapid\"\n", ""},
		{"other module in test file fails", "a_test.go", "package a\nimport _ \"github.com/x/y\"\n", "github.com/x/y"},
		{"tag-guarded external import fails", "a.go", "//go:build never\n\npackage a\nimport _ \"github.com/x/y\"\n", "github.com/x/y"},
		{"aliased external import fails", "a.go", "package a\nimport z \"golang.org/x/sys/unix\"\nvar _ = z.Getpid\n", "golang.org/x/sys/unix"},
		{"cgo fails", "a.go", "package a\nimport \"C\"\n", "\"C\""},
		{"unparsable file fails", "a.go", "package\n", "cannot parse"},
		{"garbage file fails", "a.go", "}}} not go at all\n", "cannot parse"},
		{"broken import block fails", "a.go", "package a\nimport (\n\"fmt\"\n", "cannot parse"},
		{"syntax error after imports still checks imports", "a.go", "package a\nimport _ \"github.com/x/y\"\nfunc {{{ broken\n", "github.com/x/y"},
		{"syntax error after stdlib imports passes the import check", "a.go", "package a\nimport \"fmt\"\nfunc {{{ broken\n", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), c.file)
			if err := os.WriteFile(p, []byte(c.src), 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := run([]string{p}, module, []string{"pgregory.net/rapid"})
			if err != nil {
				t.Fatal(err)
			}
			if c.wantFinding == "" {
				if len(got) != 0 {
					t.Fatalf("want clean, got %v", got)
				}
				return
			}
			if len(got) == 0 || !strings.Contains(strings.Join(got, "\n"), c.wantFinding) {
				t.Fatalf("want a finding naming %q, got %v", c.wantFinding, got)
			}
		})
	}
}
