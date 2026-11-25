package obs

import (
	"math/rand/v2"
	"testing"
)

func instanceSnapshots(t *testing.T, r *rand.Rand, n int, events []synthEvent) ([]InstanceSnapshot, InstanceSnapshot) {
	t.Helper()
	parts := make([]*synth, n)
	for i := range parts {
		parts[i] = newSynth()
	}
	all := newSynth()
	for _, ev := range events {
		parts[r.IntN(n)].apply(ev)
		all.apply(ev)
	}
	var snaps []InstanceSnapshot
	for i, p := range parts {
		s, err := synthCollector(t, "i"+string(rune('a'+i)), p, true).Collect()
		if err != nil {
			t.Fatal(err)
		}
		snaps = append(snaps, onlyComponent(s, ComponentPool))
	}
	whole, err := synthCollector(t, "fleet", all, true).Collect()
	if err != nil {
		t.Fatal(err)
	}
	return snaps, onlyComponent(whole, ComponentPool)
}

func synthRegistry(t *testing.T) *Registry {
	var reg Registry
	if err := reg.Declare(ComponentPool, synthMetrics()...); err != nil {
		t.Fatal(err)
	}
	return &reg
}

func mustMerge(t *testing.T, reg *Registry, snaps ...InstanceSnapshot) InstanceSnapshot {
	t.Helper()
	m, err := Merge(reg, "fleet", snaps...)
	if err != nil {
		t.Fatal(err)
	}
	m.Build, m.ConfigFingerprint, m.Seq = "b1", "f1", 0
	return m
}

func TestMergeOfInstancesEqualsOneInstanceThatSawEverything(t *testing.T) {
	reg := synthRegistry(t)
	for seed := uint64(0); seed < uint64(seeds()); seed++ {
		r := rand.New(rand.NewPCG(seed, 77))
		n := 1 + r.IntN(5)
		events := randomEvents(r, 1+r.IntN(400), 1+r.IntN(6))
		snaps, whole := instanceSnapshots(t, r, n, events)
		var nonEmpty []InstanceSnapshot
		for _, s := range snaps {
			if len(s.Points) > 0 {
				nonEmpty = append(nonEmpty, s)
			}
		}
		got := mustMerge(t, reg, snaps...)
		if !Equal(got, whole) {
			t.Fatalf("seed %d: merge of %d instances differs from one instance\nmerged %+v\nwhole  %+v", seed, n, got.Points, whole.Points)
		}
	}
}

func TestMergeIsAssociativeAndCommutative(t *testing.T) {
	reg := synthRegistry(t)
	for seed := uint64(0); seed < uint64(seeds()); seed++ {
		r := rand.New(rand.NewPCG(seed, 78))
		snaps, _ := instanceSnapshots(t, r, 3, randomEvents(r, 1+r.IntN(300), 1+r.IntN(5)))
		a, b, c := snaps[0], snaps[1], snaps[2]
		left := mustMerge(t, reg, mustMerge(t, reg, a, b), c)
		right := mustMerge(t, reg, a, mustMerge(t, reg, b, c))
		flat := mustMerge(t, reg, a, b, c)
		swapped := mustMerge(t, reg, c, a, b)
		if !Equal(left, right) || !Equal(left, flat) || !Equal(flat, swapped) {
			t.Fatalf("seed %d: merge laws broken", seed)
		}
		if !Equal(mustMerge(t, reg, a, b), mustMerge(t, reg, b, a)) {
			t.Fatalf("seed %d: not commutative", seed)
		}
	}
}

func TestHighWaterMergesByMaxAndCountersBySum(t *testing.T) {
	reg := synthRegistry(t)
	mk := func(largest, inUse, events int64) InstanceSnapshot {
		return InstanceSnapshot{Points: []Point{
			{Component: ComponentPool, Metric: "events", Kind: KindCounter, Value: events},
			{Component: ComponentPool, Metric: "in_use", Kind: KindGauge, Value: inUse},
			{Component: ComponentPool, Metric: "largest", Kind: KindGauge, Value: largest},
		}}
	}
	m := mustMerge(t, reg, mk(7, 2, 10), mk(30, 3, 1), mk(4, -1, 5))
	if m.Value(ComponentPool, "largest", Labels{}) != 30 || m.Value(ComponentPool, "in_use", Labels{}) != 4 || m.Value(ComponentPool, "events", Labels{}) != 16 {
		t.Fatalf("%+v", m.Points)
	}
}

func TestMergeRefusesUndeclaredAndKindClash(t *testing.T) {
	reg := synthRegistry(t)
	a := InstanceSnapshot{Points: []Point{{Component: ComponentPool, Metric: "ghost", Kind: KindCounter, Value: 1}}}
	if _, err := Merge(reg, "x", a, a); err == nil {
		t.Fatal("undeclared metric merged")
	}
	b := InstanceSnapshot{Points: []Point{{Component: ComponentPool, Metric: "events", Kind: KindGauge, Value: 1}}}
	c := InstanceSnapshot{Points: []Point{{Component: ComponentPool, Metric: "events", Kind: KindCounter, Value: 1}}}
	if _, err := Merge(reg, "x", b, c); err == nil {
		t.Fatal("kind clash merged")
	}
}

func TestMergeBucketsAddsPerBucket(t *testing.T) {
	a := []Bucket{{1, 1, 2}, {5, 6, 1}}
	b := []Bucket{{0, 0, 1}, {5, 6, 4}, {9, 9, 1}}
	got, err := MergeBuckets(a, b)
	if err != nil {
		t.Fatal(err)
	}
	want := []Bucket{{0, 0, 1}, {1, 1, 2}, {5, 6, 5}, {9, 9, 1}}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%v", got)
		}
	}
}
