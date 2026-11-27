package proxy

import (
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/retry"
)

type counterRandom uint64

func (r *counterRandom) Uint64() uint64 {
	*r++
	return uint64(*r)
}

func retryEngine(t *testing.T) *Engine {
	t.Helper()
	var rnd counterRandom
	c := Config{Retry: retry.Config{Kind: retry.KindPreForwardOnce}}
	c.Client = pgwire.ClientConfig{Random: &rnd, Database: "db", Auth: pgwire.AuthTrustForTestsOnly, MockSecret: []byte("s")}
	c.Backend = pgwire.BackendConfig{Random: &rnd, User: "u", Database: "db"}
	e, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	e.Start(1, nil)
	if err := e.Invariants(); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestInvariantsCatchAPendingRetryNoRequestHolds(t *testing.T) {
	e := retryEngine(t)
	e.snap.RetryOffered++
	e.snap.RetryAsked++
	e.snap.RetriedRequests++
	e.snap.RetryPending.Add(1)
	e.seams.Retry.Retry(1, Failure{Reached: 0, Deadline: 2})
	if err := e.snap.Check(); err != nil {
		t.Fatalf("the snapshot alone should not see it: %v", err)
	}
	if err := e.Invariants(); err == nil || !strings.Contains(err.Error(), "retried requests alive") {
		t.Fatalf("Invariants: %v", err)
	}
}

func TestInvariantsCatchTheEngineAndThePolicyDisagreeing(t *testing.T) {
	e := retryEngine(t)
	e.snap.RetryOffered++
	if err := e.snap.Check(); err != nil {
		t.Fatalf("the snapshot alone should not see it: %v", err)
	}
	if err := e.Invariants(); err == nil || !strings.Contains(err.Error(), "the policy saw") {
		t.Fatalf("Invariants: %v", err)
	}
}

func TestInvariantsCatchABrokenBudget(t *testing.T) {
	e := retryEngine(t)
	rc := e.seams.Retry.(*RetryCore)
	p := rc.p.(*retry.PreForwardOnce)
	e.seams.Retry = NewRetryCore(brokenPolicy{p})
	if err := e.Invariants(); err == nil || !strings.Contains(err.Error(), "deposits") {
		t.Fatalf("Invariants: %v", err)
	}
}

type brokenPolicy struct{ *retry.PreForwardOnce }

func (b brokenPolicy) Snapshot() retry.Snapshot {
	s := b.PreForwardOnce.Snapshot()
	s.Deposits++
	return s
}
