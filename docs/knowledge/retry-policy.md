# Retry policy (chunk 12, 2026-11-27)

Facts a later builder needs about the proxy's own retry. Full notes, claims and test ledger:
`agent_work/chunks/c12-retry/NOTES.md`. Generated tables: `docs/error-contract.md` (column
"Retried by the proxy?"), `docs/fault-matrix.md`, the retry rows of `docs/timeout-budget.md` and
`docs/capacity-table.md`.

## The core (`internal/retry`, deterministic, imports `internal/work` and `internal/obs` only)

- `retry.New(Config) (Policy, error)`; `Policy` has `Attempt(now)`, `Decide(now, Failure) Decision`,
  `Snapshot()`. Kinds: `KindOff` (the zero value, the default) and `KindPreForwardOnce`.
- `Failure{Reached work.Tri, InTx, Attempt, BreakerOpen, Draining, Deadline}`. Refusal order:
  once (`Attempt >= 1`), in-transaction, reached (`Reached != work.No`), breaker, draining,
  deadline (`now >= Deadline`, so equality refuses), budget. Each offer ends granted or refused
  for exactly one reason.
- The budget counts in tenths of a token: the bucket starts at the floor, each first attempt adds
  1, a retry takes 10 and only when 10 are held, the level is capped (the excess is counted as
  `Expired`). Conservation: `Deposits (floor included) − Withdrawals − Expired = Level`.
- Test-only: `retry.Plant` bits (`PlantIgnoreReached`, `PlantNoOnceBound`, `PlantNoWithdraw`,
  `PlantNoCap`, `PlantIgnoreBreaker`, `PlantWithdrawFirst`), never set by `internal/config`;
  `internal/retry/retrytest.Everything` (the ablation: retries every failure up to three times),
  not linked by the binary.

## The engine wiring (`internal/proxy/retry.go`)

- `proxy.Config.Retry` (a `retry.Config`); with `Seams.Retry == nil` the engine builds a
  `*RetryCore` from it, so the default engine runs `Off` and still counts offers and attempts.
  `RetryOff{}` and any other `RetryPolicy` still work as a seam; `Engine.RetrySnapshot()` is
  `ok == false` for them.
- The one call site is `statementLost`. `Failure` gained `Attempt`, `BreakerOpen`, `Draining`,
  `Deadline`. `BreakerOpen` is a pure read (`CoreBreaker.Open()`: not closed, so half-open counts
  as open); a breaker seam without `Open()` reads as closed.
- A granted retry is re-run at once: `c.req` is kept, the request re-enters `enqueue` (the
  admission queue is the one waiting place); it holds no connection (the failed one was closed or
  sent to the dirty table first) but holds one place in its tenant's admission queue while it
  waits. Draining and rate gates are not run again. The breaker is read again when a connection
  could be lent to a waiting retry (`refuseRetriesBehindOpenBreaker`, top of `dispatch`): open
  means 53M03 and retry end `breaker`; the token withdrawn at the grant is not returned. With the
  pool core this never fires in practice: the core flushes the queue (`ActRefuseQueued`, retry end
  `shed`) the moment the breaker opens; it guards other breaker seams.
- The request's deadline is `arrival + timeouts.statement`; a retry's queue wait and statement
  deadline are clamped to it (`retryClamp`). First attempts are unchanged.
- A first attempt is reported to the budget in `forward` (every first forward, statements inside
  a transaction included).
- Engine counters: `RetryOffered`, `RetryAsked` (granted), `RetriedRequests`, `RetryPending`,
  `RetryEnds[completed|failed|shed|gone|breaker]`, `RetryRows[row]`. Every end path of a request calls
  `retryEnded` (reply, final failure, shed, breaker flush, breaker at the forward, queued cancel, client gone);
  `Quiesce` refuses a non-zero `RetryPending`. First attempts ≤ forwards ≤ first attempts + retries − ends `breaker`; the upper bound is met exactly only when no granted retry ends in the queue (shed, cancelled or gone) before it is forwarded.

## Config (`retry` section)

`retry.policy` (`off` default, `pre-forward-once`), `retry.bucket_cap` (tokens, default 100),
`retry.floor` (fleet tokens, default 10, divided by `fleet.instances`; refused below 1 per instance
when the policy is on; a remainder is a warning). Zero or omitted means the default, negative is
refused naming the key. An omitted floor is `min(10, bucket_cap × fleet.instances)` (the same rule
as `retry.Config.WithDefaults` for one instance).

## Things that are easy to get wrong

- The budget bound is per window *with the carried credit*: 10 × retries ≤ first attempts in the
  window + level carried in ≤ first attempts + cap. Stored credit does not expire with time, so
  the largest burst after a quiet period is the cap, not the floor.
- `MatrixRow.NotRetried` names why a row is not retried even when its error row would allow a
  retry (deadline, breaker); exactly one matrix row is `Retried()`.

- A loss of a lent connection marks every idle sibling suspect (chunk 7), so after one zero-byte
  refusal the next lends check first and absorb the other dead connections. A scenario gets at
  most one zero-byte refusal per quiet period; `test/e2e/c12_relay.go` dooms every idle
  connection at a quiet instant to get one by construction.
- In tier S a zero-byte refusal comes from `simnet.FaultHalfOpen` on the proxy's own end of the
  connection (`Arg1 = 0`): the end is gone without a notification, the next write fails with zero
  bytes and `proxysim` posts `EvDBWriteRefused`. Applying it while the reset reply is still in
  flight loses that reply (the reset then fails, no client error).
- `Snapshot.Check` of the core enforces the budget bound only for `KindPreForwardOnce`; the
  ablation policy reports its own kind (2) so the engine invariants stay green while it misbehaves.

## Behind the fair dispatcher (join with chunk 9, 2026-11-28)

- Every admission queue runs chunk 9's fair dispatcher, so a granted retry waits behind it like
  any request of its tenant. The lost lease is reported ended (`leaseLost` / `coreDirty` →
  `fairEnded`) before `statementLost` offers the retry, and `lend` starts its next lease once.
- A retry held back by the dispatcher (its tenant at its share at the last free lease, or the
  limit reached) is not pool demand (`Queues.Releasable`): no dial for it. It still ends at the
  request deadline (the clamp is on its queue timer) and the breaker re-check at the top of
  `dispatch` covers lends made by `fairRedispatch` and after another tenant's lease ends.
- A retry is a new push, so it takes a new round-robin turn at the tail of its tenant queue: a
  tenant cannot gain service through retries; it pays one extra turn per retried request. Chunk
  8's `pushes` and `served` count a retried request twice.
- Fairness adds no row to `docs/fault-matrix.md` or `docs/error-contract.md`.
- Tests: `internal/proxy/join_retry_fair_test.go`; scenario
  `TestScenarioRetryStormTwoTenantsOfUnequalWeight` (`test/e2e/c12_c09_join_test.go`).

## Forcing budget exhaustion in a tier S scenario (join repair, 2026-11-28)

- The policy deposits only at a forward (`Engine.forward` → `attempted`, first attempts only),
  and a write to a doomed (`FaultHalfOpen`) connection is refused and decided in the same instant
  as its forward. So a doom burst yields exactly one eligible zero-byte refusal: the pool marks the
  siblings suspect and checks them before they are lent again.
- To force a budget refusal with a one-token bucket, put two doom bursts around a phase too short
  for any client to issue a second operation (shorter than `retryClientGap`) and keep the relay's
  fault window out of it. The first lend after burst 1 spends the token. At most `clients - 1`
  deposits follow, and the first lend after burst 2 adds one more, so with 8 clients the level is at
  most 8 tenths < 10 and the retry is refused for budget. The storm does this
  (`stormSpendAt`/`stormExhaustAt`, `RetrySpec.Exhausts`); the margin is `10 - clients` tenths,
  so the storm needs fewer than 10 clients.
- Picking seeds that happen to exhaust is fragile: any change to the schedule (the fair dispatcher,
  and later the adaptive limit and the coalescer) moves which seeds exhaust.
