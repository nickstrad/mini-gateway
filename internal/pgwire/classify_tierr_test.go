package pgwire

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/testclient"
	"github.com/nickstrad/mini-gateway/internal/work"
)

const (
	clsTierREnv     = "MG_TIER_R"
	clsTierRAddrEnv = "MG_PG_ADDR"
	clsTierRDB      = "mg_c05_classifier"
	clsTierRTimeout = 30 * time.Second
)

var clsTierRSchema = []string{
	"DROP SCHEMA IF EXISTS public CASCADE",
	"CREATE SCHEMA public",
	"CREATE TABLE kv (k text PRIMARY KEY, v text)",
	"INSERT INTO kv VALUES ('a', '1'), ('b', '2')",
	"CREATE TABLE t (a int, b text)",
	"INSERT INTO t VALUES (1, 'x'), (2, 'y'), (3, 'z')",
	"CREATE TABLE u (a int, b text)",
	"INSERT INTO u VALUES (1, 'x')",
	"CREATE SEQUENCE s",
	"SELECT nextval('s')",
	"CREATE MATERIALIZED VIEW mv AS SELECT 1 AS a",
	"CREATE TABLE w (n int)",
	`CREATE FUNCTION public."select"() RETURNS int LANGUAGE sql AS $$INSERT INTO w VALUES (1) RETURNING 1$$`,
	`CREATE FUNCTION public."in"(int) RETURNS int LANGUAGE sql AS $$INSERT INTO w VALUES ($1) RETURNING 1$$`,
	`CREATE FUNCTION public."exists"() RETURNS int LANGUAGE sql AS $$INSERT INTO w VALUES (2) RETURNING 1$$`,
	`CREATE FUNCTION public."current_timestamp"(int) RETURNS int LANGUAGE sql AS $$INSERT INTO w VALUES ($1) RETURNING 1$$`,
	`CREATE FUNCTION evil_write(r t) RETURNS int LANGUAGE sql AS $$INSERT INTO w VALUES (r.a) RETURNING 1$$`,
	`CREATE FUNCTION evil_write_text(r t) RETURNS text LANGUAGE sql AS $$INSERT INTO w VALUES (r.a) RETURNING 'x'$$`,
}

const clsTierRChecksum = `SELECT md5(concat_ws('|',
 (SELECT string_agg(k || '=' || coalesce(v, ''), ',' ORDER BY k) FROM kv),
 (SELECT string_agg(a || ':' || coalesce(b, ''), ',' ORDER BY a, b) FROM t),
 (SELECT string_agg(a || ':' || coalesce(b, ''), ',' ORDER BY a, b) FROM u),
 (SELECT last_value::text FROM s),
 (SELECT count(*)::text FROM w),
 (SELECT count(*)::text FROM pg_class WHERE relnamespace = 'public'::regnamespace),
 (SELECT count(*)::text FROM pg_largeobject_metadata),
 (SELECT count(*)::text FROM pg_prepared_xacts)))`

const clsTierRSessionState = `SELECT md5(concat_ws('|',
 (SELECT string_agg(name || '=' || setting, ',' ORDER BY name) FROM pg_settings WHERE source = 'session' AND setting IS DISTINCT FROM reset_val),
 (SELECT count(*)::text FROM pg_locks WHERE locktype = 'advisory' AND pid = pg_backend_pid()),
 (SELECT count(*)::text FROM pg_prepared_statements),
 (SELECT count(*)::text FROM pg_cursors),
 (SELECT count(*)::text FROM pg_listening_channels()),
 (SELECT count(*)::text FROM pg_class WHERE relnamespace = pg_my_temp_schema()),
 current_user, session_user))`

type clsPG struct {
	c    net.Conn
	sess testclient.Session
	buf  []byte
}

func clsDialPG(addr, db string) (*clsPG, error) {
	c, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return nil, err
	}
	p := &clsPG{c: c, buf: make([]byte, 64<<10)}
	out := p.sess.Startup(nil, []testclient.Param{{Name: "user", Value: "postgres"}, {Name: "database", Value: db}, {Name: "application_name", Value: "mg-tier-r-c05"}, {Name: "options", Value: "-c statement_timeout=20000"}})
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

func (p *clsPG) collect() ([]testclient.Msg, error) {
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
		_ = p.c.SetReadDeadline(time.Now().Add(clsTierRTimeout))
		k, err := p.c.Read(p.buf)
		if k > 0 {
			p.sess.Feed(p.buf[:k])
			continue
		}
		if errors.Is(err, io.EOF) {
			return msgs, errors.New("connection ended")
		}
		if err != nil {
			return msgs, err
		}
	}
}

func (p *clsPG) run(q string) ([]testclient.Msg, error) {
	if _, err := p.c.Write(p.sess.Query(nil, q)); err != nil {
		return nil, err
	}
	return p.collect()
}

func clsFirstError(msgs []testclient.Msg) *testclient.Msg {
	for i := range msgs {
		if msgs[i].Type == testclient.MsgError {
			return &msgs[i]
		}
	}
	return nil
}

func (p *clsPG) must(q string) ([]testclient.Msg, error) {
	msgs, err := p.run(q)
	if err != nil {
		return msgs, fmt.Errorf("%s: %w", q, err)
	}
	if e := clsFirstError(msgs); e != nil {
		return msgs, fmt.Errorf("%s: %s %s", q, e.Code, e.Text)
	}
	return msgs, nil
}

func (p *clsPG) value(q string) (string, error) {
	msgs, err := p.must(q)
	if err != nil {
		return "", err
	}
	for _, m := range msgs {
		if m.Type == testclient.MsgDataRow && len(m.Values) == 1 {
			return string(m.Values[0]), nil
		}
	}
	return "", errors.New("no row from " + q)
}

func (p *clsPG) close() {
	_, _ = p.c.Write(testclient.EncodeTerminate(nil))
	_ = p.c.Close()
}

func clsTierRSetup(t *testing.T, addr string) *clsPG {
	t.Helper()
	root, err := clsDialPG(addr, "postgres")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"DROP DATABASE IF EXISTS " + clsTierRDB + " WITH (FORCE)", "CREATE DATABASE " + clsTierRDB} {
		if _, err := root.must(q); err != nil {
			t.Fatal(err)
		}
	}
	root.close()
	admin, err := clsDialPG(addr, clsTierRDB)
	if err != nil {
		t.Fatal(err)
	}
	return admin
}

func clsTierRResetErr(admin *clsPG) (string, error) {
	for _, q := range clsTierRSchema {
		if _, err := admin.must(q); err != nil {
			return "", err
		}
	}
	return admin.value(clsTierRChecksum)
}

func clsLastStatus(msgs []testclient.Msg) byte {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Type == testclient.MsgReady {
			return msgs[i].Status
		}
	}
	return 0
}

func TestTierRClassifierDifferential(t *testing.T) {
	if os.Getenv(clsTierREnv) != "1" {
		t.Skip("tier R: set MG_TIER_R=1 (scripts/tier-r.sh starts Postgres 17 and does this)")
	}
	addr := os.Getenv(clsTierRAddrEnv)
	if addr == "" {
		t.Fatalf("tier R: %s is not set", clsTierRAddrEnv)
	}
	admin := clsTierRSetup(t, addr)
	defer admin.close()
	clsTierROracleCanaries(t, addr, admin)
	var skipped, session int
	for _, c := range clsLoadCorpus(t) {
		u := ClassifyQuery([]byte(c.text), false)
		if u.Class == work.ClassReject || c.noexec {
			skipped++
			continue
		}
		session++
		if err := clsTierRSessionUnchangedErr(addr, admin, c); err != nil {
			t.Errorf("line %d %q: %v", c.line, c.text, err)
		}
	}
	if session < 200 {
		t.Fatalf("too few statements exercised: %d forwarded", session)
	}
	t.Logf("tier R: classifier differential: %d forwarded statements left the session state unchanged; %d not run (rejected or noexec)", session, skipped)
}

func clsTierRReadOnly(t *testing.T, addr string, admin *clsPG, c clsCase) {
	t.Helper()
	if err := clsTierRReadOnlyErr(addr, admin, c); err != nil {
		t.Errorf("line %d %q: %v", c.line, c.text, err)
	}
}

func clsTierRReadOnlyErr(addr string, admin *clsPG, c clsCase) error {
	before, err := clsTierRResetErr(admin)
	if err != nil {
		return err
	}
	p, err := clsDialPG(addr, clsTierRDB)
	if err != nil {
		return err
	}
	defer p.close()
	if _, err := p.must(c.text); err != nil {
		return fmt.Errorf("read-only statement failed on Postgres: %w", err)
	}
	after, err := admin.value(clsTierRChecksum)
	if err != nil {
		return err
	}
	if after != before {
		return errors.New("classified read-only, but the tables changed")
	}
	if _, err := p.must("BEGIN READ ONLY"); err != nil {
		return err
	}
	_, err = p.must(c.text)
	if _, rerr := p.run("ROLLBACK"); rerr != nil {
		return rerr
	}
	if err != nil {
		return fmt.Errorf("classified read-only, refused inside BEGIN READ ONLY: %w", err)
	}
	return nil
}

func clsTierRSessionUnchangedErr(addr string, admin *clsPG, c clsCase) error {
	if _, err := clsTierRResetErr(admin); err != nil {
		return err
	}
	p, err := clsDialPG(addr, clsTierRDB)
	if err != nil {
		return err
	}
	defer p.close()
	before, err := p.value(clsTierRSessionState)
	if err != nil {
		return err
	}
	msgs, err := p.run(c.text)
	if err != nil {
		return err
	}
	if clsLastStatus(msgs) != 'I' {
		if _, err := p.run("ROLLBACK"); err != nil {
			return err
		}
	}
	after, err := p.value(clsTierRSessionState)
	if err != nil {
		return err
	}
	if after != before {
		return errors.New("classified forwardable, but it changed the session state on Postgres")
	}
	return nil
}

func clsTierRCustomSettingIsSession(addr string) error {
	p, err := clsDialPG(addr, clsTierRDB)
	if err != nil {
		return err
	}
	defer p.close()
	for _, q := range []string{"SET local.x = 1", "SET transaction.y = 2", "SET constraints.z = 3"} {
		if u := ClassifyQuery([]byte(q), false); u.Class != work.ClassReject || u.Reason != work.RejectSessionSet {
			return fmt.Errorf("%q is not rejected as a session SET", q)
		}
		if _, err := p.must(q); err != nil {
			return err
		}
	}
	got, err := p.value("SELECT concat_ws(',', current_setting('local.x', true), current_setting('transaction.y', true), current_setting('constraints.z', true))")
	if err != nil {
		return err
	}
	if got != "1,2,3" {
		return fmt.Errorf("custom settings after SET local.x and friends: %q, want 1,2,3 (they are session settings)", got)
	}
	return nil
}

func clsTierRLexingSettingHidesSet(addr string) error {
	p, err := clsDialPG(addr, clsTierRDB)
	if err != nil {
		return err
	}
	defer p.close()
	enable := "SET LOCAL standard_conforming_strings = off"
	hidden := `SELECT '\' ; SELECT ' ; SET search_path = hidden ; --'`
	if u := ClassifyQuery([]byte(enable), true); u.Class != work.ClassReject {
		return fmt.Errorf("%q is not rejected", enable)
	}
	if u := ClassifyQuery([]byte(hidden), true); u.Class == work.ClassReject {
		return fmt.Errorf("the hidden SET is visible to the lexer; the canary no longer shows the hazard")
	}
	for _, q := range []string{"BEGIN", enable, hidden, "COMMIT"} {
		if _, err := p.must(q); err != nil {
			return err
		}
	}
	got, err := p.value("SHOW search_path")
	if err != nil {
		return err
	}
	if got != "hidden" {
		return fmt.Errorf("search_path %q: the lexing hazard behind rejecting SET LOCAL standard_conforming_strings did not show", got)
	}
	return nil
}

func clsTierROracleCanaries(t *testing.T, addr string, admin *clsPG) {
	t.Helper()
	for _, q := range []string{
		"SET search_path = app",
		"SELECT set_config('search_path', 'app', false)",
		"SELECT pg_advisory_lock(1)",
		"SELECT U&\"pg!005fadvisory!005flock\" UESCAPE '!' (1)",
		"PREPARE q AS SELECT 1",
		"LISTEN jobs",
		"CREATE TEMP TABLE tt (a int)",
		"CREATE TABLE pg_temp.tt (a int)",
		"DECLARE c CURSOR WITH HOLD FOR SELECT 1",
		"SET ROLE pg_monitor",
		"UPDATE pg_settings SET setting = 'app' WHERE name = 'search_path'",
	} {
		if clsTierRSessionUnchangedErr(addr, admin, clsCase{text: q}) == nil {
			t.Errorf("oracle canary: the session-state oracle does not see %q", q)
		}
		if u := ClassifyQuery([]byte(q), false); u.Class != work.ClassReject {
			t.Errorf("oracle canary %q is not rejected", q)
		}
	}
	if err := clsTierRCustomSettingIsSession(addr); err != nil {
		t.Error(err)
	}
	if err := clsTierRLexingSettingHidesSet(addr); err != nil {
		t.Error(err)
	}
	clsTierRRepairCanaries(t, addr, admin)
	for _, q := range []string{
		"SELECT nextval('s')",
		"WITH d AS (DELETE FROM kv WHERE k = 'a' RETURNING *) SELECT * FROM d",
		"SELECT a FROM t FOR UPDATE",
		"SELECT * INTO newt FROM t",
		"SELECT lo_create(0)",
	} {
		if clsTierRReadOnlyErr(addr, admin, clsCase{text: q}) == nil {
			t.Errorf("oracle canary: the read-only oracle accepts %q", q)
		}
	}
}

func clsTierRRepairCanaries(t *testing.T, addr string, admin *clsPG) {
	t.Helper()
	for _, q := range []string{
		"BEGIN; SET LOCAL search_path = 'pg_temp'; CREATE TABLE sneaky1 (a int); COMMIT",
		"BEGIN; SELECT set_config('search_path', 'pg_temp', true); CREATE TABLE sneaky2 (a int); COMMIT",
		"BEGIN; SET LOCAL SCHEMA 'pg_temp'; CREATE TABLE sneaky3 (a int); COMMIT",
		"BEGIN; SELECT set_config('search_path', 'PG_TEMP, public', true); CREATE TABLE sneaky4 (a int); COMMIT",
		"BEGIN; SET LOCAL search_path = $$pg_temp$$; CREATE TABLE sneaky5 (a int); COMMIT",
		`BEGIN; SET LOCAL search_path = E'pg\x5ftemp'; CREATE TABLE sneaky6 (a int); COMMIT`,
		"BEGIN; SELECT set_config('search_path', 'pg_' || 'temp', true); CREATE TABLE sneaky7 (a int); COMMIT",
	} {
		if err := clsTierRSessionUnchangedErr(addr, admin, clsCase{text: q}); err == nil {
			t.Errorf("repair canary: Postgres kept no session temp table after %q", q)
		}
		if u := ClassifyQuery([]byte(q), false); u.Class != work.ClassReject || u.Reason != work.RejectTempObject {
			t.Errorf("repair canary %q: %s, want reject temp-object", q, clsDescribe(u))
		}
	}
	for _, q := range []string{
		"SELECT public.select()",
		"SELECT public.in(1)",
		"SELECT public.exists()",
		"SELECT public.current_timestamp(3)",
		`SELECT "public" . select /* c */ ()`,
		"SELECT a IS DISTINCT FROM t.evil_write FROM t",
		"SELECT a IS NOT DISTINCT FROM t.evil_write FROM t",
		"SELECT trim(FROM t.evil_write_text) FROM t",
		"SELECT trim(BOTH 'x' FROM t.evil_write_text) FROM t",
		"SELECT substring('abc' FROM t.evil_write) FROM t",
		"SELECT 1 FROM t WHERE a IS DISTINCT FROM t.evil_write",
		`EXPLAIN ("analyze") SELECT public.select()`,
		`EXPLAIN ("analyze" true) SELECT public.select()`,
		`EXPLAIN (U&"analyze") SELECT public.select()`,
	} {
		err := clsTierRReadOnlyErr(addr, admin, clsCase{text: q})
		if err == nil || !strings.Contains(err.Error(), "tables changed") {
			t.Errorf("repair canary: Postgres did not write through %q: %v", q, err)
		}
		if u := ClassifyQuery([]byte(q), false); u.SafeRead || u.Shareable {
			t.Errorf("repair canary %q: %s, want neither safe-read nor shareable", q, clsDescribe(u))
		}
	}
	if err := clsTierRTimeLiteralsMove(addr); err != nil {
		t.Error(err)
	}
	if err := clsTierRUnaryOperatorBoundary(addr); err != nil {
		t.Error(err)
	}
}

func clsTierRTimeLiteralsMove(addr string) error {
	p, err := clsDialPG(addr, clsTierRDB)
	if err != nil {
		return err
	}
	defer p.close()
	for _, q := range []string{
		"SELECT 'now'::timestamptz::text",
		"SELECT CAST('now' AS timestamp)::text",
		"SELECT ('n' || 'ow')::timestamptz::text",
		"SELECT extract(epoch FROM 'now'::timestamptz)::text",
	} {
		if u := ClassifyQuery([]byte(q), false); u.Shareable || u.SafeRead {
			return fmt.Errorf("repair canary %q is shareable or safe-read", q)
		}
		a, err := p.value(q)
		if err != nil {
			return err
		}
		if _, err := p.must("SELECT pg_sleep(0.01)"); err != nil {
			return err
		}
		b, err := p.value(q)
		if err != nil {
			return err
		}
		if a == b {
			return fmt.Errorf("repair canary %q gave %q twice: the per-call hazard did not show", q, a)
		}
	}
	return nil
}

func clsTierRUnaryOperatorBoundary(addr string) error {
	p, err := clsDialPG(addr, clsTierRDB)
	if err != nil {
		return err
	}
	defer p.close()
	for _, pair := range [][2]string{{"SELECT 71+-3", "SELECT 71+ -3"}, {"SELECT 1=-1", "SELECT 1 = -1"}, {"SELECT 1 +++ 2", "SELECT 1 + + + 2"}} {
		a, err := p.value(pair[0])
		if err != nil {
			return err
		}
		b, err := p.value(pair[1])
		if err != nil {
			return err
		}
		if a != b {
			return fmt.Errorf("repair canary: Postgres gives %q for %q and %q for %q", a, pair[0], b, pair[1])
		}
		x, y := ClassifyQuery([]byte(pair[0]), false), ClassifyQuery([]byte(pair[1]), false)
		if !clsSame(x, y) {
			return fmt.Errorf("repair canary: %q %s versus %q %s", pair[0], clsDescribe(x), pair[1], clsDescribe(y))
		}
	}
	return nil
}
