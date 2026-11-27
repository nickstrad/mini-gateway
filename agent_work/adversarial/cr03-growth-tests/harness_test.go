package adv_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var root, home, scratch, current, baseline string

const seed = 730327

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func write(path string, b []byte) { must(os.WriteFile(path, b, 0600)) }
func TestMain(m *testing.M) {
	home, _ = os.Getwd()
	root = filepath.Clean(filepath.Join(home, "../../.."))
	var errTemp error
	scratch, errTemp = os.MkdirTemp(home, ".scratch-")
	must(errTemp)
	defer os.RemoveAll(scratch)
	src, err := os.ReadFile(filepath.Join(home, "probe.txt"))
	must(err)
	write(filepath.Join(scratch, "main.go"), src)
	bridge, err := os.ReadFile(filepath.Join(home, "bridge.txt"))
	must(err)
	current = build("current", bridge, nil)
	replacements := map[string][]byte{}
	for _, name := range []string{"lex.go", "classify.go"} {
		cmd := exec.Command("git", "show", "4cf67c6:internal/pgwire/"+name)
		cmd.Dir = root
		b, err := cmd.Output()
		must(err)
		replacements[name] = b
	}
	baseline = build("baseline", []byte("package pgwire\nfunc AdvCounts([]byte) [5]int {return [5]int{}}\n"), replacements)
	code := m.Run()
	os.RemoveAll(scratch)
	os.Exit(code)
}
func build(name string, bridge []byte, replacements map[string][]byte) string {
	dir := filepath.Join(scratch, name)
	must(os.MkdirAll(dir, 0700))
	replace := map[string]string{}
	write(filepath.Join(dir, "bridge.go"), bridge)
	replace[filepath.Join(root, "internal/pgwire/zz_adv_bridge.go")] = filepath.Join(dir, "bridge.go")
	for file, b := range replacements {
		dst := filepath.Join(dir, file)
		write(dst, b)
		replace[filepath.Join(root, "internal/pgwire", file)] = dst
	}
	b, err := json.Marshal(map[string]any{"Replace": replace})
	must(err)
	overlay := filepath.Join(dir, "overlay.json")
	write(overlay, b)
	bin := filepath.Join(dir, "probe")
	cmd := exec.Command("/usr/local/go/bin/go", "build", "-overlay", overlay, "-o", bin, filepath.Join(scratch, "main.go"))
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	write(filepath.Join(dir, "build.log"), out)
	if err != nil {
		panic(fmt.Sprintf("build %s: %s %v", name, out, err))
	}
	return bin
}

type answer struct {
	Counts   [5]int
	Tokens   json.RawMessage
	Clean    bool
	Unit     json.RawMessage
	Share    bool
	Snapshot json.RawMessage
	Alloc    [3]float64
}

func run(t *testing.T, bin string, inputs []string) []answer {
	t.Helper()
	var in bytes.Buffer
	for i, s := range inputs {
		must(json.NewEncoder(&in).Encode(struct {
			SQL []byte
			Tx  bool
		}{[]byte(s), i%2 == 1}))
	}
	cmd := exec.Command(bin)
	cmd.Stdin = &in
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("seed=%d probe: %v", seed, err)
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	a := make([]answer, len(inputs))
	for i := range a {
		if err := dec.Decode(&a[i]); err != nil {
			t.Fatal(err)
		}
	}
	return a
}
func growth(t *testing.T, shape func(int) string, active int) {
	t.Helper()
	t.Logf("seed=%d", seed)
	inputs := []string{}
	for _, n := range []int{0, 1, 31, 256, 4096} {
		inputs = append(inputs, shape(n))
	}
	got := run(t, current, inputs)
	a, b := got[3].Counts, got[4].Counts
	t.Logf("counts 256->4096: %v -> %v", a, b)
	for k := range a {
		if b[k] > 17*a[k]+32 {
			t.Errorf("seed=%d counter=%d 256->4096: %d->%d exceeds 17x+32", seed, k, a[k], b[k])
		}
	}
	if active >= 0 && b[active] == 0 {
		t.Errorf("seed=%d expected counter %d to move, counts=%v", seed, active, b)
	}
}
func equivalent(t *testing.T, inputs []string) {
	t.Helper()
	t.Logf("seed=%d inputs=%d", seed, len(inputs))
	a, b := run(t, current, inputs), run(t, baseline, inputs)
	for i := range a {
		a[i].Counts = [5]int{}
		x, _ := json.Marshal(a[i])
		y, _ := json.Marshal(b[i])
		if !bytes.Equal(x, y) {
			t.Fatalf("seed=%d case=%d input=%q instrumented=%s baseline=%s", seed, i, inputs[i], x, y)
		}
	}
}
func repeated(t *testing.T, inputs []string, parallel bool) {
	t.Helper()
	t.Logf("seed=%d", seed)
	a := run(t, current, inputs)
	want, _ := json.Marshal(a)
	count := 4
	if os.Getenv("ADV_LONG") == "1" {
		count = 40
	}
	for n := 0; n < count; n++ {
		f := func(t *testing.T) {
			b := run(t, current, inputs)
			got, _ := json.Marshal(b)
			if !bytes.Equal(got, want) {
				t.Fatalf("seed=%d counts/output changed between processes", seed)
			}
		}
		if parallel {
			t.Run(fmt.Sprint(n), func(t *testing.T) { t.Parallel(); f(t) })
		} else {
			f(t)
		}
	}
}
func rep(unit string) func(int) string {
	return func(n int) string { return "SELECT " + strings.Repeat(unit, n) }
}
