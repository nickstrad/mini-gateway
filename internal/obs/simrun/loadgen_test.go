package simrun_test

import (
	"github.com/nickstrad/mini-gateway/internal/loadgen"
	"github.com/nickstrad/mini-gateway/internal/obs"
)

const loadgenStepNs = 1_000_000

func loadgenRuns() error {
	p := loadgen.Plan{Seed: 1, Rate: 200, DurationNs: 500 * loadgenStepNs, Tenants: 2, Keys: 10, Profile: loadgen.ProfileErrors}
	tr, err := loadgen.NewTracker(p, loadgen.TrackerConfig{Conns: 2, MaxOutstanding: 1, LateAfterNs: 1})
	if err != nil {
		return err
	}
	tr.ConnUp(0)
	tr.ConnectFailed(1)
	var out []loadgen.Dispatch
	for now := int64(0); now <= p.DurationNs; now += loadgenStepNs {
		tr.Completed(0, now, loadgen.Result{Outcome: loadgen.OutcomeDBError, Code: "23505"})
		out = tr.Tick(now, out[:0])
		for _, d := range out {
			tr.Sent(d.Conn, now+2)
		}
	}
	tr.Finish()
	c := obs.NewCollector(obs.Config{Instance: "loadgen", Detail: true})
	if err := c.Register(loadgen.Source(tr.Snapshot)); err != nil {
		return err
	}
	s, err := c.Publish()
	if err != nil {
		return err
	}
	baselineFinals = append(baselineFinals, s)
	return tr.Snapshot().Check()
}
