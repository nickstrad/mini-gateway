package adv_test

import (
	"bytes"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/catalog"
	"github.com/nickstrad/mini-gateway/internal/obs/jsonstats"
	"github.com/nickstrad/mini-gateway/internal/obs/logline"
	"github.com/nickstrad/mini-gateway/internal/obs/promtext"
)

const seed int64 = 730031
const module = "github.com/nickstrad/mini-gateway/"

func start(t *testing.T) {
	t.Helper()
	t.Logf("seed=%d REPLAY: go test -count=1 -run '^%s$' ./agent_work/adversarial/c03-obs-spine", seed, t.Name())
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func root(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("../../..")
	must(t, err)
	return p
}
func read(t *testing.T, p string) []byte { t.Helper(); b, e := os.ReadFile(p); must(t, e); return b }
func registry(t *testing.T) *obs.Registry {
	t.Helper()
	r := &obs.Registry{}
	must(t, r.Declare("pool", obs.CounterDesc("requests", obs.LabelTenant, "Requests."), obs.HighWaterDesc("peak", 0, "Peak."), obs.HistogramDesc("wait", obs.LabelTenant, obs.UnitNs, "Wait.")))
	return r
}
func snapshot() obs.InstanceSnapshot {
	return obs.InstanceSnapshot{Instance: "a", Build: "build", ConfigFingerprint: "cfg", Points: []obs.Point{{Component: "pool", Metric: "peak", Kind: obs.KindGauge, Value: 0}, {Component: "pool", Metric: "requests", Kind: obs.KindCounter, Labels: obs.Labels{Tenant: "tenant"}, Value: 7}}, Hists: []obs.HistPoint{{Component: "pool", Metric: "wait", Labels: obs.Labels{Tenant: "tenant"}, Unit: obs.UnitNs, Buckets: []obs.Bucket{{Lo: 1, Hi: 1, Count: 3}, {Lo: 8, Hi: 9, Count: 4}}}}}
}
func imports(t *testing.T, dir string, check func(string, string)) {
	t.Helper()
	must(t, filepath.WalkDir(filepath.Join(root(t), dir), func(p string, d fs.DirEntry, err error) error {
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
func adapterPath(p string) bool {
	for _, a := range []string{"csvflush", "jsonstats", "logline", "promtext"} {
		if strings.HasPrefix(p, module+"internal/obs/"+a) {
			return true
		}
	}
	return false
}
func TestAdv_1_AdapterDependencyBoundary(t *testing.T) {
	start(t)
	for _, a := range []string{"csvflush", "jsonstats", "logline", "promtext"} {
		imports(t, "internal/obs/"+a, func(p, i string) {
			if strings.HasPrefix(i, module) && i != module+"internal/obs" {
				t.Errorf("adapter %s directly depends on %s", p, i)
			}
		})
	}
}
func TestAdv_1_ComponentDependencyBoundary(t *testing.T) {
	start(t)
	imports(t, "internal/sim", func(p, i string) {
		if adapterPath(i) {
			t.Errorf("component %s imports adapter %s", p, i)
		}
	})
	imports(t, "internal/testclient", func(p, i string) {
		if adapterPath(i) {
			t.Errorf("client %s imports adapter %s", p, i)
		}
	})
}
func TestAdv_1_SnapshotHasNoLiveReferences(t *testing.T) {
	start(t)
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
func TestAdv_2_PrometheusHistoryIndependence(t *testing.T) {
	start(t)
	r := registry(t)
	a := promtext.New(r, nil, promtext.Config{TenantMax: 1, EvictAfter: 1})
	s := snapshot()
	must(t, a.Export(s))
	first := slices.Clone(a.Last())
	must(t, a.Export(s))
	if !bytes.Equal(first, a.Last()) {
		t.Fatalf("identical snapshot changed bytes after quiet-tenant eviction:\nfirst=%s\nsecond=%s", first, a.Last())
	}
}
func TestAdv_2_EqualInputsAfterHostileNoise(t *testing.T) {
	start(t)
	r := registry(t)
	s := snapshot()
	s.Instance = "\x00\n\\\"雪"
	a := jsonstats.New(nil)
	b := logline.New(r, nil)
	for _, x := range []interface {
		Export(obs.InstanceSnapshot) error
		Last() []byte
	}{a, b} {
		must(t, x.Export(s))
		want := slices.Clone(x.Last())
		noise := s.Clone()
		noise.Points[0].Value = math.MaxInt64
		must(t, x.Export(noise))
		must(t, x.Export(s))
		if !bytes.Equal(want, x.Last()) {
			t.Errorf("%T depends on preceding snapshot", x)
		}
	}
}
func TestAdv_2_CollectorEmissionPermutation(t *testing.T) {
	start(t)
	rng := rand.New(rand.NewSource(seed))
	var first []byte
	for n := 0; n < 40; n++ {
		order := rng.Perm(19)
		c := obs.NewCollector(obs.Config{Instance: "x", Detail: true})
		must(t, c.Register(obs.Source{Component: "pool", Metrics: []obs.MetricDesc{obs.CounterDesc("requests", obs.LabelTenant, "Requests.")}, Collect: func(e *obs.Emitter) {
			for _, i := range order {
				e.Counter("requests", obs.Labels{Tenant: fmt.Sprintf("t%02d", i)}, int64(i))
			}
		}}))
		s, e := c.Collect()
		must(t, e)
		b, e := jsonstats.Render(s)
		must(t, e)
		if n == 0 {
			first = b
		} else if !bytes.Equal(first, b) {
			t.Fatalf("seed=%d permutation=%v changed snapshot bytes", seed, order)
		}
	}
}
func TestAdv_8_CounterGaugeFamilyCollision(t *testing.T) {
	start(t)
	r := &obs.Registry{}
	must(t, r.Declare("pool", obs.CounterDesc("x", 0, "Counter."), obs.GaugeDesc("x_total", 0, "Gauge.")))
	s := obs.InstanceSnapshot{Points: []obs.Point{{Component: "pool", Metric: "x", Kind: obs.KindCounter, Value: 2}, {Component: "pool", Metric: "x_total", Kind: obs.KindGauge, Value: -3}}}
	a := promtext.New(r, nil, promtext.Config{})
	must(t, a.Export(s))
	must(t, promtext.Check(a.Last()))
	if !bytes.Contains(a.Last(), []byte(" 2\n")) || !bytes.Contains(a.Last(), []byte(" -3\n")) {
		t.Fatalf("valid declarations alias and lose independent values: %s", a.Last())
	}
}
func TestAdv_8_HistogramGaugeSeriesCollision(t *testing.T) {
	start(t)
	r := registry(t)
	must(t, r.Declare("pool", obs.GaugeDesc("wait_count", obs.LabelTenant, "Count gauge.")))
	s := snapshot()
	s.Points = append(s.Points, obs.Point{Component: "pool", Metric: "wait_count", Kind: obs.KindGauge, Labels: obs.Labels{Tenant: "tenant"}, Value: 91})
	s.Sort()
	a := promtext.New(r, nil, promtext.Config{})
	must(t, a.Export(s))
	if e := promtext.Check(a.Last()); e != nil {
		t.Fatalf("registered histogram + suffixed gauge produce invalid exposition: %v\n%s", e, a.Last())
	}
}
func TestAdv_8_HostileTenantCampaign(t *testing.T) {
	start(t)
	rng := rand.New(rand.NewSource(seed))
	r := registry(t)
	a := promtext.New(r, nil, promtext.Config{TenantMax: 3, EvictAfter: 2})
	for round := 0; round < 12; round++ {
		s := snapshot()
		s.Points = s.Points[:1]
		s.Hists = nil
		for i := 0; i < 127; i++ {
			raw := make([]byte, rng.Intn(90)+1)
			rng.Read(raw)
			s.Points = append(s.Points, obs.Point{Component: "pool", Metric: "requests", Kind: obs.KindCounter, Labels: obs.Labels{Tenant: string(raw)}, Value: int64(i + 1)})
		}
		s.Sort()
		must(t, a.Export(s))
		must(t, promtext.Check(a.Last()))
		if a.TenantsTracked() > 3 {
			t.Fatalf("round %d tracked %d > 3", round, a.TenantsTracked())
		} // Parse quoted tenant labels independently, including escapes.
		labels := map[string]bool{}
		for _, line := range strings.Split(string(a.Last()), "\n") {
			i := strings.Index(line, `tenant="`)
			if i < 0 {
				continue
			}
			q := line[i+len("tenant="):]
			for j := 1; j < len(q); j++ {
				if q[j] == '\\' {
					j++
					continue
				}
				if q[j] == '"' {
					v, e := strconv.Unquote(q[:j+1])
					must(t, e)
					labels[v] = true
					break
				}
			}
		}
		if len(labels) > 4 {
			t.Fatalf("round %d has %d tenant labels", round, len(labels))
		}
	}
}
func TestAdv_9_SumOverflow(t *testing.T) {
	start(t)
	r := registry(t)
	a := snapshot()
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
func TestAdv_9_NegativeHighWaterAndEmptyIdentity(t *testing.T) {
	start(t)
	r := registry(t)
	a := snapshot()
	a.Hists = nil
	a.Points = a.Points[:1]
	a.Points[0].Value = -9
	b := a.Clone()
	b.Points[0].Value = -4
	got, e := obs.Merge(r, "fleet", obs.InstanceSnapshot{}, a, b)
	must(t, e)
	if len(got.Points) != 1 || got.Points[0].Value != -4 {
		t.Fatalf("negative high-water maximum=%v want -4", got.Points)
	}
}
func TestAdv_9_SparseBucketPartitions(t *testing.T) {
	start(t)
	r := registry(t)
	rng := rand.New(rand.NewSource(seed))
	for n := 0; n < 80; n++ {
		var ss []obs.InstanceSnapshot
		want := make([]int64, 31)
		for k := 0; k < 7; k++ {
			s := snapshot()
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
		must(t, e)
		for _, b := range got.Hists[0].Buckets {
			if b.Count != want[b.Lo] {
				t.Fatalf("seed=%d iteration=%d bucket=%d got=%d want=%d", seed, n, b.Lo, b.Count, want[b.Lo])
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

var counterSink int64
var detailSink obs.DetailHistogram

func TestAdv_10_CounterExtremeRecordingAllocations(t *testing.T) {
	start(t)
	var c obs.Counter
	n := testing.AllocsPerRun(2000, func() { c.Inc(); c.Add(math.MaxInt64); c.Add(math.MinInt64); counterSink = c.Value() })
	if n != 0 {
		t.Fatalf("counter allocated %g times/event", n)
	}
}
func TestAdv_10_HistogramClampAllocations(t *testing.T) {
	start(t)
	h, e := obs.NewHistogram(0, obs.MaxValue)
	must(t, e)
	n := testing.AllocsPerRun(2000, func() {
		for _, v := range []int64{math.MinInt64, 0, 1, 255, 256, obs.MaxValue, math.MaxInt64} {
			h.Record(v)
		}
	})
	if n != 0 || h.Count() == 0 || h.ClampedLow() == 0 || h.ClampedHigh() == 0 {
		t.Fatalf("allocs=%g count=%d clamps=%d/%d", n, h.Count(), h.ClampedLow(), h.ClampedHigh())
	}
}
func TestAdv_10_DisabledConstructionAndSnapshots(t *testing.T) {
	start(t)
	n := testing.AllocsPerRun(2000, func() {
		detailSink, _ = obs.NewDetailHistogram(false, math.MinInt64, math.MaxInt64)
		detailSink.Record(math.MaxInt64)
	})
	if n != 0 || detailSink.HeldBytes() != 0 || detailSink.On() || detailSink.Snapshot().Buckets != nil {
		t.Fatalf("disabled histogram allocs=%g bytes=%d on=%v snapshot=%+v", n, detailSink.HeldBytes(), detailSink.On(), detailSink.Snapshot())
	}
}
func TestAdv_12_UndeclaredSingletonMerge(t *testing.T) {
	start(t)
	r := registry(t)
	s := snapshot()
	s.Hists = nil
	s.Points = s.Points[1:]
	s.Points[0].Metric = "not_declared"
	got, e := obs.Merge(r, "fleet", s)
	if e == nil {
		b, e := jsonstats.Render(got)
		must(t, e)
		t.Fatalf("undeclared singleton survived registry-aware merge and was exported: %s", b)
	}
}
func TestAdv_12_UndeclaredHistogramMerge(t *testing.T) {
	start(t)
	r := registry(t)
	s := snapshot()
	s.Points = nil
	s.Hists[0].Metric = "not_declared"
	got, e := obs.Merge(r, "fleet", s, s)
	if e == nil {
		t.Fatalf("undeclared histogram exported by registry-aware merge: %+v", got.Hists)
	}
}
func TestAdv_12_RuntimeDeclarationDocumentationGap(t *testing.T) {
	start(t)
	c := obs.NewCollector(obs.Config{Detail: true})
	must(t, c.Register(obs.Source{Component: "engine", Metrics: []obs.MetricDesc{obs.CounterDesc("adversarial_unindexed", 0, "Not in catalog.")}, Collect: func(e *obs.Emitter) { e.Counter("adversarial_unindexed", obs.Labels{}, 1) }}))
	s, e := c.Publish()
	must(t, e)
	doc, e := catalog.Markdown()
	must(t, e)
	if !bytes.Equal(doc, read(t, filepath.Join(root(t), "docs/csv-schema.md"))) {
		t.Fatal("documentation control already stale")
	}
	if s.Value("engine", "adversarial_unindexed", obs.Labels{}) == 1 && !bytes.Contains(doc, []byte("`adversarial_unindexed`")) {
		t.Fatal("registered metric exported but generated registry/document remains unchanged; dynamic registration bypasses documentation enforcement")
	}
}
