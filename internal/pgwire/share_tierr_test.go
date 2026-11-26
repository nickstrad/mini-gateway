package pgwire

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/testclient"
	"github.com/nickstrad/mini-gateway/internal/work"
)

const (
	shrTierRDB        = "mg_c05_safe_shape"
	shrTierRGenerated = 400
	shrTierRSeed      = 0x7e57
	shrDirtyCheckPath = "../../test/oracles/dirty-check.sql"
)

var shrTierRSchema = []string{
	"DROP SCHEMA IF EXISTS public CASCADE",
	"CREATE SCHEMA public",
	"CREATE TABLE kv (k text PRIMARY KEY, v text)",
	"INSERT INTO kv VALUES ('a', '1'), ('b', '2'), ('it''s', '3'), ('', '4')",
	"CREATE TABLE t (a int, b text)",
	"INSERT INTO t VALUES (1, 'x'), (2, 'y'), (3, 'z'), (4, NULL)",
	"CREATE TABLE u (a int, b text)",
	"INSERT INTO u VALUES (1, 'x'), (5, 'q')",
	"CREATE TABLE big (a int, b text)",
	"INSERT INTO big SELECT g, md5(g::text) FROM generate_series(1, 200000) g",
	"ANALYZE big",
}

var shrTierRBigTexts = []string{
	"SELECT a FROM big WHERE mod(a, 50000) = 0",
	"SELECT a, b FROM big WHERE mod(a, 20000) = 7",
	"SELECT count(*) FROM big WHERE b > 'f'",
	"SELECT sum(a) FROM big WHERE a > 10",
	"SELECT max(b), min(a), count(b) FROM big",
	"SELECT length(b), count(*) FROM big WHERE a < 150000 GROUP BY length(b)",
}

const shrTierRChecksum = `SELECT md5(concat_ws('|',
 (SELECT string_agg(k || '=' || coalesce(v, ''), ',' ORDER BY k) FROM kv),
 (SELECT string_agg(a || ':' || coalesce(b, ''), ',' ORDER BY a, b) FROM t),
 (SELECT string_agg(a || ':' || coalesce(b, ''), ',' ORDER BY a, b) FROM u),
 (SELECT count(*) || ':' || sum(a) || ':' || max(b) FROM big),
 (SELECT count(*)::text FROM pg_class WHERE relnamespace = 'public'::regnamespace),
 (SELECT count(*)::text FROM pg_largeobject_metadata),
 (SELECT count(*)::text FROM pg_prepared_xacts)))`

type shrTierR struct {
	addr  string
	admin *clsPG
	p     *clsPG
	dirty string
}

func shrOutcome(msgs []testclient.Msg) string {
	var b strings.Builder
	for _, m := range msgs {
		switch m.Type {
		case testclient.MsgDataRow:
			b.WriteString("D")
			for _, v := range m.Values {
				if v == nil {
					b.WriteString("|null")
					continue
				}
				b.WriteString("|" + string(v))
			}
			b.WriteString(";")
		case testclient.MsgCommand:
			b.WriteString("C:" + m.Tag + ";")
		case testclient.MsgError:
			b.WriteString("E:" + m.Code + ";")
		}
	}
	return b.String()
}

func shrRowMultiset(outcome string) string {
	var rows, rest []string
	for _, part := range strings.Split(outcome, ";") {
		if strings.HasPrefix(part, "D") {
			rows = append(rows, part)
			continue
		}
		rest = append(rest, part)
	}
	slices.Sort(rows)
	return strings.Join(rows, ";") + "#" + strings.Join(rest, ";")
}

func (r *shrTierR) outcome(q string) (string, error) {
	msgs, err := r.p.run(q)
	if err != nil {
		return "", err
	}
	if st := clsLastStatus(msgs); st != 'I' && st != 'T' && st != 'E' {
		return "", fmt.Errorf("status %c after %q", st, q)
	}
	return shrOutcome(msgs), nil
}

func (r *shrTierR) session() (string, error) {
	msgs, err := r.p.must(r.dirty)
	if err != nil {
		return "", err
	}
	return shrOutcome(msgs), nil
}

func (r *shrTierR) checkErr(q string) error {
	before, err := r.admin.value(shrTierRChecksum)
	if err != nil {
		return err
	}
	clean, err := r.session()
	if err != nil {
		return err
	}
	first, err := r.outcome(q)
	if err != nil {
		return err
	}
	second, err := r.outcome(q)
	if err != nil {
		return err
	}
	if shrRowMultiset(first) != shrRowMultiset(second) {
		return fmt.Errorf("(d) two runs differ as multisets of rows:\n %s\n %s", first, second)
	}
	if _, err := r.p.must("BEGIN READ ONLY"); err != nil {
		return err
	}
	inside, err := r.outcome(q)
	if _, rerr := r.p.run("ROLLBACK"); rerr != nil {
		return rerr
	}
	if err != nil {
		return err
	}
	if strings.Contains(inside, "E:25006;") {
		return errors.New("(a) refused inside BEGIN READ ONLY with 25006")
	}
	if shrRowMultiset(inside) != shrRowMultiset(first) {
		return fmt.Errorf("(a) outcome inside BEGIN READ ONLY differs:\n %s\n %s", first, inside)
	}
	after, err := r.admin.value(shrTierRChecksum)
	if err != nil {
		return err
	}
	if after != before {
		return errors.New("(b) the watched tables changed")
	}
	still, err := r.session()
	if err != nil {
		return err
	}
	if still != clean {
		return fmt.Errorf("(c) the session is not clean by the dirty check:\n %s\n %s", clean, still)
	}
	return nil
}

func shrTierRTexts(t *testing.T) (corpus, generated []string) {
	t.Helper()
	for _, c := range clsLoadCorpus(t) {
		if c.want.SafeRead && !c.noexec {
			corpus = append(corpus, c.text)
		}
	}
	for _, q := range shrFitting {
		if len(q) < 200 {
			corpus = append(corpus, q)
		}
	}
	generated = shrGenerate(shrTierRSeed, shrTierRGenerated)
	return corpus, generated
}

// TestTierRSafeShapeDifferential proves against live Postgres 17 (plain tables
// without row-level security) that safe-shape corpus, generated, and parallel-
// plan texts give the same outcome in BEGIN READ ONLY, leave watched tables
// and session state clean (via dirty-check.sql), and repeat as row multisets.
func TestTierRSafeShapeDifferential(t *testing.T) {
	if os.Getenv(clsTierREnv) != "1" {
		t.Skip("tier R: set MG_TIER_R=1 (scripts/tier-r.sh starts Postgres 17 and does this)")
	}
	addr := os.Getenv(clsTierRAddrEnv)
	if addr == "" {
		t.Fatalf("tier R: %s is not set", clsTierRAddrEnv)
	}
	dirty, err := os.ReadFile(shrDirtyCheckPath)
	if err != nil {
		t.Fatal(err)
	}
	r := &shrTierR{addr: addr, admin: shrTierRSetup(t, addr), dirty: string(dirty)}
	defer r.admin.close()
	if r.p, err = clsDialPG(addr, shrTierRDB); err != nil {
		t.Fatal(err)
	}
	defer r.p.close()
	shrTierRCanaries(t, r)
	if err := shrTierRReset(r.admin); err != nil {
		t.Fatal(err)
	}
	corpus, generated := shrTierRTexts(t)
	shrTierRParallelPlan(t, r)
	errored := 0
	for _, set := range [][]string{corpus, generated, shrTierRBigTexts} {
		for _, q := range set {
			if !ShareFits([]byte(q)) {
				t.Fatalf("%q does not fit the safe shape", q)
			}
			if err := r.checkErr(q); err != nil {
				t.Errorf("%q: %v", q, err)
			}
			if o, _ := r.outcome(q); strings.Contains(o, "E:") {
				errored++
			}
		}
	}
	if len(corpus) < 50 || len(generated) < shrTierRGenerated {
		t.Fatalf("too few texts: %d corpus, %d generated", len(corpus), len(generated))
	}
	t.Logf("tier R: safe shape differential: %d corpus, %d generated and %d parallel-plan texts that fit the safe shape gave the same outcome inside BEGIN READ ONLY (never 25006), left the tables and the session clean, and returned the same multiset of rows twice; %d of them end in a Postgres error, the same each time", len(corpus), len(generated), len(shrTierRBigTexts), errored)
}

func shrTierRParallelPlan(t *testing.T, r *shrTierR) {
	t.Helper()
	parallel := 0
	for _, q := range shrTierRBigTexts {
		msgs, err := r.p.must("EXPLAIN " + q)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(shrOutcome(msgs), "Parallel Seq Scan") {
			parallel++
		}
	}
	if parallel == 0 {
		t.Fatalf("no text on table big gets a parallel plan: the oracle does not see parallel scans")
	}
	t.Logf("tier R: %d of %d texts on table big get a parallel plan", parallel, len(shrTierRBigTexts))
}

func shrTierRSetup(t *testing.T, addr string) *clsPG {
	t.Helper()
	root, err := clsDialPG(addr, "postgres")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"DROP DATABASE IF EXISTS " + shrTierRDB + " WITH (FORCE)", "CREATE DATABASE " + shrTierRDB} {
		if _, err := root.must(q); err != nil {
			t.Fatal(err)
		}
	}
	root.close()
	admin, err := clsDialPG(addr, shrTierRDB)
	if err != nil {
		t.Fatal(err)
	}
	if err := shrTierRReset(admin); err != nil {
		t.Fatal(err)
	}
	return admin
}

func shrTierRReset(admin *clsPG) error {
	for _, q := range shrTierRSchema {
		if _, err := admin.must(q); err != nil {
			return err
		}
	}
	return nil
}

func shrTierRCanaries(t *testing.T, r *shrTierR) {
	t.Helper()
	caught := map[string]int{}
	for _, q := range []string{
		"SET search_path = public, pg_catalog",
		"PREPARE shr_q AS SELECT 1",
		"LISTEN shr_channel",
		"SELECT pg_advisory_lock(7)",
		"CREATE TEMP TABLE shr_tmp (a int)",
		"DECLARE shr_c CURSOR WITH HOLD FOR SELECT 1",
		"INSERT INTO kv VALUES ('z', '9')",
		"DELETE FROM u WHERE a = 0",
		"UPDATE kv SET v = v",
		"SELECT random()",
		"SELECT clock_timestamp()",
		"SELECT 'now'::timestamptz::text",
	} {
		if err := shrTierRReset(r.admin); err != nil {
			t.Fatal(err)
		}
		p, err := clsDialPG(r.addr, shrTierRDB)
		if err != nil {
			t.Fatal(err)
		}
		fresh := &shrTierR{addr: r.addr, admin: r.admin, p: p, dirty: r.dirty}
		err = fresh.checkErr(q)
		p.close()
		if err == nil || !strings.HasPrefix(err.Error(), "(") {
			t.Errorf("oracle canary %q: no oracle caught it (%v)", q, err)
			continue
		}
		caught[err.Error()[:3]]++
		if ShareFits([]byte(q)) {
			t.Errorf("oracle canary %q fits the safe shape", q)
		}
	}
	for _, o := range []string{"(a)", "(c)", "(d)"} {
		if caught[o] == 0 {
			t.Errorf("no canary was caught by oracle %s: %v", o, caught)
		}
	}
	before, err := r.admin.value(shrTierRChecksum)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.must("UPDATE t SET b = 'w' WHERE a = 1"); err != nil {
		t.Fatal(err)
	}
	if after, err := r.admin.value(shrTierRChecksum); err != nil || after == before {
		t.Errorf("oracle canary (b): the checksum does not see a write (%v)", err)
	}
	for _, q := range []string{
		"SELECT 1 / (('now'::timestamptz = transaction_timestamp())::int)",
		"SELECT 1 / ((CAST('today' AS date) = current_date)::int)",
	} {
		if _, err := r.p.must(q); err != nil {
			t.Errorf("time literal oracle: Postgres does not resolve the literal to the current time: %v", err)
		}
		if ShareFits([]byte(q)) {
			t.Errorf("%q fits the safe shape", q)
		}
	}
	shrTierRKeywordCategories(t, r)
	shrTierRContinuation(t, r)
}

func shrTierRKeywordCategories(t *testing.T, r *shrTierR) {
	t.Helper()
	for _, w := range ShareParenKeywords {
		cat, err := r.p.value("SELECT catcode FROM pg_get_keywords() WHERE word = '" + w + "'")
		if err != nil {
			t.Errorf("paren keyword %q: %v", w, err)
			continue
		}
		if cat != "R" && cat != "C" {
			t.Errorf("paren keyword %q has category %s on Postgres: a function of that name can be called unqualified", w, cat)
		}
	}
}

func shrTierRContinuation(t *testing.T, r *shrTierR) {
	t.Helper()
	for _, q := range []string{"SELECT E'ok'\n'\\'; SET application_name = 'adv_escape'; --'", "SELECT E'ok'\n'\\'; LISTEN adv_escape; --'"} {
		before, err := r.session()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.p.run(q); err != nil {
			t.Fatal(err)
		}
		after, err := r.session()
		if err != nil {
			t.Fatal(err)
		}
		app, err := r.p.value("SHOW application_name")
		if err != nil {
			t.Fatal(err)
		}
		if before != after || app == "adv_escape" {
			t.Errorf("%q changed the session on Postgres; the lexer reads it as one string", q)
		}
		if u := ClassifyQuery([]byte(q), false); u.Class == work.ClassReject {
			t.Errorf("%q is rejected; Postgres runs no session change here", q)
		}
	}
	hidden := "SELECT E'ok'\n'\\' ; SELECT ' ; SET search_path = hidden ; --'"
	if _, err := r.p.must(hidden); err != nil {
		t.Fatal(err)
	}
	got, err := r.p.value("SHOW search_path")
	if err != nil {
		t.Fatal(err)
	}
	if got != "hidden" {
		t.Errorf("continuation canary: search_path %q, want hidden: the hazard behind the lexer fix did not show", got)
	}
	if u := ClassifyQuery([]byte(hidden), false); u.Class != work.ClassReject || u.Reason != work.RejectSessionSet {
		t.Errorf("continuation canary %q: %s, want reject session-set", hidden, clsDescribe(u))
	}
	if _, err := r.p.must("RESET search_path"); err != nil {
		t.Fatal(err)
	}
}
