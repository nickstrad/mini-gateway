package e2e

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pool"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
)

func poolSnap(r *SimRun) pool.Snapshot {
	s, _ := r.World.Proxy.Engine.PoolSnapshot()
	return s
}

func clientErrors(r *SimRun, code string) int {
	n := 0
	for _, c := range r.Clients {
		for _, st := range c.Steps {
			for _, tk := range st {
				if strings.HasPrefix(tk, "E:") && strings.HasSuffix(tk, ":"+code) {
					n++
				}
			}
		}
	}
	return n
}

func anyClientErrors(r *SimRun) int {
	n := 0
	for _, c := range r.Clients {
		for _, st := range c.Steps {
			if HasToken(st, "E:") || HasToken(st, "closed:") {
				n++
			}
		}
	}
	return n
}

func okSteps(r *SimRun, from time.Duration) int {
	n := 0
	for c, res := range r.Clients {
		at := r.Spec.Clients[c].Start
		for i, st := range res.Steps {
			if i < len(r.Spec.Clients[c].Steps) {
				at += r.Spec.Clients[c].Steps[i].Delay
			}
			if at >= from && len(st) > 0 && st[len(st)-1] == "ZI" && !HasToken(st, "E:") {
				n++
			}
			if i < len(res.Elapsed) {
				at += res.Elapsed[i]
			}
		}
	}
	return n
}

func dialSpacing(r *SimRun, interval time.Duration) error {
	acc := r.Path.accepts
	for i := 1; i < len(acc); i++ {
		if d := acc[i].Sub(acc[i-1]); d < interval-time.Millisecond {
			return failf("dials %d and %d only %s apart, the creation rate allows one per %s", i, i+1, d, interval)
		}
	}
	return nil
}

func maxIn(times []sim.Time, window time.Duration) int {
	most := 0
	for i := range times {
		j := i
		for j < len(times) && times[j].Sub(times[i]) < window {
			j++
		}
		most = max(most, j-i)
	}
	return most
}

func quietPool(r *SimRun) error {
	s := poolSnap(r)
	if s.Lent.Value != 0 || s.Checking.Value != 0 || s.Cleaning.Value != 0 || s.CancelsInFlight.Value != 0 {
		return failf("pool not quiet: lent %d checking %d cleaning %d cancels %d", s.Lent.Value, s.Checking.Value, s.Cleaning.Value, s.CancelsInFlight.Value)
	}
	if s.OpenPlusConnecting.Peak > s.Cap {
		return failf("open plus connecting peaked at %d above the cap %d", s.OpenPlusConnecting.Peak, s.Cap)
	}
	return s.Check()
}

func checkPoolSlowReader(r *SimRun) error {
	e := r.Engine
	if e.RelayHighWater > int64(r.Spec.ReplyCap) || e.ClientsDropped != 1 {
		return failf("relay high-water %d dropped %d", e.RelayHighWater, e.ClientsDropped)
	}
	if s := poolSnap(r); s.Ends[pool.EndWriteStalled] != 1 {
		return failf("the stalled client's lease did not enter the dirty table: %v", s.Ends)
	}
	for c := 1; c < 3; c++ {
		if err := allSteps(r, c); err != nil {
			return err
		}
	}
	return quietPool(r)
}

func checkPoolVanish(r *SimRun) error {
	for c := 2; c < 4; c++ {
		if err := allSteps(r, c); err != nil {
			return err
		}
		if err := want(r, c, 0, readOK("clean")); err != nil {
			return err
		}
		if err := want(r, c, 1, readOK("init")); err != nil {
			return err
		}
	}
	s := poolSnap(r)
	dirty := s.Ends[pool.EndVanishedInTx] + s.Ends[pool.EndVanishedMidStatement]
	if dirty < 2 || s.DirtyReused[pool.EndVanishedInTx]+s.DirtyReused[pool.EndVanishedMidStatement]+
		s.DirtyClosed[pool.EndVanishedInTx]+s.DirtyClosed[pool.EndVanishedMidStatement] != dirty {
		return failf("dirty-table entries %d, reused %v closed %v", dirty, s.DirtyReused, s.DirtyClosed)
	}
	if s.Rollbacks == 0 || r.World.Fake.Snapshot().TxRolledBack == 0 {
		return failf("the vanished client's transaction was not rolled back: rollbacks %d", s.Rollbacks)
	}
	return quietPool(r)
}

func checkHardCap(r *SimRun) error {
	f := r.World.Fake.Snapshot()
	s := poolSnap(r)
	if f.Sessions.Peak > s.Cap || r.Path.peak > s.Cap || s.OpenPlusConnecting.Peak > s.Cap {
		return failf("database sessions peaked at %d, relay pairs at %d, open+connecting at %d; cap %d", f.Sessions.Peak, r.Path.peak,
			s.OpenPlusConnecting.Peak, s.Cap)
	}
	if s.OpenPlusConnecting.Peak != s.Cap || s.Exhausted == 0 {
		return failf("the cap was never reached (peak %d, exhausted %d): the scenario did not exercise it", s.OpenPlusConnecting.Peak, s.Exhausted)
	}
	served := 0
	for c := range r.Clients {
		if slices.Equal(step(r, c, 0), readOK("init")) {
			served++
		}
	}
	if served < len(r.Clients)*9/10 {
		return failf("only %d of %d clients were served", served, len(r.Clients))
	}
	return quietPool(r)
}

func checkStorm(r *SimRun) error {
	if err := checkHardCap(r); err != nil {
		return err
	}
	if r.Engine.ClientsAccepted < 1000 {
		return failf("the storm did not reconnect: %d clients accepted", r.Engine.ClientsAccepted)
	}
	return nil
}

func checkColdStart(r *SimRun) error {
	if err := dialSpacing(r, time.Second/time.Duration(r.Spec.Pool.RatePerSec)); err != nil {
		return err
	}
	if len(r.Path.accepts) < r.Spec.Conns || poolSnap(r).RateLimited == 0 {
		return failf("the cold start dialled %d times and was rate limited %d times", len(r.Path.accepts), poolSnap(r).RateLimited)
	}
	if err := everyClientFinished(r); err != nil {
		return err
	}
	return quietPool(r)
}

func checkLifetimes(r *SimRun) error {
	s := poolSnap(r)
	if s.Closes[pool.CloseLifetime] < 16 {
		return failf("only %d lifetime closes", s.Closes[pool.CloseLifetime])
	}
	if most := maxIn(r.Path.gone, 100*time.Millisecond); most > 4 {
		return failf("%d connections ended within 100 ms of each other: synchronized expiry", most)
	}
	if most := maxIn(r.Path.accepts[16:], time.Second); most > 16 {
		return failf("a dial spike of %d in one second", most)
	}
	return quietPool(r)
}

func checkRestart(r *SimRun) error {
	s := poolSnap(r)
	if s.BreakerTransitions[pool.TransClosedOpen] == 0 || s.BreakerTransitions[pool.TransHalfOpenClosed] == 0 {
		return failf("breaker transitions %v", s.BreakerTransitions)
	}
	if s.BreakerTrials != s.BreakerTransitions[pool.TransOpenHalfOpen] && s.BreakerTrials > s.BreakerTransitions[pool.TransOpenHalfOpen] {
		return failf("%d trials for %d half-open periods", s.BreakerTrials, s.BreakerTransitions[pool.TransOpenHalfOpen])
	}
	if clientErrors(r, "53M03") == 0 {
		return failf("no request was answered 53M03 while the breaker was open")
	}
	fast := 0
	for c, res := range r.Clients {
		for i, st := range res.Steps {
			if !HasToken(st, "E:ERROR:53M03") || i >= len(res.Elapsed) {
				continue
			}
			if res.Elapsed[i] < 10*time.Millisecond {
				fast++
			}
			if res.Elapsed[i] > r.Spec.Timeouts.DBConnect {
				return failf("client %d step %d: 53M03 after %s", c, i, res.Elapsed[i])
			}
		}
	}
	if fast == 0 {
		return failf("no 53M03 was answered at once while the breaker was open")
	}
	if okSteps(r, 8*time.Second) == 0 {
		return failf("goodput did not return after the restart")
	}
	return quietPool(r)
}

func checkIdleInTx(r *SimRun) error {
	if !HasToken(step(r, 0, 2), "E:FATAL:25P03") && !HasToken(step(r, 0, 2), "closed:") && !r.Clients[0].Closed {
		return failf("the idle transaction was not ended: %s", joinSteps(r.Clients[0]))
	}
	if err := want(r, 1, 0, readOK("init")); err != nil {
		return err
	}
	s := poolSnap(r)
	if s.DirtyReused[pool.EndIdleInTx] != 1 || s.Dials != 1 || r.World.Fake.Snapshot().TxRolledBack != 1 {
		return failf("idle-in-transaction reused %d, dials %d, rolled back %d", s.DirtyReused[pool.EndIdleInTx], s.Dials,
			r.World.Fake.Snapshot().TxRolledBack)
	}
	return quietPool(r)
}

func checkCancel(r *SimRun) error {
	if err := want(r, 0, 0, append([]string{"T"}, errTokens("57014")...)); err != nil {
		return err
	}
	if err := want(r, 1, 0, []string{"T", "D:", "C:SELECT 1", "ZI"}); err != nil {
		return err
	}
	if err := want(r, 2, 0, readOK("init")); err != nil {
		return err
	}
	if f := r.World.Fake.Snapshot(); f.CancelsApplied != 1 {
		return failf("the database applied %d cancels, want exactly the one for client 0", f.CancelsApplied)
	}
	if e := r.Engine; e.CancelsForwarded < 1 || e.CancelsSent != e.CancelsDone {
		return failf("cancels forwarded %d sent %d done %d", e.CancelsForwarded, e.CancelsSent, e.CancelsDone)
	}
	return quietPool(r)
}

func lentAt(*SimRun) int64 { return pathLoopers }

func checkMassDeath(r *SimRun) error {
	errs := int64(anyClientErrors(r))
	if errs > lentAt(r) {
		return failf("%d client errors after the mass death, at most the %d connections that could be lent", errs, lentAt(r))
	}
	return checkDeathRecovers(r)
}

func checkDeathRecovers(r *SimRun) error {
	s := poolSnap(r)
	if r.Path.stats.Blackholed+r.Path.stats.Forgot == 0 {
		return failf("the fault never fired: %s", r.Path)
	}
	if s.ChecksByResult[pool.CheckOK] == 0 && s.Closes[pool.CloseCheckFailed] == 0 && s.Closes[pool.CloseNoReply] == 0 && s.Closes[pool.CloseError] == 0 {
		return failf("no dead connection was found: closes %v", s.Closes)
	}
	if okSteps(r, pathDeath+r.Spec.Timeouts.Statement+r.Spec.Timeouts.Cleanup+time.Second) == 0 {
		return failf("goodput did not return within the statement deadline and the clean-up")
	}
	if most := maxIn(r.Path.accepts, time.Second); most > poolRate+1 {
		return failf("a dial spike of %d in one second above the creation rate %d", most, poolRate)
	}
	return quietPool(r)
}

func checkPoliteClose(r *SimRun) error {
	if n := anyClientErrors(r); n != 0 {
		return failf("%d client errors after a polite close", n)
	}
	s := poolSnap(r)
	if s.Closes[pool.ClosePeerClosedIdle] == 0 || r.Path.stats.Polite == 0 {
		return failf("no peer_closed_idle close: %v (%s)", s.Closes, r.Path)
	}
	return quietPool(r)
}

func checkCheckHangs(r *SimRun) error {
	if n := anyClientErrors(r); n != 0 {
		return failf("%d client errors: the check must never cost a client", n)
	}
	s := poolSnap(r)
	if s.ChecksByResult[pool.CheckTimeout] == 0 || s.Closes[pool.CloseCheckFailed] == 0 {
		return failf("checks %v closes %v", s.ChecksByResult, s.Closes)
	}
	if el := r.Clients[1].Elapsed; len(el) == 0 || el[0] > r.Spec.Timeouts.CheckTimeout+proxy.DefaultCheckTimeout+100*time.Millisecond {
		return failf("the queued request waited %v behind the hanging check", el)
	}
	return quietPool(r)
}

func checkSlotsHeld(r *SimRun) error {
	late := len(r.Clients) - 1
	if err := allSteps(r, late); err != nil {
		return err
	}
	for i, st := range r.Clients[late].Steps {
		if HasToken(st, "E:") || HasToken(st, "closed:") || !HasToken(st, "C:SELECT 1") {
			return failf("post-reap client step %d did not succeed: %v", i, st)
		}
	}
	refused := int64(clientErrors(r, "53M03"))
	if other := int64(anyClientErrors(r)) - refused; other > lentAt(r) {
		return failf("%d client errors other than 53M03 after the mass death, at most the %d connections that could be lent", other, lentAt(r))
	}
	s := poolSnap(r)
	if refused > 0 && (s.DialFailures == 0 || s.BreakerTransitions[pool.TransClosedOpen] == 0) {
		return failf("%d 53M03 refusals without the breaker opening on refused dials (dial failures %d)", refused, s.DialFailures)
	}
	if err := checkDeathRecovers(r); err != nil {
		return err
	}
	f := r.World.Fake.Snapshot()
	if f.Refused.TooMany == 0 || r.Path.stats.Reaped == 0 {
		return failf("no re-dial was refused while slots were held (refused %+v, %s, sessions peak %d, pool %+v, accepts %v, gone %v)", f.Refused, r.Path,
			f.Sessions.Peak, poolSnap(r).Closes, r.Path.accepts, r.Path.gone)
	}
	return dialSpacing(r, time.Second/10)
}

func checkNight(remedied bool) func(r *SimRun) error {
	return func(r *SimRun) error {
		s := poolSnap(r)
		errs := anyClientErrors(r)
		if remedied && (errs != 0 || s.NoReply != 0) {
			return failf("with the remedies on: %d client errors, %d no-reply deadlines (%s)", errs, s.NoReply, r.Path)
		}
		if !remedied && s.NoReply == 0 {
			return failf("with every remedy off the morning ramp hung no statement: the scenario cannot fail (%s)", r.Path)
		}
		if !remedied && r.Path.stats.IdleDrops == 0 {
			return failf("the middlebox dropped nothing: %s", r.Path)
		}
		return quietPool(r)
	}
}

func clientOf(tag string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(tag, "c"))
	if err != nil {
		return -1
	}
	return n
}

func TenantCrossings(r *SimRun) (cross, same int) {
	type state struct {
		tag   string
		dirty settingSet
	}
	backends := map[int64]*state{}
	for _, s := range r.World.Fake.StatementLog() {
		b := backends[s.Backend]
		if b == nil {
			b = &state{dirty: settingSet{}}
			backends[s.Backend] = b
		}
		if s.Tag != "" && s.Tag != b.tag {
			if len(b.dirty) > 0 && b.tag != "" {
				prev, next := clientOf(b.tag), clientOf(s.Tag)
				if prev >= 0 && next >= 0 && r.Spec.Clients[prev].Tenant == r.Spec.Clients[next].Tenant {
					same++
				} else {
					cross++
				}
			}
			b.tag = s.Tag
		}
		if s.Code != "" || s.Aborted {
			continue
		}
		switch s.Kind {
		case fakepg.KindSet:
			if name, v, ok := parseSet(stripTag(s.Text)); ok {
				b.dirty[name] = v
			}
		case fakepg.KindReset:
			f := strings.Fields(strings.ToLower(stripTag(s.Text)))
			if len(f) == 2 {
				delete(b.dirty, f[1])
			}
		case fakepg.KindDiscard:
			b.dirty = settingSet{}
		}
	}
	return cross, same
}

func checkPolicy(reset proxy.ResetPolicy, reseed bool) func(r *SimRun) error {
	return func(r *SimRun) error {
		cross, same := TenantCrossings(r)
		if cross != 0 {
			return failf("session state crossed tenants %d times under %s", cross, reset)
		}
		if reset == proxy.ResetOnSwitch && same == 0 {
			return failf("on-switch showed no same-tenant carry-over: the policy was not exercised")
		}
		if reset == proxy.ResetAlways && same != 0 {
			return failf("always carried state over %d times within a tenant", same)
		}
		if err := everyClientFinished(r); err != nil {
			return err
		}
		s := poolSnap(r)
		if reset == proxy.ResetOnSwitch && (s.TenantSwitches == 0 || s.LentAsIs == 0) {
			return failf("on-switch: switches %d lent as is %d", s.TenantSwitches, s.LentAsIs)
		}
		if reseed != (s.Reseeds > 0) || reseed != (r.Path.stats.Reseeds > 0) {
			return failf("reseed %v but %d re-seeds sent, %d seen on the path", reseed, s.Reseeds, r.Path.stats.Reseeds)
		}
		return quietPool(r)
	}
}
