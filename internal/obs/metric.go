package obs

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

type Kind uint8

const (
	KindCounter Kind = iota + 1
	KindGauge
	KindHistogram
)

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

type MergeRule uint8

const (
	MergeSum MergeRule = iota
	MergeMax
)

func (m MergeRule) String() string {
	if m == MergeMax {
		return "max"
	}
	return "sum"
}

type Unit string

const (
	UnitNone  Unit = ""
	UnitNs    Unit = "ns"
	UnitBytes Unit = "bytes"
	UnitCount Unit = "count"
)

type LabelSet uint8

const (
	LabelTenant LabelSet = 1 << iota
	LabelOutcome
	LabelReason
	LabelMode
)

func (s LabelSet) Has(l LabelSet) bool { return s&l != 0 }

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

type Labels struct {
	Tenant  string
	Outcome string
	Reason  string
	Mode    string
}

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

func Components() []string {
	return []string{ComponentPool, ComponentAdmission, ComponentCoalescer, ComponentRetry, ComponentWire,
		ComponentClassifier, ComponentEngine, ComponentObs, ComponentFakepg, ComponentWorkload, ComponentLoadgen,
		ComponentSimnet, ComponentSim}
}

func KnownComponent(c string) bool { return slices.Contains(Components(), c) }

const (
	MaxNameLen = 64
	MaxHelpLen = 200
)

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

func CounterDesc(name string, labels LabelSet, help string) MetricDesc {
	return MetricDesc{Name: name, Kind: KindCounter, Labels: labels, Unit: UnitCount, Help: help}
}

func GaugeDesc(name string, labels LabelSet, help string) MetricDesc {
	return MetricDesc{Name: name, Kind: KindGauge, Labels: labels, Unit: UnitCount, Help: help}
}

func HighWaterDesc(name string, labels LabelSet, help string) MetricDesc {
	return MetricDesc{Name: name, Kind: KindGauge, Labels: labels, Unit: UnitCount, Merge: MergeMax, Help: help}
}

func HistogramDesc(name string, labels LabelSet, unit Unit, help string) MetricDesc {
	return MetricDesc{Name: name, Kind: KindHistogram, Labels: labels, Unit: unit, Help: help}
}

func (d MetricDesc) WithUnit(u Unit) MetricDesc {
	d.Unit = u
	return d
}

func (d MetricDesc) WithOpenLabels() MetricDesc {
	d.OpenLabels = true
	return d
}

func (d MetricDesc) AsHeadline() MetricDesc {
	d.Headline = true
	return d
}

const (
	ClampedLowSuffix  = "_clamped_low"
	ClampedHighSuffix = "_clamped_high"
)

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

type Registry struct {
	descs []MetricDesc
}

func compareDesc(a, b MetricDesc) int {
	if c := strings.Compare(a.Component, b.Component); c != 0 {
		return c
	}
	return strings.Compare(a.Name, b.Name)
}

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

func (r *Registry) Lookup(component, name string) (MetricDesc, bool) {
	i, found := slices.BinarySearchFunc(r.descs, MetricDesc{Component: component, Name: name}, compareDesc)
	if !found {
		return MetricDesc{}, false
	}
	return r.descs[i], true
}

func (r *Registry) All() []MetricDesc { return slices.Clone(r.descs) }

func (r *Registry) Len() int { return len(r.descs) }
