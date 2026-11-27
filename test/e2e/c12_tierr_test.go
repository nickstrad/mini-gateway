package e2e

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/obs"
)

const (
	tierRRetryClients  = 12
	tierRRetryWarm     = time.Second
	tierRRetryErrors   = 3 * time.Second
	tierRRetryRecover  = 3 * time.Second
	tierRRetrySettle   = 1500 * time.Millisecond
	tierRRetryKillGap  = 100 * time.Millisecond
	tierRRetryFastShed = 500 * time.Millisecond
	tierRRetryAttempts = 6
)

type tierROp struct {
	client, n int
	ok        bool
	start     time.Time
	attempts  int
}

type tierRStorm struct {
	mu      sync.Mutex
	ops     []tierROp
	slow    []string
	codes   map[string]int
	kills   int
	started time.Time
}

func (s *tierRStorm) add(o tierROp) {
	s.mu.Lock()
	s.ops = append(s.ops, o)
	s.mu.Unlock()
}

func (s *tierRStorm) code(c string, d time.Duration) {
	s.mu.Lock()
	s.codes[c]++
	if (c == "53M01" || c == "53M03") && d > tierRRetryFastShed {
		s.slow = append(s.slow, fmt.Sprintf("%s after %v", c, d))
	}
	s.mu.Unlock()
}

func errCode(tokens []string) string {
	for _, tok := range tokens {
		if rest, ok := strings.CutPrefix(tok, "E:"); ok {
			return rest[strings.LastIndex(rest, ":")+1:]
		}
	}
	for _, tok := range tokens {
		if strings.HasPrefix(tok, "closed") {
			return "closed"
		}
	}
	return ""
}

func (s *tierRStorm) client(env *RealEnv, g *Gateway, arm string, idx int, end time.Time) {
	var c *RealClient
	dial := func() bool {
		var err error
		c, err = env.Dial(g, idx%2, false)
		return err == nil
	}
	if !dial() {
		return
	}
	defer func() { c.Close() }()
	for n := 0; time.Now().Before(end); n++ {
		o := tierROp{client: idx, n: n, start: time.Now()}
		sql := fmt.Sprintf("INSERT INTO marks (client, round, txid, at) VALUES (%d, %d, 0, 'retry-%s')", idx, n, arm)
		for o.attempts < tierRRetryAttempts {
			o.attempts++
			sent := time.Now()
			code := errCode(c.Query(sql))
			if code == "" {
				o.ok = true
				break
			}
			s.code(code, time.Since(sent))
			if code == "closed" || code == "08007" || strings.HasPrefix(code, "08") && code != "08006" {
				c.Close()
				if !dial() {
					s.add(o)
					return
				}
				break
			}
			if code != "53M01" && code != "53M03" && code != "08006" {
				break
			}
			time.Sleep(time.Millisecond)
		}
		s.add(o)
		time.Sleep(10 * time.Millisecond)
	}
}

func (s *tierRStorm) killer(env *RealEnv, from, until time.Time, n *int) {
	time.Sleep(time.Until(from))
	sup, err := env.Super()
	if err != nil {
		return
	}
	defer sup.Close()
	for time.Now().Before(until) {
		got := sup.Query("SELECT count(pg_terminate_backend(pid)) FROM pg_stat_activity WHERE usename = '" + RealBackendUser +
			"' AND pid <> pg_backend_pid() AND random() < 0.3")
		for _, tok := range got {
			if v, ok := strings.CutPrefix(tok, "D:"); ok {
				var k int
				fmt.Sscan(v, &k)
				*n += k
			}
		}
		time.Sleep(tierRRetryKillGap)
	}
}

func tierRRetryRun(t *testing.T, env *RealEnv, arm string, retry map[string]any) (*tierRStorm, obs.InstanceSnapshot) {
	t.Helper()
	g := gateway(t, env, GatewayOpts{Scenario: "tierr-retry-storm", Conns: 4, QueueCap: 64, Tenants: 2, Retry: retry})
	s := &tierRStorm{codes: map[string]int{}, started: time.Now()}
	end := s.started.Add(tierRRetryWarm + tierRRetryErrors + tierRRetryRecover)
	var wg sync.WaitGroup
	for i := range tierRRetryClients {
		wg.Add(1)
		go func() { defer wg.Done(); s.client(env, g, arm, i, end) }()
	}
	s.killer(env, s.started.Add(tierRRetryWarm), s.started.Add(tierRRetryWarm+tierRRetryErrors), &s.kills)
	wg.Wait()
	return s, stopAndRead(t, env, g, "tierr-retry-storm")
}

func TestTierRRetryStorm(t *testing.T) {
	env := tierR(t)
	if err := env.Exec("DELETE FROM marks WHERE at LIKE 'retry-%'"); err != nil {
		t.Fatal(err)
	}
	for _, arm := range []struct {
		name  string
		retry map[string]any
	}{{"off", nil}, {"on", map[string]any{"policy": "pre-forward-once", "bucket_cap": 20, "floor": 4}}} {
		t.Run(arm.name, func(t *testing.T) {
			s, final := tierRRetryRun(t, env, arm.name, arm.retry)
			sup, err := env.Super()
			if err != nil {
				t.Fatal(err)
			}
			defer sup.Close()
			dup := sup.Query("SELECT count(*) FROM (SELECT client, round FROM marks WHERE at = 'retry-" + arm.name + "' GROUP BY client, round HAVING count(*) > 1) d")
			if !HasToken(dup, "D:0") {
				t.Fatalf("a statement was applied twice: %v", dup)
			}
			recoverFrom := s.started.Add(tierRRetryWarm + tierRRetryErrors + tierRRetrySettle)
			var ok, late, lateOK int
			for _, o := range s.ops {
				if o.ok {
					ok++
				}
				if o.start.After(recoverFrom) {
					late++
					if o.ok {
						lateOK++
					}
				}
			}
			t.Logf("%s: %d kills, %d operations, %d ok, codes %v; after the errors %d of %d ok", arm.name, s.kills, len(s.ops), ok, s.codes, lateOK, late)
			if s.kills == 0 || late == 0 || lateOK != late {
				t.Fatalf("goodput did not return: %d of %d operations ok after the errors stopped (%d kills)", lateOK, late, s.kills)
			}
			if len(s.slow) > 0 {
				t.Fatalf("refusals were slow: %v", s.slow)
			}
			none := obs.Labels{}
			attempts, retries := final.Value(obs.ComponentRetry, "attempts", none), final.Value(obs.ComponentRetry, "retries", none)
			floor := final.Value(obs.ComponentRetry, "budget_floor", none)
			forwards := ev(final, "stage_passed", obs.Labels{Mode: "forward"})
			if forwards != attempts+retries || 10*retries > attempts+floor {
				t.Fatalf("forwards %d attempts %d retries %d floor %d", forwards, attempts, retries, floor)
			}
			if arm.retry == nil && retries != 0 {
				t.Fatalf("%d retries with the policy off", retries)
			}
			if err := realConservation(final); err != nil {
				t.Fatal(err)
			}
		})
	}
}
