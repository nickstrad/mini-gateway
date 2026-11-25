package obstest

import (
	"testing"

	"github.com/nickstrad/mini-gateway/internal/obs"
)

func TestHeadlineFidelityNeedsEveryHeadlineValue(t *testing.T) {
	var reg obs.Registry
	err := reg.Declare(obs.ComponentPool, obs.CounterDesc("a", 0, "A.").AsHeadline(), obs.CounterDesc("b", 0, "B.").AsHeadline(),
		obs.CounterDesc("c", 0, "C."), obs.CounterDesc("t", obs.LabelTenant, "T."))
	if err != nil {
		t.Fatal(err)
	}
	pt := func(m string, tenant string, v int64) obs.Point {
		return obs.Point{Component: obs.ComponentPool, Metric: m, Kind: obs.KindCounter, Labels: obs.Labels{Tenant: tenant}, Value: v}
	}
	want := obs.InstanceSnapshot{Points: []obs.Point{pt("a", "", 1), pt("b", "", 2), pt("c", "", 3), pt("t", "x", 4)}}
	full := obs.InstanceSnapshot{Points: []obs.Point{pt("a", "", 1), pt("b", "", 2)}}
	if err := compare(Headline, &reg, want, full); err != nil {
		t.Fatalf("every headline value present: %v", err)
	}
	first := obs.InstanceSnapshot{Points: []obs.Point{pt("a", "", 1)}}
	if err := compare(Headline, &reg, want, first); err == nil {
		t.Fatal("an output with only the first headline value passed")
	}
	wrong := obs.InstanceSnapshot{Points: []obs.Point{pt("a", "", 1), pt("b", "", 5)}}
	if err := compare(Headline, &reg, want, wrong); err == nil {
		t.Fatal("a wrong headline value passed")
	}
}
