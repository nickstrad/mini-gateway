package pgwire

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
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
}

const clsTierRChecksum = `SELECT md5(concat_ws('|',
 (SELECT string_agg(k || '=' || coalesce(v, ''), ',' ORDER BY k) FROM kv),
 (SELECT string_agg(a || ':' || coalesce(b, ''), ',' ORDER BY a, b) FROM t),
 (SELECT string_agg(a || ':' || coalesce(b, ''), ',' ORDER BY a, b) FROM u),
 (SELECT last_value::text FROM s),
 (SELECT count(*)::text FROM pg_class WHERE relnamespace = 'public'::regnamespace),
 (SELECT count(*)::text FROM pg_largeobject_metadata),
 (SELECT count(*)::text FROM pg_prepared_xacts)))`

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
	var readOnly, closed, skipped int
	for _, c := range clsLoadCorpus(t) {
		u := ClassifyQuery([]byte(c.text), false)
		if u.Class == work.ClassReject || c.noexec {
			skipped++
			continue
		}
		if u.MayLeaveOpen() == work.No {
			closed++
			clsTierRLeavesIdle(t, addr, admin, c)
		}
		if u.ReadOnly {
			readOnly++
			clsTierRReadOnly(t, addr, admin, c)
		}
	}
	if readOnly < 100 || closed < 200 {
		t.Fatalf("too few statements exercised: %d read-only, %d transaction-free", readOnly, closed)
	}
	t.Logf("tier R: classifier differential: %d statements marked \"leaves no transaction open\" left status I; %d read-only-provable statements left the tables unchanged and ran inside BEGIN READ ONLY; %d not run (rejected or noexec)", closed, readOnly, skipped)
}

func clsTierRLeavesIdle(t *testing.T, addr string, admin *clsPG, c clsCase) {
	t.Helper()
	if err := clsTierRLeavesIdleErr(addr, admin, c); err != nil {
		t.Errorf("line %d %q: %v", c.line, c.text, err)
	}
}

func clsTierRLeavesIdleErr(addr string, admin *clsPG, c clsCase) error {
	if _, err := clsTierRResetErr(admin); err != nil {
		return err
	}
	p, err := clsDialPG(addr, clsTierRDB)
	if err != nil {
		return err
	}
	defer p.close()
	msgs, err := p.run(c.text)
	if err != nil {
		return err
	}
	if st := clsLastStatus(msgs); st != 'I' {
		return fmt.Errorf("classified as leaving no transaction open, Postgres left status %c", st)
	}
	return nil
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

func clsTierROracleCanaries(t *testing.T, addr string, admin *clsPG) {
	t.Helper()
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
	for _, q := range []string{"BEGIN", "BEGIN; SELECT 1/0; COMMIT", "SELECT 1; BEGIN"} {
		if clsTierRLeavesIdleErr(addr, admin, clsCase{text: q}) == nil {
			t.Errorf("oracle canary: the transaction oracle accepts %q", q)
		}
	}
}
