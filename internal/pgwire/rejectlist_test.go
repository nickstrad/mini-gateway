package pgwire

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/work"
)

// TestRejectRulesExamplesClassifyAsDocumented proves that every RejectRules
// example classifies under its listed reason and every allowed form forwards.
func TestRejectRulesExamplesClassifyAsDocumented(t *testing.T) {
	seen := map[work.RejectReason]bool{}
	for _, r := range RejectRules {
		if seen[r.Reason] {
			t.Errorf("reason %s listed twice", r.Reason)
		}
		seen[r.Reason] = true
		if r.Reason == work.RejectExtendedProtocol {
			continue
		}
		u := ClassifyQuery([]byte(r.Example), false)
		if u.Class != work.ClassReject || u.Reason != r.Reason {
			t.Errorf("example %q: %s, want reject %s", r.Example, clsDescribe(u), r.Reason)
		}
	}
	for x := work.RejectSessionSet; x < work.NumRejectReasons; x++ {
		if !seen[x] {
			t.Errorf("reason %s has no rule in the table", x)
		}
	}
	for _, f := range RejectAllowedForms {
		if u := ClassifyQuery([]byte(f.Example), false); u.Class == work.ClassReject {
			t.Errorf("allowed form %q is rejected: %s", f.Example, u.Reason)
		}
	}
}

// TestShareAllowListIsSortedAndExcludesPerCallFunctions proves that
// ShareAllowList is strictly sorted and contains no session, write, or per-call function.
func TestShareAllowListIsSortedAndExcludesPerCallFunctions(t *testing.T) {
	for i := 1; i < len(ShareAllowList); i++ {
		if ShareAllowList[i-1] >= ShareAllowList[i] {
			t.Errorf("allow-list not sorted or duplicate at %q", ShareAllowList[i])
		}
	}
	for _, e := range ShareAllowList {
		for _, bad := range append(append([]string{}, rejSessionAdvisory...), "set_config", "nextval", "setval", "currval", "lastval", "txid_current", "pg_notify", "setseed", "dblink") {
			if e == bad {
				t.Errorf("%q must not be on the allow-list", bad)
			}
		}
	}
	for _, e := range clsFirstAllowList {
		if !e.Coalesce && slices.Contains(ShareAllowList, e.Name) {
			t.Errorf("%q was not coalescable in the first build and must not be on the share allow-list", e.Name)
		}
	}
	for _, name := range []string{"now", "random", "pg_backend_pid", "clock_timestamp", "pg_sleep", "current_setting", "gen_random_uuid"} {
		if slices.Contains(ShareAllowList, name) {
			t.Errorf("%q must not be on the share allow-list", name)
		}
	}
}

const rejDocsEnv = "MG_GEN_DOCS_DIR"

func rejDocs() map[string]string {
	return map[string]string{
		"error-contract.md": ErrorRowsMarkdown(),
		"reject-list.md":    RejectListMarkdown(),
		"safe-shape.md":     SafeShapeMarkdown(),
	}
}

// TestClassifierWriteContractDocs regenerates docs/error-contract.md,
// docs/reject-list.md, and docs/safe-shape.md when MG_GEN_DOCS_DIR is set.
func TestClassifierWriteContractDocs(t *testing.T) {
	dir := os.Getenv(rejDocsEnv)
	if dir == "" {
		t.Skip("set " + rejDocsEnv + " to write the generated documents (scripts/gen-docs.sh does)")
	}
	for name, body := range rejDocs() {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestClassifierContractDocsMatchTheCode proves that the committed error,
// reject-list, and safe-shape Markdown files match the code generators verbatim.
func TestClassifierContractDocsMatchTheCode(t *testing.T) {
	for name, body := range rejDocs() {
		got, err := os.ReadFile(filepath.Join("..", "..", "docs", name))
		if err != nil {
			t.Fatalf("%v: run scripts/gen-docs.sh", err)
		}
		if string(got) != body {
			t.Errorf("docs/%s differs from the code: run scripts/gen-docs.sh", name)
		}
	}
}
