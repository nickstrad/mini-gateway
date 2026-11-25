package obs

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Sentinel errors returned by registration, collection, and Last reads.
var (
	ErrUndeclared   = errors.New("obs: metric not declared")
	ErrWrongKind    = errors.New("obs: metric emitted with the wrong kind")
	ErrBadLabel     = errors.New("obs: label not declared for the metric")
	ErrDuplicate    = errors.New("obs: point emitted twice")
	ErrMissing      = errors.New("obs: declared metric not emitted")
	ErrNegative     = errors.New("obs: negative counter")
	ErrBadSource    = errors.New("obs: bad source")
	ErrBadAdapter   = errors.New("obs: bad adapter")
	ErrNotPublished = errors.New("obs: nothing published yet")
)

// Emitter receives points from one component mapping during Collector.Collect,
// checking every emission against the registry and enforcing the obs.detail gate.
type Emitter struct {
	reg       *Registry
	component string
	detail    bool
	points    []Point
	hists     []HistPoint
	errs      []error
}

// Detail reports whether obs.detail is enabled so a mapping can skip building
// histogram snapshots when detail is off.
func (e *Emitter) Detail() bool { return e.detail }

func (e *Emitter) fail(err error) {
	// Cap recorded errors per source so a misbehaving mapping cannot allocate without bound.
	if len(e.errs) < maxEmitErrors {
		e.errs = append(e.errs, err)
	}
}

const maxEmitErrors = 16

func (e *Emitter) check(name string, kind Kind, l Labels) (MetricDesc, bool) {
	d, ok := e.reg.Lookup(e.component, name)
	if !ok {
		e.fail(fmt.Errorf("%w: %s.%s", ErrUndeclared, e.component, name))
		return d, false
	}
	if d.Kind != kind {
		e.fail(fmt.Errorf("%w: %s.%s is a %s, emitted as a %s", ErrWrongKind, e.component, name, d.Kind, kind))
		return d, false
	}
	if extra := l.Set() &^ d.Labels; extra != 0 {
		e.fail(fmt.Errorf("%w: %s.%s does not declare %s", ErrBadLabel, e.component, name, strings.Join(extra.Names(), ",")))
		return d, false
	}
	return d, true
}

// Counter emits one non-negative cumulative counter point if it matches the registry.
func (e *Emitter) Counter(name string, l Labels, v int64) {
	if _, ok := e.check(name, KindCounter, l); !ok {
		return
	}
	if v < 0 {
		e.fail(fmt.Errorf("%w: %s.%s = %d", ErrNegative, e.component, name, v))
		return
	}
	e.add(name, KindCounter, l, v)
}

// Gauge emits one instantaneous gauge point if it matches the registry.
func (e *Emitter) Gauge(name string, l Labels, v int64) {
	if _, ok := e.check(name, KindGauge, l); ok {
		e.add(name, KindGauge, l, v)
	}
}

func (e *Emitter) add(name string, k Kind, l Labels, v int64) {
	// When detail is off, clear the tenant label so combinePoints folds tenants together.
	if !e.detail {
		l.Tenant = ""
	}
	e.points = append(e.points, Point{Component: e.component, Metric: name, Kind: k, Labels: l, Value: v})
}

// Histogram emits a histogram and its auto-declared clamp counters when
// obs.detail is on. Declaration checks still run when detail is off so invalid
// calls fail deterministically regardless of configuration.
func (e *Emitter) Histogram(name string, l Labels, h HistogramSnapshot) {
	d, ok := e.check(name, KindHistogram, l)
	if !ok || !e.detail {
		return
	}
	e.hists = append(e.hists, HistPoint{Component: e.component, Metric: name, Labels: l, Unit: d.Unit, Buckets: slices.Clone(h.Buckets)})
	e.add(name+ClampedLowSuffix, KindCounter, l, h.ClampedLow)
	e.add(name+ClampedHighSuffix, KindCounter, l, h.ClampedHigh)
}

func (e *Emitter) finish() {
	slices.SortFunc(e.points, ComparePoint)
	slices.SortFunc(e.hists, CompareHist)
	e.points = e.combinePoints(e.points)
	for i := 1; i < len(e.hists); i++ {
		if CompareHist(e.hists[i-1], e.hists[i]) == 0 {
			e.fail(fmt.Errorf("%w: histogram %s.%s %v", ErrDuplicate, e.component, e.hists[i].Metric, e.hists[i].Labels))
		}
	}
}

func (e *Emitter) combinePoints(ps []Point) []Point {
	out := ps[:0]
	for _, p := range ps {
		n := len(out)
		if n == 0 || ComparePoint(out[n-1], p) != 0 {
			out = append(out, p)
			continue
		}
		// With detail on, identical keys are a mapping bug. With detail off,
		// distinct tenants were collapsed to Tenant="" and must be combined
		// according to the metric's merge rule.
		if e.detail {
			e.fail(fmt.Errorf("%w: %s.%s %v", ErrDuplicate, e.component, p.Metric, p.Labels))
			continue
		}
		d, _ := e.reg.Lookup(e.component, p.Metric)
		out[n-1].Value = combine(d.Merge, out[n-1].Value, p.Value)
	}
	return out
}

func combine(m MergeRule, a, b int64) int64 {
	if m == MergeMax {
		return max(a, b)
	}
	return a + b
}

// Source binds a component name, its metric declarations, and a collection
// callback that maps the component's raw snapshot into an Emitter.
type Source struct {
	Component string
	Metrics   []MetricDesc
	Collect   func(e *Emitter)
}

// NewSource builds a Source that reads one component's plain Snapshot() struct
// and passes it to mapping without the component importing any adapter.
func NewSource[T any](component string, metrics []MetricDesc, snapshot func() T, mapping func(T, *Emitter)) Source {
	return Source{Component: component, Metrics: metrics, Collect: func(e *Emitter) { mapping(snapshot(), e) }}
}

// Adapter is the single interface every output exporter implements. Adapters
// read only the immutable InstanceSnapshot passed to Export and must not
// mutate it or retain references to its backing slices.
type Adapter interface {
	Name() string
	Export(s InstanceSnapshot) error
}

// FlushStats reports CSV flush progress and drop counters from an adapter to
// the collector's self metrics without the collector importing csvflush.
type FlushStats struct {
	Flushes      int64
	FlushErrors  int64
	RowsDropped  int64
	BytesWritten int64
	QueueDepth   int64
}

// FlushReporter is optionally implemented by CSV flush adapters so the
// collector can publish obs.csv_* metrics.
type FlushReporter interface {
	FlushStats() FlushStats
}

// TenantReporter is optionally implemented by bounded-label exporters so the
// collector can publish obs.prom_tenants_tracked.
type TenantReporter interface {
	TenantsTracked() int64
}

// Config sets the static identity fields and obs.detail switch for a Collector.
type Config struct {
	Instance          string
	Build             string
	ConfigFingerprint string
	Detail            bool
}

const (
	// MaxSources bounds the number of component sources registered with one collector.
	MaxSources = 32
	// MaxAdapters bounds the number of output adapters attached to one collector.
	MaxAdapters = 16
)

type adapterSlot struct {
	a      Adapter
	errors int64
	failed bool
}

// Collector walks registered component sources in canonical order, validates
// their metrics against the registry, retains the last published snapshot, and
// fans snapshots out to attached adapters.
//
// Because internal/obs is determinism-linted, Collector holds no locks and must
// be driven from a single owner goroutine (the simulator loop or proxy engine).
type Collector struct {
	cfg        Config
	reg        Registry
	sources    []Source
	adapters   []adapterSlot
	seq        int64
	last       InstanceSnapshot
	published  bool
	emitErrors int64
	lastEmit   error
}

// NewCollector creates a Collector with its built-in "obs" self-metric source
// already registered.
func NewCollector(cfg Config) *Collector {
	c := &Collector{cfg: cfg}
	if err := c.Register(c.selfSource()); err != nil {
		c.lastEmit = err
	}
	return c
}

// Registry returns the collector's metric registry.
func (c *Collector) Registry() *Registry { return &c.reg }

// Config returns the collector's configuration.
func (c *Collector) Config() Config { return c.cfg }

// Register validates s and its declarations and inserts s in fixed component
// rank order. Validation runs on a cloned registry so a refused source leaves
// c.reg unchanged.
func (c *Collector) Register(s Source) error {
	if !KnownComponent(s.Component) || s.Collect == nil {
		return fmt.Errorf("%w: component %q", ErrBadSource, s.Component)
	}
	if len(c.sources) >= MaxSources {
		return fmt.Errorf("%w: more than %d sources", ErrBadSource, MaxSources)
	}
	for _, x := range c.sources {
		if x.Component == s.Component {
			return fmt.Errorf("%w: component %q registered twice", ErrBadSource, s.Component)
		}
	}
	var reg Registry
	reg.descs = slices.Clone(c.reg.descs)
	if err := reg.Declare(s.Component, s.Metrics...); err != nil {
		return err
	}
	c.reg = reg
	i, _ := slices.BinarySearchFunc(c.sources, s, func(a, b Source) int { return componentRank(a.Component) - componentRank(b.Component) })
	c.sources = slices.Insert(c.sources, i, s)
	return nil
}

func componentRank(name string) int { return slices.Index(Components(), name) }

// Attach registers an output adapter under its unique name.
func (c *Collector) Attach(a Adapter) error {
	name := a.Name()
	if !validAdapterName(name) {
		return fmt.Errorf("%w: name %q", ErrBadAdapter, name)
	}
	if len(c.adapters) >= MaxAdapters {
		return fmt.Errorf("%w: more than %d adapters", ErrBadAdapter, MaxAdapters)
	}
	for _, x := range c.adapters {
		if x.a.Name() == name {
			return fmt.Errorf("%w: %q attached twice", ErrBadAdapter, name)
		}
	}
	c.adapters = append(c.adapters, adapterSlot{a: a})
	return nil
}

func validAdapterName(s string) bool {
	if s == "" || len(s) > MaxNameLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-') {
			return false
		}
	}
	return true
}

// Collect invokes every registered source in fixed component order, checks
// every emitted point and required zero-row metric, and returns a sorted
// InstanceSnapshot without advancing the publish sequence or calling adapters.
func (c *Collector) Collect() (InstanceSnapshot, error) {
	snap := InstanceSnapshot{Instance: c.cfg.Instance, Build: c.cfg.Build, ConfigFingerprint: c.cfg.ConfigFingerprint, Seq: c.seq}
	var errs []error
	for _, s := range c.sources {
		e := Emitter{reg: &c.reg, component: s.Component, detail: c.cfg.Detail}
		s.Collect(&e)
		e.finish()
		e.requireUnlabelled(s.Component)
		snap.Points = append(snap.Points, e.points...)
		snap.Hists = append(snap.Hists, e.hists...)
		errs = append(errs, e.errs...)
	}
	snap.Sort()
	return snap, errors.Join(errs...)
}

// requireUnlabelled enforces the schema v1 presence rule: every declared
// counter or gauge without a tenant label (and not marked OpenLabels) must emit
// at least one point on every collect so zero values appear as rows.
func (e *Emitter) requireUnlabelled(component string) {
	for _, d := range e.reg.descs {
		if d.Component != component || d.Labels.Has(LabelTenant) || d.OpenLabels || d.Kind == KindHistogram {
			continue
		}
		if strings.HasSuffix(d.Name, ClampedLowSuffix) || strings.HasSuffix(d.Name, ClampedHighSuffix) {
			continue
		}
		i, _ := slices.BinarySearchFunc(e.points, Point{Component: component, Metric: d.Name}, ComparePoint)
		if i == len(e.points) || e.points[i].Metric != d.Name {
			e.fail(fmt.Errorf("%w: %s.%s has no tenant label and must be emitted at every collect", ErrMissing, component, d.Name))
		}
	}
}

// Publish collects a snapshot, advances the publish sequence, retains the
// published snapshot for Last(), and exports it to every attached adapter.
// Adapter errors are counted in obs.export_errors and marked for health, and
// are never returned; only source emit errors are returned.
func (c *Collector) Publish() (InstanceSnapshot, error) {
	snap, err := c.Collect()
	c.lastEmit = err
	if err != nil {
		c.emitErrors++
	}
	c.seq++
	c.last = snap
	c.published = true
	for i := range c.adapters {
		slot := &c.adapters[i]
		if xerr := slot.a.Export(snap); xerr != nil {
			slot.errors++
			slot.failed = true
		} else {
			slot.failed = false
		}
	}
	return snap.Clone(), err
}

// Last returns a deep copy of the last published snapshot without touching any
// component source, or ErrNotPublished before the first Publish.
func (c *Collector) Last() (InstanceSnapshot, error) {
	if !c.published {
		return InstanceSnapshot{}, ErrNotPublished
	}
	return c.last.Clone(), nil
}

// Seq returns the number of Publish calls completed so far (the next snapshot's Seq).
func (c *Collector) Seq() int64 { return c.seq }

// ExportErrors returns the cumulative failed Export count for the named adapter.
func (c *Collector) ExportErrors(adapter string) int64 {
	for _, s := range c.adapters {
		if s.a.Name() == adapter {
			return s.errors
		}
	}
	return 0
}
