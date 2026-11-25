package wiresrc_test

import (
	"reflect"
	"slices"
	"strconv"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/wiresrc"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
)

type filler struct {
	n     int64
	paths map[int64]string
}

func (f *filler) fill(v reflect.Value, path string) {
	switch v.Kind() {
	case reflect.Int64:
		f.n++
		v.SetInt(f.n * 1009)
		f.paths[f.n*1009] = path
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			f.fill(v.Field(i), path+"."+v.Type().Field(i).Name)
		}
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			f.fill(v.Index(i), path+"["+strconv.Itoa(i)+"]")
		}
	}
}

// TestMappingMovesEveryMetricAndExportsEveryField fills every int64 field of
// WireSnapshot via reflection and proves every declared wire metric moves and
// no snapshot field is dropped.
func TestMappingMovesEveryMetricAndExportsEveryField(t *testing.T) {
	var s pgwire.WireSnapshot
	f := &filler{paths: map[int64]string{}}
	f.fill(reflect.ValueOf(&s).Elem(), "")
	c := obs.NewCollector(obs.Config{Instance: "i", Detail: true})
	if err := c.Register(wiresrc.Source(func() pgwire.WireSnapshot { return s })); err != nil {
		t.Fatal(err)
	}
	snap, err := c.Collect()
	if err != nil {
		t.Fatal(err)
	}
	values := map[int64]bool{}
	moved := map[string]bool{}
	for _, p := range snap.Points {
		if p.Component != obs.ComponentWire {
			continue
		}
		values[p.Value] = true
		moved[p.Metric] = moved[p.Metric] || p.Value != 0
	}
	for _, d := range wiresrc.Metrics() {
		if !moved[d.Name] {
			t.Errorf("wire.%s never moves with every snapshot field non-zero", d.Name)
		}
	}
	var lost []string
	for v, path := range f.paths {
		if !values[v] {
			lost = append(lost, path)
		}
	}
	slices.Sort(lost)
	if len(lost) > 0 {
		t.Errorf("snapshot fields not exported: %v", lost)
	}
	if len(f.paths) < 100 {
		t.Fatalf("the filler reached only %d fields", len(f.paths))
	}
}

// TestZeroSnapshotEmitsEveryLabelValue proves a zero WireSnapshot emits explicit
// zero rows for every enum label value so SQL assertions never miss a slot.
func TestZeroSnapshotEmitsEveryLabelValue(t *testing.T) {
	c := obs.NewCollector(obs.Config{Instance: "i", Detail: true})
	if err := c.Register(wiresrc.Source(func() pgwire.WireSnapshot { return pgwire.WireSnapshot{} })); err != nil {
		t.Fatal(err)
	}
	snap, err := c.Collect()
	if err != nil {
		t.Fatal(err)
	}
	count := func(metric string) int {
		n := 0
		for _, p := range snap.Points {
			if p.Component == obs.ComponentWire && p.Metric == metric {
				n++
			}
		}
		return n
	}
	want := map[string]int{
		"client_startups_failed":     int(pgwire.NumReasons),
		"database_startups_failed":   int(pgwire.NumClasses),
		"client_prestartup_requests": int(pgwire.NumRequestKinds) * int(pgwire.NumAnswers),
		"database_tls_answers":       int(pgwire.NumAnswers),
		"client_tls_failed":          int(pgwire.NumHandshakeOutcomes),
		"database_tls_failed":        int(pgwire.NumHandshakeOutcomes),
		"client_violations":          int(pgwire.NumViolations),
		"database_auth_failures":     int(pgwire.NumAuthCauses),
		"database_ready":             3,
	}
	for m, n := range want {
		if got := count(m); got != n {
			t.Errorf("wire.%s: %d rows, want %d", m, got, n)
		}
	}
	if got := snap.Value(obs.ComponentWire, "client_prestartup_requests", obs.Labels{Mode: "tls", Outcome: "S"}); got != 0 {
		t.Fatalf("tls S = %d", got)
	}
}
