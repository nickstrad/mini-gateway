package enginesrc_test

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/enginesrc"
	"github.com/nickstrad/mini-gateway/internal/obs/obstest"
	"github.com/nickstrad/mini-gateway/internal/proxy"
)

type filler struct {
	n     int64
	paths map[int64]string
}

func (f *filler) fill(v reflect.Value, path string) {
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		f.n++
		v.SetInt(f.n * 1009)
		f.paths[f.n*1009] = path
	case reflect.Bool:
		v.SetBool(true)
	case reflect.String:
		f.n++
		v.SetString("s" + strconv.FormatInt(f.n, 10))
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			f.fill(v.Field(i), path+"."+v.Type().Field(i).Name)
		}
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			f.fill(v.Index(i), path+"["+strconv.Itoa(i)+"]")
		}
	case reflect.Slice:
		s := reflect.MakeSlice(v.Type(), 2, 2)
		for i := 0; i < 2; i++ {
			f.fill(s.Index(i), path+"["+strconv.Itoa(i)+"]")
		}
		v.Set(s)
	}
}

var unexported = []string{".Lo", ".Hi", ".Count", ".Min", ".Max", "ClampedLow", "ClampedHigh", ".Buckets",
	"ByRow[19]", "ClientCloses[0]", "DBCloses[0]", "DBConnectFails[0]", "Engine.Draining", "Engine.DBDown"}

// TestMappingMovesEveryMetricAndExportsEveryField proves every field of proxy.Snapshot and proxy.ShellSnapshot maps to an exported metric that moves.
func TestMappingMovesEveryMetricAndExportsEveryField(t *testing.T) {
	var v enginesrc.Snapshot
	f := &filler{paths: map[int64]string{}}
	f.fill(reflect.ValueOf(&v).Elem(), "")
	for i := range v.Engine.ClientCloses {
		if !proxy.CloseReason(i).ClientSide() {
			delete(f.paths, v.Engine.ClientCloses[i])
		}
	}
	for i := range v.Engine.DBCloses {
		if proxy.CloseReason(i).ClientSide() {
			delete(f.paths, v.Engine.DBCloses[i])
		}
	}
	c := obs.NewCollector(obs.Config{Instance: "i", Detail: true})
	if err := c.Register(obs.NewSource(obs.ComponentEngine, enginesrc.AllMetrics(), func() enginesrc.Snapshot { return v }, enginesrc.MapAll)); err != nil {
		t.Fatal(err)
	}
	s, err := c.Collect()
	if err != nil {
		t.Fatal(err)
	}
	values := map[int64]bool{}
	moved := map[string]bool{}
	for _, p := range s.Points {
		if p.Component != obs.ComponentEngine {
			continue
		}
		values[p.Value] = true
		moved[p.Metric] = moved[p.Metric] || p.Value != 0
	}
	for _, h := range s.Hists {
		moved[h.Metric] = moved[h.Metric] || h.Count() > 0
	}
	for _, d := range enginesrc.AllMetrics() {
		if !moved[d.Name] {
			t.Errorf("engine.%s never moves with every snapshot field non-zero", d.Name)
		}
	}
	var lost []string
	for val, path := range f.paths {
		if !values[val] && !slices.ContainsFunc(unexported, func(s string) bool { return strings.Contains(path, s) }) {
			lost = append(lost, path)
		}
	}
	slices.Sort(lost)
	if len(lost) > 0 {
		t.Errorf("snapshot fields not exported: %v", lost)
	}
}

// TestZeroSnapshotEmitsEveryMetricAndPlainStructs proves zero snapshots emit all declared metrics and contain only plain value types.
func TestZeroSnapshotEmitsEveryMetricAndPlainStructs(t *testing.T) {
	c := obs.NewCollector(obs.Config{Instance: "i", Detail: true})
	if err := c.Register(enginesrc.Source(func() proxy.Snapshot { return proxy.Snapshot{} }, nil)); err != nil {
		t.Fatal(err)
	}
	s, err := c.Collect()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, p := range s.Points {
		seen[p.Metric] = true
	}
	for _, h := range s.Hists {
		seen[h.Metric] = true
	}
	for _, d := range enginesrc.AllMetrics() {
		if !seen[d.Name] {
			t.Errorf("engine.%s has no point in a zero snapshot", d.Name)
		}
	}
	obstest.RequirePlain(t, proxy.Snapshot{})
	obstest.RequirePlain(t, proxy.ShellSnapshot{})
}

// TestDatabaseHealthRuleIsDegradedNotFailed proves database unreachability reports StateDegraded rather than StateFailed.
func TestDatabaseHealthRuleIsDegradedNotFailed(t *testing.T) {
	for _, down := range []bool{false, true} {
		c := obs.NewCollector(obs.Config{Instance: "i"})
		if err := c.Register(enginesrc.Source(func() proxy.Snapshot { return proxy.Snapshot{DBDown: down} }, nil)); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Publish(); err != nil {
			t.Fatal(err)
		}
		var tbl obs.HealthTable
		for _, r := range enginesrc.HealthRules() {
			if err := tbl.Add(r); err != nil {
				t.Fatal(err)
			}
		}
		h := c.Health(&tbl)
		want := obs.StateOK
		if down {
			want = obs.StateDegraded
		}
		if h.State != want {
			t.Fatalf("database down %v: health %s, want %s", down, h.State, want)
		}
	}
}
