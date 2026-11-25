package catalog_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/obs/catalog"
	"github.com/nickstrad/mini-gateway/internal/obs/csvflush"
)

func TestCSVSchemaDocumentIsGeneratedFromTheCode(t *testing.T) {
	want, err := catalog.Markdown()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join("..", "..", "..", catalog.DocPath))
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
