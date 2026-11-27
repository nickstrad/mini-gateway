package adv_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/shell"
	"github.com/nickstrad/mini-gateway/test/e2e"
)

type blockedCore struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockedCore) Start(_ proxy.Instant, a []proxy.Action) []proxy.Action { return a }
func (b *blockedCore) Step(_ proxy.Instant, e proxy.Event, a []proxy.Action) []proxy.Action {
	if e.Kind == proxy.EvClientAccepted {
		b.once.Do(func() { close(b.entered) })
		<-b.release
	}
	return a
}
func listen(t *testing.T) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}
func newShell(t *testing.T, core shell.Core) *shell.Shell {
	t.Helper()
	s, err := shell.New(core, shell.Config{Listener: listen(t), AdminListener: listen(t), DBAddress: "unavailable", Dial: func(ctx context.Context, _ string) (net.Conn, error) { <-ctx.Done(); return nil, ctx.Err() }, PublishInterval: time.Millisecond, Timeouts: proxy.DefaultTimeouts(), MailboxCap: 4})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func TestAdv_8_AdminWhileMailboxBackedUp(t *testing.T) {
	t.Logf("seed=%d", seed)
	b := &blockedCore{entered: make(chan struct{}), release: make(chan struct{})}
	s := newShell(t, b)
	defer close(b.release)
	var conns []net.Conn
	for i := 0; i < 12; i++ {
		c, err := net.DialTimeout("tcp", s.Addr().String(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
		defer c.Close()
	}
	select {
	case <-b.entered:
	case <-time.After(time.Second):
		t.Fatal("core did not block")
	}
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	for _, path := range []string{"/healthz", "/stats", "/metrics"} {
		resp, err := client.Get("http://" + s.AdminAddr().String() + path)
		if err != nil {
			t.Fatalf("blocked core prevents %s: %v", path, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || len(body) == 0 {
			t.Fatalf("%s returned empty/broken response", path)
		}
	}
	if s.Snapshot().MailboxDepth.Peak > 4 {
		t.Fatalf("mailbox exceeded cap: %+v", s.Snapshot().MailboxDepth)
	}
}
func TestAdv_8_ConcurrentCloseDuringDial(t *testing.T) {
	r := newRig(t, nil)
	s := newShell(t, r.e)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); s.Close() }()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("concurrent Close hung")
	}
	if s.Snapshot().Goroutines.Value != 0 {
		t.Fatalf("goroutines remain: %+v", s.Snapshot())
	}
}
func TestAdv_8_HalfStartupDisconnectStorm(t *testing.T) {
	r := newRig(t, nil)
	s := newShell(t, r.e)
	for i := 0; i < 80; i++ {
		c, err := net.DialTimeout("tcp", s.Addr().String(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		c.Write([]byte{0, 0, 0, 64, 0, 3, 0, 0, 'u'})
		c.Close()
	}
	s.Close()
	v := s.Snapshot()
	if v.Accepted == 0 || v.Goroutines.Value != 0 || v.ClientReaders.Value != 0 || v.ClientWriters.Value != 0 {
		t.Fatalf("disconnect teardown leaked: %+v", v)
	}
}

var realOnce sync.Once
var realSetup *e2e.RealSetup
var realErr error

func TestMain(m *testing.M) {
	code := m.Run()
	if realSetup != nil {
		if err := realSetup.Close(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			code = 1
		}
	}
	os.Exit(code)
}
func realEnv(t *testing.T) *e2e.RealEnv {
	t.Helper()
	if os.Getenv("ADV_LONG") != "1" {
		t.Skip("real PostgreSQL campaign: ADV_LONG=1")
	}
	t.Logf("seed=%d", seed)
	realOnce.Do(func() { realSetup, realErr = e2e.StartRealEnv(fmt.Sprintf("mg-adv-c06-%d", os.Getpid()), t.TempDir()) })
	if realErr != nil {
		t.Fatal(realErr)
	}
	realSetup.Env.RunDir = t.TempDir()
	return realSetup.Env
}
func sqlOK(t *testing.T, c *e2e.RealClient, s string) []string {
	t.Helper()
	got := c.Query(s)
	if e2e.HasToken(got, "E:") || e2e.HasToken(got, "closed:") {
		t.Fatalf("%s: %v", s, got)
	}
	return got
}
func isolation(t *testing.T, body, observe, expect string, abort bool) {
	env := realEnv(t)
	fn := "adv_" + fmt.Sprint(len(body))
	err := env.Exec("CREATE OR REPLACE FUNCTION " + fn + "() RETURNS int LANGUAGE plpgsql AS $adv$ BEGIN " + body + " RETURN 1; END $adv$")
	if err != nil {
		t.Fatal(err)
	}
	g, err := env.StartGateway(e2e.GatewayOpts{Scenario: t.Name(), Conns: 1, Tenants: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Stop()
	a, err := env.Dial(g, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := env.Dial(g, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if abort {
		sqlOK(t, a, "BEGIN")
	}
	sqlOK(t, a, "SELECT "+fn+"()")
	if abort {
		got := a.Query("SELECT 1/0")
		if !e2e.HasToken(got, "E:ERROR:22012") {
			t.Fatalf("failed transaction not exercised: %v", got)
		}
		sqlOK(t, a, "ROLLBACK")
	}
	got := sqlOK(t, b, observe)
	if !e2e.HasToken(got, expect) {
		t.Fatalf("session residue crossed tenants: %v want %s", got, expect)
	}
	sqlOK(t, b, "SELECT 808")
}
func TestAdv_3b_AbortedLeaseSettings(t *testing.T) {
	isolation(t, "PERFORM set_config('application_name','adversary',false);", "SELECT current_setting('application_name') = 'adversary'", "D:f", true)
}
func TestAdv_3b_DynamicTempPreparedListener(t *testing.T) {
	isolation(t, "EXECUTE 'CREATE TEMP TABLE adv_hidden (x int)'; EXECUTE 'PREPARE adv_hidden AS SELECT 1'; EXECUTE 'LISTEN adv_hidden';", "SELECT (SELECT count(*) FROM pg_prepared_statements WHERE name='adv_hidden') + (SELECT count(*) FROM pg_listening_channels()) + (SELECT count(*) FROM pg_class WHERE relname='adv_hidden' AND relnamespace=pg_my_temp_schema())", "D:0", false)
}
func TestAdv_3b_ReentrantAdvisoryLocks(t *testing.T) {
	isolation(t, "PERFORM pg_advisory_lock(6062026); PERFORM pg_advisory_lock(6062026); PERFORM pg_advisory_lock_shared(6062027);", "SELECT count(*) FROM pg_locks WHERE pid=pg_backend_pid() AND locktype='advisory'", "D:0", false)
}
