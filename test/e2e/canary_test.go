package e2e

import (
	"errors"
	"testing"
)

func mustFailCheck(t *testing.T, sc Scenario, check func(*SimRun) error) *SimRun {
	t.Helper()
	r, err := RunSim(sc.Spec, 1, SimOptions{Tamper: sc.Tamper})
	if err == nil {
		err = check(r)
	}
	if err == nil {
		t.Fatalf("%s: the check passed with the bug planted", sc.Spec.Name)
	}
	t.Logf("%s fails as it must: %v", sc.Spec.Name, err)
	return r
}

func TestCanaryReleaseOnAnyReadyBreaksTransactionIntegrity(t *testing.T) {
	r := mustFailCheck(t, Scenario{Spec: TxIntegrityCanarySpec()}, checkTxIntegrity)
	if n := TxCrossings(r.World.Fake.StatementLog()); n == 0 {
		t.Fatal("no statement ran inside another client's transaction with the bug planted")
	}
}

func TestAblationWithoutResetSessionStateCrosses(t *testing.T) {
	r := mustFailCheck(t, Scenario{Spec: SessionAblationSpec()}, checkSession)
	if n := SessionCrossings(r); n == 0 {
		t.Fatal("no session state crossed with the reset switched off")
	}
	if r.Engine.ResetsStarted != 0 {
		t.Fatalf("the no-reset arm started %d resets", r.Engine.ResetsStarted)
	}
}

func TestCanaryLendBeforeResetReplyIsCaught(t *testing.T) {
	r, err := RunSim(SessionLendEarlySpec(), 1, SimOptions{})
	if err == nil {
		err = checkSession(r)
	}
	if err == nil {
		t.Fatal("lending before the reset reply passed the session scenario")
	}
	if !errors.Is(err, ErrCheck) {
		t.Logf("caught by an invariant: %v", err)
	}
}

func TestResultsThroughTheProxyEqualDirectOnEveryPinnedSeed(t *testing.T) {
	sc, _ := Lookup("e2e-results")
	for _, seed := range []int64{1, 2, 3} {
		r, err := RunSim(sc.Spec, seed, SimOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := checkResults(r); err != nil {
			t.Fatal(err)
		}
	}
}
