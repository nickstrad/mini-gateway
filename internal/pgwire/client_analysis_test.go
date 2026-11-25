package pgwire_test

import (
	"os"
	"path/filepath"
	"testing"
)

func requireAnalysisFile(t *testing.T, scenario, file string) {
	t.Helper()
	path := analysisPath(scenario, file)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if len(b) == 0 {
		t.Fatalf("%s is empty", path)
	}
}

func analysisPath(scenario, file string) string {
	return filepath.Join("..", "..", "test", "analysis", scenario, file)
}
