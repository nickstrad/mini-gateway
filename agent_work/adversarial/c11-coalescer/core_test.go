package adv_test

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"reflect"
	"testing"

	c "github.com/nickstrad/mini-gateway/internal/coalesce"
	"github.com/nickstrad/mini-gateway/internal/work"
)

const seed int64 = 110028

func cfg() c.Config {
	return c.Config{Mode: c.ModeAuto, Join: c.JoinStrict, MaxFlights: 8, MaxWaiters: 1024, MaxReplyBytes: 64, Deadline: 1000, NoShareFor: 40, NoShareMax: 2}
}
func table(t *testing.T, conf c.Config) *c.Table {
	t.Helper()
	t.Logf("seed=%d REPLAY: go test -count=1 -run '^%s$' ./agent_work/adversarial/c11-coalescer", seed, t.Name())
	x, e := c.New(conf)
	if e != nil {
		t.Fatal(e)
	}
	return x
}
func unit(s string) *work.Unit {
	return &work.Unit{HasKey: true, Shareable: true, Key: work.ShareKey{Text: []byte(s)}, Tenant: "a"}
}
func drain(x *c.Table) []c.Notice {
	var ns []c.Notice
	for {
		n, ok := x.TakeNotice()
		if !ok {
			return ns
		}
		ns = append(ns, n)
	}
}
func check(t *testing.T, x *c.Table) {
	t.Helper()
	if e := x.Check(); e != nil {
		t.Fatal(e)
	}
	if e := x.Snapshot().Check(); e != nil {
		t.Fatal(e)
	}
}
func quiet(t *testing.T, x *c.Table) {
	t.Helper()
	drain(x)
	check(t, x)
	if e := x.Quiesce(); e != nil {
		t.Fatal(e)
	}
	if x.Next() != 0 {
		t.Fatalf("timer remains: %d", x.Next())
	}
}
func want(t *testing.T, got, w any) {
	t.Helper()
	if !reflect.DeepEqual(got, w) {
		t.Fatalf("observed %#v; expected %#v", got, w)
	}
}

// The backend model deliberately keeps cancelled reads alive until their acknowledgement.
func TestAdv_1_CancelAcknowledgementGap(t *testing.T) {
	t.Skip("SETTLED by the orchestrator: claim 1 narrowed: at most one read per key among the flights the coalescer owns; a cancelled read still finishing, a key on the no-share list and a full flight table pass outside it and are counted. See agent_work/chunks/c11-coalescer/ADVERSARIAL.md.")
	x := table(t, cfg())
	u := unit("k")
	f := x.Join(0, 1, u)
	x.Started(0, f.Flight)
	want(t, x.Leave(1, 1, c.LeaveCancel).Kind, c.LeftLast)
	g := x.Join(1, 2, u)
	if g.Verdict == c.VerdictLead {
		t.Fatalf("old database read has no cancellation acknowledgement; new same-key read may start: %v", g)
	}
}
func TestAdv_1_NoShareBypassOverlap(t *testing.T) {
	t.Skip("SETTLED by the orchestrator: claim 1 narrowed: at most one read per key among the flights the coalescer owns; a cancelled read still finishing, a key on the no-share list and a full flight table pass outside it and are counted. See agent_work/chunks/c11-coalescer/ADVERSARIAL.md.")
	x := table(t, cfg())
	u := unit("k")
	f := x.Join(0, 1, u)
	x.Started(0, f.Flight)
	x.Stored(1, f.Flight, 65)
	g := x.Join(2, 2, u)
	if g.Verdict == c.VerdictPass {
		t.Fatalf("oversized read still running; same-key request passed unshared, permitting a concurrent database read")
	}
}
func TestAdv_1_FullTableDuplicateBypass(t *testing.T) {
	t.Skip("SETTLED by the orchestrator: claim 1 narrowed: at most one read per key among the flights the coalescer owns; a cancelled read still finishing, a key on the no-share list and a full flight table pass outside it and are counted. See agent_work/chunks/c11-coalescer/ADVERSARIAL.md.")
	conf := cfg()
	conf.MaxFlights = 1
	x := table(t, conf)
	x.Join(0, 1, unit("occupier"))
	a := x.Join(1, 2, unit("other"))
	b := x.Join(1, 3, unit("other"))
	if a.Verdict == c.VerdictPass && b.Verdict == c.VerdictPass {
		t.Fatal("full table passes two identical reads to database without serializing them")
	}
}

func TestAdv_2_AutoIgnoresSafeReadOnly(t *testing.T) {
	x := table(t, cfg())
	u := unit("SELECT 8")
	u.Shareable = false
	u.SafeRead = true
	u.Class = work.ClassContinueLease
	for i := 1; i < 20; i++ {
		want(t, x.Join(0, c.RequestID(i), u).Verdict, c.VerdictPass)
	}
	want(t, x.Snapshot().Started, int64(0))
}
func TestAdv_2_OffIgnoresBothFlags(t *testing.T) {
	conf := cfg()
	conf.Mode = c.ModeOff
	x := table(t, conf)
	u := unit("x")
	u.Hinted = true
	for i := 1; i < 20; i++ {
		want(t, x.Join(0, c.RequestID(i), u).Verdict, c.VerdictPass)
	}
	quiet(t, x)
}
func TestAdv_2_NoKeyCannotShare(t *testing.T) {
	x := table(t, cfg())
	u := unit("x")
	u.HasKey = false
	want(t, x.Join(0, 1, u).Verdict, c.VerdictPass)
	want(t, x.Join(0, 2, nil).Verdict, c.VerdictPass)
	quiet(t, x)
}
func TestAdv_3_OpaqueHostileText(t *testing.T) {
	x := table(t, cfg())
	for i, s := range []string{"\xff\x00DELETE /*", "-- malformed ('", ""} {
		u := unit(s)
		f := x.Join(0, c.RequestID(2*i+1), u)
		g := x.Join(0, c.RequestID(2*i+2), u)
		want(t, f.Verdict, c.VerdictLead)
		want(t, g.Flight, f.Flight)
	}
	check(t, x)
}
func TestAdv_3_ExactBytesNotNormalized(t *testing.T) {
	x := table(t, cfg())
	for i, s := range []string{"SELECT 8", "select 8", "SELECT  8", "SELECT 8 "} {
		want(t, x.Join(0, c.RequestID(i+1), unit(s)).Verdict, c.VerdictLead)
	}
	check(t, x)
}
func TestAdv_3_TenantDelimiterCollision(t *testing.T) {
	x := table(t, cfg())
	a := unit("b\x00c")
	a.Key.Tenant = "a"
	b := unit("c")
	b.Key.Tenant = "a\x00b"
	if a.Key.Equal(b.Key) {
		t.Fatal("invalid fixture")
	}
	x.Join(0, 1, a)
	want(t, x.Join(0, 2, b).Verdict, c.VerdictLead)
}

func TestAdv_4_StrictHeldCannotStartEarly(t *testing.T) {
	x := table(t, cfg())
	u := unit("x")
	f := x.Join(0, 1, u)
	x.Started(0, f.Flight)
	h := x.Join(1, 2, u)
	x.Started(2, h.Flight)
	want(t, x.Snapshot().Sent, int64(1))
	want(t, h.Verdict, c.VerdictHold)
	x.Finish(3, f.Flight, c.Result{})
	ns := drain(x)
	want(t, ns, []c.Notice{{Kind: c.NoticeStart, Flight: h.Flight, Waiter: 2}})
}
func TestAdv_4_CompletionThenArrivalSameTick(t *testing.T) {
	x := table(t, cfg())
	u := unit("x")
	f := x.Join(0, 1, u)
	x.Started(0, f.Flight)
	x.Finish(5, f.Flight, c.Result{})
	g := x.Join(5, 2, u)
	want(t, g.Verdict, c.VerdictLead)
	if g.Flight == f.Flight {
		t.Fatal("completed flight reused")
	}
}
func TestAdv_4_RelaxedWindowExplicit(t *testing.T) {
	conf := cfg()
	conf.Join = c.JoinRelaxed
	x := table(t, conf)
	u := unit("x")
	f := x.Join(0, 1, u)
	x.Started(0, f.Flight)
	g := x.Join(900, 2, u)
	want(t, g.Flight, f.Flight)
	want(t, g.Verdict, c.VerdictFollow)
}

func cancelOrder(t *testing.T, ownerFirst bool, why c.LeaveWhy) {
	x := table(t, cfg())
	u := unit("x")
	f := x.Join(0, 1, u)
	for i := 2; i <= 101; i++ {
		x.Join(0, c.RequestID(i), u)
	}
	x.Started(1, f.Flight)
	ids := rand.New(rand.NewSource(seed)).Perm(100)
	if ownerFirst {
		ids[0], ids[index(ids, 0)] = 0, ids[0]
	}
	for _, i := range ids {
		l := x.Leave(2, c.RequestID(i+1), why)
		if l.Kind == c.LeftLast {
			t.Fatalf("cancelled while waiter 101 survives: id=%d", i+1)
		}
		check(t, x)
	}
	want(t, x.Snapshot().Cancelled, int64(0))
	x.Finish(3, f.Flight, c.Result{})
	want(t, x.Snapshot().WaiterReply, int64(1))
	quiet(t, x)
}
func index(a []int, v int) int {
	for i, x := range a {
		if x == v {
			return i
		}
	}
	return -1
}
func TestAdv_5_ShuffledOwnerHandoffs(t *testing.T) { cancelOrder(t, true, c.LeaveCancel) }
func TestAdv_5_ShuffledDisconnects(t *testing.T)   { cancelOrder(t, false, c.LeaveGone) }
func TestAdv_5_RepeatedCancelIsIdempotent(t *testing.T) {
	x := table(t, cfg())
	f := x.Join(0, 1, unit("x"))
	x.Join(0, 2, unit("x"))
	want(t, x.Leave(1, 1, c.LeaveCancel).Kind, c.LeftHandoff)
	for range 40 {
		want(t, x.Leave(1, 1, c.LeaveCancel).Kind, c.LeftNone)
	}
	x.Finish(2, f.Flight, c.Result{})
	want(t, x.Snapshot().WaiterReply, int64(1))
	quiet(t, x)
}
func tie(t *testing.T, firstResult bool, why c.LeaveWhy) {
	x := table(t, cfg())
	f := x.Join(0, 1, unit("x"))
	x.Join(0, 2, unit("x"))
	if firstResult {
		x.Finish(7, f.Flight, c.Result{})
	}
	x.Leave(7, 2, why)
	if !firstResult {
		x.Finish(7, f.Flight, c.Result{})
	}
	for _, n := range drain(x) {
		if n.Waiter == 2 {
			t.Fatalf("leaver at same instant received %s (resultFirst=%v)", n.Kind, firstResult)
		}
	}
	quiet(t, x)
}
func TestAdv_6_CancelBeforeResultTie(t *testing.T) { tie(t, false, c.LeaveCancel) }
func TestAdv_6_GoneBeforeResultTie(t *testing.T)   { tie(t, false, c.LeaveGone) }
func TestAdv_6_ResultBeforeCancelTie(t *testing.T) {
	t.Skip("SETTLED by the orchestrator: claim 6 narrowed: at one instant the order of events decides; a result applied before a waiter's leave is delivered to it. See agent_work/chunks/c11-coalescer/ADVERSARIAL.md.")
	tie(t, true, c.LeaveCancel)
}
func allLeave(t *testing.T, held bool, why c.LeaveWhy) {
	x := table(t, cfg())
	u := unit("x")
	f := x.Join(0, 1, u)
	x.Join(0, 2, u)
	x.Started(0, f.Flight)
	if held {
		x.Join(1, 3, u)
		x.Join(1, 4, u)
	}
	for i := 1; i <= 4; i++ {
		x.Leave(2, c.RequestID(i), why)
		check(t, x)
	}
	x.Finish(3, f.Flight, c.Result{})
	quiet(t, x)
}
func TestAdv_7_AllGoneWithHeldSuccessor(t *testing.T)    { allLeave(t, true, c.LeaveGone) }
func TestAdv_7_AllCancelWithHeldSuccessor(t *testing.T)  { allLeave(t, true, c.LeaveCancel) }
func TestAdv_7_LateReplyAfterLastDeparture(t *testing.T) { allLeave(t, false, c.LeaveGone) }
func TestAdv_8_JoinCannotExtendDeadline(t *testing.T) {
	x := table(t, cfg())
	f := x.Join(100, 1, unit("x"))
	x.Join(1099, 2, unit("x"))
	want(t, x.Next(), c.Instant(1100))
	x.Finish(1100, f.Flight, c.Result{})
	want(t, x.Snapshot().Expired, int64(1))
	want(t, x.Snapshot().WaiterSharedError, int64(2))
	quiet(t, x)
}
func TestAdv_8_HandoffCannotExtendDeadline(t *testing.T) {
	x := table(t, cfg())
	x.Join(0, 1, unit("x"))
	x.Join(900, 2, unit("x"))
	x.Leave(999, 1, c.LeaveCancel)
	x.Sweep(1000)
	want(t, x.Snapshot().WaiterSharedError, int64(1))
	quiet(t, x)
}
func TestAdv_8_DeadlineOverflow(t *testing.T) {
	x := table(t, cfg())
	now := c.Instant(math.MaxInt64 - 100)
	x.Join(now, 1, unit("x"))
	if x.Next() < now {
		t.Fatalf("future deadline wrapped into past: now=%d next=%d", now, x.Next())
	}
	x.Sweep(now)
	want(t, x.Snapshot().Expired, int64(0))
}
func TestAdv_9_RefusedWaiterCanRetryAfterDeparture(t *testing.T) {
	conf := cfg()
	conf.MaxWaiters = 2
	x := table(t, conf)
	x.Join(0, 1, unit("x"))
	x.Join(0, 2, unit("x"))
	want(t, x.Join(0, 3, unit("x")).Verdict, c.VerdictRefuse)
	x.Leave(1, 2, c.LeaveGone)
	want(t, x.Join(1, 3, unit("x")).Verdict, c.VerdictFollow)
	check(t, x)
}
func TestAdv_9_ReplyCapAtExactBoundary(t *testing.T) {
	x := table(t, cfg())
	f := x.Join(0, 1, unit("x"))
	x.Join(0, 2, unit("x"))
	for range 64 {
		want(t, x.Stored(1, f.Flight, 1), true)
	}
	want(t, x.Stored(2, f.Flight, 1), false)
	want(t, drain(x), []c.Notice{{Kind: c.NoticeRefused, Flight: f.Flight, Waiter: 2}})
	want(t, x.Join(41, 3, unit("x")).Verdict, c.VerdictPass)
	x.Finish(41, f.Flight, c.Result{})
	want(t, x.Join(42, 4, unit("x")).Verdict, c.VerdictLead)
	check(t, x)
}
func TestAdv_9_NoShareEvictionChurn(t *testing.T) {
	x := table(t, cfg())
	for i := 1; i <= 70; i++ {
		u := unit(fmt.Sprint(i))
		f := x.Join(0, c.RequestID(i), u)
		x.Stored(0, f.Flight, 65)
		x.Finish(0, f.Flight, c.Result{})
		check(t, x)
	}
	want(t, x.Snapshot().NoShare.Value, int64(2))
	want(t, x.Snapshot().NoShareEvicted, int64(68))
	x.Sweep(40)
	quiet(t, x)
}
func errorsOnce(t *testing.T, r c.Result) {
	x := table(t, cfg())
	f := x.Join(0, 1, unit("x"))
	for i := 2; i <= 17; i++ {
		x.Join(0, c.RequestID(i), unit("x"))
	}
	x.Started(1, f.Flight)
	x.Leave(2, 5, c.LeaveCancel)
	for range 3 {
		x.Finish(3, f.Flight, r)
	}
	ns := drain(x)
	want(t, len(ns), 15)
	seen := map[c.RequestID]bool{}
	for _, n := range ns {
		if seen[n.Waiter] || n.Waiter == 5 || n.Kind == c.NoticeStart {
			t.Fatalf("duplicate, leaver or redrive: %+v", n)
		}
		seen[n.Waiter] = true
	}
	want(t, x.Snapshot().SharedErrors, int64(1))
	quiet(t, x)
}
func TestAdv_10_TransportErrorOnlyOnce(t *testing.T) { errorsOnce(t, c.Result{Failed: true}) }
func TestAdv_10_SQLFailureOnlyOnce(t *testing.T)     { errorsOnce(t, c.Result{ErrorReply: true}) }
func TestAdv_10_FailedHeldSuccessorOnlyStartsOnce(t *testing.T) {
	x := table(t, cfg())
	u := unit("x")
	f := x.Join(0, 1, u)
	x.Started(0, f.Flight)
	h := x.Join(1, 2, u)
	for i := 3; i < 25; i++ {
		x.Join(1, c.RequestID(i), u)
	}
	for range 4 {
		x.Finish(2, f.Flight, c.Result{Failed: true})
	}
	want(t, drain(x), []c.Notice{{Kind: c.NoticeStart, Flight: h.Flight, Waiter: 2}})
	x.Finish(3, h.Flight, c.Result{Failed: true})
	want(t, len(drain(x)), 22)
	quiet(t, x)
}

func hot(t *testing.T, mode c.Mode, join c.JoinMode) {
	hotEngine(t, mode, join)
	conf := cfg()
	conf.Mode = mode
	conf.Join = join
	x := table(t, conf)
	u := unit("hot")
	f := x.Join(0, 1, u)
	x.Started(0, f.Flight)
	var held c.FlightID
	passes := 0
	if f.Verdict == c.VerdictPass {
		passes++
	}
	for i := 2; i <= 1000; i++ {
		j := x.Join(1, c.RequestID(i), u)
		if j.Verdict == c.VerdictHold {
			held = j.Flight
		}
		if j.Verdict == c.VerdictPass {
			passes++
		}
	}
	x.Finish(2, f.Flight, c.Result{})
	if held != 0 {
		x.Started(2, held)
		x.Finish(3, held, c.Result{})
	}
	s := x.Snapshot()
	if mode == c.ModeOff {
		want(t, passes, 1000)
	} else {
		expected := int64(1)
		if join == c.JoinStrict {
			expected = 2
		}
		want(t, s.Sent, expected)
		want(t, s.WaiterReply, int64(1000))
	}
	quiet(t, x)
}
func TestAdv_13_StrictThousandAfterSend(t *testing.T)  { hot(t, c.ModeAuto, c.JoinStrict) }
func TestAdv_13_RelaxedThousandAfterSend(t *testing.T) { hot(t, c.ModeAuto, c.JoinRelaxed) }
func TestAdv_13_OffThousandPassThrough(t *testing.T)   { hot(t, c.ModeOff, c.JoinStrict) }
func TestAdv_14_HintedFlagOverridesShape(t *testing.T) {
	conf := cfg()
	conf.Mode = c.ModeHinted
	x := table(t, conf)
	u := unit("/* mg:share */ SELECT 1")
	want(t, x.Join(0, 1, u).Verdict, c.VerdictPass)
	u.Hinted = true
	u.Shareable = false
	want(t, x.Join(0, 2, u).Verdict, c.VerdictLead)
}
func TestAdv_14_InvalidEnumsAndCaps(t *testing.T) {
	for _, modify := range []func(*c.Config){func(x *c.Config) { x.Mode = 255 }, func(x *c.Config) { x.Join = 255 }, func(x *c.Config) { x.MaxWaiters = 1 }, func(x *c.Config) { x.Deadline = -1 }, func(x *c.Config) { x.MaxFlights = c.MaxFlightsLimit + 1 }} {
		conf := cfg()
		modify(&conf)
		if _, e := c.New(conf); e == nil {
			t.Fatalf("invalid config accepted: %+v", conf)
		}
	}
}
func TestAdv_14_DefaultStrict(t *testing.T) {
	x := table(t, c.Config{Mode: c.ModeAuto})
	want(t, x.Config().Join, c.JoinStrict)
	u := unit("x")
	f := x.Join(0, 1, u)
	x.Started(0, f.Flight)
	want(t, x.Join(0, 2, u).Verdict, c.VerdictHold)
}
func TestAdv_15_StaleFlightIDCannotFinishReusedSlot(t *testing.T) {
	x := table(t, cfg())
	f := x.Join(0, 1, unit("x"))
	x.Finish(0, f.Flight, c.Result{})
	g := x.Join(1, 2, unit("x"))
	x.Finish(1, f.Flight, c.Result{})
	_, ok := x.Owner(g.Flight)
	want(t, ok, true)
	x.Finish(2, g.Flight, c.Result{})
	quiet(t, x)
}
func TestAdv_15_ShutdownClearsHeldAndNoShare(t *testing.T) {
	x := table(t, cfg())
	f := x.Join(0, 1, unit("x"))
	x.Started(0, f.Flight)
	x.Join(1, 2, unit("x"))
	x.Stored(2, f.Flight, 65)
	x.Shutdown(3)
	quiet(t, x)
	x.Shutdown(3)
	quiet(t, x)
}
func TestAdv_15_SeededReuseCampaign(t *testing.T) {
	n := 40
	if os.Getenv("ADV_LONG") == "1" {
		n = 4000
	}
	for k := 0; k < n; k++ {
		x := table(t, cfg())
		r := rand.New(rand.NewSource(seed + int64(k)))
		for i := 0; i < 20; i++ {
			u := unit(fmt.Sprint(r.Intn(4)))
			f := x.Join(c.Instant(i), c.RequestID(i*2+1), u)
			x.Join(c.Instant(i), c.RequestID(i*2+2), u)
			if r.Intn(2) == 0 {
				x.Stored(c.Instant(i), f.Flight, 65)
			}
			x.Finish(c.Instant(i), f.Flight, c.Result{Failed: r.Intn(2) == 0})
			drain(x)
			check(t, x)
		}
		x.Shutdown(21)
		quiet(t, x)
	}
}
