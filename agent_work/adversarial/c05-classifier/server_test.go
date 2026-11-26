package adv_test

import (
	"fmt"
	p "github.com/nickstrad/mini-gateway/internal/pgwire"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

var serverOnce sync.Once
var serverName, serverFailure string

func TestMain(m *testing.M) {
	code := m.Run()
	if serverName != "" {
		exec.Command("docker", "rm", "-f", serverName).Run()
	}
	os.Exit(code)
}
func server(t *testing.T) string {
	t.Helper()
	serverOnce.Do(func() {
		serverName = fmt.Sprintf("zgw-c05-adversary-%d", os.Getpid())
		out, e := command(t, "docker", "run", "--rm", "-d", "--name", serverName, "-e", "POSTGRES_HOST_AUTH_METHOD=trust", "postgres:17")
		if e != nil {
			serverFailure = fmt.Sprintf("start: %v %s", e, out)
			return
		}
		for i := 0; i < 300; i++ {
			out, e = command(t, "docker", "exec", serverName, "psql", "-X", "-h", "127.0.0.1", "-U", "postgres", "-Atqc", "SHOW server_version_num")
			if e == nil && strings.HasPrefix(strings.TrimSpace(string(out)), "17") {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		serverFailure = fmt.Sprintf("Postgres 17 did not become ready: %s", out)
	})
	if serverFailure != "" {
		t.Fatal(serverFailure)
	}
	return serverName
}
func sql(t *testing.T, s string) string {
	t.Helper()
	out, e := command(t, "docker", "exec", server(t), "psql", "-X", "-U", "postgres", "-Atq", "-v", "ON_ERROR_STOP=1", "-c", s)
	if e != nil {
		t.Fatalf("SQL oracle failed: %v\n%s\nSQL: %s", e, out, s)
	}
	return strings.TrimSpace(string(out))
}
func fitting(t *testing.T, q string) {
	t.Helper()
	u := p.ClassifyQuery([]byte(q), false)
	if !u.SafeRead || !u.Shareable {
		t.Fatalf("attack no longer fits: %q %+v", q, u)
	}
}
func between(t *testing.T, s, a, b string) string {
	t.Helper()
	_, tail, ok := strings.Cut(s, a+"\n")
	if !ok {
		t.Fatalf("missing marker %s in %q", a, s)
	}
	part, _, ok := strings.Cut(tail, "\n"+b)
	if !ok {
		t.Fatalf("missing marker %s in %q", b, s)
	}
	return part
}
func TestAdv_2_PlainTableCoercionAndAggregates(t *testing.T) {
	mark(t)
	sql(t, "CREATE TABLE adv_plain(k integer, v text); INSERT INTO adv_plain VALUES (7,'z'),(1,'a'),(3,'b');")
	queries := []string{"SELECT string_agg(v, ' ') FROM adv_plain", "SELECT greatest(max(k), abs(29)), min(v) FROM adv_plain", "SELECT timestamp 'epoch', date 'infinity', time 'allballs'", "SELECT k FROM adv_plain WHERE k IN (SELECT k FROM adv_plain) ORDER BY k"}
	for _, q := range queries {
		fitting(t, q)
		out := sql(t, "SELECT 'A'; "+q+"; SELECT 'B'; "+q+"; SELECT 'C'; BEGIN READ ONLY; "+q+"; COMMIT; SELECT 'D'; SELECT sum(k), count(*) FROM adv_plain;")
		a, b, c := between(t, out, "A", "B"), between(t, out, "B", "C"), between(t, out, "C", "D")
		if a != b || a != c || !strings.HasSuffix(out, "11|3") {
			t.Fatalf("%q unstable/wrote/read-only differs: %q", q, out)
		}
	}
}
func TestAdv_2_PlainTablePolicyChangesSession(t *testing.T) {
	t.Skip("SETTLED by the orchestrator: claim 2 narrowed: tables with row-level security policies are outside it (RECONCILIATION.md R3). See agent_work/chunks/c05-classifier/ADVERSARIAL.md.")
	mark(t)
	q := "SELECT k FROM adv_policy"
	fitting(t, q)
	setup := "CREATE ROLE adv_policy_reader; CREATE TABLE adv_policy(k integer); INSERT INTO adv_policy VALUES (17); GRANT SELECT ON adv_policy TO adv_policy_reader; ALTER TABLE adv_policy ENABLE ROW LEVEL SECURITY; CREATE POLICY visible ON adv_policy USING (set_config('application_name','adversary_changed',false) = 'adversary_changed'); SET ROLE adv_policy_reader;"
	out := sql(t, setup+"SELECT 'A'; SHOW application_name; SELECT 'B'; "+q+"; SELECT 'C'; SHOW application_name; SELECT 'D';")
	before, after := between(t, out, "A", "B"), between(t, out, "C", "D")
	if before != after {
		t.Fatalf("SafeRead/Shareable query %q changed session application_name from %q to %q on an ordinary table with a built-in-only RLS policy", q, before, after)
	}
}
func TestAdv_2_PlainTablePolicyChangesRepeatedRows(t *testing.T) {
	t.Skip("SETTLED by the orchestrator: claim 2 narrowed: tables with row-level security policies are outside it (RECONCILIATION.md R3). See agent_work/chunks/c05-classifier/ADVERSARIAL.md.")
	mark(t)
	q := "SELECT count(*) FROM adv_repeat"
	fitting(t, q)
	setup := "CREATE ROLE adv_repeat_reader; CREATE TABLE adv_repeat(k integer); INSERT INTO adv_repeat VALUES (1); GRANT SELECT ON adv_repeat TO adv_repeat_reader; ALTER TABLE adv_repeat ENABLE ROW LEVEL SECURITY; CREATE POLICY visible ON adv_repeat USING (set_config('adv.counter', (current_setting('adv.counter')::int + 1)::text, false) = '1'); SET ROLE adv_repeat_reader; SET adv.counter = '0';"
	out := sql(t, setup+"SELECT 'A'; "+q+"; SELECT 'B'; "+q+"; SELECT 'C';")
	a, b := between(t, out, "A", "B"), between(t, out, "B", "C")
	if a != b {
		t.Fatalf("SafeRead/Shareable query %q returned %q then %q with no other activity; ordinary table, built-in-only RLS policy", q, a, b)
	}
}
