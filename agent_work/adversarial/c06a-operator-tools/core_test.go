package adv_test

import (
	"bytes"
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	lg "github.com/nickstrad/mini-gateway/internal/loadgen"
	"github.com/nickstrad/mini-gateway/internal/obs"
)

const seed int64 = 606013

func plan() lg.Plan {
	return lg.Plan{Seed: seed, Rate: 37, DurationNs: 2e9, Tenants: 1, Keys: 997, Profile: lg.ProfileErrors}
}
func tracker(t *testing.T, p lg.Plan, cap int) *lg.Tracker {
	t.Helper()
	t.Logf("seed=%d", p.Seed)
	x, e := lg.NewTracker(p, lg.TrackerConfig{Conns: p.Tenants, MaxOutstanding: cap, LateAfterNs: 13})
	if e != nil {
		t.Fatal(e)
	}
	for c := 0; c < p.Tenants; c++ {
		x.ConnUp(c)
	}
	return x
}
func check(t *testing.T, x *lg.Tracker) {
	t.Helper()
	if e := x.Snapshot().Check(); e != nil {
		t.Fatal(e)
	}
}
func TestAdv_1_ReplyPermutation(t *testing.T) {
	p := plan()
	a, b := tracker(t, p, 3), tracker(t, p, 3)
	for i := int64(0); i < p.Count(); i++ {
		now := p.At(i)
		a.Tick(now, nil)
		b.Tick(now, nil)
		a.Sent(0, now)
		a.Completed(0, now, lg.Result{})
		if a.Snapshot().Intended != b.Snapshot().Intended {
			t.Fatalf("reply timing changed intended count at %d", i)
		}
	}
	if b.Snapshot().Intended != p.Count() {
		t.Fatal("stalled schedule incomplete")
	}
}
func TestAdv_1_BigIntegerClockOracle(t *testing.T) {
	t.Logf("seed=%d", seed)
	p := plan()
	p.Rate = lg.MaxRate - 1
	p.DurationNs = lg.MaxDurationNs
	for _, i := range []int64{0, 1, p.Count() / 2, p.Count() - 1} {
		want := new(big.Int).Mul(big.NewInt(i), big.NewInt(1e9))
		want.Div(want, big.NewInt(p.Rate))
		if p.At(i) != want.Int64() {
			t.Fatalf("index %d time %d expected %s", i, p.At(i), want)
		}
		if p.Due(p.At(i)) != i+1 {
			t.Fatalf("index %d due %d", i, p.Due(p.At(i)))
		}
	}
}
func TestAdv_1_ClockBeyondDuration(t *testing.T) {
	t.Logf("seed=%d", seed)
	p := plan()
	p.Rate = lg.MaxRate
	if n := p.Due(math.MaxInt64); n != p.Count() {
		t.Fatalf("expired schedule due=%d expected %d", n, p.Count())
	}
}
func TestAdv_2_QueueDelayIncluded(t *testing.T) {
	p := plan()
	x := tracker(t, p, 100)
	x.Tick(p.DurationNs, nil)
	want := obs.NewDurationHistogram()
	for i := int64(0); i < p.Count(); i++ {
		x.Sent(0, 3e9)
		x.Completed(0, 4e9, lg.Result{})
		want.Record(4e9 - p.At(i))
	}
	s := x.Snapshot()
	if !reflect.DeepEqual(s.Latency, want.Snapshot()) || s.LatencyFromSend.Min != 1e9 || s.LatencyFromSend.Max != 1e9 {
		t.Fatalf("latency histograms observed %+v/%+v expected %+v/1s", s.Latency, s.LatencyFromSend, want.Snapshot())
	}
}
func TestAdv_2_ErrorRepliesHaveLatency(t *testing.T) {
	x := tracker(t, plan(), 1)
	x.Tick(0, nil)
	x.Sent(0, 123000)
	x.Completed(0, 456000, lg.Result{Outcome: lg.OutcomeRefused, Code: "53M01"})
	s := x.Snapshot()
	if s.Latency.Max != 456000 || s.LatencyFromSend.Max != 333000 {
		t.Fatalf("refusal latency %d/%d", s.Latency.Max, s.LatencyFromSend.Max)
	}
}
func TestAdv_2_WraparoundQueueLatency(t *testing.T) {
	p := plan()
	x := tracker(t, p, 2)
	want := obs.NewDurationHistogram()
	for i := int64(0); i < p.Count(); i++ {
		x.Tick(p.At(i), nil)
		x.Sent(0, p.At(i)+17)
		x.Completed(0, p.At(i)+101+i, lg.Result{})
		want.Record(101 + i)
	}
	if s := x.Snapshot(); !reflect.DeepEqual(s.Latency, want.Snapshot()) || s.Latency.Count != p.Count() {
		t.Fatalf("ring reused latency %+v expected %+v", s.Latency, want.Snapshot())
	}
}
func summary(x *lg.Tracker) string {
	return strings.Join(lg.Summary(x.Snapshot(), lg.Latencies{}, lg.Latencies{}), "\n")
}
func TestAdv_3_ZeroIntendedShortfall(t *testing.T) {
	t.Skip("SETTLED by the orchestrator: the input no longer exists: a plan with zero operations is refused at validation, naming the flag (review 1 finding 6), so the summary rule is never reached with zero intended operations. See agent_work/chunks/c06a-operator-tools/ADVERSARIAL.md.")
	p := plan()
	p.DurationNs = 1
	x := tracker(t, p, 1)
	x.Tick(1, nil)
	x.Finish()
	if lg.AchievedTenths(x.Snapshot()) < p.Rate*10 && !strings.Contains(summary(x), "SHORTFALL") {
		t.Fatalf("achieved below positive target without SHORTFALL: %s", summary(x))
	}
}
func TestAdv_3_OverflowedLatencyFormatting(t *testing.T) {
	t.Logf("seed=%d", seed)
	s := lg.FormatNs(math.MaxInt64)
	if strings.HasPrefix(s, "-") || s == "0.0s" {
		t.Fatalf("maximum positive latency rendered %q", s)
	}
}
func TestAdv_3_MixedDropsAndLateOracle(t *testing.T) {
	p := plan()
	x := tracker(t, p, 2)
	x.Tick(p.DurationNs, nil)
	x.Sent(0, 13)
	x.Sent(0, p.At(1)+14)
	x.Finish()
	s := x.Snapshot()
	if s.Late != 1 || s.CouldNotSend != p.Count()-2 || !strings.Contains(summary(x), "SHORTFALL") {
		t.Fatalf("expected one late and %d dropped: %+v", p.Count()-2, s)
	}
}
func TestAdv_4_ReverseRandomAccess(t *testing.T) {
	t.Logf("seed=%d", seed)
	p := plan()
	x := make([]lg.Op, p.Count())
	for i := range x {
		x[i] = p.Op(int64(i))
	}
	for i := len(x) - 1; i >= 0; i-- {
		if !reflect.DeepEqual(x[i], p.Op(int64(i))) {
			t.Fatalf("seed=%d op %d order dependent", seed, i)
		}
	}
}
func TestAdv_4_ExtremeSeeds(t *testing.T) {
	for _, s := range []int64{0, -1, math.MinInt64, math.MaxInt64} {
		p := plan()
		p.Seed = s
		t.Logf("seed=%d", s)
		for i := int64(0); i < 100; i++ {
			a, b := p.Op(i), p.Op(i)
			if a != b || a.Key < 1 || a.Key > p.Keys || len(a.Payload) < lg.MinPayload || len(a.Payload) > lg.MaxPayload {
				t.Fatalf("op %d invalid or unstable: %+v %+v", i, a, b)
			}
		}
	}
}
func TestAdv_4_ProcessReplay(t *testing.T) {
	if os.Getenv("ADV_REPLAY_CHILD") == "1" {
		p := plan()
		for i := int64(73); i >= 0; i-- {
			fmt.Printf("%+v\n", p.Op(i))
		}
		os.Exit(0)
	}
	t.Logf("seed=%d", seed)
	var prev []byte
	for i := 0; i < 3; i++ {
		c := exec.Command(os.Args[0], "-test.run=^TestAdv_4_ProcessReplay$")
		c.Env = append(os.Environ(), "ADV_REPLAY_CHILD=1", "GOMAXPROCS="+fmt.Sprint(i+1))
		b, e := c.CombinedOutput()
		if e != nil {
			t.Fatal(e)
		}
		if i > 0 && !bytes.Equal(prev, b) {
			t.Fatal("operation output differs across processes")
		}
		prev = b
	}
}
func TestAdv_5_StopWithQueuedAndSent(t *testing.T) {
	p := plan()
	x := tracker(t, p, 10)
	x.Tick(p.At(8), nil)
	for i := 0; i < 4; i++ {
		x.Sent(0, 1e9)
	}
	x.Completed(0, 1e9, lg.Result{})
	x.Stop()
	x.Finish()
	x.Finish()
	check(t, x)
	s := x.Snapshot()
	if s.Intended != 9 || s.Sent != 4 || s.CouldNotSend != 5 || s.InFlight != 3 {
		t.Fatalf("interrupt final snapshot %+v", s)
	}
}
func TestAdv_5_ConnectionReuseAndStaleCallbacks(t *testing.T) {
	p := plan()
	x := tracker(t, p, 3)
	for i := int64(0); i < p.Count(); i++ {
		x.ConnUp(0)
		x.Tick(p.At(i), nil)
		x.Sent(0, p.At(i))
		x.ConnDown(0)
		x.Completed(0, p.At(i)+1, lg.Result{})
		x.ConnDown(0)
		check(t, x)
	}
	x.Finish()
	if s := x.Snapshot(); s.Completed[lg.OutcomeLost] != p.Count() {
		t.Fatalf("lost=%d expected %d", s.Completed[lg.OutcomeLost], p.Count())
	}
}
func TestAdv_5_EventCampaign(t *testing.T) {
	n := 80
	if os.Getenv("ADV_LONG") == "1" {
		n = 10000
	}
	for k := 0; k < n; k++ {
		p := plan()
		p.Seed = seed + int64(k)
		x := tracker(t, p, 7)
		r := rand.New(rand.NewSource(p.Seed))
		for j := 0; j < 200; j++ {
			switch r.Intn(6) {
			case 0:
				x.Tick(int64(j)*1e7, nil)
			case 1:
				x.Sent(0, int64(j)*1e7)
			case 2:
				x.Completed(0, int64(j)*1e7, lg.Result{Outcome: lg.Outcome(r.Intn(5)), Code: "22012"})
			case 3:
				x.ConnDown(0)
			case 4:
				x.ConnUp(0)
			case 5:
				if j > 180 {
					x.Stop()
				}
			}
			check(t, x)
		}
		x.Finish()
		check(t, x)
	}
}
func TestAdv_6_ProfileManySeeds(t *testing.T) {
	for k := int64(0); k < 20; k++ {
		p := plan()
		p.Seed = seed + k
		t.Logf("seed=%d", p.Seed)
		seen := map[lg.Kind]bool{}
		for i := int64(0); i < 1000; i++ {
			o := p.Op(i)
			seen[o.Kind] = true
			switch o.Kind {
			case lg.KindUnique:
				if !strings.Contains(o.SQL, "'k1'") {
					t.Fatal(o.SQL)
				}
			case lg.KindMissing:
				if !strings.Contains(o.SQL, "mg_load_missing") {
					t.Fatal(o.SQL)
				}
			case lg.KindDivZero:
				if o.SQL != "SELECT 1 / 0" {
					t.Fatal(o.SQL)
				}
			case lg.KindRefused:
				if o.SQL != "LISTEN mg_load" {
					t.Fatal(o.SQL)
				}
			}
		}
		if len(seen) != 6 {
			t.Fatalf("only %d outcomes generated", len(seen))
		}
	}
}
func TestAdv_6_ClassCountsInterleaved(t *testing.T) {
	p := plan()
	x := tracker(t, p, 100)
	codes := []string{"23505", "42P01", "22012", "0A000", "57014"}
	x.Tick(p.DurationNs, nil)
	want := map[string]int64{}
	for i := int64(0); i < p.Count(); i++ {
		code := codes[i%5]
		x.Sent(0, 2e9)
		x.Completed(0, 3e9, lg.Result{Outcome: lg.OutcomeDBError, Code: code})
		want[code[:2]]++
	}
	for _, c := range x.Snapshot().ErrorsByClass {
		if c.Count != want[c.Class] {
			t.Fatalf("class %s count %d expected %d", c.Class, c.Count, want[c.Class])
		}
		delete(want, c.Class)
	}
	if len(want) > 0 {
		t.Fatal(want)
	}
}
