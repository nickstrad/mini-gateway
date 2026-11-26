package adv_test

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/testclient"
	"github.com/nickstrad/mini-gateway/internal/work"
)

const (
	advTierREnv  = "ADV_TIER_R"
	advPGAddrEnv = "ADV_PG_ADDR"
	advTierRDB   = "mg_c05_nadv_pg17"
)

type advPG struct {
	c    net.Conn
	sess testclient.Session
	buf  []byte
}

func requireTierR(t *testing.T) string {
	t.Helper()
	if os.Getenv(advTierREnv) != "1" {
		t.Skip("tier R: set ADV_TIER_R=1 and ADV_PG_ADDR=127.0.0.1:<port> (or run run-tier-r.sh)")
	}
	addr := os.Getenv(advPGAddrEnv)
	if addr == "" {
		t.Fatalf("%s=1 but %s is empty", advTierREnv, advPGAddrEnv)
	}
	return addr
}

func dialAdvPG(addr, db string) (*advPG, error) {
	c, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return nil, err
	}
	p := &advPG{c: c, buf: make([]byte, 64<<10)}
	out := p.sess.Startup(nil, []testclient.Param{
		{Name: "user", Value: "postgres"},
		{Name: "database", Value: db},
		{Name: "application_name", Value: "mg-c05-nadv"},
		{Name: "options", Value: "-c statement_timeout=20000"},
	})
	if _, err := c.Write(out); err != nil {
		c.Close()
		return nil, err
	}
	if _, err := p.collect(); err != nil {
		c.Close()
		return nil, err
	}
	return p, nil
}

func (p *advPG) collect() ([]testclient.Msg, error) {
	var msgs []testclient.Msg
	for {
		for {
			m, ok, err := p.sess.Next()
			if ok {
				msgs = append(msgs, m)
				if m.Type == testclient.MsgReady {
					return msgs, nil
				}
				continue
			}
			if err != nil && !errors.Is(err, testclient.ErrClosed) {
				return msgs, err
			}
			break
		}
		_ = p.c.SetReadDeadline(time.Now().Add(30 * time.Second))
		k, err := p.c.Read(p.buf)
		if k > 0 {
			p.sess.Feed(p.buf[:k])
			continue
		}
		if errors.Is(err, io.EOF) {
			return msgs, errors.New("connection closed")
		}
		if err != nil {
			return msgs, err
		}
	}
}

func (p *advPG) run(q string) ([]testclient.Msg, error) {
	if _, err := p.c.Write(p.sess.Query(nil, q)); err != nil {
		return nil, err
	}
	return p.collect()
}

func (p *advPG) must(q string) ([]testclient.Msg, error) {
	msgs, err := p.run(q)
	if err != nil {
		return msgs, fmt.Errorf("%s: %w", q, err)
	}
	for _, m := range msgs {
		if m.Type == testclient.MsgError {
			return msgs, fmt.Errorf("%s: SQLSTATE %s: %s", q, m.Code, m.Text)
		}
	}
	return msgs, nil
}

func (p *advPG) value(q string) (string, error) {
	msgs, err := p.must(q)
	if err != nil {
		return "", err
	}
	for _, m := range msgs {
		if m.Type == testclient.MsgDataRow && len(m.Values) == 1 {
			if m.Values[0] == nil {
				return "<NULL>", nil
			}
			return string(m.Values[0]), nil
		}
	}
	return "", fmt.Errorf("no row returned for %s", q)
}

func (p *advPG) close() {
	_, _ = p.c.Write(testclient.EncodeTerminate(nil))
	_ = p.c.Close()
}

func encodeOutcome(msgs []testclient.Msg) string {
	var b strings.Builder
	for _, m := range msgs {
		switch m.Type {
		case testclient.MsgDataRow:
			b.WriteString("D")
			for _, v := range m.Values {
				if v == nil {
					b.WriteString("|<NULL>")
				} else {
					b.WriteString("|")
					b.Write(v)
				}
			}
			b.WriteByte(';')
		case testclient.MsgCommand:
			b.WriteString("C:" + m.Tag + ";")
		case testclient.MsgError:
			b.WriteString("E:" + m.Code + ";")
		}
	}
	return b.String()
}

func setupFreshDB(t *testing.T, addr, db string) *advPG {
	t.Helper()
	root, err := dialAdvPG(addr, "postgres")
	if err != nil {
		t.Fatal(err)
	}
	defer root.close()
	for _, q := range []string{
		"DROP DATABASE IF EXISTS " + db + " WITH (FORCE)",
		"CREATE DATABASE " + db,
	} {
		if _, err := root.must(q); err != nil {
			t.Fatal(err)
		}
	}
	conn, err := dialAdvPG(addr, db)
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

// --- Claim 2: Safe shape is safe on a real server (Postgres 17, plain tables only) ---

func TestAdv_2_ParallelScanDifferentRowsOnRerun(t *testing.T) {
	t.Skip("SETTLED by the orchestrator: claim 2 narrowed: re-runs repeat the result as a multiset of rows; row order, order-dependent aggregates and float aggregates may differ under a parallel plan (RECONCILIATION.md R4). See agent_work/chunks/c05-classifier/ADVERSARIAL.md.")
	_ = advSeed(t)
	addr := requireTierR(t)

	p := setupFreshDB(t, addr, advTierRDB+"_par")
	defer p.close()

	// Create ONE plain table (> 8 MB default min_parallel_table_scan_size). No views, UDFs,
	// user-defined operators, rules, or foreign tables.
	for _, ddl := range []string{
		"CREATE TABLE big (a int, b text)",
		"INSERT INTO big SELECT g, repeat('x', 80) FROM generate_series(1, 200000) g",
		"ANALYZE big",
	} {
		if _, err := p.must(ddl); err != nil {
			t.Fatal(err)
		}
	}

	queries := []string{
		"SELECT md5(string_agg(to_hex(a), ',')) FROM (SELECT a FROM big WHERE mod(a, 10000) = 0) s",
		"SELECT a FROM big WHERE mod(a, 50000) = 0",
	}
	for _, q := range queries {
		if !pgwire.ShareFits([]byte(q)) {
			t.Fatalf("precondition failed: %q does not fit ShareFits", q)
		}
		u := pgwire.ClassifyQuery([]byte(q), false)
		if !u.SafeRead || !u.Shareable || u.Class != work.ClassStartLease {
			t.Fatalf("precondition failed: %q classified %+v", q, u)
		}

		firstMsgs, err := p.must(q)
		if err != nil {
			t.Fatal(err)
		}
		first := encodeOutcome(firstMsgs)
		// Run up to 10 immediate back-to-back re-runs with nothing else running.
		for run := 2; run <= 10; run++ {
			nextMsgs, err := p.must(q)
			if err != nil {
				t.Fatal(err)
			}
			next := encodeOutcome(nextMsgs)
			if next != first {
				t.Errorf("CLAIM 2 BROKEN on Postgres 17 plain table: safe-shape query %q returned different rows on immediate re-run #%d:\n run 1: %s\n run %d: %s",
					q, run, first, run, next)
				break
			}
		}
	}
}

func TestAdv_2_FloatSumParallelNonAssociativity(t *testing.T) {
	t.Skip("SETTLED by the orchestrator: claim 2 narrowed: re-runs repeat the result as a multiset of rows; row order, order-dependent aggregates and float aggregates may differ under a parallel plan (RECONCILIATION.md R4). See agent_work/chunks/c05-classifier/ADVERSARIAL.md.")
	_ = advSeed(t)
	addr := requireTierR(t)

	p := setupFreshDB(t, addr, advTierRDB+"_flt")
	defer p.close()

	// Single plain table with IEEE-754 double precision values whose sum depends on
	// summation order when parallel partial aggregates merge in worker race order.
	for _, ddl := range []string{
		"CREATE TABLE bigf (a int, x double precision, pad text)",
		`INSERT INTO bigf SELECT g,
		   CASE WHEN g % 3 = 0 THEN 1e16::double precision
		        WHEN g % 3 = 1 THEN -1e16::double precision
		        ELSE (g * 0.123456789)::double precision END,
		   repeat('p', 80)
		 FROM generate_series(1, 200000) g`,
		"ANALYZE bigf",
	} {
		if _, err := p.must(ddl); err != nil {
			t.Fatal(err)
		}
	}

	q := "SELECT sum(x) FROM bigf WHERE a > 0"
	if !pgwire.ShareFits([]byte(q)) {
		t.Fatalf("precondition failed: %q does not fit ShareFits", q)
	}
	first, err := p.value(q)
	if err != nil {
		t.Fatal(err)
	}
	for run := 2; run <= 15; run++ {
		next, err := p.value(q)
		if err != nil {
			t.Fatal(err)
		}
		if next != first {
			t.Errorf("CLAIM 2 BROKEN: single-scalar float8 sum(x) on plain table varied on immediate re-run #%d: run1=%s run%d=%s", run, first, run, next)
			return
		}
	}
}

func TestAdv_2_CatalogPurityOfAllowListOperatorsAndKeywords(t *testing.T) {
	_ = advSeed(t)
	addr := requireTierR(t)

	p := setupFreshDB(t, addr, advTierRDB+"_cat")
	defer p.close()

	// 1. No overload of any ShareAllowList function may be volatile or SECURITY DEFINER.
	quoted := make([]string, len(pgwire.ShareAllowList))
	for i, fn := range pgwire.ShareAllowList {
		quoted[i] = "'" + fn + "'"
	}
	q1 := fmt.Sprintf(
		"SELECT oid::regprocedure::text || ':' || provolatile::text || ':' || prosecdef::text FROM pg_proc WHERE proname IN (%s) AND (provolatile = 'v' OR prosecdef)",
		strings.Join(quoted, ","),
	)
	msgs, err := p.must(q1)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		if m.Type == testclient.MsgDataRow {
			t.Errorf("allow-list function overload is volatile or SECURITY DEFINER: %s", string(m.Values[0]))
		}
	}

	// 2. No built-in operator made solely of *=<> may be volatile.
	q2 := "SELECT o.oprname || '(' || o.oprleft::regtype || ',' || o.oprright::regtype || ')' FROM pg_operator o JOIN pg_proc pr ON o.oprcode = pr.oid WHERE o.oprname ~ '^[*=<>]+$' AND pr.provolatile = 'v'"
	msgs, err = p.must(q2)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		if m.Type == testclient.MsgDataRow {
			t.Errorf("safe-alphabet operator is volatile: %s", string(m.Values[0]))
		}
	}

	// 3. Every ShareParenKeywords entry must be category R or C (not callable as unqualified function).
	for _, kw := range pgwire.ShareParenKeywords {
		cat, err := p.value("SELECT catcode FROM pg_get_keywords() WHERE word = '" + kw + "'")
		if err != nil {
			t.Errorf("paren keyword %q lookup failed: %v", kw, err)
			continue
		}
		if cat != "R" && cat != "C" {
			t.Errorf("paren keyword %q has category %q (callable as unqualified function!)", kw, cat)
		}
	}

	// 4. Sweep EVERY keyword in pg_get_keywords(): any zero-arg expression keyword
	//    (other than boolean/null constants true/false/null) MUST be in ShareStopWords.
	q4 := `DO $$
DECLARE
  rec record;
  val text;
  found text := '';
BEGIN
  CREATE TEMP TABLE adv_zero_kw (word text);
  FOR rec IN SELECT word FROM pg_get_keywords() ORDER BY word LOOP
    BEGIN
      EXECUTE 'SELECT (' || rec.word || ')::text' INTO val;
      INSERT INTO adv_zero_kw VALUES (rec.word);
    EXCEPTION WHEN others THEN
      NULL;
    END;
  END LOOP;
END $$;`
	if _, err := p.must(q4); err != nil {
		t.Fatal(err)
	}
	msgs, err = p.must("SELECT word FROM adv_zero_kw ORDER BY word")
	if err != nil {
		t.Fatal(err)
	}
	constants := map[string]bool{"true": true, "false": true, "null": true}
	for _, m := range msgs {
		if m.Type != testclient.MsgDataRow {
			continue
		}
		w := string(m.Values[0])
		if constants[w] {
			continue
		}
		if !slices.Contains(pgwire.ShareStopWords, w) {
			t.Errorf("Postgres 17 zero-arg keyword %q is NOT on ShareStopWords!", w)
		}
	}
}

func TestAdv_2_HostileSafeShapePlainTableSafety(t *testing.T) {
	_ = advSeed(t)
	addr := requireTierR(t)

	dirtySQLBytes, err := os.ReadFile(filepath.Join(repoRoot(t), "test", "oracles", "dirty-check.sql"))
	if err != nil {
		t.Fatal(err)
	}
	dirtySQL := string(dirtySQLBytes)

	p := setupFreshDB(t, addr, advTierRDB+"_safe")
	defer p.close()

	for _, ddl := range []string{
		"CREATE TABLE kv (k text PRIMARY KEY, v text)",
		"INSERT INTO kv VALUES ('a', '1'), ('b', '2'), ('it''s', '3')",
		"CREATE TABLE nums (a int, b text, ts timestamptz, d date)",
		"INSERT INTO nums VALUES (1, 'alpha', '2025-01-01 00:00:00+00', '2025-01-01'), (2, 'beta', '2025-06-01 00:00:00+00', '2025-06-01')",
		"CREATE TABLE local (a int)",
		"INSERT INTO local VALUES (42)",
	} {
		if _, err := p.must(ddl); err != nil {
			t.Fatal(err)
		}
	}

	checksumSQL := `SELECT md5(concat_ws('|',
		(SELECT string_agg(k || '=' || coalesce(v,''), ',' ORDER BY k) FROM kv),
		(SELECT string_agg(a || ':' || coalesce(b,''), ',' ORDER BY a) FROM nums),
		(SELECT string_agg(a::text, ',' ORDER BY a) FROM local),
		(SELECT count(*)::text FROM pg_class WHERE relnamespace = 'public'::regnamespace)))`

	// Hostile but safe-shape queries covering edge syntax, typed constants, all 66 allow-list
	// functions, empty SELECT, DISTINCT ON, set ops, nested subqueries, and safe-shape rejects.
	queries := []string{
		"SELECT",
		"SELECT(1)",
		"SELECT ''",
		"SELECT ''''",
		"SELECT date 'epoch'",
		"SELECT timestamptz 'infinity'",
		"SELECT time 'allballs'",
		"SELECT regclass 'kv'",
		"SELECT DISTINCT ON (a) a, b FROM nums ORDER BY a, b",
		"SELECT a FROM nums UNION SELECT a FROM local ORDER BY 1",
		"SELECT a FROM nums INTERSECT SELECT a FROM local ORDER BY 1",
		"SELECT a FROM nums EXCEPT SELECT a FROM local ORDER BY 1",
		"SELECT * FROM (SELECT a FROM nums ORDER BY a) s WHERE EXISTS (SELECT 1 FROM kv WHERE k = 'a')",
		"SELECT a FROM local temp",
		"SELECT pg_temp",
		"SELECT CASE WHEN a = 1 THEN lower(b) ELSE upper(b) END FROM nums ORDER BY a",
		"SELECT concat(ts, d, a, b) FROM nums ORDER BY a",
		"SELECT concat_ws(',', ts, d, a, b) FROM nums ORDER BY a",
		"SELECT string_agg(b, ',' ORDER BY a) FROM nums",
		"SELECT abs(a), cbrt(a), ceil(a), ceiling(a), degrees(a), div(a, 1), exp(0), floor(a), gcd(a, 2), lcm(a, 2), ln(a), log(a), log10(a), mod(a, 2), pi(), pow(a, 2), power(a, 2), radians(a), round(a), sign(a), sqrt(a), trunc(a) FROM nums ORDER BY a",
		"SELECT ascii(b), bit_length(b), btrim(b), char_length(b), character_length(b), chr(65), initcap(b), left(b, 1), length(b), lower(b), lpad(b, 5, '0'), ltrim(b), md5(b), octet_length(b), repeat(b, 2), replace(b, 'a', 'z'), reverse(b), right(b, 1), rpad(b, 5, '0'), rtrim(b), split_part(b, 'a', 1), starts_with(b, 'a'), strpos(b, 'a'), substr(b, 1, 2), to_hex(a), translate(b, 'a', 'z'), upper(b) FROM nums ORDER BY a",
		"SELECT avg(a), bit_and(a), bit_or(a), bool_and(a > 0), bool_or(a > 1), count(*), every(a > 0), max(a), min(a), sum(a) FROM nums",
		"SELECT coalesce(b, 'x'), nullif(a, 1), greatest(a, 0), least(a, 9) FROM nums ORDER BY a",
	}

	beforeSum, err := p.value(checksumSQL)
	if err != nil {
		t.Fatal(err)
	}
	cleanSession, err := p.must(dirtySQL)
	if err != nil {
		t.Fatal(err)
	}
	cleanEnc := encodeOutcome(cleanSession)

	for _, q := range queries {
		if !pgwire.ShareFits([]byte(q)) {
			t.Fatalf("expected %q to fit ShareFits", q)
		}
		r1, err := p.run(q)
		if err != nil {
			t.Fatalf("%q run 1: %v", q, err)
		}
		r2, err := p.run(q)
		if err != nil {
			t.Fatalf("%q run 2: %v", q, err)
		}
		o1, o2 := encodeOutcome(r1), encodeOutcome(r2)
		if o1 != o2 {
			t.Errorf("%q differed on immediate re-run:\n 1: %s\n 2: %s", q, o1, o2)
		}

		if _, err := p.must("BEGIN READ ONLY"); err != nil {
			t.Fatal(err)
		}
		rRO, err := p.run(q)
		_, _ = p.run("ROLLBACK")
		if err != nil {
			t.Fatalf("%q inside BEGIN READ ONLY: %v", q, err)
		}
		oRO := encodeOutcome(rRO)
		if strings.Contains(oRO, "E:25006;") {
			t.Errorf("%q refused inside BEGIN READ ONLY with 25006: %s", q, oRO)
		}
		if oRO != o1 {
			t.Errorf("%q outcome inside BEGIN READ ONLY differs:\n outside: %s\n inside:  %s", q, o1, oRO)
		}

		afterSum, err := p.value(checksumSQL)
		if err != nil {
			t.Fatal(err)
		}
		if afterSum != beforeSum {
			t.Errorf("%q mutated watched plain tables!", q)
		}
		sessNow, err := p.must(dirtySQL)
		if err != nil {
			t.Fatal(err)
		}
		if encodeOutcome(sessNow) != cleanEnc {
			t.Errorf("%q dirtied session state: got %s want %s", q, encodeOutcome(sessNow), cleanEnc)
		}
	}
}
