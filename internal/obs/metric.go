package obs

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Kind identifies whether a declared metric is a monotonic counter, an
// instantaneous gauge, or a bucketed histogram.
type Kind uint8

const (
	// KindCounter is a cumulative non-negative integer that never decreases.
	KindCounter Kind = iota + 1
	// KindGauge is an integer level sampled at the instant a snapshot is built.
	KindGauge
	// KindHistogram is a fixed-size integer distribution merged bucket by bucket.
	KindHistogram
)

// String returns the canonical lowercase name used in CSV and Prometheus output.
func (k Kind) String() string {
	switch k {
	case KindCounter:
		return "counter"
	case KindGauge:
		return "gauge"
	case KindHistogram:
		return "histogram"
	}
	return "unknown"
}

// MergeRule states how values of one metric combine across proxy instances or
// when per-tenant points are folded with obs.detail off.
type MergeRule uint8

const (
	// MergeSum adds values across instances (used for counters and additive gauges).
	MergeSum MergeRule = iota
	// MergeMax keeps the largest value across instances (used for high-water marks).
	MergeMax
)

// String returns "max" for MergeMax and "sum" for MergeSum.
func (m MergeRule) String() string {
	if m == MergeMax {
		return "max"
	}
	return "sum"
}

// Unit records the measurement unit of a metric or histogram.
type Unit string

const (
	// UnitNone marks a dimensionless value.
	UnitNone Unit = ""
	// UnitNs marks durations in nanoseconds.
	UnitNs Unit = "ns"
	// UnitBytes marks sizes in bytes.
	UnitBytes Unit = "bytes"
	// UnitCount marks discrete counts.
	UnitCount Unit = "count"
)

// LabelSet is a bitmask of the fixed label dimensions permitted on a metric.
type LabelSet uint8

const (
	// LabelTenant identifies the client tenant (folded into "other" in Prometheus).
	LabelTenant LabelSet = 1 << iota
	// LabelOutcome identifies a request or operation outcome.
	LabelOutcome
	// LabelReason identifies a refusal, close, error, or fault reason.
	LabelReason
	// LabelMode identifies an operating mode, direction, or statement kind.
	LabelMode
)

// Has reports whether s includes every bit set in l.
func (s LabelSet) Has(l LabelSet) bool { return s&l != 0 }

// Names returns the label column names present in s in schema order.
func (s LabelSet) Names() []string {
	var out []string
	for _, l := range labelOrder {
		if s.Has(l.set) {
			out = append(out, l.name)
		}
	}
	return out
}

var labelOrder = [...]struct {
	set  LabelSet
	name string
}{{LabelTenant, "tenant"}, {LabelOutcome, "outcome"}, {LabelReason, "reason"}, {LabelMode, "mode"}}

// Labels holds the four fixed label values of a point or histogram.
// Unused labels are empty strings.
type Labels struct {
	Tenant  string
	Outcome string
	Reason  string
	Mode    string
}

// Set returns the bitmask of non-empty fields in l.
func (l Labels) Set() LabelSet {
	var s LabelSet
	if l.Tenant != "" {
		s |= LabelTenant
	}
	if l.Outcome != "" {
		s |= LabelOutcome
	}
	if l.Reason != "" {
		s |= LabelReason
	}
	if l.Mode != "" {
		s |= LabelMode
	}
	return s
}

// CompareLabels orders two label tuples lexicographically by tenant, outcome,
// reason, and mode.
func CompareLabels(a, b Labels) int {
	if c := strings.Compare(a.Tenant, b.Tenant); c != 0 {
		return c
	}
	if c := strings.Compare(a.Outcome, b.Outcome); c != 0 {
		return c
	}
	if c := strings.Compare(a.Reason, b.Reason); c != 0 {
		return c
	}
	return strings.Compare(a.Mode, b.Mode)
}

// Fixed component names shared by the registry, CSV schema, and exporters.
const (
	ComponentPool       = "pool"
	ComponentAdmission  = "admission"
	ComponentCoalescer  = "coalescer"
	ComponentRetry      = "retry"
	ComponentWire       = "wire"
	ComponentClassifier = "classifier"
	ComponentEngine     = "engine"
	ComponentObs        = "obs"
	ComponentFakepg     = "fakepg"
	ComponentWorkload   = "workload"
	ComponentLoadgen    = "loadgen"
	ComponentSimnet     = "simnet"
	ComponentSim        = "sim"
)

// Components returns the complete, fixed component list in canonical rank order.
func Components() []string {
	return []string{ComponentPool, ComponentAdmission, ComponentCoalescer, ComponentRetry, ComponentWire,
		ComponentClassifier, ComponentEngine, ComponentObs, ComponentFakepg, ComponentWorkload, ComponentLoadgen,
		ComponentSimnet, ComponentSim}
}

// KnownComponent reports whether c is one of the fixed component names.
func KnownComponent(c string) bool { return slices.Contains(Components(), c) }

const (
	// MaxNameLen caps metric and adapter names in bytes.
	MaxNameLen = 64
	// MaxHelpLen caps single-line help text in bytes.
	MaxHelpLen = 200
)

// MetricDesc declares one metric's schema, merge behaviour, and documentation.
// Every point or histogram emitted by a source must match a registered MetricDesc.
type MetricDesc struct {
	Component  string
	Name       string
	Kind       Kind
	Labels     LabelSet
	Unit       Unit
	Merge      MergeRule
	Headline   bool
	OpenLabels bool
	Help       string
}

// CounterDesc returns a counter declaration in UnitCount that merges by sum.
func CounterDesc(name string, labels LabelSet, help string) MetricDesc {
	return MetricDesc{Name: name, Kind: KindCounter, Labels: labels, Unit: UnitCount, Help: help}
}

// GaugeDesc returns an additive gauge declaration in UnitCount that merges by sum.
func GaugeDesc(name string, labels LabelSet, help string) MetricDesc {
	return MetricDesc{Name: name, Kind: KindGauge, Labels: labels, Unit: UnitCount, Help: help}
}

// HighWaterDesc returns a peak gauge declaration that merges by max across instances.
func HighWaterDesc(name string, labels LabelSet, help string) MetricDesc {
	return MetricDesc{Name: name, Kind: KindGauge, Labels: labels, Unit: UnitCount, Merge: MergeMax, Help: help}
}

// HistogramDesc returns a histogram declaration with the required measurement unit.
func HistogramDesc(name string, labels LabelSet, unit Unit, help string) MetricDesc {
	return MetricDesc{Name: name, Kind: KindHistogram, Labels: labels, Unit: unit, Help: help}
}

// WithUnit returns a copy of d with its measurement unit set to u.
func (d MetricDesc) WithUnit(u Unit) MetricDesc {
	d.Unit = u
	return d
}

// WithOpenLabels marks a non-tenant labelled metric whose label values are
// open-ended (such as adapter names), exempting it from requiring at least one
// emitted row when no values exist yet.
func (d MetricDesc) WithOpenLabels() MetricDesc {
	d.OpenLabels = true
	return d
}

// AsHeadline marks an untenanted counter or gauge for inclusion in the
// structured one-line publish log.
func (d MetricDesc) AsHeadline() MetricDesc {
	d.Headline = true
	return d
}

const (
	// ClampedLowSuffix is appended to a histogram name for its auto-declared
	// counter of values clamped below the lowest bucket.
	ClampedLowSuffix = "_clamped_low"
	// ClampedHighSuffix is appended to a histogram name for its auto-declared
	// counter of values clamped above the highest bucket.
	ClampedHighSuffix = "_clamped_high"
)

// expand turns each histogram declaration into three declarations—the histogram
// plus its low/high clamp counters—so out-of-range counts appear in metrics.csv
// and pass registry validation automatically.
func (d MetricDesc) expand() []MetricDesc {
	if d.Kind != KindHistogram {
		return []MetricDesc{d}
	}
	lo := MetricDesc{Component: d.Component, Name: d.Name + ClampedLowSuffix, Kind: KindCounter, Labels: d.Labels,
		Unit: UnitCount, Help: "Values recorded into " + d.Name + " below its range, clamped to the lowest bucket."}
	hi := MetricDesc{Component: d.Component, Name: d.Name + ClampedHighSuffix, Kind: KindCounter, Labels: d.Labels,
		Unit: UnitCount, Help: "Values recorded into " + d.Name + " above its range, clamped to the highest bucket."}
	return []MetricDesc{d, lo, hi}
}

func validName(s string) bool {
	if s == "" || len(s) > MaxNameLen || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

// ErrBadDesc is returned when a metric declaration violates naming, unit,
// merge, label, or Prometheus exposition uniqueness rules.
var ErrBadDesc = errors.New("obs: bad metric declaration")

func (d MetricDesc) validate() error {
	switch {
	case !KnownComponent(d.Component):
		return fmt.Errorf("%w: unknown component %q", ErrBadDesc, d.Component)
	case !validName(d.Name):
		return fmt.Errorf("%w: %s: name %q is not [a-z][a-z0-9_]* of at most %d bytes", ErrBadDesc, d.Component, d.Name, MaxNameLen)
	case d.Kind < KindCounter || d.Kind > KindHistogram:
		return fmt.Errorf("%w: %s.%s: no kind", ErrBadDesc, d.Component, d.Name)
	case d.Help == "" || len(d.Help) > MaxHelpLen || strings.ContainsAny(d.Help, "\n\r\\|"):
		return fmt.Errorf("%w: %s.%s: help must be one plain line of 1 to %d bytes without | or \\", ErrBadDesc, d.Component, d.Name, MaxHelpLen)
	case d.Kind == KindHistogram && d.Unit != UnitNs && d.Unit != UnitBytes && d.Unit != UnitCount:
		return fmt.Errorf("%w: %s.%s: a histogram needs unit ns, bytes or count", ErrBadDesc, d.Component, d.Name)
	case d.Kind == KindCounter && d.Merge != MergeSum:
		return fmt.Errorf("%w: %s.%s: a counter merges by sum", ErrBadDesc, d.Component, d.Name)
	case d.Kind == KindHistogram && d.Merge != MergeSum:
		return fmt.Errorf("%w: %s.%s: a histogram merges by bucket", ErrBadDesc, d.Component, d.Name)
	case d.OpenLabels && (d.Labels == 0 || d.Labels.Has(LabelTenant)):
		return fmt.Errorf("%w: %s.%s: open label values need a label other than tenant", ErrBadDesc, d.Component, d.Name)
	case d.Headline && (d.Kind == KindHistogram || d.Labels.Has(LabelTenant)):
		return fmt.Errorf("%w: %s.%s: a headline metric is a counter or gauge without a tenant label", ErrBadDesc, d.Component, d.Name)
	}
	return nil
}

// Registry holds the sorted set of declared metrics for a collector or catalog.
// Keeping declarations sorted in a slice avoids map iteration order everywhere.
type Registry struct {
	descs []MetricDesc
}

func compareDesc(a, b MetricDesc) int {
	if c := strings.Compare(a.Component, b.Component); c != 0 {
		return c
	}
	return strings.Compare(a.Name, b.Name)
}

// Declare validates and adds descs under component, expanding histograms to
// include their clamp counters and rejecting duplicates or Prometheus series
// name clashes within the same component.
func (r *Registry) Declare(component string, descs ...MetricDesc) error {
	for _, d := range descs {
		d.Component = component
		for _, x := range d.expand() {
			if err := x.validate(); err != nil {
				return err
			}
			i, found := slices.BinarySearchFunc(r.descs, x, compareDesc)
			if found {
				return fmt.Errorf("%w: %s.%s declared twice", ErrBadDesc, x.Component, x.Name)
			}
			if y, clash := r.exposureClash(x); clash {
				return fmt.Errorf("%w: %s.%s and %s.%s would export the same series name", ErrBadDesc, x.Component, x.Name, y.Component, y.Name)
			}
			r.descs = slices.Insert(r.descs, i, x)
		}
	}
	return nil
}

// exposedNames lists every Prometheus series name suffix that promtext can
// synthesize for d so Declare can refuse collisions before export.
func exposedNames(d MetricDesc) []string {
	switch d.Kind {
	case KindCounter:
		return []string{d.Name + "_total"}
	case KindHistogram:
		return []string{d.Name, d.Name + "_bucket", d.Name + "_count", d.Name + "_sum"}
	}
	return []string{d.Name}
}

func (r *Registry) exposureClash(d MetricDesc) (MetricDesc, bool) {
	mine := exposedNames(d)
	for _, y := range r.descs {
		if y.Component != d.Component {
			continue
		}
		for _, n := range exposedNames(y) {
			if slices.Contains(mine, n) {
				return y, true
			}
		}
	}
	return MetricDesc{}, false
}

// Lookup returns the declaration for (component, name), if registered.
func (r *Registry) Lookup(component, name string) (MetricDesc, bool) {
	i, found := slices.BinarySearchFunc(r.descs, MetricDesc{Component: component, Name: name}, compareDesc)
	if !found {
		return MetricDesc{}, false
	}
	return r.descs[i], true
}

// All returns a sorted copy of every declaration in r.
func (r *Registry) All() []MetricDesc { return slices.Clone(r.descs) }

// Len returns the number of declared metrics in r, including expanded clamp counters.
func (r *Registry) Len() int { return len(r.descs) }
