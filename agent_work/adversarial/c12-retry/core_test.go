package adv_test

import (
	"math"
	"math/rand"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/config"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/retry"
	"github.com/nickstrad/mini-gateway/internal/work"
)

func TestAdv_1_ZeroCoreHostileOffers(t *testing.T) {
	begin(t)
	p, e := retry.New(retry.Config{})
	must(t, e)
	rng := rand.New(rand.NewSource(seed))
	for i := 0; i < 10000; i++ {
		p.Attempt(retry.Time(i))
		f := eligible()
		f.Attempt = rng.Int()
		f.Reached = work.Tri(rng.Intn(256))
		if p.Decide(retry.Time(i), f).Retry {
			t.Fatalf("off granted at offer %d", i)
		}
	}
	must(t, p.Snapshot().Check())
}
func TestAdv_1_EngineDefaultAcrossRequests(t *testing.T) {
	r := newRig(t, func(c *proxy.Config) { c.Retry = retry.Config{} })
	for i := 0; i < 4; i++ {
		r.query(1001, "UPDATE marks SET n=n+1")
		r.lose(r.take(), 0, true)
		if r.retryCount() != 0 || r.replies[1001] != i+1 {
			t.Fatalf("off retry/reply=%d/%d", r.retryCount(), r.replies[1001])
		}
		r.connect()
	}
	r.end()
}
func TestAdv_1_BinaryDefaultAndExplicitZero(t *testing.T) {
	begin(t)
	for _, s := range []string{`{}`, `{"retry":{}}`, `{"retry":{"policy":"","floor":0,"bucket_cap":0}}`} {
		f := fileConfig(t, s)
		if f.Retry.Policy != "off" {
			t.Fatalf("default policy=%s", f.Retry.Policy)
		}
	}
}

func TestAdv_2_LargeAttemptIndices(t *testing.T) {
	begin(t)
	for _, n := range []int{1, 2, 255, math.MaxInt} {
		p := policy(t, 100, 100)
		f := eligible()
		f.Attempt = n
		if p.Decide(0, f).Retry {
			t.Fatalf("attempt %d granted", n)
		}
	}
}
func TestAdv_2_RepeatedRefusalAndStaleEvents(t *testing.T) {
	r := newRig(t, nil)
	r.query(1001, "UPDATE marks SET n=n+1")
	a := r.take()
	r.lose(a, 0, true)
	r.connect()
	b := r.take()
	r.lose(b, 0, true)
	for i := 0; i < 8; i++ {
		r.lose(a, 0, true)
		r.lose(b, 0, true)
	}
	if r.retryCount() != 1 || r.replies[1001] != 1 {
		t.Fatalf("retries=%d replies=%d", r.retryCount(), r.replies[1001])
	}
	r.end()
}
func TestAdv_2_OnceStateDoesNotLeakToNextRequest(t *testing.T) {
	r := newRig(t, nil)
	for i := 0; i < 3; i++ {
		r.query(1001, "UPDATE marks SET n=n+1")
		r.lose(r.take(), 0, true)
		r.connect()
		r.success(r.take(), 'I')
	}
	if r.retryCount() != 3 || r.replies[1001] != 3 {
		t.Fatalf("retries=%d replies=%d", r.retryCount(), r.replies[1001])
	}
	r.end()
}

func TestAdv_5_CarriedCreditExceedsWindowFloor(t *testing.T) {
	t.Skip("SETTLED by the orchestrator: claim 5 narrowed: over any window ten times the retries is at most the first attempts plus the credit carried in, at most the cap; stored credit does not expire with time. See agent_work/chunks/c12-retry/ADVERSARIAL.md.")
	begin(t)
	p := policy(t, 100, 1)
	for i := 0; i < 990; i++ {
		p.Attempt(0)
	}
	before := p.Snapshot()
	for i := 0; i < 20; i++ {
		p.Attempt(1)
		p.Decide(1, eligible())
	}
	after := p.Snapshot()
	a, r := after.Attempts-before.Attempts, after.Retries-before.Retries
	if 10*r > a+before.Floor {
		t.Fatalf("window first_attempts=%d retries=%d floor_tenths=%d: %d > %d", a, r, before.Floor, 10*r, a+before.Floor)
	}
}
func TestAdv_5_QuietHourRetainsBurstCredit(t *testing.T) {
	t.Skip("SETTLED by the orchestrator: claim 5 narrowed: over any window ten times the retries is at most the first attempts plus the credit carried in, at most the cap; stored credit does not expire with time. See agent_work/chunks/c12-retry/ADVERSARIAL.md.")
	begin(t)
	p := policy(t, 100, 1)
	for i := 0; i < 990; i++ {
		p.Attempt(0)
	}
	before := p.Snapshot()
	for i := 0; i < 15; i++ {
		p.Attempt(3600000000000)
		p.Decide(3600000000000, eligible())
	}
	s := p.Snapshot()
	if 10*(s.Retries-before.Retries) > s.Attempts-before.Attempts+before.Floor {
		t.Fatalf("after quiet hour retries=%d new attempts=%d floor=%d tokens; old credit still usable", s.Retries-before.Retries, s.Attempts-before.Attempts, before.Floor/10)
	}
}
func TestAdv_5_CapAtMaximumAndFractionalDeposits(t *testing.T) {
	begin(t)
	p := policy(t, retry.MaxTokens, retry.MaxTokens)
	for i := 0; i < 1009; i++ {
		p.Attempt(retry.Time(math.MaxInt64))
	}
	s := p.Snapshot()
	if s.Level.Value != 10*retry.MaxTokens || s.Expired != 1009 {
		t.Fatalf("cap/expired=%d/%d", s.Level.Value, s.Expired)
	}
	for i := 0; i < 999; i++ {
		p.Decide(0, eligible())
		p.Attempt(0)
	}
	must(t, p.Snapshot().Check())
}

func TestAdv_9_AllVetoCombinationsConserve(t *testing.T) {
	begin(t)
	p := policy(t, 10, 1)
	for mask := 0; mask < 256; mask++ {
		f := eligible()
		f.InTx = mask&1 != 0
		f.Attempt = (mask >> 1) & 1
		f.BreakerOpen = mask&4 != 0
		f.Draining = mask&8 != 0
		f.Reached = work.Tri((mask >> 4) & 3)
		if mask&64 != 0 {
			f.Deadline = 0
		}
		p.Decide(0, f)
		must(t, p.Snapshot().Check())
	}
	s := p.Snapshot()
	if s.Offered != 256 || s.Retries+s.RefusedTotal() != 256 {
		t.Fatalf("counts %+v", s)
	}
}
func TestAdv_9_ExhaustionThenExactReplenishment(t *testing.T) {
	begin(t)
	p := policy(t, 1, 1)
	if !p.Decide(0, eligible()).Retry {
		t.Fatal("initial token refused")
	}
	for i := 0; i < 9; i++ {
		p.Attempt(0)
		if d := p.Decide(0, eligible()); d.Reason != retry.RefusedBudget {
			t.Fatalf("deposit %d decision=%+v", i, d)
		}
	}
	p.Attempt(0)
	if !p.Decide(0, eligible()).Retry {
		t.Fatal("ten fractional deposits failed to refill")
	}
	s := p.Snapshot()
	if s.Refused[retry.RefusedBudget] != 9 || s.Withdrawals != 20 {
		t.Fatalf("counts=%+v", s)
	}
	must(t, s.Check())
}
func TestAdv_9_ShutdownAndLateDriverReports(t *testing.T) {
	r := newRig(t, nil)
	r.query(1001, "UPDATE marks SET n=n+1")
	a := r.take()
	r.lose(a, 0, true)
	r.end()
	for i := 0; i < 4; i++ {
		r.lose(a, len(a.Data), true)
		r.step(proxy.Event{Kind: proxy.EvClientClosed, Conn: 1001})
	}
	r.end()
	s := r.e.Snapshot()
	if s.RetryEnds[proxy.RetryEndGone] != 1 {
		t.Fatalf("shutdown ends=%v", s.RetryEnds)
	}
}

func TestAdv_12_RemainderFleetPreservesRatio(t *testing.T) {
	begin(t)
	for _, n := range []int{3, 7, 17, 99} {
		floor, e := retry.Share(100, n)
		must(t, e)
		var retries, attempts int64
		for k := 0; k < n; k++ {
			p := policy(t, 100, floor)
			for i := 0; i < 173; i++ {
				p.Attempt(0)
				p.Decide(0, eligible())
			}
			s := p.Snapshot()
			attempts += s.Attempts
			retries += s.Retries
		}
		if retries*10 > attempts+int64(n)*floor*10 || retries*10 <= int64(n)*floor*10 {
			t.Fatalf("n=%d fleet attempts=%d retries=%d", n, attempts, retries)
		}
	}
}
func TestAdv_12_ExtremeInvalidKeys(t *testing.T) {
	begin(t)
	for _, tc := range []struct {
		c   retry.Config
		key string
	}{{retry.Config{Kind: 255}, "retry.policy"}, {retry.Config{CapTokens: math.MaxInt64}, "retry.bucket_cap"}, {retry.Config{FloorTokens: math.MinInt64}, "retry.floor"}} {
		_, err := retry.New(tc.c)
		if err == nil || !strings.Contains(err.Error(), tc.key) {
			t.Fatalf("%s: %v", tc.key, err)
		}
	}
	for _, n := range []int{-1, 0, 101} {
		_, e := retry.Share(100, n)
		if e == nil {
			t.Fatalf("fleet n=%d accepted", n)
		}
	}
}
func TestAdv_12_ConfigFloorDivisionAtBoundary(t *testing.T) {
	begin(t)
	f := fileConfig(t, `{"retry":{"policy":"pre-forward-once","floor":19,"bucket_cap":20},"fleet":{"instances":6}}`)
	if f.RetryFloorPerInstance() != 3 {
		t.Fatalf("floor=%d", f.RetryFloorPerInstance())
	}
	rng := rand.New(rand.NewSource(seed))
	c, e := f.Proxy(rng, pgwire.NewKeyIssuer(rng))
	must(t, e)
	if c.Retry.FloorTokens != 3 || c.Retry.CapTokens != 20 || c.Retry.Plant != 0 {
		t.Fatalf("effective=%+v", c.Retry)
	}
}

// Complete valid binary configuration assembled independently of builder tests.
func fileConfig(t *testing.T, extra string) *config.File {
	t.Helper()
	b := configJSON(extra)
	f, e := config.Parse([]byte(b))
	must(t, e)
	return f
}
func configJSON(extra string) string {
	return `{"database":{"address":"127.0.0.1:5432","name":"adv","user":"adv","password":"pw"},"client_tls":{"mode":"off"},"backend_tls":{"mode":"disable"},"tenants":[{"user":"adv","verifier":"SCRAM-SHA-256$4096:YWxpY2Utc2FsdC0xNmJ5dA==$wFsWRjw4CdurXhNfUCJ5TYgWLpm7VaVe1BnlT3YMkHQ=:kigZ7dHs129bcEpWRXAE1OULr7tdMHWVvgKrM5fmcQU="}]` + func() string {
		e := strings.TrimSpace(extra)
		e = strings.TrimSuffix(strings.TrimPrefix(e, "{"), "}")
		if e != "" {
			return "," + e
		}
		return ""
	}() + `}`
}
