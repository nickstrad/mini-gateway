package pgwire

import (
	"testing"

	"github.com/nickstrad/mini-gateway/internal/work"
)

func clsWantNotReadOnly(t *testing.T, qs ...string) {
	t.Helper()
	for _, q := range qs {
		for _, inTx := range []bool{false, true} {
			u := ClassifyQuery([]byte(q), inTx)
			if u.SafeRead || u.Shareable {
				t.Errorf("%q (in tx %v): %s, want neither safe-read nor shareable", q, inTx, clsDescribe(u))
			}
		}
	}
}

func clsWantNotCoalescable(t *testing.T, qs ...string) {
	t.Helper()
	for _, q := range qs {
		if u := ClassifyQuery([]byte(q), false); u.Shareable || u.SafeRead {
			t.Errorf("%q: %s, want neither safe-read nor shareable", q, clsDescribe(u))
		}
	}
}

func clsWantRejected(t *testing.T, r work.RejectReason, qs ...string) {
	t.Helper()
	for _, q := range qs {
		for _, inTx := range []bool{false, true} {
			u := ClassifyQuery([]byte(q), inTx)
			if u.Class != work.ClassReject || u.Reason != r {
				t.Errorf("%q (in tx %v): %s, want reject %s", q, inTx, clsDescribe(u), r)
			}
		}
	}
}

func clsWantForwardedReadOnly(t *testing.T, co bool, qs ...string) {
	t.Helper()
	for _, q := range qs {
		u := ClassifyQuery([]byte(q), false)
		if u.Class != work.ClassStartLease || (!co && (u.SafeRead || u.Shareable)) {
			t.Errorf("%q: %s, want forwarded, and never shared unless the first build coalesced it (%v)", q, clsDescribe(u), co)
		}
	}
}

func TestClassifierSchemaQualifiedKeywordNamesAreCalls(t *testing.T) {
	for _, w := range clsFirstNonCallWords {
		clsWantNotReadOnly(t,
			"SELECT public."+w+"()",
			"SELECT public."+w+"(1)",
			"SELECT \"public\" . "+w+" /* c */ (1)",
			"SELECT 1 FROM t WHERE public."+w+"(a) IS NULL",
		)
	}
	clsWantNotReadOnly(t, "SELECT public.select()", "SELECT public.in(1)", "SELECT public.exists()", "SELECT public.current_timestamp(3)")
	clsWantForwardedReadOnly(t, true, "SELECT pg_catalog.count(*) FROM kv", "SELECT b::varchar(10) FROM t", "SELECT CAST(a AS numeric(10, 2)) FROM t")
}

func TestClassifierAttributeCallAfterExpressionFrom(t *testing.T) {
	clsWantNotReadOnly(t,
		"SELECT a IS DISTINCT FROM t.evil_write FROM t",
		"SELECT a IS NOT DISTINCT FROM t.evil_write FROM t",
		"SELECT trim(FROM t.evil_write_text) FROM t",
		"SELECT trim(BOTH FROM t.evil_write_text) FROM t",
		"SELECT trim(LEADING 'x' FROM t.evil_write_text) FROM t",
		"SELECT substring('abc' FROM t.evil_write) FROM t",
		"SELECT substring('abc' FROM 1 FOR t.evil_write) FROM t",
		"SELECT extract(year FROM t.evil_write) FROM t",
		"SELECT overlay('a' PLACING 'b' FROM t.evil_write) FROM t",
		"SELECT a IS DISTINCT FROM t.evil_write.x FROM t",
		"SELECT (SELECT a IS DISTINCT FROM t.evil_write) FROM t",
		"SELECT 1 FROM t WHERE a IS DISTINCT FROM t.evil_write",
		"SELECT 1 FROM t WHERE trim(FROM t.evil_write_text) = ''",
	)
	clsWantForwardedReadOnly(t, true,
		"SELECT * FROM public.kv",
		"SELECT (SELECT count(*) FROM public.kv)",
		"SELECT * FROM t JOIN public.u ON true",
		"SELECT 1 FROM t WHERE a IN (SELECT a FROM public.u)",
		"SELECT * FROM ONLY public.t",
		"SELECT a IS DISTINCT FROM 1 FROM public.t",
	)
}

func TestClassifierSearchPathStringNamingTempSchema(t *testing.T) {
	clsWantRejected(t, work.RejectTempObject,
		"SET LOCAL search_path = 'pg_temp'",
		"SET LOCAL search_path TO 'pg_temp'",
		"SET LOCAL SCHEMA 'pg_temp'",
		"SET LOCAL search_path = 'public, PG_TEMP'",
		"SET LOCAL search_path = public, 'pg_temp'",
		"SET LOCAL search_path = '\"pg_temp\"'",
		"SET LOCAL search_path = 'pg_temp_3'",
		"SET LOCAL \"search_path\" = 'pg_temp'",
		"SET LOCAL SEARCH_PATH = 'pg_temp'",
		"SET LOCAL search_path = $$pg_temp$$",
		"SET LOCAL search_path = $x$pg_temp$x$",
		`SET LOCAL search_path = E'pg\x5ftemp'`,
		`SET LOCAL search_path = U&'pg\005ftemp'`,
		`SET LOCAL search_path = U&'pg!005ftemp' UESCAPE '!'`,
		`SET LOCAL U&"search\005fpath" = 'pg_temp'`,
		"SELECT set_config('search_path', 'pg_temp', true)",
		"SELECT set_config('SEARCH_PATH', 'pg_temp', true)",
		"SELECT set_config('search_path', 'public, pg_temp', true)",
		"SELECT set_config('search_path', $$pg_temp$$, true)",
		"SELECT set_config('search_path', 'pg_' || 'temp', true)",
		"SELECT set_config('search_path', lower('PG_TEMP'), true)",
		`SELECT set_config('search_path', E'pg\x5ftemp', true)`,
		"SELECT pg_catalog.set_config('search_path', 'pg_temp', true)",
		"BEGIN; SET LOCAL search_path = 'pg_temp'; CREATE TABLE sneaky1 (a int); COMMIT",
		"BEGIN; SELECT set_config('search_path', 'pg_temp', true); CREATE TABLE sneaky2 (a int); COMMIT",
		"BEGIN; SET LOCAL SCHEMA 'pg_temp'; CREATE TABLE sneaky3 (a int); COMMIT",
	)
	for _, q := range []string{
		"SET LOCAL search_path = 'app'",
		"SET LOCAL search_path TO app, public",
		"SET LOCAL search_path = DEFAULT",
		"SET LOCAL SCHEMA 'app'",
		"SET LOCAL statement_timeout = 'pg_temp'",
		"SELECT set_config('search_path', 'app', true)",
		"SELECT set_config('application_name', 'pg_temp', true)",
	} {
		if u := ClassifyQuery([]byte(q), true); u.Class != work.ClassContinueLease {
			t.Errorf("%q: %s, want forwarded", q, clsDescribe(u))
		}
	}
}

func TestClassifierExplainQuotedAnalyzeOption(t *testing.T) {
	clsWantNotReadOnly(t,
		`EXPLAIN ("analyze") SELECT 1`,
		`EXPLAIN ("analyze" true) SELECT 1`,
		`EXPLAIN ("analyse") SELECT 1`,
		`EXPLAIN (verbose, "analyze" on) SELECT 1`,
		`EXPLAIN (U&"analyze") SELECT 1`,
		`EXPLAIN (U&"an\0061lyze") SELECT 1`,
		`EXPLAIN ( /* c */ "analyze" ) SELECT 1`,
	)
	clsWantForwardedReadOnly(t, false, `EXPLAIN (verbose) SELECT 1`, `EXPLAIN ("verbose") SELECT 1`, `EXPLAIN (costs off) SELECT 1`)
}

func TestClassifierTimeDependentLiteralsAreNotCoalescable(t *testing.T) {
	clsWantNotCoalescable(t,
		`SELECT 'now'::timestamp`,
		`SELECT 'now'::timestamptz`,
		`SELECT CAST('today' AS date)`,
		`SELECT 'NOW'::timestamp`,
		`SELECT ' now '::timestamp`,
		`SELECT 'tomorrow'::date`,
		`SELECT 'yesterday 10:00'::timestamp`,
		`SELECT date 'today'`,
		`SELECT timestamp 'now'`,
		`SELECT $$now$$::timestamptz`,
		`SELECT E'n\x6fw'::timestamptz`,
		`SELECT U&'n\006fw'::timestamptz`,
		`SELECT ('n' || 'ow')::timestamptz`,
		`SELECT CAST(lower('NOW') AS pg_catalog.timestamptz)`,
		`SELECT concat('to', 'day')::"date"`,
		`SELECT ('n' || 'ow')::time with time zone`,
		`SELECT ('n' || 'ow')::timetz`,
		`SELECT a FROM t WHERE a < extract(day FROM 'now')`,
		`SELECT date_trunc('day', 'now')`,
	)
	clsWantForwardedReadOnly(t, true, `SELECT 'abc'`, `SELECT lower('Abc')`, `SELECT date_trunc('day', make_date(2020, 1, 2))`)
}

func TestLexOperatorTrailingSignFollowsPostgres(t *testing.T) {
	cases := []struct{ src, want string }{
		{"71+-3", "number:71 operator:+ operator:- number:3"},
		{"a=-1", "word:a operator:= operator:- number:1"},
		{"a<>-+1", "word:a operator:<> operator:- operator:+ number:1"},
		{"a*-1", "word:a operator:* operator:- number:1"},
		{"a@-1", "word:a operator:@- number:1"},
		{"a?-1", "word:a operator:?- number:1"},
		{"a-1", "word:a operator:- number:1"},
		{"a+--c\n1", "word:a operator:+ line-comment:--c number:1"},
		{"a!=-1", "word:a operator:!=- number:1"},
	}
	for _, c := range cases {
		got, _ := lexKinds(c.src)
		if got != c.want {
			t.Errorf("%q:\n got %s\nwant %s", c.src, got, c.want)
		}
	}
	for _, pair := range [][2]string{{"SELECT 71+-3", "SELECT 71+ -3"}, {"SELECT 1=-1", "SELECT 1 = - 1"}, {"SELECT a FROM t WHERE a<>-+1", "SELECT a FROM t WHERE a <> - + 1"}} {
		a, b := ClassifyQuery([]byte(pair[0]), false), ClassifyQuery([]byte(pair[1]), false)
		if !clsSame(a, b) {
			t.Errorf("%q %s versus %q %s", pair[0], clsDescribe(a), pair[1], clsDescribe(b))
		}
	}
}

func TestClassifierTempWordsRejectOnlyWhereATemporaryObjectIsMade(t *testing.T) {
	clsWantRejected(t, work.RejectTempObject,
		"CREATE TEMP TABLE t (a int)",
		"CREATE TEMPORARY TABLE t (a int)",
		"CREATE LOCAL TEMP TABLE t (a int)",
		"CREATE GLOBAL TEMPORARY TABLE t (a int)",
		"create global temp sequence s",
		"CREATE TEMP VIEW v AS SELECT 1",
		"CREATE OR REPLACE TEMP VIEW v AS SELECT 1",
		"CREATE OR REPLACE TEMPORARY VIEW v AS SELECT 1",
		"CREATE OR REPLACE LOCAL TEMP VIEW v AS SELECT 1",
		"CREATE /* c */ OR /* c */ REPLACE /* c */ GLOBAL /* c */ TEMPORARY VIEW v AS SELECT 1",
		"SELECT 1 INTO TEMP t",
		"SELECT 1 INTO TEMPORARY TABLE t",
		"SELECT 1 INTO LOCAL TEMP t",
		"SELECT 1 INTO GLOBAL TEMPORARY TABLE t",
		"SELECT 1; CREATE TEMP TABLE t (a int)",
		"EXPLAIN ANALYZE CREATE TEMP TABLE t AS SELECT 1",
	)
	for _, q := range []string{
		"SELECT a FROM local temp",
		"SELECT a FROM global temporary",
		"SELECT a FROM replace temp",
		"SELECT a FROM local temporary",
		"SELECT local temp FROM t",
		"SELECT a FROM t global temp",
		"SELECT replace temporary FROM t",
		"INSERT INTO temp VALUES (1)",
	} {
		for _, inTx := range []bool{false, true} {
			if u := ClassifyQuery([]byte(q), inTx); u.Class == work.ClassReject {
				t.Errorf("%q (in tx %v): %s, want forwarded: no temporary object is made", q, inTx, clsDescribe(u))
			}
		}
	}
}
