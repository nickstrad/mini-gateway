package pgwire_test

import (
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/pgwire/pgwiretest/clsworld"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/simtest"
)

func clsScenarioTest(t *testing.T, sc clsworld.Scenario) {
	simtest.Sweep(t, func(t *testing.T, seed int64) {
		var last clsworld.Result
		s := simtest.RequireDeterministic(t, seed, func(seed int64) *sim.Sim {
			last = clsworld.Run(seed, sc, simtest.ReplayCommand(t, seed))
			if last.World == nil {
				return sim.New(sim.Config{Seed: seed})
			}
			return last.World.Sim
		})
		d := s.Digests()
		t.Logf("digests %s seed=%d trace=%s state=%s", t.Name(), seed, d.Trace, d.State)
		if err := last.Verify(); err != nil {
			t.Fatalf("%v\n%s", err, simtest.ReplayCommand(t, seed))
		}
	})
}

func TestScenarioClassifierRejectList(t *testing.T) {
	clsScenarioTest(t, clsworld.RejectList())
}

func TestScenarioClassifierRejectOnly(t *testing.T) {
	clsScenarioTest(t, clsworld.RejectOnly())
}

func TestClassifierPlantedSetForwardableIsCaught(t *testing.T) {
	for _, seed := range []int64{1, 2, 3} {
		if err := clsworld.Run(seed, clsworld.RejectList(), "").Verify(); err != nil {
			t.Fatalf("seed %d: the scenario fails without the planted bug: %v", seed, err)
		}
		sc := clsworld.RejectList()
		sc.Classifier = pgwire.PlantSetForwardable
		err := clsworld.Run(seed, sc, "").Verify()
		if err == nil {
			t.Fatalf("seed %d: with SET treated as forwardable the scenario still passes", seed)
		}
		msg := err.Error()
		if !strings.Contains(msg, "a rejected statement reached the database") || !strings.Contains(msg, "session state changed") {
			t.Fatalf("seed %d: the planted bug was caught for the wrong reason: %v", seed, err)
		}
	}
}

func TestClassifierRejectOnlyReachesNothing(t *testing.T) {
	r := clsworld.Run(1, clsworld.RejectOnly(), "")
	if err := r.Verify(); err != nil {
		t.Fatal(err)
	}
	var rejects int64
	for _, v := range r.Classifier.ByReason {
		rejects += v
	}
	if rejects != r.Plan.SentRejects || r.Classifier.Classified != r.Plan.SentRejects || r.Wire.Client.QueriesSeen != r.Plan.SentRejects {
		t.Fatalf("rejects %d, classified %d, queries seen %d, sent %d", rejects, r.Classifier.Classified, r.Wire.Client.QueriesSeen, r.Plan.SentRejects)
	}
	if r.PG.StatementsStarted != 0 || r.Plan.SentAllowed != 0 {
		t.Fatalf("the fake Postgres started %d statements, want 0", r.PG.StatementsStarted)
	}
}
