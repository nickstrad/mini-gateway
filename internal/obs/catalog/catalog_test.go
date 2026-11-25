package catalog_test

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/obs/catalog"
	"github.com/nickstrad/mini-gateway/internal/obs/csvflush"
)

var (
	update = flag.Bool("update", false, "rewrite docs/csv-schema.md from the code")
	out    = flag.String("out", "", "write the generated docs/csv-schema.md to this path instead of checking it")
)

func TestCSVSchemaDocumentIsGeneratedFromTheCode(t *testing.T) {
	want, err := catalog.Markdown()
	if err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join("..", "..", "..", catalog.DocPath)
	if *out != "" || *update {
		dst := *out
		if dst == "" {
			dst = doc
		}
		if err := os.WriteFile(dst, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is stale: run scripts/gen-docs.sh", catalog.DocPath)
	}
}

func TestEveryDeclaredMetricIsInTheDocument(t *testing.T) {
	reg, err := catalog.Registry()
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := catalog.Markdown()
	for _, d := range reg.All() {
		if !strings.Contains(string(doc), "| `"+d.Component+"` | `"+d.Name+"` |") {
			t.Fatalf("%s.%s is not documented", d.Component, d.Name)
		}
	}
	for _, c := range csvflush.MetricsHeader() {
		if !strings.Contains(string(doc), "| `"+c+"` |") {
			t.Fatalf("column %s is not documented", c)
		}
	}
}
