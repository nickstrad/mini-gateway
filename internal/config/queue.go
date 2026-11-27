package config

import (
	"errors"

	"github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/proxy"
)

const (
	// DefaultQueueRule is the admission shedding rule selected when queue.rule is omitted.
	DefaultQueueRule = "timeout"
	// DefaultQueueLIFO is the adaptive LIFO setting selected when queue.lifo is omitted.
	DefaultQueueLIFO = "adaptive"
)

func (f *File) applyQueueDefaults(p proxy.Config) {
	q := &f.Queue
	setString(&q.Rule, DefaultQueueRule)
	setString(&q.LIFO, DefaultQueueLIFO)
	d := admission.Config{Capacity: p.QueueCap, TenantCapacity: q.TenantCapacity, MaxTenants: q.MaxTenants,
		Target: int64(q.Target), Interval: int64(q.Interval), LIFOHold: int64(q.LIFOHold)}.WithDefaults()
	if q.Target == 0 {
		q.Target = Duration(d.Target)
	}
	if q.Interval == 0 {
		q.Interval = Duration(d.Interval)
	}
	if q.LIFOHold == 0 {
		q.LIFOHold = Duration(d.LIFOHold)
	}
	if q.TenantCapacity == 0 && q.Capacity > 0 {
		q.TenantCapacity = d.TenantCapacity
	}
	if q.MaxTenants == 0 {
		q.MaxTenants = d.MaxTenants
	}
}

func (f *File) admission() admission.Config {
	rule, _ := admission.ParseProductRule(f.Queue.Rule)
	lifo, _ := admission.ParseLIFO(f.Queue.LIFO)
	if rule == 0 {
		rule = admission.RuleTimeout
	}
	if lifo == 0 {
		lifo = admission.LIFOAdaptive
	}
	return admission.Config{Rule: rule, Target: int64(f.Queue.Target), Interval: int64(f.Queue.Interval),
		TenantCapacity: f.Queue.TenantCapacity, MaxTenants: f.Queue.MaxTenants, LIFO: lifo,
		LIFOHold: int64(f.Queue.LIFOHold), Detail: f.Obs.Detail}
}

func (f *File) validateQueue() error {
	var errs []error
	if _, ok := admission.ParseProductRule(f.Queue.Rule); !ok {
		errs = append(errs, bad("queue.rule", "%q: must be timeout or control-law", f.Queue.Rule))
	}
	if _, ok := admission.ParseLIFO(f.Queue.LIFO); !ok {
		errs = append(errs, bad("queue.lifo", "%q: must be adaptive or off", f.Queue.LIFO))
	}
	return errors.Join(errs...)
}
