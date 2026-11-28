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
  `Quiesce` refuses a non-zero `RetryPending`. Forwards = first attempts + retries − ends `breaker`.

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
