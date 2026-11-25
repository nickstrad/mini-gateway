package pgwire

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/work"
)

const (
	clsSpellSeed    = 0xc05
	clsSpellPerName = 6
	clsSpellPerRule = 40
)

var clsSpellExprPositions = []string{
	"SELECT %s",
	"SELECT 1 FROM t WHERE %s IS NULL",
	"SELECT a FROM t ORDER BY %s",
	"SELECT abs(%s)",
	"SELECT coalesce(%s, 1)",
	"SELECT CASE WHEN %s IS NULL THEN 1 ELSE 2 END",
	"SELECT a IS DISTINCT FROM %s FROM t",
	"SELECT a IS NOT DISTINCT FROM %s FROM t",
	"SELECT trim(FROM %s) FROM t",
	"SELECT trim(BOTH 'x' FROM %s) FROM t",
	"SELECT substring('abc' FROM %s) FROM t",
	"SELECT substring('abc' FROM 1 FOR %s) FROM t",
	"SELECT extract(year FROM %s) FROM t",
	"SELECT overlay('a' PLACING 'b' FROM %s) FROM t",
	"SELECT (SELECT %s)",
	"SELECT EXISTS (SELECT %s)",
	"SELECT ARRAY[%s]",
	"SELECT 1 WHERE %s IN (1, 2)",
	"WITH c AS (SELECT %s) SELECT * FROM c",
	"VALUES (%s)",
	"EXPLAIN SELECT %s",
	"EXPLAIN (verbose, costs off) SELECT %s",
	"SELECT 1 FROM t WHERE a = ANY (SELECT %s)",
	"SELECT * FROM t, LATERAL (SELECT %s) s",
	"SELECT * FROM t JOIN u ON %s",
	"(SELECT %s)",
	"SELECT 1; SELECT %s",
	"SELECT a FROM t GROUP BY a HAVING %s",
	"SELECT %s FROM t FETCH FIRST 1 ROWS ONLY",
	"SELECT count(*) FILTER (WHERE %s) FROM t",
	"SELECT %s::text",
	"SELECT CAST(%s AS text)",
	"SELECT 1 FROM t WHERE a BETWEEN 1 AND %s",
}

var clsSpellFromPositions = []string{
	"SELECT * FROM %s",
	"SELECT * FROM t JOIN %s ON true",
	"SELECT * FROM ROWS FROM (%s)",
	"SELECT * FROM t, LATERAL %s",
}

var clsSpellWriters = []string{
	"nextval", "setval", "lo_create", "lo_unlink", "lo_import", "pg_terminate_backend",
	"pg_cancel_backend", "pg_notify", "txid_current", "pg_current_xact_id", "pg_reload_conf",
	"pg_stat_reset", "dblink_exec", "evil_write", "pg_switch_wal", "pg_create_restore_point",
}

func clsSpellCase(rng *rand.Rand, s string) string {
	b := []byte(s)
	clsMutateCase(rng, b)
	return string(b)
}

func clsSpellName(rng *rand.Rand, name string, mustQuote bool) string {
	k := rng.IntN(4)
	if mustQuote && k == 0 {
		k = 1 + rng.IntN(3)
	}
	switch k {
	case 1:
		return `"` + name + `"`
	case 2:
		return fmt.Sprintf(`U&"\%04x%s"`, name[0], name[1:])
	case 3:
		return fmt.Sprintf(`U&"!%04x%s" UESCAPE '!'`, name[0], name[1:])
	}
	return clsSpellCase(rng, name)
}

func clsSpellQualifier(rng *rand.Rand) string {
	return []string{"", "public.", "pg_catalog.", `"public".`, "PUBLIC . ", "pg_catalog/* q */./* q */", "app.", `U&"public".`}[rng.IntN(8)]
}

func clsSpellCall(rng *rand.Rand, fn string) string {
	return fn + []string{"()", "(1)", " (1)", "/* c */(1)", "\n(1, 2)", "('a')", "(VARIADIC ARRAY[1])"}[rng.IntN(7)]
}

func clsSpellQualifiedName(rng *rand.Rand, name string, nonCall bool) string {
	q := clsSpellQualifier(rng)
	return q + clsSpellName(rng, name, nonCall && q == "")
}

func clsSpellAttribute(rng *rand.Rand, name string) string {
	n := clsSpellName(rng, name, false)
	if strings.Contains(n, "UESCAPE") {
		n = `"` + name + `"`
	}
	return []string{"t." + n, "t . " + n, `"t".` + n, "public.t." + n, "t." + n + ".x", "t/* c */./* c */" + n}[rng.IntN(6)]
}

func clsSpellIn(rng *rand.Rand, positions []string, x string) string {
	return fmt.Sprintf(positions[rng.IntN(len(positions))], x)
}

func clsSpellFinish(rng *rand.Rand, q string) string {
	return string(clsPad([]byte(q), rng))
}

func clsNotAllowListed(rng *rand.Rand) []string {
	var names []string
	names = append(names, rejNonCallWords...)
	names = append(names, rejWriteKeywords...)
	names = append(names, clsSpellWriters...)
	for _, e := range AllowList {
		names = append(names, e.Name+"_x")
	}
	for k := 0; k < 40; k++ {
		names = append(names, fmt.Sprintf("fn_%x", rng.Uint32()))
	}
	return names
}

func clsCheckNeverProved(t *testing.T, q string, seen map[string]bool) {
	t.Helper()
	seen[q] = true
	for _, inTx := range []bool{false, true} {
		u := ClassifyQuery([]byte(q), inTx)
		if u.ReadOnly || u.Coalescable {
			t.Errorf("calls a function outside the allow-list but is proved: %q (in tx %v): %s", q, inTx, clsDescribe(u))
		}
	}
}

func TestClassifierSpellingsNeverProveUnlistedCalls(t *testing.T) {
	rng := rand.New(rand.NewPCG(clsSpellSeed, 1))
	seen := map[string]bool{}
	nonCall := map[string]bool{}
	for _, w := range rejNonCallWords {
		nonCall[w] = true
	}
	for _, name := range clsNotAllowListed(rng) {
		for k := 0; k < clsSpellPerName; k++ {
			call := clsSpellCall(rng, clsSpellQualifiedName(rng, name, nonCall[name]))
			clsCheckNeverProved(t, clsSpellFinish(rng, clsSpellIn(rng, clsSpellExprPositions, call)), seen)
			clsCheckNeverProved(t, clsSpellFinish(rng, clsSpellIn(rng, clsSpellFromPositions, call)), seen)
			clsCheckNeverProved(t, clsSpellFinish(rng, clsSpellIn(rng, clsSpellExprPositions, clsSpellAttribute(rng, name))), seen)
		}
	}
	for _, e := range AllowList {
		for k := 0; k < clsSpellPerName; k++ {
			q := []string{"public.", "app.", `"public".`, "pg_catalog.pg_catalog.", "t.", `"pg_Catalog".`}[rng.IntN(6)]
			call := clsSpellCall(rng, q+clsSpellName(rng, e.Name, false))
			clsCheckNeverProved(t, clsSpellFinish(rng, clsSpellIn(rng, clsSpellExprPositions, call)), seen)
			clsCheckNeverProved(t, clsSpellFinish(rng, clsSpellIn(rng, clsSpellExprPositions, clsSpellAttribute(rng, e.Name))), seen)
		}
	}
	if len(seen) < 3000 {
		t.Fatalf("only %d distinct statements generated", len(seen))
	}
	t.Logf("%d distinct generated statements calling unlisted functions; none proved", len(seen))
}

func TestClassifierSpellingsNeverCoalesceNonCoalescableCalls(t *testing.T) {
	rng := rand.New(rand.NewPCG(clsSpellSeed, 2))
	n := 0
	for _, e := range AllowList {
		if e.Coalesce {
			continue
		}
		for k := 0; k < clsSpellPerName*4; k++ {
			call := clsSpellCall(rng, []string{"", "pg_catalog.", "PG_CATALOG . "}[rng.IntN(3)]+clsSpellName(rng, e.Name, false))
			q := clsSpellFinish(rng, clsSpellIn(rng, clsSpellExprPositions, call))
			n++
			if u := ClassifyQuery([]byte(q), false); u.Coalescable {
				t.Errorf("not coalescable by the list, but coalesced: %q: %s", q, clsDescribe(u))
			}
		}
	}
	temporal := map[string]bool{}
	for _, w := range rejTemporalTypes {
		temporal[w] = true
	}
	for _, w := range append(append([]string{}, rejPerCallKeywords...), rejTemporalTypes...) {
		for k := 0; k < clsSpellPerName; k++ {
			x := clsSpellCase(rng, w)
			if temporal[w] && rng.IntN(2) == 0 {
				x = "CAST(x AS " + clsSpellQualifier(rng) + clsSpellName(rng, w, false) + ")"
			}
			q := clsSpellFinish(rng, clsSpellIn(rng, clsSpellExprPositions[:6], x))
			n++
			if u := ClassifyQuery([]byte(q), false); u.Coalescable {
				t.Errorf("per-call word coalesced: %q: %s", q, clsDescribe(u))
			}
		}
	}
	for _, w := range rejRelativeTimeWords {
		for _, lit := range []string{"'%s'", "' %s '", "$$%s$$", "$x$%s 10:00$x$", "'%s'::date", "N'%s'"} {
			q := clsSpellFinish(rng, clsSpellIn(rng, clsSpellExprPositions[:6], fmt.Sprintf(lit, clsSpellCase(rng, w))))
			n++
			if u := ClassifyQuery([]byte(q), false); u.Coalescable {
				t.Errorf("relative time literal coalesced: %q: %s", q, clsDescribe(u))
			}
		}
	}
	t.Logf("%d generated statements outside D10; none coalesced", n)
}

type clsSpellRule struct {
	reason work.RejectReason
	gen    func(rng *rand.Rand) string
}

func clsSpellPick(rng *rand.Rand, xs ...string) string { return xs[rng.IntN(len(xs))] }

func clsSpellRules() []clsSpellRule {
	call := func(names ...string) func(rng *rand.Rand) string {
		return func(rng *rand.Rand) string {
			name := clsSpellPick(rng, names...)
			args := clsSpellPick(rng, "(1)", " (1, 2)", "/* c */(1)", "\n(a)")
			return clsSpellIn(rng, append(clsSpellExprPositions, clsSpellFromPositions...), clsSpellQualifiedName(rng, name, false)+args)
		}
	}
	fixed := func(forms ...string) func(rng *rand.Rand) string {
		return func(rng *rand.Rand) string { return clsSpellPick(rng, forms...) }
	}
	tempName := func(rng *rand.Rand) string {
		return clsSpellPick(rng, "pg_temp", `"pg_temp"`, "PG_TEMP", `U&"pg\005ftemp"`, `U&"pg!005ftemp" UESCAPE '!'`, "pg_temp_7", `"pg_temp_12"`)
	}
	return []clsSpellRule{
		{work.RejectSessionSet, func(rng *rand.Rand) string {
			return clsSpellPick(rng, "SET", "SET SESSION", "/* c */ SET") + " " + clsSpellName(rng, clsSpellPick(rng, "search_path", "application_name", "work_mem"), false) + clsSpellPick(rng, " = 'x'", " TO x", " TO DEFAULT", "=1")
		}},
		{work.RejectSessionSet, fixed("SET ROLE pg_monitor", "SET SESSION AUTHORIZATION DEFAULT", "SET TIME ZONE 'UTC'", "SET NAMES 'UTF8'", "SET SCHEMA 'app'", "SET local.x = 1", "SET LOCAL standard_conforming_strings = off", "SET LOCAL client_encoding = 'LATIN1'", "SET SESSION CHARACTERISTICS AS TRANSACTION READ ONLY", "UPDATE pg_settings SET setting = 'x' WHERE name = 'search_path'")},
		{work.RejectReset, fixed("RESET ALL", "RESET search_path", "reset \"work_mem\"", "RESET ROLE", "RESET SESSION AUTHORIZATION")},
		{work.RejectPrepare, fixed("PREPARE q AS SELECT 1", "PREPARE q (int) AS SELECT $1", "prepare \"Q\" AS VALUES (1)")},
		{work.RejectExecute, fixed("EXECUTE q", "EXECUTE q (1)", "EXPLAIN EXECUTE q", "EXPLAIN (ANALYZE, FORMAT JSON) EXECUTE q", "EXPLAIN ANALYZE VERBOSE EXECUTE q", "EXPLAIN (\"analyze\") EXECUTE q", "CREATE TABLE x AS EXECUTE q")},
		{work.RejectDeallocate, fixed("DEALLOCATE q", "DEALLOCATE ALL", "DEALLOCATE PREPARE q")},
		{work.RejectListen, fixed("LISTEN jobs", "listen \"Jobs\"")},
		{work.RejectUnlisten, fixed("UNLISTEN *", "UNLISTEN jobs")},
		{work.RejectCursorWithHold, fixed("DECLARE c CURSOR WITH HOLD FOR SELECT 1", "DECLARE c NO SCROLL CURSOR WITH HOLD FOR SELECT 1", "DECLARE c BINARY INSENSITIVE CURSOR WITH HOLD FOR SELECT 1", "DECLARE \"c\" SCROLL CURSOR WITH HOLD FOR TABLE t")},
		{work.RejectTempObject, func(rng *rand.Rand) string {
			return "CREATE " + clsSpellPick(rng, "TEMP", "TEMPORARY", "GLOBAL TEMP", "LOCAL TEMPORARY", "/* c */ temp") + " " + clsSpellPick(rng, "TABLE x (a int)", "VIEW v AS SELECT 1", "SEQUENCE s", "TABLE x AS SELECT 1")
		}},
		{work.RejectTempObject, fixed("CREATE OR REPLACE TEMP VIEW v AS SELECT 1", "SELECT 1 INTO TEMP x", "SELECT 1 INTO TEMPORARY TABLE x")},
		{work.RejectTempObject, func(rng *rand.Rand) string {
			return fmt.Sprintf(clsSpellPick(rng, "CREATE TABLE %s.x (a int)", "SELECT * FROM %s.x", "DROP TABLE %s.x", "SELECT %s.f()"), tempName(rng))
		}},
		{work.RejectTempObject, func(rng *rand.Rand) string {
			v := clsSpellPick(rng, "'pg_temp'", "'public, PG_TEMP'", "$$pg_temp$$", `E'pg\x5ftemp'`, `U&'pg\005ftemp'`, `'"pg_temp"'`)
			switch rng.IntN(3) {
			case 0:
				return "SET LOCAL " + clsSpellName(rng, "search_path", false) + clsSpellPick(rng, " = ", " TO ") + v
			case 1:
				return "SET LOCAL SCHEMA " + v
			}
			return clsSpellIn(rng, clsSpellExprPositions, clsSpellQualifier(rng)+"set_config('search_path', "+clsSpellPick(rng, v, "'pg_' || 'temp'", "lower('PG_TEMP')")+", true)")
		}},
		{work.RejectAdvisoryLock, call(rejSessionAdvisory...)},
		{work.RejectSetConfig, func(rng *rand.Rand) string {
			third := clsSpellPick(rng, ", false", ", FALSE", ", 'f'", ", 'false'", ", 0::boolean", ", (true)", ", true::bool", ", NOT true", ", NULL", "", ", $$t$$")
			first := clsSpellPick(rng, "'a.b'", "'work_mem'", "lower('A.B')", "$$a.b$$", "'standard_conforming_strings'")
			return clsSpellIn(rng, clsSpellExprPositions, clsSpellQualifiedName(rng, "set_config", false)+"("+first+", 'x'"+third+")")
		}},
		{work.RejectDiscard, fixed("DISCARD ALL", "DISCARD PLANS", "DISCARD SEQUENCES", "DISCARD TEMP", "DISCARD TEMPORARY")},
		{work.RejectLoad, fixed("LOAD 'auto_explain'", "load '$libdir/plugins/x'")},
		{work.RejectCopy, fixed("COPY t TO STDOUT", "COPY (SELECT 1) TO STDOUT", "copy t from stdin", "COPY t (a) FROM STDIN WITH (FORMAT csv)")},
		{work.RejectTwoPhase, fixed("PREPARE TRANSACTION 'x'", "COMMIT PREPARED 'x'", "ROLLBACK PREPARED 'x'")},
	}
}

func TestClassifierSpellingsAlwaysRejectTheRejectList(t *testing.T) {
	rng := rand.New(rand.NewPCG(clsSpellSeed, 3))
	wraps := []string{"%s", "BEGIN; %s; COMMIT", "SELECT 1; %s", "%s; SELECT 1", "%s;"}
	covered := map[work.RejectReason]int{}
	for _, r := range clsSpellRules() {
		for k := 0; k < clsSpellPerRule; k++ {
			stmt := r.gen(rng)
			q := clsSpellFinish(rng, fmt.Sprintf(wraps[rng.IntN(len(wraps))], stmt))
			covered[r.reason]++
			for _, inTx := range []bool{false, true} {
				u := ClassifyQuery([]byte(q), inTx)
				if u.Class != work.ClassReject || u.Reason != r.reason {
					t.Errorf("%q (in tx %v): %s, want reject %s", q, inTx, clsDescribe(u), r.reason)
				}
			}
		}
	}
	for r := work.RejectSessionSet; r < work.RejectExtendedProtocol; r++ {
		if covered[r] == 0 {
			t.Errorf("no generated spelling for %s", r)
		}
	}
}
