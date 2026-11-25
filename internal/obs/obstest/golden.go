package obstest

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// Golden compares got against testdata/<name>, or rewrites the file when update
// is true. Callers pass their own -update flag value so obstest registers no
// package-level flags that could clash across test binaries.
func Golden(t testing.TB, update bool, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run the test with -update to create it)", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("output differs from %s (run with -update after checking the change)\n--- got\n%s", path, got)
	}
}
