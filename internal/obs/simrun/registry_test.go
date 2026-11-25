package simrun_test

import (
	"testing"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/catalog"
)

func TestEveryDeclaredMetricIsEmittedAndEveryEmittedOneDocumented(t *testing.T) {
	baselineRuns(t)
	reg, err := catalog.Registry()
	if err != nil {
		t.Fatal(err)
	}
	type key struct{ c, m string }
	emitted := map[key]bool{}
	moved := map[key]bool{}
	for _, s := range baselineFinals {
		for _, p := range s.Points {
			if _, ok := reg.Lookup(p.Component, p.Metric); !ok {
				t.Fatalf("%s.%s exported but not in the catalog (docs/csv-schema.md)", p.Component, p.Metric)
			}
			emitted[key{p.Component, p.Metric}] = true
			moved[key{p.Component, p.Metric}] = moved[key{p.Component, p.Metric}] || p.Value != 0
		}
		for _, h := range s.Hists {
			if _, ok := reg.Lookup(h.Component, h.Metric); !ok {
				t.Fatalf("histogram %s.%s exported but not in the catalog", h.Component, h.Metric)
			}
			emitted[key{h.Component, h.Metric}] = true
			moved[key{h.Component, h.Metric}] = moved[key{h.Component, h.Metric}] || h.Count() > 0
		}
	}
	var quiet []string
	for _, d := range reg.All() {
		if !emitted[key{d.Component, d.Name}] {
			t.Errorf("%s.%s is declared but never emitted by any baseline run", d.Component, d.Name)
		}
		if !moved[key{d.Component, d.Name}] {
			quiet = append(quiet, d.Component+"."+d.Name)
		}
	}
	t.Logf("declared %d metrics; %d stay 0 in every baseline run (their mapping is proved by simsrc's TestMappingsMoveEveryMetric): %v", reg.Len(), len(quiet), quiet)
	if s := baselineFinals[0]; s.Value(obs.ComponentObs, obs.MetricPublishes, obs.Labels{}) == 0 {
		t.Fatal("publishes did not move")
	}
}
