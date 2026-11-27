package obs_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"math"
	"math/rand"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/obs"
)

const c03Seed int64 = 730031

const c03Module = "github.com/nickstrad/mini-gateway/"

func c03Start(t *testing.T) {
	t.Helper()
	t.Logf("seed=%d REPLAY: go test -count=1 -run '^%s$' ./internal/obs", c03Seed, t.Name())
}

func c03Must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func c03Root(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("../..")
	c03Must(t, err)
	return p
}

func c03Registry(t *testing.T) *obs.Registry {
	t.Helper()
	r := &obs.Registry{}
	c03Must(t, r.Declare("pool", obs.CounterDesc("requests", obs.LabelTenant, "Requests."), obs.HighWaterDesc("peak", 0, "Peak."), obs.HistogramDesc("wait", obs.LabelTenant, obs.UnitNs, "Wait.")))
	return r
}

func c03Snapshot() obs.InstanceSnapshot {
	return obs.InstanceSnapshot{Instance: "a", Build: "build", ConfigFingerprint: "cfg", Points: []obs.Point{{Component: "pool", Metric: "peak", Kind: obs.KindGauge, Value: 0}, {Component: "pool", Metric: "requests", Kind: obs.KindCounter, Labels: obs.Labels{Tenant: "tenant"}, Value: 7}}, Hists: []obs.HistPoint{{Component: "pool", Metric: "wait", Labels: obs.Labels{Tenant: "tenant"}, Unit: obs.UnitNs, Buckets: []obs.Bucket{{Lo: 1, Hi: 1, Count: 3}, {Lo: 8, Hi: 9, Count: 4}}}}}
}

func c03Imports(t *testing.T, dir string, check func(string, string)) {
	t.Helper()
	c03Must(t, filepath.WalkDir(filepath.Join(c03Root(t), dir), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, e := parser.ParseFile(token.NewFileSet(), p, nil, parser.ImportsOnly)
		if e != nil {
			return e
		}
		for _, i := range f.Imports {
			v, e := strconv.Unquote(i.Path.Value)
			if e != nil {
				return e
			}
			check(p, v)
		}
		return nil
	}))
}

func c03AdapterPath(p string) bool {
	for _, a := range []string{"csvflush", "jsonstats", "logline", "promtext"} {
		if strings.HasPrefix(p, c03Module+"internal/obs/"+a) {
			return true
		}
	}
	return false
}

func TestAdvC03_1_AdapterDependencyBoundary(t *testing.T) {
	c03Start(t)
	for _, a := range []string{"csvflush", "jsonstats", "logline", "promtext"} {
		c03Imports(t, "internal/obs/"+a, func(p, i string) {
			if strings.HasPrefix(i, c03Module) && i != c03Module+"internal/obs" {
				t.Errorf("adapter %s directly depends on %s", p, i)
			}
		})
	}
}

func TestAdvC03_1_ComponentDependencyBoundary(t *testing.T) {
	c03Start(t)
	c03Imports(t, "internal/sim", func(p, i string) {
		if c03AdapterPath(i) {
			t.Errorf("component %s imports adapter %s", p, i)
		}
	})
	c03Imports(t, "internal/testclient", func(p, i string) {
		if c03AdapterPath(i) {
			t.Errorf("client %s imports adapter %s", p, i)
		}
	})
}

func TestAdvC03_1_SnapshotHasNoLiveReferences(t *testing.T) {
	c03Start(t)
	var walk func(reflect.Type)
	walk = func(x reflect.Type) {
		switch x.Kind() {
		case reflect.Pointer, reflect.Func, reflect.Chan, reflect.Interface, reflect.Map:
			t.Errorf("snapshot retains live/unordered type %s", x)
		case reflect.Struct:
			for i := 0; i < x.NumField(); i++ {
				f := x.Field(i)
				if f.Type.PkgPath() == "time" {
					t.Errorf("snapshot carries time: %s", f.Name)
				}
				walk(f.Type)
			}
		case reflect.Array, reflect.Slice:
			walk(x.Elem())
		}
	}
	walk(reflect.TypeOf(obs.InstanceSnapshot{}))
}

func TestAdvC03_9_SumOverflow(t *testing.T) {
	c03Start(t)
	r := c03Registry(t)
	a := c03Snapshot()
	a.Hists = nil
	a.Points = a.Points[1:]
	a.Points[0].Value = math.MaxInt64
	b := a.Clone()
	b.Points[0].Value = 1
	got, e := obs.Merge(r, "fleet", a, b)
	if e == nil && got.Points[0].Value < 0 {
		t.Fatalf("positive counter sum silently wrapped: MaxInt64+1=%d; want exact result or overflow error", got.Points[0].Value)
	}
}

func TestAdvC03_9_NegativeHighWaterAndEmptyIdentity(t *testing.T) {
	c03Start(t)
	r := c03Registry(t)
	a := c03Snapshot()
	a.Hists = nil
	a.Points = a.Points[:1]
	a.Points[0].Value = -9
	b := a.Clone()
	b.Points[0].Value = -4
	got, e := obs.Merge(r, "fleet", obs.InstanceSnapshot{}, a, b)
	c03Must(t, e)
	if len(got.Points) != 1 || got.Points[0].Value != -4 {
		t.Fatalf("negative high-water maximum=%v want -4", got.Points)
	}
}

func TestAdvC03_9_SparseBucketPartitions(t *testing.T) {
	c03Start(t)
	r := c03Registry(t)
	rng := rand.New(rand.NewSource(c03Seed))
	for n := 0; n < 80; n++ {
		var ss []obs.InstanceSnapshot
		want := make([]int64, 31)
		for k := 0; k < 7; k++ {
			s := c03Snapshot()
			s.Points = nil
			s.Hists[0].Buckets = nil
			for j := 0; j < 31; j++ {
				v := rng.Int63n(5)
				want[j] += v
				if v > 0 {
					s.Hists[0].Buckets = append(s.Hists[0].Buckets, obs.Bucket{Lo: int64(j), Hi: int64(j), Count: v})
				}
			}
			ss = append(ss, s)
		}
		rng.Shuffle(len(ss), func(i, j int) { ss[i], ss[j] = ss[j], ss[i] })
		got, e := obs.Merge(r, "fleet", ss...)
		c03Must(t, e)
		for _, b := range got.Hists[0].Buckets {
			if b.Count != want[b.Lo] {
				t.Fatalf("seed=%d iteration=%d bucket=%d got=%d want=%d", c03Seed, n, b.Lo, b.Count, want[b.Lo])
			}
			want[b.Lo] = 0
		}
		for _, v := range want {
			if v != 0 {
				t.Fatal("lost sparse bucket", want)
			}
		}
	}
}

var c03CounterSink int64

var c03DetailSink obs.DetailHistogram

func TestAdvC03_10_CounterExtremeRecordingAllocations(t *testing.T) {
	c03Start(t)
	var c obs.Counter
	n := testing.AllocsPerRun(2000, func() { c.Inc(); c.Add(math.MaxInt64); c.Add(math.MinInt64); c03CounterSink = c.Value() })
	if n != 0 {
		t.Fatalf("counter allocated %g times/event", n)
	}
}

func TestAdvC03_10_HistogramClampAllocations(t *testing.T) {
	c03Start(t)
	h, e := obs.NewHistogram(0, obs.MaxValue)
	c03Must(t, e)
	n := testing.AllocsPerRun(2000, func() {
		for _, v := range []int64{math.MinInt64, 0, 1, 255, 256, obs.MaxValue, math.MaxInt64} {
			h.Record(v)
		}
	})
	if n != 0 || h.Count() == 0 || h.ClampedLow() == 0 || h.ClampedHigh() == 0 {
		t.Fatalf("allocs=%g count=%d clamps=%d/%d", n, h.Count(), h.ClampedLow(), h.ClampedHigh())
	}
}

func TestAdvC03_10_DisabledConstructionAndSnapshots(t *testing.T) {
	c03Start(t)
	n := testing.AllocsPerRun(2000, func() {
		c03DetailSink, _ = obs.NewDetailHistogram(false, math.MinInt64, math.MaxInt64)
		c03DetailSink.Record(math.MaxInt64)
	})
	if n != 0 || c03DetailSink.HeldBytes() != 0 || c03DetailSink.On() || c03DetailSink.Snapshot().Buckets != nil {
		t.Fatalf("disabled histogram allocs=%g bytes=%d on=%v snapshot=%+v", n, c03DetailSink.HeldBytes(), c03DetailSink.On(), c03DetailSink.Snapshot())
	}
}

func TestAdvC03_12_UndeclaredHistogramMerge(t *testing.T) {
	c03Start(t)
	r := c03Registry(t)
	s := c03Snapshot()
	s.Points = nil
	s.Hists[0].Metric = "not_declared"
	got, e := obs.Merge(r, "fleet", s, s)
	if e == nil {
		t.Fatalf("undeclared histogram exported by registry-aware merge: %+v", got.Hists)
	}
}
