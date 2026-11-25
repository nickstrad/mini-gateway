package world_test

import (
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim/world"
	"github.com/nickstrad/mini-gateway/internal/testclient"
)

const (
	tierREnv      = "MG_TIER_R"
	tierRAddr     = "MG_PG_ADDR"
	tierRBurst    = "MG_PG_BURST_ADDR"
	tierRApp      = "mg-tier-r"
	ioTimeout     = 60 * time.Second
	burstClients  = 500
	burstLimit    = 100
	manyClients   = 500
	readerClients = 100
	readsEach     = 10
	burstHold     = 5 * time.Second
)

func tierR(t *testing.T, env string) string {
	t.Helper()
	if os.Getenv(tierREnv) != "1" {
		t.Skip("tier R: set MG_TIER_R=1 (scripts/tier-r.sh starts Postgres 17 and does this)")
	}
	addr := os.Getenv(env)
	if addr == "" {
		t.Fatalf("tier R: %s is not set", env)
	}
	return addr
}

type tcpConn struct {
	c    net.Conn
	sess testclient.Session
	buf  []byte
}

func dialPG(addr, user, db string) (*tcpConn, []testclient.Msg, error) {
	c, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return nil, nil, err
	}
	tc := &tcpConn{c: c, buf: make([]byte, 64<<10)}
	out := tc.sess.Startup(nil, []testclient.Param{{Name: "user", Value: user}, {Name: "database", Value: db}, {Name: "application_name", Value: tierRApp}})
	if _, err := c.Write(out); err != nil {
		c.Close()
		return nil, nil, err
	}
	msgs, ended, err := tc.Collect(1)
	if err != nil {
		c.Close()
		return nil, msgs, err
	}
	if ended {
		c.Close()
		return nil, msgs, errors.New("startup refused: " + world.Tokens(msgs, ended, false))
	}
	return tc, msgs, nil
}

func (tc *tcpConn) Send(qs ...string) error {
	var out []byte
	for _, q := range qs {
		out = tc.sess.Query(out, q)
	}
	_, err := tc.c.Write(out)
	return err
}

func (tc *tcpConn) Collect(n int) ([]testclient.Msg, bool, error) {
	var msgs []testclient.Msg
	ready := 0
	for {
		for {
			m, ok, err := tc.sess.Next()
			if ok {
				msgs = append(msgs, m)
				if m.Type == testclient.MsgReady {
					ready++
					if ready == n {
						return msgs, false, nil
					}
				}
				continue
			}
			if err != nil && !errors.Is(err, testclient.ErrClosed) {
				return msgs, true, err
			}
			break
		}
		_ = tc.c.SetReadDeadline(time.Now().Add(ioTimeout))
		k, err := tc.c.Read(tc.buf)
		if k > 0 {
			tc.sess.Feed(tc.buf[:k])
			continue
		}
		if errors.Is(err, io.EOF) || isReset(err) {
			return msgs, true, nil
		}
		if err != nil {
			return msgs, false, err
		}
	}
}

func isReset(err error) bool {
	return err != nil && strings.Contains(err.Error(), "connection reset")
}

func (tc *tcpConn) close() {
	_, _ = tc.c.Write(testclient.EncodeTerminate(nil))
	_ = tc.c.Close()
}

func (tc *tcpConn) exec(q string) ([]testclient.Msg, error) {
	if err := tc.Send(q); err != nil {
		return nil, err
	}
	msgs, ended, err := tc.Collect(1)
	if err == nil && ended {
		err = errors.New("connection ended")
	}
	for _, m := range msgs {
		if m.Type == testclient.MsgError {
			return msgs, fmt.Errorf("%s: %s %s", q, m.Code, m.Text)
		}
	}
	return msgs, err
}

func (tc *tcpConn) value(q string) (string, error) {
	msgs, err := tc.exec(q)
	if err != nil {
		return "", err
	}
	for _, m := range msgs {
		if m.Type == testclient.MsgDataRow && len(m.Values) > 0 {
			return string(m.Values[0]), nil
		}
	}
	return "", errors.New("no row from " + q)
}

type realTarget struct {
	addr  string
	conns []*tcpConn
}

func (r *realTarget) Open(user, db string) (world.Conn, []testclient.Msg, error) {
	c, msgs, err := dialPG(r.addr, user, db)
	if err != nil {
		return nil, msgs, err
	}
	r.conns = append(r.conns, c)
	return c, msgs, nil
}

func (r *realTarget) Terminate(c world.Conn) error {
	tc, ok := c.(*tcpConn)
	if !ok {
		return errors.New("not a TCP connection")
	}
	killer, _, err := dialPG(r.addr, world.FidelityUser, world.FidelityUser)
	if err != nil {
		return err
	}
	defer killer.close()
	_, err = killer.exec(fmt.Sprintf("SELECT pg_terminate_backend(%d)", tc.sess.ProcessID()))
	return err
}

func (r *realTarget) Pause(d time.Duration) error {
	time.Sleep(d)
	return nil
}

func admin(t *testing.T, addr string, stmts ...string) *tcpConn {
	t.Helper()
	c, _, err := dialPG(addr, world.FidelityUser, world.FidelityUser)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range stmts {
		if _, err := c.exec(s); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func TestTierRFidelity(t *testing.T) {
	addr := tierR(t, tierRAddr)
	admin(t, addr, "DROP TABLE IF EXISTS kv", "CREATE TABLE kv (k text PRIMARY KEY, v text)",
		"INSERT INTO kv VALUES ('"+world.ProbeKey+"', '1')").close()
	target := &realTarget{addr: addr}
	real, err := world.RunScript(world.ProbeScript(), target)
	for _, c := range target.conns {
		_ = c.c.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Log("\n" + real.String())
	if *update || os.Getenv("MG_TIER_R_UPDATE") == "1" {
		head := "# Postgres 17 side of the fidelity script, recorded by scripts/tier-r.sh on " + time.Now().UTC().Format("2006-01-02") + "\n"
		if err := os.MkdirAll(filepath.Dir(recording), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.FromSlash(recording), []byte(head+real.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := world.Compare(real, simTranscript(t)); err != nil {
		t.Fatalf("the fake differs from Postgres 17:\n%v", err)
	}
}

func holdClients(t *testing.T, addr string, n int, perClient func(c *tcpConn) error) (int64, []error) {
	t.Helper()
	var wg sync.WaitGroup
	var ok atomic.Int64
	errs := make([]error, n)
	release := make(chan struct{})
	ready := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, _, err := dialPG(addr, world.FidelityUser, world.FidelityUser)
			if err != nil {
				errs[i] = err
				ready <- struct{}{}
				return
			}
			defer c.close()
			if err := perClient(c); err != nil {
				errs[i] = err
			} else {
				ok.Add(1)
			}
			ready <- struct{}{}
			<-release
		}(i)
	}
	for i := 0; i < n; i++ {
		<-ready
	}
	held := ok.Load()
	time.Sleep(100 * time.Millisecond)
	count := countClients(t, addr)
	close(release)
	wg.Wait()
	if held != int64(n) {
		return count, errs
	}
	return count, nil
}

func countClients(t *testing.T, addr string) int64 {
	t.Helper()
	c := admin(t, addr)
	defer c.close()
	v, err := c.value("SELECT count(*) FROM pg_stat_activity WHERE application_name = '" + tierRApp + "' AND backend_type = 'client backend' AND pid <> pg_backend_pid()")
	if err != nil {
		t.Fatal(err)
	}
	var n int64
	fmt.Sscan(v, &n)
	return n
}

func TestTierRManyConnections(t *testing.T) {
	addr := tierR(t, tierRAddr)
	admin(t, addr, "DROP TABLE IF EXISTS kv", "CREATE TABLE kv (k text PRIMARY KEY, v text)", "INSERT INTO kv VALUES ('k0', 'v')").close()
	count, errs := holdClients(t, addr, manyClients, func(c *tcpConn) error {
		for i := 0; i < 5; i++ {
			if _, err := c.exec("SELECT v FROM kv WHERE k = 'k0'"); err != nil {
				return err
			}
		}
		return nil
	})
	if errs != nil {
		t.Fatalf("clients failed: %v", errors.Join(errs...))
	}
	if count != manyClients {
		t.Fatalf("database client connections while all were held: %d, want %d", count, manyClients)
	}
	t.Logf("tier R: %d clients held %d database connections", manyClients, count)
}

func TestTierRIdenticalReads(t *testing.T) {
	addr := tierR(t, tierRAddr)
	adm := admin(t, addr, "CREATE EXTENSION IF NOT EXISTS pg_stat_statements", "DROP TABLE IF EXISTS kv",
		"CREATE TABLE kv (k text PRIMARY KEY, v text)", "INSERT INTO kv VALUES ('"+world.HotKey+"', 'h')", "SELECT pg_stat_statements_reset()")
	defer adm.close()
	start := make(chan struct{})
	var wg sync.WaitGroup
	var failures atomic.Int64
	for i := 0; i < readerClients; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, _, err := dialPG(addr, world.FidelityUser, world.FidelityUser)
			if err != nil {
				failures.Add(1)
				return
			}
			defer c.close()
			<-start
			for j := 0; j < readsEach; j++ {
				if _, err := c.exec(world.HotRead()); err != nil {
					failures.Add(1)
				}
			}
		}()
	}
	time.Sleep(500 * time.Millisecond)
	close(start)
	wg.Wait()
	if failures.Load() != 0 {
		t.Fatalf("%d reads failed", failures.Load())
	}
	v, err := adm.value("SELECT coalesce(sum(calls), 0) FROM pg_stat_statements WHERE query LIKE 'SELECT v FROM kv WHERE k = $1%'")
	if err != nil {
		t.Fatal(err)
	}
	if v != fmt.Sprint(readerClients*readsEach) {
		t.Fatalf("database executions of the identical read: %s, want %d", v, readerClients*readsEach)
	}
	t.Logf("tier R: %d identical reads cost %s database executions", readerClients*readsEach, v)
}

func TestTierRReconnectBurst(t *testing.T) {
	addr := tierR(t, tierRBurst)
	var wg sync.WaitGroup
	var ok, refused, other atomic.Int64
	var mu sync.Mutex
	var otherErr error
	r := rand.New(rand.NewPCG(1, 2))
	delays := make([]time.Duration, burstClients)
	for i := range delays {
		delays[i] = time.Duration(r.Int64N(int64(world.BurstWindow)))
	}
	for i := 0; i < burstClients; i++ {
		wg.Add(1)
		go func(d time.Duration) {
			defer wg.Done()
			time.Sleep(d)
			c, msgs, err := dialPG(addr, world.FidelityUser, world.FidelityUser)
			if err == nil {
				ok.Add(1)
				time.Sleep(burstHold)
				c.close()
				return
			}
			for _, m := range msgs {
				if m.Type == testclient.MsgError && m.Code == "53300" && m.Text == "sorry, too many clients already" {
					refused.Add(1)
					return
				}
			}
			other.Add(1)
			mu.Lock()
			otherErr = err
			mu.Unlock()
		}(delays[i])
	}
	wg.Wait()
	if other.Load() != 0 {
		t.Fatalf("%d connections failed for another reason: %v", other.Load(), otherErr)
	}
	if ok.Load() > burstLimit || refused.Load() < burstClients-burstLimit {
		t.Fatalf("accepted %d refused %d with max_connections %d", ok.Load(), refused.Load(), burstLimit)
	}
	t.Logf("tier R: burst of %d within %s against max_connections=%d: %d accepted, %d refused with 53300", burstClients, world.BurstWindow, burstLimit, ok.Load(), refused.Load())
}
