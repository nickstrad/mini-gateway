package workload_test

import (
	"math"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
)

var kindArrive = sim.K("wl.arr")

func arrivals(t *testing.T, service time.Duration, tenant workload.Tenant) ([]sim.Time, *world) {
	t.Helper()
	s := sim.New(sim.Config{Seed: 7, Scenario: t.Name()})
	var at []sim.Time
	s.SetTraceTap(func(r sim.Record) {
		if r.Kind == kindArrive {
			at = append(at, r.At)
		}
	})
	w := buildOn(t, s, fakepg.Config{Capacity: 1, Rows: []fakepg.Row{{Key: "k0", Value: "v"}}, Service: fakepg.Service{Fixed: service}},
		workload.Config{Tenants: []workload.Tenant{tenant}})
	w.finish(t)
	return at, w
}

func TestOpenLoopArrivalsEqualIntendedWhateverTheServerSpeed(t *testing.T) {
	const clients, perClient, interval = 10, 50, 10 * time.Millisecond
	tenant := workload.Tenant{Name: "a", Clients: clients, Requests: perClient, Interval: interval, Spread: true, Keys: workload.Keys{N: 1}}
	window := sim.Time(int64(interval) * perClient)
	for _, service := range []time.Duration{0, time.Millisecond, 20 * time.Millisecond} {
		at, w := arrivals(t, service, tenant)
		in := 0
		for _, a := range at {
			if a < window {
				in++
			}
		}
		if in != clients*perClient || len(at) != clients*perClient {
			t.Fatalf("service %s: %d arrivals in the window (%d total), want %d", service, in, len(at), clients*perClient)
		}
		for i, a := range at[:clients] {
			if want := sim.Time(int64(interval) * int64(i) / clients); a != want {
				t.Fatalf("service %s: arrival %d at %d, want %d", service, i, a, want)
			}
		}
		s := w.wl.Snapshot().Total
		if s.Intended != clients*perClient || s.Sent != s.Intended || s.OK != s.Intended {
			t.Fatalf("service %s: %+v", service, s)
		}
	}
}

func TestLatencyIsMeasuredFromIntendedSendTime(t *testing.T) {
	const interval = 10 * time.Millisecond
	stall := fakepg.Stall{From: sim.Time(300 * time.Millisecond), To: sim.Time(301 * time.Millisecond), Extra: 200 * time.Millisecond}
	s := sim.New(sim.Config{Seed: 3, Scenario: t.Name()})
	w := buildOn(t, s, fakepg.Config{Rows: []fakepg.Row{{Key: "k0", Value: "v"}}, Service: fakepg.Service{Fixed: time.Millisecond, Stalls: []fakepg.Stall{stall}}},
		workload.Config{Tenants: []workload.Tenant{{Name: "a", Clients: 1, Requests: 100, Interval: interval, Keys: workload.Keys{N: 1}}}})
	w.finish(t)
	snap := w.wl.Snapshot().Total
	over := func(h []obs.Bucket, limit int64) int64 {
		n := int64(0)
		for _, b := range h {
			if b.Lo > limit {
				n += b.Count
			}
		}
		return n
	}
	fromIntended := over(snap.Latency.Buckets, int64(50*time.Millisecond))
	fromSend := over(snap.LatencyFromSend.Buckets, int64(50*time.Millisecond))
	if snap.Latency.Max < int64(200*time.Millisecond) {
		t.Fatalf("the stall does not show in the latency: max %d", snap.Latency.Max)
	}
	if fromIntended < 15 || fromSend != 1 {
		t.Fatalf("requests above 50ms: %d from the intended time, %d from the send time; want at least 15 and exactly 1", fromIntended, fromSend)
	}
	t.Logf("requests slower than 50ms: %d measured from the intended send time, %d measured from the actual send", fromIntended, fromSend)
}

func zipfShare(n int, s float64, top int) (float64, float64) {
	total, head := 0.0, 0.0
	for k := 1; k <= n; k++ {
		w := math.Pow(float64(k), -s)
		total += w
		if k <= top {
			head += w
		}
	}
	return head / total, math.Pow(1, -s) / total
}

func TestZipfSharesMatchTheCalculation(t *testing.T) {
	const n, draws = 100_000, 200_000
	const bound = 0.005
	wantTop, wantHot := zipfShare(n, 1.2, n/100)
	if math.Abs(wantTop-0.85) > 0.005 {
		t.Fatalf("the calculation disagrees with Phase 0 (top 1%% share 0.85 at s=1.2, N=100000): %.4f", wantTop)
	}
	kc := workload.NewKeyChooser(sim.NewStream(1, "zipf-test"), workload.Keys{Kind: workload.KeysZipf, N: n, Zipf: 1200})
	top, hot := 0, 0
	for i := 0; i < draws; i++ {
		k := kc.Index()
		if k < 0 || k >= n {
			t.Fatalf("key %d out of range", k)
		}
		if k < n/100 {
			top++
		}
		if k == 0 {
			hot++
		}
	}
	gotTop, gotHot := float64(top)/draws, float64(hot)/draws
	if math.Abs(gotTop-wantTop) > bound || math.Abs(gotHot-wantHot) > bound {
		t.Fatalf("top 1%% share %.4f (want %.4f), hottest key %.4f (want %.4f), bound %.3f", gotTop, wantTop, gotHot, wantHot, bound)
	}
	t.Logf("s=1.2, N=%d, %d draws: top 1%% share %.4f (exact %.4f), hottest key %.4f (exact %.4f)", n, draws, gotTop, wantTop, gotHot, wantHot)
}

func TestUniformKeysCoverTheRange(t *testing.T) {
	kc := workload.NewKeyChooser(sim.NewStream(1, "uniform"), workload.Keys{N: 10})
	var seen [10]int
	for i := 0; i < 10_000; i++ {
		seen[kc.Index()]++
	}
	for k, c := range seen {
		if c < 900 || c > 1100 {
			t.Fatalf("key %d drawn %d times of 10000", k, c)
		}
	}
	if workload.NewKeyChooser(nil, workload.Keys{Kind: workload.KeysFixed, Fixed: "hot"}).Next() != "hot" {
		t.Fatal("fixed key")
	}
}

func TestPoissonInterArrivalMean(t *testing.T) {
	const mean, n = 10 * time.Millisecond, 5000
	at, _ := arrivals(t, 0, workload.Tenant{Name: "a", Clients: 1, Requests: n, Interval: mean, Arrival: workload.ArrivalPoisson, Keys: workload.Keys{N: 1}})
	if len(at) != n {
		t.Fatalf("%d arrivals", len(at))
	}
	below := 0
	for i := 1; i < len(at); i++ {
		if time.Duration(at[i]-at[i-1]) < mean {
			below++
		}
	}
	got := time.Duration(int64(at[len(at)-1]-at[0]) / int64(n-1))
	if d := got - mean; d < -mean/20 || d > mean/20 {
		t.Fatalf("mean gap %s, want %s within 5%%", got, mean)
	}
	if frac := float64(below) / float64(n-1); math.Abs(frac-(1-math.Exp(-1))) > 0.03 {
		t.Fatalf("share of gaps below the mean %.3f, want 0.632 within 0.03", frac)
	}
	t.Logf("Poisson: mean gap %s (want %s), %d of %d gaps below the mean", got, mean, below, n-1)
}

func TestClosedLoopThinkTime(t *testing.T) {
	at, w := arrivals(t, 5*time.Millisecond, workload.Tenant{Name: "a", Clients: 1, Requests: 5, Arrival: workload.ArrivalClosed, Think: 10 * time.Millisecond, Keys: workload.Keys{N: 1}})
	if len(at) != 5 {
		t.Fatalf("%d arrivals", len(at))
	}
	for i := 1; i < len(at); i++ {
		if gap := time.Duration(at[i] - at[i-1]); gap < 15*time.Millisecond {
			t.Fatalf("closed-loop gap %s: the next request did not wait for the reply and the think time", gap)
		}
	}
	if s := w.wl.Snapshot().Total; s.OK != 5 {
		t.Fatalf("%+v", s)
	}
}
