package clssrc_test

import (
	"reflect"
	"slices"
	"strconv"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/clssrc"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/work"
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

func collect(t *testing.T, s clssrc.Snapshot) obs.InstanceSnapshot {
	t.Helper()
	c := obs.NewCollector(obs.Config{Instance: "i", Detail: true})
	if err := c.Register(clssrc.Source(func() clssrc.Snapshot { return s })); err != nil {
		t.Fatal(err)
	}
	snap, err := c.Collect()
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func TestMappingMovesEveryMetricAndExportsEveryField(t *testing.T) {
	var s clssrc.Snapshot
	f := &filler{paths: map[int64]string{}}
	f.fill(reflect.ValueOf(&s).Elem(), "")
	snap := collect(t, s)
	values := map[int64]bool{}
	moved := map[string]bool{}
	for _, p := range snap.Points {
		if p.Component != obs.ComponentClassifier {
			continue
		}
		values[p.Value] = true
		moved[p.Metric] = moved[p.Metric] || p.Value != 0
	}
	for _, d := range clssrc.Metrics() {
		if !moved[d.Name] {
			t.Errorf("classifier.%s never moves with every snapshot field non-zero", d.Name)
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
	if len(f.paths) < 40 {
		t.Fatalf("the filler reached only %d fields", len(f.paths))
	}
}

func TestZeroSnapshotEmitsEveryLabelValue(t *testing.T) {
	snap := collect(t, clssrc.Snapshot{})
	count := func(metric string) int {
		n := 0
		for _, p := range snap.Points {
			if p.Component == obs.ComponentClassifier && p.Metric == metric {
				n++
			}
		}
		return n
	}
	for m, n := range map[string]int{"statements_by_class": int(work.NumClasses), "rejects": int(work.NumRejectReasons), "replies": int(pgwire.ErrRowCount)} {
		if got := count(m); got != n {
			t.Errorf("classifier.%s: %d rows, want %d", m, got, n)
		}
	}
}

func TestReplyMetricsCoverEveryRow(t *testing.T) {
	var b pgwire.ReplyBuilder
	_, _, _ = b.Build(nil, pgwire.ErrRowShed, 'I')
	snap := collect(t, clssrc.Snapshot{Replies: b.Snapshot()})
	if got := snap.Value(obs.ComponentClassifier, "replies", obs.Labels{Reason: "shed"}); got != 1 {
		t.Fatalf("replies{shed} = %d, want 1", got)
	}
	if got := snap.Value(obs.ComponentClassifier, "replies_built", obs.Labels{}); got != 1 {
		t.Fatalf("replies_built = %d, want 1", got)
	}
}

func TestClassifierCountsReachTheExport(t *testing.T) {
	var c pgwire.Classifier
	for _, q := range []string{"SELECT 1", "SET search_path = x", "SELECT 'open", "/* mg:share */ SELECT now()"} {
		c.Classify("t", []byte(q), false)
	}
	c.Classify("t", []byte("SELECT 1"), true)
	s := c.Snapshot()
	snap := collect(t, clssrc.Snapshot{Classifier: s})
	for _, p := range []struct {
		metric string
		l      obs.Labels
		want   int64
	}{
		{"statements", obs.Labels{}, 5},
		{"statements_by_class", obs.Labels{Outcome: "reject"}, 1},
		{"statements_by_class", obs.Labels{Outcome: "continue-lease"}, 1},
		{"rejects", obs.Labels{Reason: "session-set"}, 1},
		{"opaque", obs.Labels{}, 1},
		{"safe_read", obs.Labels{}, 2},
		{"shareable", obs.Labels{}, 1},
		{"hinted", obs.Labels{}, 1},
		{"share_keys", obs.Labels{}, 2},
	} {
		if got := snap.Value(obs.ComponentClassifier, p.metric, p.l); got != p.want {
			t.Errorf("classifier.%s %+v = %d, want %d", p.metric, p.l, got, p.want)
		}
	}
}
