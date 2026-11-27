package simrun_test

import (
	"runtime"
	"sync"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/test/e2e"
)

const maxEngineWorkers = 4

type engineRun func() (obs.InstanceSnapshot, error)

func engineJobs(dir string) []engineRun {
	var jobs []engineRun
	for _, sc := range e2e.Scenarios() {
		jobs = append(jobs, func() (obs.InstanceSnapshot, error) {
			r, err := e2e.RunSim(sc.Spec, 1, e2e.SimOptions{Dir: dir, Tamper: sc.Tamper})
			if err == nil {
				err = sc.Check(r)
			}
			if err == nil {
				err = r.Flush.Check()
			}
			if err != nil {
				return obs.InstanceSnapshot{}, err
			}
			return r.Final, nil
		})
	}
	for _, sc := range e2e.PoolScenarios() {
		if sc.Heavy && testing.Short() {
			continue
		}
		jobs = append(jobs, func() (obs.InstanceSnapshot, error) {
			r, err := e2e.RunSim(sc.Spec, 1, e2e.SimOptions{Dir: dir})
			if err == nil {
				err = sc.Check(r)
			}
			if err == nil {
				err = r.Flush.Check()
			}
			if err == nil && sc.Direct {
				_, err = e2e.RunSim(sc.Spec, 1, e2e.SimOptions{Dir: dir, Direct: true, Arm: "direct"})
			}
			if err != nil {
				return obs.InstanceSnapshot{}, err
			}
			return r.Final, nil
		})
	}
	return jobs
}

func shortSkipped() []string {
	var out []string
	if !testing.Short() {
		return out
	}
	for _, sc := range e2e.PoolScenarios() {
		if sc.Heavy {
			out = append(out, sc.Spec.Name)
		}
	}
	return out
}

func inParallel[T any](n int, work func(int) T) []T {
	out := make([]T, n)
	next := make(chan int, n)
	for i := range n {
		next <- i
	}
	close(next)
	var wg sync.WaitGroup
	for range min(maxEngineWorkers, runtime.GOMAXPROCS(0), n) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				out[i] = work(i)
			}
		}()
	}
	wg.Wait()
	return out
}

type engineResult struct {
	final obs.InstanceSnapshot
	err   error
}

func engineRuns(dir string) error {
	jobs := engineJobs(dir)
	results := inParallel(len(jobs), func(i int) engineResult {
		f, err := jobs[i]()
		return engineResult{f, err}
	})
	for _, r := range results {
		if r.err != nil {
			return r.err
		}
		baselineFinals = append(baselineFinals, r.final)
	}
	adm, err := e2e.AdmissionBaseline(dir, !testing.Short())
	baselineFinals = append(baselineFinals, adm...)
	return err
}
