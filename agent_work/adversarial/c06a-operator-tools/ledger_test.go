package adv_test

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

type promotion struct {
	src, old, dst, name string
	split               bool
}

func promotions(t *testing.T) []promotion {
	t.Helper()
	notes := read(t, "agent_work/chunks/c06a-operator-tools/NOTES.md")
	pair := regexp.MustCompile("`([^`]+\\.go)`, `([^`]+)`")
	var rows []promotion
	for _, line := range strings.Split(notes, "\n") {
		if !strings.HasPrefix(line, "| `agent_work/adversarial/c0") || !strings.Contains(line, "| promoted") {
			continue
		}
		m := pair.FindAllStringSubmatch(line, -1)
		if len(m) < 2 {
			t.Fatalf("malformed promotion row: %s", line)
		}
		for _, dst := range m[1:] {
			rows = append(rows, promotion{m[0][1], m[0][2], dst[1], dst[2], len(m) > 2})
		}
	}
	if len(rows) < 132 {
		t.Fatalf("found only %d promoted functions", len(rows))
	}
	return rows
}
func fn(t *testing.T, path, name string) *ast.FuncDecl {
	t.Helper()
	f, e := parser.ParseFile(token.NewFileSet(), filepath.Join(root(t), path), nil, 0)
	if e != nil {
		t.Fatal(e)
	}
	for _, d := range f.Decls {
		if x, ok := d.(*ast.FuncDecl); ok && x.Name.Name == name {
			return x
		}
	}
	t.Fatalf("ledger function %s absent from %s", name, path)
	return nil
}
func TestAdv_14_AllLedgerSymbolsExist(t *testing.T) {
	t.Logf("seed=%d", seed)
	for _, p := range promotions(t) {
		fn(t, p.src, p.old)
		fn(t, p.dst, p.name)
		if strings.HasPrefix(p.dst, "agent_work/") {
			t.Fatalf("promoted test still only in agent_work: %+v", p)
		}
	}
}
func assertions(f *ast.FuncDecl) map[string]int {
	m := map[string]int{}
	ast.Inspect(f.Body, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		s, ok := c.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch s.Sel.Name {
		case "Fatal", "Fatalf", "Error", "Errorf", "Fail", "FailNow":
			var b bytes.Buffer
			printer.Fprint(&b, token.NewFileSet(), c)
			m[strings.NewReplacer("c02Shape", "shape", "c03DetailSink", "detailSink", "c04Writes", "writes", "c03Seed", "seed", "c04Seed", "seed").Replace(b.String())]++
		}
		return true
	})
	return m
}
func TestAdv_14_OriginalAssertionsRetained(t *testing.T) {
	t.Logf("seed=%d", seed)
	for _, p := range promotions(t) {
		if p.split {
			continue
		}
		a, b := assertions(fn(t, p.src, p.old)), assertions(fn(t, p.dst, p.name))
		for msg, n := range a {
			if b[msg] < n {
				t.Errorf("%s lost original assertion %s (%d -> %d)", p.name, msg, n, b[msg])
			}
		}
	}
}
func TestAdv_14_FastSetGatesExternalCampaigns(t *testing.T) {
	t.Logf("seed=%d", seed)
	files := map[string]bool{}
	for _, p := range promotions(t) {
		files[p.dst] = true
	}
	for p := range files {
		s := read(t, p)
		if strings.Contains(s, "exec.Command") && !strings.Contains(s, "testing.Short()") && !strings.Contains(s, "MG_TIER_R") && !strings.Contains(s, "os.Args[0]") {
			t.Errorf("external commands have no short/tier-R gate in %s", p)
		}
	}
	ci := read(t, "scripts/ci-fast.sh")
	if !strings.Contains(ci, "GATE_SHORT=1") {
		t.Fatal("CI does not select short tests")
	}
	if os.Getenv("ADV_LONG") == "1" {
		out, rc := command(t, nil, "/usr/local/go/bin/go", "test", "-short", "-count=1", "-run", "^TestAdv", "./internal/sim", "./internal/sim/simnet", "./internal/obs", "./internal/sim/world", "./internal/obs/jsonstats", "./internal/obs/promtext", "./internal/obs/csvflush", "./internal/obs/simrun", "./internal/pgwire")
		if rc != 0 {
			t.Fatalf("promoted fast set failed rc=%d\n%s", rc, out)
		}
	}
}
func TestAdv_15_ModuleRequirements(t *testing.T) {
	t.Logf("seed=%d", seed)
	out, rc := command(t, nil, "/usr/local/go/bin/go", "mod", "edit", "-json")
	if rc != 0 {
		t.Fatal(out)
	}
	var m struct {
		Require []struct {
			Path, Version string
			Indirect      bool
		}
	}
	if e := json.Unmarshal([]byte(out), &m); e != nil {
		t.Fatal(e)
	}
	if len(m.Require) != 1 || m.Require[0].Path != "github.com/jackc/pgx/v5" || m.Require[0].Indirect {
		t.Fatalf("requirements %+v", m.Require)
	}
}
func TestAdv_15_NoRequirementChangeFromParent(t *testing.T) {
	t.Logf("seed=%d", seed)
	out, rc := command(t, nil, "git", "show", "1285707:go.mod")
	if rc != 0 {
		t.Fatal(out)
	}
	if out != read(t, "go.mod") {
		t.Fatalf("go.mod changed from pre-chunk base:\n%s", read(t, "go.mod"))
	}
}
func TestAdv_15_OperatorImportsUseExistingModuleOnly(t *testing.T) {
	t.Logf("seed=%d", seed)
	for _, dir := range []string{"cmd/loadgen", "cmd/mgctl", "internal/loadgen", "internal/mgctl"} {
		e := filepath.WalkDir(filepath.Join(root(t), dir), func(p string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if d.IsDir() || !strings.HasSuffix(p, ".go") {
				return nil
			}
			f, e := parser.ParseFile(token.NewFileSet(), p, nil, parser.ImportsOnly)
			if e != nil {
				return e
			}
			for _, i := range f.Imports {
				path := strings.Trim(i.Path.Value, "\"")
				first := strings.Split(path, "/")[0]
				if strings.Contains(first, ".") && !strings.HasPrefix(path, "github.com/nickstrad/mini-gateway/") && !strings.HasPrefix(path, "github.com/jackc/pgx/v5/") {
					t.Errorf("new external import %s in %s", path, p)
				}
			}
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
	}
}
