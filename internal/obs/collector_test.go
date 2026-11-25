package obs

import (
	"encoding/json"
	"errors"
	"math/rand/v2"
	"strings"
	"testing"
)

type fakeAdapter struct {
	name string
	err  error
	got  []InstanceSnapshot
}

func (f *fakeAdapter) Name() string { return f.name }

func (f *fakeAdapter) Export(s InstanceSnapshot) error {
	f.got = append(f.got, s.Clone())
	return f.err
}

func TestUndeclaredMetricIsAnError(t *testing.T) {
	c := NewCollector(Config{Instance: "i"})
	err := c.Register(Source{Component: ComponentPool, Metrics: []MetricDesc{CounterDesc("a", 0, "A.")},
		Collect: func(e *Emitter) {
			e.Counter("a", Labels{}, 1)
			e.Counter("b", Labels{}, 1)
		}})
	if err != nil {
		t.Fatal(err)
	}
	snap, err := c.Publish()
	if !errors.Is(err, ErrUndeclared) {
		t.Fatalf("want ErrUndeclared, got %v", err)
	}
	if _, ok := snap.Point(ComponentPool, "b", Labels{}); ok {
		t.Fatal("undeclared point exported")
	}
	if snap.Value(ComponentPool, "a", Labels{}) != 1 {
		t.Fatal("declared point lost")
	}
	next, _ := c.Publish()
	if next.Value(ComponentObs, MetricEmitErrors, Labels{}) != 1 {
		t.Fatalf("emit errors not counted: %+v", next.Points)
	}
}

func TestEmitterRefusesWrongKindLabelDuplicateNegativeAndMissing(t *testing.T) {
	descs := []MetricDesc{CounterDesc("c", LabelReason, "C."), GaugeDesc("g", 0, "G."), CounterDesc("must", 0, "M.")}
	cases := []struct {
		name string
		emit func(e *Emitter)
		want error
	}{
		{"wrong kind", func(e *Emitter) { e.Gauge("c", Labels{}, 1); e.Counter("must", Labels{}, 0); e.Gauge("g", Labels{}, 0) }, ErrWrongKind},
		{"label", func(e *Emitter) {
			e.Counter("c", Labels{Tenant: "x"}, 1)
			e.Counter("must", Labels{}, 0)
			e.Gauge("g", Labels{}, 0)
		}, ErrBadLabel},
		{"duplicate", func(e *Emitter) {
			e.Counter("c", Labels{Reason: "r"}, 1)
			e.Counter("c", Labels{Reason: "r"}, 2)
			e.Counter("must", Labels{}, 0)
			e.Gauge("g", Labels{}, 0)
		}, ErrDuplicate},
		{"negative", func(e *Emitter) { e.Counter("must", Labels{}, -1); e.Gauge("g", Labels{}, 0) }, ErrNegative},
		{"missing", func(e *Emitter) { e.Gauge("g", Labels{}, 0) }, ErrMissing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := NewCollector(Config{Instance: "i", Detail: true})
			if err := c.Register(Source{Component: ComponentPool, Metrics: descs, Collect: tc.emit}); err != nil {
				t.Fatal(err)
			}
			if _, err := c.Collect(); !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

func TestRegistrationRules(t *testing.T) {
	c := NewCollector(Config{})
	ok := Source{Component: ComponentPool, Metrics: []MetricDesc{CounterDesc("a", 0, "A.")}, Collect: func(*Emitter) {}}
	if err := c.Register(ok); err != nil {
		t.Fatal(err)
	}
	bad := []Source{
		ok,
		{Component: "nope", Collect: func(*Emitter) {}},
		{Component: ComponentRetry},
		{Component: ComponentRetry, Metrics: []MetricDesc{CounterDesc("Bad-Name", 0, "x")}, Collect: func(*Emitter) {}},
		{Component: ComponentRetry, Metrics: []MetricDesc{CounterDesc("a", 0, "")}, Collect: func(*Emitter) {}},
		{Component: ComponentRetry, Metrics: []MetricDesc{CounterDesc("a", 0, "two\nlines")}, Collect: func(*Emitter) {}},
		{Component: ComponentRetry, Metrics: []MetricDesc{CounterDesc("a", 0, "x"), GaugeDesc("a", 0, "y")}, Collect: func(*Emitter) {}},
		{Component: ComponentRetry, Metrics: []MetricDesc{HistogramDesc("h", 0, UnitNone, "x")}, Collect: func(*Emitter) {}},
		{Component: ComponentRetry, Metrics: []MetricDesc{CounterDesc("h", LabelTenant, "x").AsHeadline()}, Collect: func(*Emitter) {}},
		{Component: ComponentRetry, Metrics: []MetricDesc{{Name: "m", Kind: KindCounter, Merge: MergeMax, Help: "x"}}, Collect: func(*Emitter) {}},
	}
	for i, s := range bad {
		if err := c.Register(s); err == nil {
			t.Fatalf("case %d registered", i)
		}
	}
	if _, ok := c.Registry().Lookup(ComponentRetry, "a"); ok {
		t.Fatal("a refused source left a declaration behind")
	}
	if err := c.Attach(&fakeAdapter{name: "Bad Name"}); err == nil {
		t.Fatal("bad adapter name accepted")
	}
	if err := c.Attach(&fakeAdapter{name: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Attach(&fakeAdapter{name: "x"}); err == nil {
		t.Fatal("duplicate adapter accepted")
	}
}

func TestPublishSequenceLastCopyAndAdapterErrors(t *testing.T) {
	s := newSynth()
	c := synthCollector(t, "i1", s, true)
	if _, err := c.Last(); !errors.Is(err, ErrNotPublished) {
		t.Fatal("Last before publish")
	}
	good := &fakeAdapter{name: "good"}
	bad := &fakeAdapter{name: "bad", err: errors.New("disk on fire")}
	if err := c.Attach(bad); err != nil {
		t.Fatal(err)
	}
	if err := c.Attach(good); err != nil {
		t.Fatal(err)
	}
	s.apply(synthEvent{tenant: "a", outcome: "ok", value: 5000})
	for i := 0; i < 3; i++ {
		snap, err := c.Publish()
		if err != nil {
			t.Fatalf("an adapter error propagated: %v", err)
		}
		if snap.Seq != int64(i) {
			t.Fatalf("seq %d want %d", snap.Seq, i)
		}
		if got := snap.Value(ComponentObs, MetricExportErrors, Labels{Reason: "bad"}); got != int64(i) {
			t.Fatalf("export errors %d want %d", got, i)
		}
		if got := snap.Value(ComponentObs, MetricPublishes, Labels{}); got != int64(i)+1 {
			t.Fatalf("publishes %d", got)
		}
	}
	if len(good.got) != 3 || len(bad.got) != 3 {
		t.Fatal("adapters not called on every publish")
	}
	last, err := c.Last()
	if err != nil {
		t.Fatal(err)
	}
	last.Points[0].Value = -99
	last.Hists[0].Buckets[0].Count = -99
	again, _ := c.Last()
	if !Equal(again, good.got[2]) {
		t.Fatal("mutating a Last copy changed the published snapshot")
	}
	s.apply(synthEvent{tenant: "b", outcome: "ok", value: 5000})
	again2, _ := c.Last()
	if !Equal(again2, good.got[2]) {
		t.Fatal("Last touched the component between publishes")
	}
	h := c.Health(healthTable(t))
	if h.State != StateDegraded || !strings.Contains(h.Checks[0].Reason, "bad") {
		t.Fatalf("health %+v", h)
	}
}

func healthTable(t *testing.T) *HealthTable {
	var ht HealthTable
	if err := ht.Add(CollectorRule()); err != nil {
		t.Fatal(err)
	}
	return &ht
}

func TestSnapshotIsSortedAndHasNoTime(t *testing.T) {
	s := newSynth()
	r := rand.New(rand.NewPCG(1, 2))
	for _, ev := range randomEvents(r, 500, 7) {
		s.apply(ev)
	}
	a, err := synthCollector(t, "i", s, true).Collect()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := synthCollector(t, "i", s, true).Collect()
	if !Equal(a, b) {
		t.Fatal("two collects of one state differ")
	}
	for i := 1; i < len(a.Points); i++ {
		if ComparePoint(a.Points[i-1], a.Points[i]) >= 0 {
			t.Fatalf("points not sorted at %d", i)
		}
	}
	for i := 1; i < len(a.Hists); i++ {
		if CompareHist(a.Hists[i-1], a.Hists[i]) >= 0 {
			t.Fatalf("histograms not sorted at %d", i)
		}
	}
	js, _ := json.Marshal(a)
	if strings.Contains(strings.ToLower(string(js)), "time") {
		t.Fatalf("snapshot mentions time: %s", js[:200])
	}
}

func TestDetailOffFoldsTenantsAndHoldsNoHistogram(t *testing.T) {
	s := newSynth()
	r := rand.New(rand.NewPCG(3, 4))
	for _, ev := range randomEvents(r, 300, 5) {
		s.apply(ev)
	}
	on, err := synthCollector(t, "i", s, true).Collect()
	if err != nil {
		t.Fatal(err)
	}
	off, err := synthCollector(t, "i", s, false).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(off.Hists) != 0 {
		t.Fatal("histograms exported with detail off")
	}
	if len(off.Tenants()) != 0 {
		t.Fatalf("tenants with detail off: %v", off.Tenants())
	}
	for _, o := range synthOutcomes {
		want := int64(0)
		for _, p := range on.Points {
			if p.Metric == "requests" && p.Labels.Outcome == o {
				want += p.Value
			}
		}
		if got := off.Value(ComponentPool, "requests", Labels{Outcome: o}); got != want {
			t.Fatalf("folded %s = %d want %d", o, got, want)
		}
	}
	dh := NewDetailDurationHistogram(false)
	dh.Record(5)
	if dh.On() || dh.HeldBytes() != 0 || dh.Snapshot().Count != 0 {
		t.Fatal("detail-off histogram holds memory")
	}
	dh = NewDetailDurationHistogram(true)
	dh.Record(5000)
	if !dh.On() || dh.HeldBytes() == 0 || dh.Snapshot().Count != 1 {
		t.Fatal("detail-on histogram does not record")
	}
}

func TestHealthTableWorstWinsAndJSON(t *testing.T) {
	var ht HealthTable
	add := func(name string, st State, reason string) {
		if err := ht.Add(Rule{Name: name, Check: func(InstanceSnapshot) (State, string) { return st, reason }}); err != nil {
			t.Fatal(err)
		}
	}
	add("alpha", StateOK, "fine")
	add("beta", StateDegraded, "shedding \"hard\"\n<now>")
	if err := ht.Add(Rule{Name: "alpha", Check: func(InstanceSnapshot) (State, string) { return StateOK, "" }}); err == nil {
		t.Fatal("duplicate rule accepted")
	}
	h := ht.Evaluate(InstanceSnapshot{})
	if h.State != StateDegraded {
		t.Fatalf("state %v", h.State)
	}
	add("gamma", StateFailed, "cannot listen")
	h = ht.Evaluate(InstanceSnapshot{})
	if h.State != StateFailed {
		t.Fatalf("state %v", h.State)
	}
	var back struct {
		State  string
		Checks []struct{ Name, State, Reason string }
	}
	if err := json.Unmarshal(h.AppendJSON(nil), &back); err != nil {
		t.Fatalf("%v: %s", err, h.AppendJSON(nil))
	}
	if back.State != "failed" || len(back.Checks) != 3 || back.Checks[1].Reason != "shedding \"hard\"\n<now>" || back.Checks[2].Name != "gamma" {
		t.Fatalf("%+v", back)
	}
}

func TestCollectorSelfCheck(t *testing.T) {
	c := NewCollector(Config{Instance: "i"})
	ht := healthTable(t)
	if h := c.Health(ht); h.State != StateDegraded {
		t.Fatalf("before publish %+v", h)
	}
	if _, err := c.Publish(); err != nil {
		t.Fatal(err)
	}
	if h := c.Health(ht); h.State != StateOK || h.Checks[0].Name != CheckCollector {
		t.Fatalf("after publish %+v", h)
	}
}

func TestAppendJSONStringMatchesEncodingJSON(t *testing.T) {
	r := rand.New(rand.NewPCG(9, 9))
	inputs := []string{"", "plain", "q\"b\\s", "\x00\x01\x1f\t\r\n", "<a>&", "  ", "héllo wörld", "\xff\xfe"}
	for i := 0; i < 2000; i++ {
		b := make([]byte, r.IntN(12))
		for j := range b {
			b[j] = byte(r.IntN(256))
		}
		inputs = append(inputs, string(b))
	}
	for _, in := range inputs {
		want, _ := json.Marshal(in)
		if got := AppendJSONString(nil, in); string(got) != string(want) {
			t.Fatalf("%q: got %s want %s", in, got, want)
		}
	}
}
