package simrun_test

import (
	"testing"

	"github.com/nickstrad/mini-gateway/test/e2e"
)

func engineRuns(dir string) error {
	for _, sc := range e2e.Scenarios() {
		r, err := e2e.RunSim(sc.Spec, 1, e2e.SimOptions{Dir: dir, Tamper: sc.Tamper})
		if err == nil {
			err = sc.Check(r)
		}
		if err == nil {
			err = r.Flush.Check()
		}
		if err != nil {
			return err
		}
		baselineFinals = append(baselineFinals, r.Final)
	}
	adm, err := e2e.AdmissionBaseline(dir, !testing.Short())
	baselineFinals = append(baselineFinals, adm...)
	return err
}
