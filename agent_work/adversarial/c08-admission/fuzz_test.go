package adv_test

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The decoder and operation runner are unexported. Compile an unmodified copy
// beside a tiny access bridge; never inject a file or fuzz corpus into product dirs.
func fuzzCopy(t *testing.T, bridge string) string {
	t.Helper()
	d := filepath.Join(scratch(t), "admission")
	src := filepath.Join(root(t), "internal/admission")
	entries, e := os.ReadDir(src)
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range entries {
		if strings.HasSuffix(v.Name(), ".go") {
			write(t, filepath.Join(d, v.Name()), read(t, filepath.Join(src, v.Name())))
		}
	}
	write(t, filepath.Join(d, "adv_bridge_test.go"), []byte(bridge))
	return d
}

const decodeBridge = `package admission
import("testing";"math/rand";"reflect")
func TestBridge(t *testing.T){r:=rand.New(rand.NewSource(808271));for n:=0;n<1024;n++{b:=make([]byte,n);r.Read(b);ops:=decodeOps(b);if len(ops)!=min(n/3,64){t.Fatalf("length %d decoded %d",n,len(ops))};for i,o:=range ops{if o.kind>=numOpKinds||o.dt<0||o.dt>400*Millisecond||o.tenant<0||o.tenant>=4{t.Fatalf("n=%d i=%d invalid op %+v",n,i,o)}};if n>=192{if !reflect.DeepEqual(ops,decodeOps(b[:192])){t.Fatalf("suffix affected first 64 ops n=%d",n)}}};b:=make([]byte,1<<20);r.Read(b);if len(decodeOps(b))!=64{t.Fatal("megabyte input escaped op cap")}}
`

func TestAdv_15_DecoderLengthAndSuffix(t *testing.T) {
	start(t)
	d := fuzzCopy(t, decodeBridge)
	b, e := command(t, d, nil, "/usr/local/go/bin/go", "test", "-count=1", "-run=^TestBridge$", ".")
	if e != nil {
		t.Fatalf("decoder boundary campaign: %v\n%s", e, b)
	}
}
func TestAdv_15_SeedCannotDependOnInput(t *testing.T) {
	start(t)
	p := filepath.Join(root(t), "internal/admission/fuzz_test.go")
	f, e := parser.ParseFile(token.NewFileSet(), p, nil, 0)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "FuzzAdmissionOps" {
			continue
		}
		found = true
		ast.Inspect(fn, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok {
				switch x := c.Fun.(type) {
				case *ast.Ident:
					if x.Name != "fuzzSeeds" && x.Name != "fuzzPlant" && x.Name != "decodeOps" && x.Name != "fuzzConfigs" && x.Name != "runOps" && x.Name != "trail" {
						t.Errorf("unreviewed input-dependent call %s", x.Name)
					}
				case *ast.SelectorExpr:
					if x.Sel.Name != "Add" && x.Sel.Name != "Fuzz" && x.Sel.Name != "Fatalf" {
						t.Errorf("unexpected external call %s", x.Sel.Name)
					}
				}
			}
			return true
		})
	}
	if !found {
		t.Fatal("fuzz entry missing")
	}
}

const plantBridge = `package admission
import("testing";"math/rand")
func TestBridge(t *testing.T){r:=rand.New(rand.NewSource(808271));caught:=false;for n:=0;n<1000;n++{b:=make([]byte,192);for i:=0;i<len(b);i+=3{b[i]=byte(r.Intn(10));b[i+1]=byte(r.Intn(16));b[i+2]=0};ops:=decodeOps(b);for _,c:=range fuzzConfigs(0){if _,e:=runOps(c,ops);e!=nil{t.Fatalf("unplanted generated case %d: %v",n,e)}};for _,c:=range fuzzConfigs(PlantRemoveSwapsTail){if _,e:=runOps(c,ops);e!=nil{caught=true;break}};if caught{t.Logf("seed=808271 detected plant at generated case %d",n);break}};if !caught{t.Fatal("1000 generated remove/interleave inputs missed ordering plant")}}
`

func TestAdv_15_IndependentCorpusFindsPlant(t *testing.T) {
	start(t)
	d := fuzzCopy(t, plantBridge)
	b, e := command(t, d, nil, "/usr/local/go/bin/go", "test", "-count=1", "-v", "-run=^TestBridge$", ".")
	if e != nil {
		t.Fatalf("plant campaign: %v\n%s", e, b)
	}
	t.Log(string(b))
}
func TestAdv_15_LongActualFuzzCampaign(t *testing.T) {
	start(t)
	if os.Getenv("ADV_LONG") != "1" {
		t.Skip("ADV_LONG=1: actual 20s fuzz campaign")
	}
	d := fuzzCopy(t, "package admission\n")
	b, e := command(t, d, []string{"MG_FUZZ_PLANT=admission-remove-order", "GOMAXPROCS=2"}, "/usr/local/go/bin/go", "test", "-run=^$", "-fuzz=^FuzzAdmissionOps$", "-fuzztime=20s", "-parallel=2", ".")
	if e == nil || !bytes.Contains(b, []byte("reference")) {
		t.Fatalf("actual fuzz did not diagnose planted ordering mismatch: %v\n%s", e, b)
	}
	t.Log(string(b))
	entries, err := os.ReadDir(filepath.Join(d, "testdata/fuzz/FuzzAdmissionOps"))
	if err != nil {
		t.Fatal("missing replay corpus:", err)
	}
	for _, entry := range entries {
		t.Logf("replay corpus %s:\n%s", entry.Name(), read(t, filepath.Join(d, "testdata/fuzz/FuzzAdmissionOps", entry.Name())))
	}
}
