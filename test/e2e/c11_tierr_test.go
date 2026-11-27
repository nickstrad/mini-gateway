package e2e

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/randdist"
)

const (
	c11HotKey        = "c11hot"
	c11HotRead       = "SELECT v FROM c11slow"
	c11LongRead      = "SELECT v FROM c11long"
	c11HotSleep      = 500 * time.Millisecond
	c11BurstSize     = 1000
	c11Conns         = 20
	c11CancelGroup   = 100
	c11CancelAt      = 300 * time.Millisecond
	c11DirectClients = 100
	c11ZipfKeys      = 200
	c11ZipfClients   = 100
	c11ZipfReads     = 30
	c11ZipfExponent  = 1100
	c11ZipfSeed      = 11
)

func c11Setup(t *testing.T, env *RealEnv) {
	t.Helper()
	sqls := []string{
		"INSERT INTO kv VALUES ('" + c11HotKey + "', 'old') ON CONFLICT (k) DO UPDATE SET v = 'old'",
		"CREATE OR REPLACE VIEW c11slow AS SELECT v FROM kv, (SELECT pg_sleep(0.5)) s WHERE k = '" + c11HotKey + "'",
		"CREATE OR REPLACE VIEW c11long AS SELECT v FROM kv, (SELECT pg_sleep(2)) s WHERE k = '" + c11HotKey + "'",
		"CREATE OR REPLACE VIEW c11kv AS SELECT k, v FROM kv, (SELECT pg_sleep(0.01)) s",
		"GRANT SELECT ON c11slow, c11long, c11kv TO " + RealBackendUser,
	}
	for i := 0; i < c11ZipfKeys; i++ {
		sqls = append(sqls, "INSERT INTO kv VALUES ('z"+strconv.Itoa(i)+"', 'z') ON CONFLICT (k) DO NOTHING")
	}
	if err := env.Exec(sqls...); err != nil {
		t.Fatal(err)
	}
}

func c11Calls(t *testing.T, env *RealEnv, query string) int64 {
	t.Helper()
	c, err := env.Super()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	got := c.Query("SELECT coalesce(sum(calls), 0) FROM pg_stat_statements WHERE query = '" + strings.ReplaceAll(query, "'", "''") +
		"' AND dbid = (SELECT oid FROM pg_database WHERE datname = '" + RealDatabase + "')")
	for _, tk := range got {
		if strings.HasPrefix(tk, "D:") {
			n, err := strconv.ParseInt(strings.TrimPrefix(tk, "D:"), 10, 64)
			if err == nil {
				return n
			}
		}
	}
	t.Fatalf("pg_stat_statements: %v", got)
	return 0
}

func c11Gateway(t *testing.T, env *RealEnv, scenario, mode, join string) *Gateway {
	return gateway(t, env, GatewayOpts{Scenario: scenario, Conns: c11Conns, QueueCap: 1024, Tenants: 4, MaxStartup: 1024,
		MaxClients: 2048, Timeouts: map[string]string{"queue_wait": "60s"},
		Queue:    map[string]any{"rule": "timeout", "interval": "60s", "target": "30s", "tenant_capacity": 1024},
		Coalesce: map[string]any{"mode": mode, "join": join}})
}

type burstResult struct {
	replies [][]string
	elapsed []time.Duration
}

func c11Burst(t *testing.T, n int, dial func(i int) (*RealClient, error), sql string, during func(cs []*RealClient)) burstResult {
	t.Helper()
	cs := make([]*RealClient, n)
	sem := make(chan struct{}, 64)
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			cs[i], errs[i] = dial(i)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("client %d: %v", i, err)
		}
	}
	res := burstResult{replies: make([][]string, n), elapsed: make([]time.Duration, n)}
	gate := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			t0 := time.Now()
			_ = cs[i].Conn().SetDeadline(time.Now().Add(2 * time.Minute))
			res.replies[i] = cs[i].Query(sql)
			res.elapsed[i] = time.Since(t0)
		}()
	}
	close(gate)
	if during != nil {
		during(cs)
	}
	wg.Wait()
	for _, c := range cs {
		c.Close()
	}
	return res
}

func c11Value(st []string, v string) bool {
	return slices.Contains(st, "D:"+v) && len(st) > 0 && st[len(st)-1] == "ZI" && !HasToken(st, "E:")
}

type hotArm struct {
	name    string
	mode    string
	join    string
	reads   int64
	p50     time.Duration
	p99     time.Duration
	clients int
}

func TestTierRCoalesceHotKey(t *testing.T) {
	env := tierR(t)
	c11Setup(t, env)
	var arms []hotArm
	for _, a := range []hotArm{{name: "strict", mode: "auto", join: "strict"}, {name: "relaxed", mode: "auto", join: "relaxed"},
		{name: "off", mode: "off", join: "strict"}} {
		scenario := "tierr-coalesce-hot-key-" + a.name
		g := c11Gateway(t, env, scenario, a.mode, a.join)
		before := c11Calls(t, env, c11HotRead)
		r := c11Burst(t, c11BurstSize, func(i int) (*RealClient, error) { return env.Dial(g, i%4, false) }, c11HotRead, nil)
		a.reads = c11Calls(t, env, c11HotRead) - before
		for i, st := range r.replies {
			if !c11Value(st, "old") {
				t.Fatalf("%s: client %d: %v", a.name, i, st)
			}
		}
		a.p50, a.p99, a.clients = Percentile(r.elapsed, 500), Percentile(r.elapsed, 990), c11BurstSize
		final := stopAndRead(t, env, g, scenario)
		if a.mode != "off" {
			if a.reads < 1 || a.reads > 2 {
				t.Fatalf("%s: %d database reads for %d identical reads, want 1 or 2", a.name, a.reads, c11BurstSize)
			}
			if hits := final.Value(obs.ComponentCoalescer, "hits", obs.Labels{}); hits < c11BurstSize-2 {
				t.Fatalf("%s: %d hits", a.name, hits)
			}
		} else if a.reads != c11BurstSize {
			t.Fatalf("coalescer off: %d database reads, want %d", a.reads, c11BurstSize)
		}
		arms = append(arms, a)
	}
	before := c11Calls(t, env, c11HotRead)
	d := c11Burst(t, c11DirectClients, func(int) (*RealClient, error) { return env.DialDirect(false) }, c11HotRead, nil)
	arms = append(arms, hotArm{name: "direct", reads: c11Calls(t, env, c11HotRead) - before, p50: Percentile(d.elapsed, 500),
		p99: Percentile(d.elapsed, 990), clients: c11DirectClients})
	for _, a := range arms {
		t.Logf("HEADLINE hot-key %s: %d clients, %d database reads, p50 %v, p99 %v (read time %v)", a.name, a.clients, a.reads,
			a.p50.Round(time.Millisecond), a.p99.Round(time.Millisecond), c11HotSleep)
	}
}

func TestTierRCoalesceCancel99(t *testing.T) {
	env := tierR(t)
	c11Setup(t, env)
	for _, join := range []string{"strict", "relaxed"} {
		t.Run(join, func(t *testing.T) {
			g := c11Gateway(t, env, "tierr-coalesce-cancel-99-"+join, "auto", join)
			before := c11Calls(t, env, c11LongRead)
			r := c11Burst(t, c11CancelGroup, func(i int) (*RealClient, error) { return env.Dial(g, i%4, false) }, c11LongRead,
				func(cs []*RealClient) {
					time.Sleep(c11CancelAt)
					for _, c := range cs[:len(cs)-1] {
						if err := cancelThrough(g, env, c); err != nil {
							t.Errorf("cancel: %v", err)
						}
					}
				})
			last := len(r.replies) - 1
			if !c11Value(r.replies[last], "old") {
				t.Fatalf("the waiter that stayed got %v", r.replies[last])
			}
			for i, st := range r.replies[:last] {
				if !HasToken(st, "E:ERROR:57014") {
					t.Fatalf("client %d cancelled and got %v", i, st)
				}
			}
			if n := c11Calls(t, env, c11LongRead) - before; n != 1 {
				t.Fatalf("%d reads completed on the database, want exactly the one the last waiter needed", n)
			}
			final := stopAndRead(t, env, g, "tierr-coalesce-cancel-99-"+join)
			if ev(final, "coalesce_cancels", obs.Labels{}) == 0 {
				t.Fatal("no waiter's cancel was answered by the coalescer")
			}
		})
	}
}

func TestTierRCoalesceAllCancel(t *testing.T) {
	env := tierR(t)
	c11Setup(t, env)
	g := c11Gateway(t, env, "tierr-coalesce-all-cancel", "auto", "relaxed")
	before := c11Calls(t, env, c11LongRead)
	r := c11Burst(t, c11CancelGroup, func(i int) (*RealClient, error) { return env.Dial(g, i%4, false) }, c11LongRead,
		func(cs []*RealClient) {
			time.Sleep(c11CancelAt)
			for _, c := range cs {
				if err := cancelThrough(g, env, c); err != nil {
					t.Errorf("cancel: %v", err)
				}
			}
		})
	for i, st := range r.replies {
		if !HasToken(st, "E:ERROR:57014") {
			t.Fatalf("client %d cancelled and got %v", i, st)
		}
	}
	time.Sleep(3 * time.Second)
	if n := c11Calls(t, env, c11LongRead) - before; n != 0 {
		t.Fatalf("%d reads completed on the database after every waiter cancelled, want 0", n)
	}
	c, err := env.Dial(g, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if st := c.Query("SELECT 1"); !HasToken(st, "D:1") {
		t.Fatalf("no recovery: %v", st)
	}
	c.Close()
	final := stopAndRead(t, env, g, "tierr-coalesce-all-cancel")
	if ev(final, "coalesce_cancels", obs.Labels{}) != c11CancelGroup-1 || ev(final, "client_cancels", obs.Labels{Outcome: "forwarded"}) != 1 {
		t.Fatalf("cancels: %d answered by the coalescer, %d forwarded; want %d and 1", ev(final, "coalesce_cancels", obs.Labels{}),
			ev(final, "client_cancels", obs.Labels{Outcome: "forwarded"}), c11CancelGroup-1)
	}
}

func TestTierRCoalesceReadYourWrite(t *testing.T) {
	env := tierR(t)
	c11Setup(t, env)
	for _, join := range []string{"strict", "relaxed"} {
		t.Run(join, func(t *testing.T) {
			if err := env.Exec("UPDATE kv SET v = 'old' WHERE k = '" + c11HotKey + "'"); err != nil {
				t.Fatal(err)
			}
			g := c11Gateway(t, env, "tierr-coalesce-read-your-write-"+join, "auto", join)
			reader, err := env.Dial(g, 0, false)
			if err != nil {
				t.Fatal(err)
			}
			writer, err := env.Dial(g, 1, false)
			if err != nil {
				t.Fatal(err)
			}
			var first []string
			done := make(chan struct{})
			go func() { first = reader.Query(c11HotRead); close(done) }()
			time.Sleep(100 * time.Millisecond)
			if st := writer.Query("UPDATE kv SET v = 'new' WHERE k = '" + c11HotKey + "'"); !HasToken(st, "C:UPDATE 1") {
				t.Fatalf("write: %v", st)
			}
			after := writer.Query(c11HotRead)
			<-done
			reader.Close()
			writer.Close()
			if !c11Value(first, "old") {
				t.Fatalf("first read %v", first)
			}
			switch join {
			case "strict":
				if !c11Value(after, "new") {
					t.Fatalf("strict: a read after the client's own acknowledged write returned %v", after)
				}
			default:
				if !c11Value(after, "old") {
					t.Fatalf("relaxed: expected the window (the old value from the read already running), got %v", after)
				}
				t.Logf("relaxed: the read after the write returned the old value: the window, as stated")
			}
			stopAndRead(t, env, g, "tierr-coalesce-read-your-write-"+join)
		})
	}
}

func TestTierRCoalesceResultsEqualDirect(t *testing.T) {
	env := tierR(t)
	spec := ResultsSpec("tierr-coalesce-results", false)
	if err := env.ResetRows(RowsSQL(spec)); err != nil {
		t.Fatal(err)
	}
	direct := RunReal(context.Background(), spec, func(int) (*RealClient, error) { return env.DialDirect(false) }, false)
	if err := env.ResetRows(RowsSQL(spec)); err != nil {
		t.Fatal(err)
	}
	g := gateway(t, env, GatewayOpts{Scenario: "tierr-coalesce-results", Conns: 3, Tenants: 3, Coalesce: map[string]any{"mode": "auto"}})
	proxied := RunReal(context.Background(), spec, func(i int) (*RealClient, error) { return env.Dial(g, i, false) }, false)
	if err := ResultsEqual(direct, proxied); err != nil {
		t.Fatal(err)
	}
	final := stopAndRead(t, env, g, "tierr-coalesce-results")
	if final.Value(obs.ComponentCoalescer, "offered", obs.Labels{}) == 0 {
		t.Fatal("coalescing was not offered any read")
	}
}

type zipfArm struct {
	name, mode, join string
	reads            int64
	p50, p99         time.Duration
}

func zipfKeys() ([][]int, string) {
	r := rand.New(rand.NewPCG(c11ZipfSeed, 0))
	z := randdist.NewZipf(r, c11ZipfExponent, c11ZipfKeys)
	keys := make([][]int, c11ZipfClients)
	counts := make([]int, c11ZipfKeys)
	for c := range keys {
		for range c11ZipfReads {
			k := int(z.Uint64())
			keys[c] = append(keys[c], k)
			counts[k]++
		}
	}
	total := c11ZipfClients * c11ZipfReads
	top10, used := 0, 0
	sorted := slices.Clone(counts)
	slices.SortFunc(sorted, func(a, b int) int { return b - a })
	for i, n := range sorted {
		if i < 10 {
			top10 += n
		}
		if n > 0 {
			used++
		}
	}
	return keys, fmt.Sprintf("Zipf s=%d.%03d over %d keys, %d reads: hottest key %d (%.1f%%), top 10 keys %.1f%%, %d keys used",
		c11ZipfExponent/1000, c11ZipfExponent%1000, c11ZipfKeys, total, sorted[0], 100*float64(sorted[0])/float64(total),
		100*float64(top10)/float64(total), used)
}

func TestTierRCoalesceHeadlineZipf(t *testing.T) {
	env := tierR(t)
	c11Setup(t, env)
	keys, shape := zipfKeys()
	t.Logf("HEADLINE zipf generator: %s", shape)
	query := func(k int) string { return "SELECT v FROM c11kv WHERE k = 'z" + strconv.Itoa(k) + "'" }
	const norm = "SELECT v FROM c11kv WHERE k = $1"
	var arms []zipfArm
	for _, a := range []zipfArm{{name: "off", mode: "off", join: "strict"}, {name: "strict", mode: "auto", join: "strict"},
		{name: "relaxed", mode: "auto", join: "relaxed"}} {
		scenario := "tierr-coalesce-zipf-" + a.name
		g := c11Gateway(t, env, scenario, a.mode, a.join)
		before := c11Calls(t, env, norm)
		var mu sync.Mutex
		var lat []time.Duration
		var wg sync.WaitGroup
		for c := range c11ZipfClients {
			wg.Add(1)
			go func() {
				defer wg.Done()
				cl, err := env.Dial(g, c%4, false)
				if err != nil {
					t.Error(err)
					return
				}
				defer cl.Close()
				for _, k := range keys[c] {
					t0 := time.Now()
					st := cl.Query(query(k))
					d := time.Since(t0)
					if !c11Value(st, "z") {
						t.Errorf("key %d: %v", k, st)
						return
					}
					mu.Lock()
					lat = append(lat, d)
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		a.reads = c11Calls(t, env, norm) - before
		a.p50, a.p99 = Percentile(lat, 500), Percentile(lat, 990)
		stopAndRead(t, env, g, scenario)
		arms = append(arms, a)
	}
	total := int64(c11ZipfClients * c11ZipfReads)
	for _, a := range arms {
		t.Logf("HEADLINE zipf %s: %d requests, %d database reads (%.1f%%), p50 %v, p99 %v", a.name, total, a.reads,
			100*float64(a.reads)/float64(total), a.p50.Round(time.Millisecond), a.p99.Round(time.Millisecond))
	}
	if arms[0].reads != total {
		t.Fatalf("coalescer off: %d reads for %d requests", arms[0].reads, total)
	}
	for _, a := range arms[1:] {
		if a.reads >= total {
			t.Fatalf("%s: coalescing saved no database read", a.name)
		}
	}
}
