package e2e

import (
	"time"

	"github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/coalesce"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/proxy/proxysim"
	"github.com/nickstrad/mini-gateway/internal/retry"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/work"
)

const (
	joinReaders      = 200
	joinWriters      = 8
	joinWrites       = 30
	joinWriteTime    = 20 * time.Millisecond
	joinBurstAt      = 100 * time.Millisecond
	joinHeavyWeight  = 3
	joinWarmups      = 4
	joinDoomAt       = 500 * time.Millisecond
	joinRetryBurstAt = 600 * time.Millisecond
)

func joinWrite(c, i int) string {
	return "UPDATE kv SET v = 'w" + itoa(i) + "' WHERE k = '" + key(c, "a") + "'"
}

func HotKeyWeightedSpec(join coalesce.JoinMode) Spec {
	sp := coalBase("coalesce-hot-key-weighted-"+joinName(join), join, hotService)
	sp.Tenants = 2
	sp.Duration = 10 * time.Second
	sp.Rows = coalRows(clientRows(2 * joinWriters)...)
	sp.Service.PerKind = append(sp.Service.PerKind, fakepg.KindTime{Kind: fakepg.KindUpdate, Time: joinWriteTime})
	sp.Fairness = admission.FairConfig{Weights: []admission.TenantWeight{{Tenant: proxysim.TenantName(0), Weight: joinHeavyWeight},
		{Tenant: proxysim.TenantName(1), Weight: 1}}}
	for c := 0; c < 2*joinWriters; c++ {
		var steps []Step
		for i := 0; i < joinWrites; i++ {
			steps = append(steps, Step{SQL: joinWrite(c, i)})
		}
		sp.Clients = append(sp.Clients, Script{Tenant: c % 2, Start: time.Duration(c) * time.Millisecond, Steps: steps})
	}
	for i := 0; i < joinReaders; i++ {
		sp.Clients = append(sp.Clients, Script{Tenant: i % 2, Start: joinBurstAt + time.Duration(i)*hotSpread, Steps: []Step{{SQL: hotRead()}}})
	}
	return sp
}

func readerIndexes(r *SimRun) []int {
	var out []int
	for c, sc := range r.Spec.Clients {
		if len(sc.Steps) == 1 && sc.Steps[0].SQL == hotRead() {
			out = append(out, c)
		}
	}
	return out
}

func checkHotReaders(r *SimRun) error {
	if err := everyClientFinished(r); err != nil {
		return err
	}
	reads := readsOf(r, hotRead())
	if len(reads) < 1 || len(reads) > 2 {
		return failf("%d database reads for a burst of %d identical reads, want 1 or 2", len(reads), len(readerIndexes(r)))
	}
	if n := MaxConcurrent(reads); n > 1 {
		return failf("%d reads of one key running on the database at once", n)
	}
	for _, c := range readerIndexes(r) {
		if st := stepOf(r, c, 0); !gotValue(st, hotValue) {
			return failf("reader %d (tenant %d): %v", c, r.Spec.Clients[c].Tenant, st)
		}
	}
	return nil
}

func checkHotKeyWeighted(r *SimRun) error {
	if err := checkHotReaders(r); err != nil {
		return err
	}
	hits := map[work.TenantID]int64{}
	for _, row := range r.World.Proxy.Engine.CoalesceSnapshot().PerTenant {
		hits[row.Tenant] = row.Hits
	}
	if hits[work.TenantID(proxysim.TenantName(0))] == 0 || hits[work.TenantID(proxysim.TenantName(1))] == 0 {
		return failf("shared replies by tenant %v: followers of both weights must ride the flight", hits)
	}
	a := r.World.Proxy.Engine.AdmissionSnapshot()
	if !a.Fair.On || a.Fair.HeldShare == 0 {
		return failf("the fair dispatcher never held a tenant at its share (on %v): the weights did not bind", a.Fair.On)
	}
	if a.Fair.LeaseStarts != r.Engine.LeasesStarted {
		return failf("fair lease starts %d, engine %d", a.Fair.LeaseStarts, r.Engine.LeasesStarted)
	}
	return coalSnapOK(r)
}

func HotKeyRetrySpec(join coalesce.JoinMode) Spec {
	sp := coalBase("coalesce-hot-key-retry-"+joinName(join), join, hotService)
	sp.Path = nil
	sp.Relay = &RelayPlan{DoomAllAt: []time.Duration{joinDoomAt}}
	sp.Retry = retry.Config{Kind: retry.KindPreForwardOnce}
	sp.Duration = 5 * time.Second
	sp.Rows = coalRows(clientRows(joinWarmups)...)
	sp.Service.PerKind = append(sp.Service.PerKind, fakepg.KindTime{Kind: fakepg.KindUpdate, Time: joinWriteTime})
	for c := 0; c < joinWarmups; c++ {
		sp.Clients = append(sp.Clients, Script{Tenant: c % 4, Steps: []Step{{SQL: joinWrite(c, 0)}}})
	}
	for i := 0; i < joinReaders; i++ {
		sp.Clients = append(sp.Clients, Script{Tenant: i % 4, Start: joinRetryBurstAt + time.Duration(i)*hotSpread, Steps: []Step{{SQL: hotRead()}}})
	}
	return sp
}

func checkHotKeyRetry(r *SimRun) error {
	if err := checkHotReaders(r); err != nil {
		return err
	}
	if r.Relay == nil || r.Relay.Stats().Dooms == 0 {
		return failf("no connection was doomed before the burst")
	}
	e := r.Engine
	if e.RetryAsked == 0 || e.RetryRows[pgwire.ErrRowLostAfterRead] == 0 || e.RetryEnds[proxy.RetryEndCompleted] != e.RetriedRequests {
		return failf("the leader's zero-byte refusal was not retried to completion: asked %d, rows %v, ends %v of %d", e.RetryAsked,
			e.RetryRows, e.RetryEnds, e.RetriedRequests)
	}
	if e.CoalesceErrors != 0 || e.CoalesceReplies < int64(joinReaders-2) {
		return failf("followers: %d shared errors, %d shared replies for %d readers", e.CoalesceErrors, e.CoalesceReplies, joinReaders)
	}
	p, _ := r.World.Proxy.Engine.RetrySnapshot()
	sent := r.World.Proxy.Engine.CoalesceSnapshot().Sent
	if p.Attempts != int64(joinWarmups)+sent {
		return failf("the budget saw %d first attempts for %d warm-up writes and %d flights sent: a flight counts once, not per follower",
			p.Attempts, joinWarmups, sent)
	}
	return coalSnapOK(r)
}

func retryOff(sp Spec) Spec {
	sp.Retry = retry.Config{}
	return sp
}

func JoinCoalesceScenarios() []CoalesceScenario {
	var out []CoalesceScenario
	for _, j := range []coalesce.JoinMode{coalesce.JoinStrict, coalesce.JoinRelaxed} {
		out = append(out,
			CoalesceScenario{Spec: HotKeyWeightedSpec(j), Check: checkHotKeyWeighted, Ablate: coalesceOff, Ablated: "coalescer off"},
			CoalesceScenario{Spec: HotKeyRetrySpec(j), Check: checkHotKeyRetry, Ablate: retryOff, Ablated: "retry policy off"},
		)
	}
	return out
}
