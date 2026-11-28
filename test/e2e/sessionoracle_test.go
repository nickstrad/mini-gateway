package e2e

import (
	"slices"
	"testing"
)

func oracleResult() sessionResult {
	return sessionResult{columns: []string{"prepared", "cursors", "listens"}, moved: make([]int, 3)}
}

// TestSessionOracleCreditsOnlyCompleteDifferingRows proves observe credits per-column
// movement only on clean, exact-width rows and counts errored, empty, closed, or
// wrong-width replies as unreadable.
func TestSessionOracleCreditsOnlyCompleteDifferingRows(t *testing.T) {
	clean := []string{"0", "0", "f"}
	for _, c := range []struct {
		name       string
		got        []string
		moved      []int
		unreadable int
	}{
		{"error", []string{"E:ERROR:42501", "ZI"}, []int{0, 0, 0}, 1},
		{"empty", nil, []int{0, 0, 0}, 1},
		{"closed", []string{"T", "D:1|1|t", "closed:eof"}, []int{0, 0, 0}, 1},
		{"truncated", []string{"D:1|1", "ZI"}, []int{0, 0, 0}, 1},
		{"too wide", []string{"D:1|1|t|x", "ZI"}, []int{0, 0, 0}, 1},
		{"one column", []string{"D:0|1|f", "ZI"}, []int{0, 1, 0}, 0},
		{"same", []string{"D:0|0|f", "ZI"}, []int{0, 0, 0}, 0},
	} {
		r := oracleResult()
		r.observe(clean, c.got)
		if !slices.Equal(r.moved, c.moved) || r.unreadable != c.unreadable {
			t.Errorf("%s: moved %v unreadable %d, want %v and %d", c.name, r.moved, r.unreadable, c.moved, c.unreadable)
		}
	}
}

// TestSessionOracleCountsABrokenLeaseAsDirtyAndUnreadable proves record counts a broken
// observer lease as dirty and unreadable without crediting columns, and counts a leaked
// secret as dirty.
func TestSessionOracleCountsABrokenLeaseAsDirtyAndUnreadable(t *testing.T) {
	clean := []string{"T", "D:0|0|f", "C:SELECT 1", "ZT"}
	path := []string{"T", `D:"$user", public`, "C:SHOW", "ZT"}
	r := oracleResult()
	r.record(leaseObservation{catalog: []string{"E:ERROR:42501", "ZE"}, path: path, broken: true}, clean, []string{"0", "0", "f"})
	if r.dirty != 1 || r.unreadable != 1 || slices.ContainsFunc(r.moved, func(n int) bool { return n != 0 }) {
		t.Fatalf("broken lease: %s, dirty %d", r, r.dirty)
	}
	r = oracleResult()
	r.record(leaseObservation{catalog: clean, path: path, secret: true}, clean, []string{"0", "0", "f"})
	if r.dirty != 1 || r.secrets != 1 || r.unreadable != 0 {
		t.Fatalf("secret lease: %s, dirty %d", r, r.dirty)
	}
}

// TestHostileCallFailsUnlessItAnsweredCleanly proves hostileFailed rejects errors,
// abrupt closes, empty replies, and missing data rows while accepting a clean query reply.
func TestHostileCallFailsUnlessItAnsweredCleanly(t *testing.T) {
	for _, c := range []struct {
		got    []string
		failed bool
	}{
		{[]string{"T", "D:", "C:SELECT 1", "ZI"}, false},
		{[]string{"E:ERROR:42883", "ZI"}, true},
		{[]string{"closed:write"}, true},
		{[]string{"T", "D:", "closed:eof"}, true},
		{[]string{"C:SELECT 0", "ZI"}, true},
		{nil, true},
	} {
		if got := hostileFailed(c.got); got != c.failed {
			t.Errorf("hostileFailed(%v) = %v, want %v", c.got, got, c.failed)
		}
	}
}
