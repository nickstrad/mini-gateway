package simrun

import (
	"errors"
	"time"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/sim"
)

// CustomInterval is the default virtual-time publish interval used by Custom.
const CustomInterval = time.Second

// Custom runs an arbitrary simulator workload alongside a collector and CSV
// flush writer, publishing at t=0, every opt.Interval (and any opt.FlushAt
// timestamps), and once after run returns.
func Custom(scenario string, seed int64, fingerprint string, opt Options, s *sim.Sim, sources []obs.Source, run func() error) (*Run, error) {
	if opt.Interval <= 0 {
		opt.Interval = CustomInterval
	}
	if opt.Arm == "" {
		opt.Arm = DefaultArm
	}
	if opt.Dir == "" {
		return nil, errors.New("simrun: no CSV directory")
	}
	clock := func() int64 { return int64(s.Now()) }
	r, err := newRun(scenario, seed, fingerprint, opt, clock)
	if err != nil {
		return nil, err
	}
	d := &driver{opt: opt, run: r}
	for _, src := range sources {
		if err := r.Collector.Register(src); err != nil {
			return nil, errors.Join(err, r.CSV.Close())
		}
	}
	publish := func() {
		if _, err := r.Collector.Publish(); err != nil {
			d.errs = append(d.errs, err)
		}
		r.Publishes++
		r.PublishAt = append(r.PublishAt, s.Now())
	}
	var tick func()
	tick = func() {
		publish()
		if r.Publishes < MaxPublishes {
			t := s.After(opt.Interval, tick)
			t.MarkBackground()
		}
	}
	publish()
	t := s.After(opt.Interval, tick)
	t.MarkBackground()
	for _, at := range opt.FlushAt {
		ft := s.At(at, publish)
		ft.MarkBackground()
	}
	rerr := run()
	publish()
	r.Final, _ = r.Collector.Last()
	cerr := r.CSV.Close()
	return r, errors.Join(rerr, errors.Join(d.errs...), cerr)
}
