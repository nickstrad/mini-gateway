package promtext_test

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/obstest"
	"github.com/nickstrad/mini-gateway/internal/obs/promtext"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("scrape gone") }

func tenantPolicy(max int) func(obs.InstanceSnapshot) error {
	return func(s obs.InstanceSnapshot) error {
		ts := s.Tenants()
		if len(ts) > max+1 {
			return fmt.Errorf("%d tenant label values, bound %d", len(ts), max+1)
		}
		for _, tn := range ts {
			if tn != promtext.OtherTenant && !promtext.Labelable(tn) {
				return fmt.Errorf("tenant %q should have been folded", tn)
			}
		}
		return nil
	}
}

func subject(cfg promtext.Config) obstest.Subject {
	return obstest.Subject{
		Name:     promtext.AdapterName,
		Fidelity: obstest.Folded,
		New: func(t testing.TB, reg *obs.Registry) obstest.Instance {
			var buf bytes.Buffer
			a := promtext.New(reg, &buf, cfg)
			return obstest.Instance{
				Adapter: a,
				Output:  func(testing.TB) []byte { return bytes.Clone(buf.Bytes()) },
				Decoded: func(t testing.TB) obs.InstanceSnapshot {
					if err := promtext.Check(a.Last()); err != nil {
						t.Fatal(err)
					}
					s, err := promtext.Decode(reg, a.Last())
					if err != nil {
						t.Fatal(err)
					}
					return s
				},
			}
		},
		NewFailing:  func(_ testing.TB, reg *obs.Registry) obs.Adapter { return promtext.New(reg, failWriter{}, cfg) },
		LabelPolicy: tenantPolicy(max(cfg.TenantMax, 0)),
	}
}

func TestConformance(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		sub := subject(promtext.Config{})
		sub.LabelPolicy = tenantPolicy(promtext.DefaultTenantMax)
		obstest.Conformance(t, sub)
	})
	t.Run("max=3", func(t *testing.T) { obstest.Conformance(t, subject(promtext.Config{TenantMax: 3})) })
	t.Run("configured", func(t *testing.T) {
		obstest.Conformance(t, subject(promtext.Config{TenantMax: 4, Tenants: []string{"beta", "other", "new\nline", "zzz"}}))
	})
}

func TestGolden(t *testing.T) {
	s, reg := obstest.FixtureSnapshot(t)
	a := promtext.New(reg, nil, promtext.Config{TenantMax: 4})
	if err := a.Export(s); err != nil {
		t.Fatal(err)
	}
	if err := promtext.Check(a.Last()); err != nil {
		t.Fatal(err)
	}
	obstest.Golden(t, *update, "fixture.prom", a.Last())
}

func manyTenants(n int) (*obs.Registry, func(round int) obs.InstanceSnapshot) {
	var reg obs.Registry
	_ = reg.Declare(obs.ComponentAdmission,
		obs.CounterDesc("requests", obs.LabelTenant|obs.LabelOutcome, "Requests."),
		obs.HistogramDesc("wait", obs.LabelTenant, obs.UnitNs, "Wait."))
	names := make([]string, n)
	for i := range names {
		switch i % 5 {
		case 0:
			names[i] = "t" + strconv.Itoa(i)
		case 1:
			names[i] = "q\"" + strconv.Itoa(i) + "\\\n"
		case 2:
			names[i] = strings.Repeat("L", 70) + strconv.Itoa(i)
		case 3:
			names[i] = "other" + strings.Repeat(" ", i%3)
		default:
			names[i] = "ü" + strconv.Itoa(i) + "\xff"
		}
	}
	return &reg, func(round int) obs.InstanceSnapshot {
		var s obs.InstanceSnapshot
		h := obs.NewDurationHistogram()
		h.Record(2_000_000)
		hs := h.Snapshot()
		for i, tn := range names {
			v := int64(i + 1)
			if round > 0 && i%2 == 0 {
				v += int64(round)
			}
			s.Points = append(s.Points, obs.Point{Component: obs.ComponentAdmission, Metric: "requests", Kind: obs.KindCounter,
				Labels: obs.Labels{Tenant: tn, Outcome: "ok"}, Value: v})
			s.Hists = append(s.Hists, obs.HistPoint{Component: obs.ComponentAdmission, Metric: "wait", Labels: obs.Labels{Tenant: tn}, Unit: obs.UnitNs, Buckets: hs.Buckets})
		}
		s.Sort()
		return s
	}
}

func TestTenantLabelBoundWithTenThousandHostileTenants(t *testing.T) {
	reg, snap := manyTenants(10_000)
	for _, max := range []int{0, 1, 32, 100} {
		a := promtext.New(reg, nil, promtext.Config{TenantMax: max})
		bound := max
		if bound == 0 {
			bound = promtext.DefaultTenantMax
		}
		for round := 0; round < 3; round++ {
			if err := a.Export(snap(round)); err != nil {
				t.Fatal(err)
			}
			if err := promtext.Check(a.Last()); err != nil {
				t.Fatal(err)
			}
			d, err := promtext.Decode(reg, a.Last())
			if err != nil {
				t.Fatal(err)
			}
			if err := tenantPolicy(bound)(d); err != nil {
				t.Fatalf("max %d round %d: %v", max, round, err)
			}
			if a.TenantsTracked() > int64(bound) {
				t.Fatalf("tracked %d", a.TenantsTracked())
			}
			if got, want := d.Sum(obs.ComponentAdmission, "requests"), snap(round).Sum(obs.ComponentAdmission, "requests"); got != want {
				t.Fatalf("folding lost requests: %d want %d", got, want)
			}
		}
	}
}

func TestQuietTenantsAreEvicted(t *testing.T) {
	var reg obs.Registry
	_ = reg.Declare(obs.ComponentAdmission, obs.CounterDesc("requests", obs.LabelTenant, "Requests."))
	mk := func(a, b, c int64) obs.InstanceSnapshot {
		return obs.InstanceSnapshot{Points: []obs.Point{
			{Component: obs.ComponentAdmission, Metric: "requests", Kind: obs.KindCounter, Labels: obs.Labels{Tenant: "a"}, Value: a},
			{Component: obs.ComponentAdmission, Metric: "requests", Kind: obs.KindCounter, Labels: obs.Labels{Tenant: "b"}, Value: b},
			{Component: obs.ComponentAdmission, Metric: "requests", Kind: obs.KindCounter, Labels: obs.Labels{Tenant: "c"}, Value: c},
		}}
	}
	a := promtext.New(&reg, nil, promtext.Config{TenantMax: 2, EvictAfter: 3})
	tenants := func() []string {
		d, err := promtext.Decode(&reg, a.Last())
		if err != nil {
			t.Fatal(err)
		}
		return d.Tenants()
	}
	export := func(s obs.InstanceSnapshot) {
		if err := a.Export(s); err != nil {
			t.Fatal(err)
		}
	}
	export(mk(1, 1, 1))
	if got := tenants(); strings.Join(got, ",") != "a,b,other" {
		t.Fatalf("first: %v", got)
	}
	export(mk(2, 1, 2))
	export(mk(3, 1, 3))
	if got := tenants(); strings.Join(got, ",") != "a,b,other" || a.TenantsTracked() != 2 {
		t.Fatalf("b quiet for 2: %v", got)
	}
	export(mk(4, 1, 4))
	if got := tenants(); strings.Join(got, ",") != "a,c,other" {
		t.Fatalf("b quiet for 3 publishes must be evicted and c take the slot: %v", got)
	}
	d, _ := promtext.Decode(&reg, a.Last())
	if d.Value(obs.ComponentAdmission, "requests", obs.Labels{Tenant: "other"}) != 1 {
		t.Fatalf("evicted b's count must be in other: %v", d.Points)
	}
}

func TestRebucketIsCumulativeOnTheShortList(t *testing.T) {
	h := obs.NewDurationHistogram()
	for _, v := range []int64{50_000, 100_000, 100_001, 3_000_000, 59_000_000_000, 70_000_000_000} {
		h.Record(v)
	}
	cum, total := promtext.Rebucket(promtext.DefaultExportBuckets, h.Snapshot().Buckets)
	if len(promtext.DefaultExportBuckets) != 16 || total != 6 {
		t.Fatalf("%d bounds, total %d", len(promtext.DefaultExportBuckets), total)
	}
	if cum[0] != 1 || cum[1] != 3 || cum[5] != 4 || cum[14] != 4 || cum[15] != 6 {
		t.Fatalf("%v", cum)
	}
}

func TestCheckerRejectsBadExposition(t *testing.T) {
	good := "# HELP a_total A.\n# TYPE a_total counter\na_total{x=\"1\"} 1\n"
	if err := promtext.Check([]byte(good)); err != nil {
		t.Fatal(err)
	}
	bad := map[string]string{
		"no newline":         "# TYPE a_total counter\na_total 1",
		"no type":            "a 1\n",
		"raw newline":        "# TYPE a gauge\na{x=\"1\n\"} 1\n",
		"bad escape":         "# TYPE a gauge\na{x=\"\\q\"} 1\n",
		"unquoted":           "# TYPE a gauge\na{x=1} 1\n",
		"dup series":         "# TYPE a gauge\na{x=\"1\"} 1\na{x=\"1\"} 2\n",
		"dup label":          "# TYPE a gauge\na{x=\"1\",x=\"2\"} 1\n",
		"trailing comma":     "# TYPE a gauge\na{x=\"1\",} 1\n",
		"split family":       "# TYPE a gauge\na 1\n# TYPE b gauge\nb 1\n# TYPE a gauge\n",
		"counter name":       "# TYPE a counter\na 1\n",
		"negative counter":   "# TYPE a_total counter\na_total -1\n",
		"bad value":          "# TYPE a gauge\na one\n",
		"bad name":           "# TYPE 1a gauge\n1a 1\n",
		"hist no inf":        "# TYPE h histogram\nh_bucket{le=\"1\"} 1\nh_count 1\n",
		"hist not cum":       "# TYPE h histogram\nh_bucket{le=\"1\"} 2\nh_bucket{le=\"2\"} 1\nh_bucket{le=\"+Inf\"} 2\nh_count 2\n",
		"hist count differs": "# TYPE h histogram\nh_bucket{le=\"+Inf\"} 2\nh_count 3\n",
		"reserved label":     "# TYPE a gauge\na{__x=\"1\"} 1\n",
		"wrong family":       "# TYPE a gauge\nb 1\n",
	}
	for name, text := range bad {
		if err := promtext.Check([]byte(text)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
