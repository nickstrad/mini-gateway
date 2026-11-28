# Retry policy (chunk 12, 2026-11-27)

Reference notes on the proxy's own retry policy for later work in the codebase. Full notes, claims, and the test ledger live in `agent_work/chunks/c12-retry/NOTES.md`. Generated contract tables live in `docs/error-contract.md` (column "Retried by the proxy?"), `docs/fault-matrix.md`, and the retry rows of `docs/timeout-budget.md` and `docs/capacity-table.md`.

## The core (`internal/retry`, deterministic, imports `internal/work` and `internal/obs` only)

- `retry.New(Config) (Policy, error)` builds a `Policy` with `Attempt(now)`, `Decide(now, Failure) Decision`, and `Snapshot()`. The two product kinds are `KindOff` (the zero value and default) and `KindPreForwardOnce`.
- `Failure` carries `Reached work.Tri`, `InTx`, `Attempt`, `BreakerOpen`, `Draining`, and `Deadline`. `PreForwardOnce` evaluates refusals in fixed order: `once` (`Attempt >= 1`), `in-transaction`, `reached` (`Reached != work.No`), `breaker`, `draining`, `deadline` (`now >= Deadline`, so exact equality refuses), and `budget`. Every offered failure ends either granted or refused for exactly one reason.
- The budget tracks integer tenths of a token: the bucket starts at the floor, each forwarded first attempt deposits `1` tenth, each granted retry withdraws `10` tenths (only when at least `10` are held), and deposits past the cap are discarded and counted in `Expired`. Token conservation holds at every step: `Deposits (floor included) − Withdrawals − Expired = Level`.
- Test-only hooks: `retry.Plant` bits (`PlantIgnoreReached`, `PlantNoOnceBound`, `PlantNoWithdraw`, `PlantNoCap`, `PlantIgnoreBreaker`, `PlantWithdrawFirst`) are never set by `internal/config`, and `internal/retry/retrytest.Everything` (the ablation policy that retries every failure up to three times) is never linked into the binary.

## The engine wiring (`internal/proxy/retry.go`)

- `proxy.Config.Retry` holds a `retry.Config`. When `Seams.Retry == nil`, `proxy.New` constructs a `*RetryCore` from it, so the default engine runs `Off` while still counting first attempts and offered failures. Passing `RetryOff{}` or a custom `RetryPolicy` through the seam remains supported; `Engine.RetrySnapshot()` returns `ok == false` for them.
- The single call site is `statementLost`. `Failure` includes `Attempt`, `BreakerOpen`, `Draining`, and `Deadline`. `BreakerOpen` uses a pure read (`CoreBreaker.Open()`, which returns true when the breaker is not closed, so half-open counts as open); a custom breaker seam without `Open()` reads as closed.
- A granted retry re-runs immediately: `c.req` stays attached and re-enters `enqueue`, keeping the admission queue as the single waiting place. The failed connection was already closed or moved to the dirty-connection table, so a waiting retry holds no database connection and holds one place in its tenant's admission queue. Draining and rate gates do not run again.
- Before lending any connection to a waiting retry, `dispatch` calls `refuseRetriesBehindOpenBreaker`: if the breaker is open, waiting retries are answered `53M03` with retry end `breaker`, and the token withdrawn at grant is not refunded. With the shipped pool core this path does not fire in practice because the pool flushes queued requests (`ActRefuseQueued`, retry end `shed`) the instant its breaker opens; the check guards custom breaker seams.
- A request's retry deadline is `arrival + timeouts.statement`. For a granted retry, `retryClamp` clamps both its queue wait and its forwarded statement deadline to that instant; first attempts keep their normal timers.
- Every first attempt is reported to the budget in `forward`, including statements inside transactions.
- Engine metrics track `RetryOffered`, `RetryAsked` (granted), `RetriedRequests`, `RetryPending`, `RetryEnds[completed|failed|shed|gone|breaker]`, and `RetryRows[row]`. Every request exit path calls `retryEnded` (reply, final failure, shed, breaker flush, breaker at forward, queued cancel, and client gone), and `Quiesce` rejects any non-zero `RetryPending`. Forwards obey `attempts <= forwards <= attempts + retries − ends[breaker]`; the upper bound is met exactly only when no granted retry leaves the queue (shed, cancelled, or gone) before forwarding.

## Config (`retry` section)

- `retry.policy` (`off` default, `pre-forward-once`), `retry.bucket_cap` (tokens per instance, default `100`), and `retry.floor` (fleet-wide tokens, default `10`, divided by `fleet.instances`; refused below `1` token per instance when the policy is on; any remainder produces a warning).
- Zero or omitted selects the default; negative values fail closed naming the key. An omitted `retry.floor` clamps to `min(10, bucket_cap × fleet.instances)`, matching `retry.Config.WithDefaults` for one instance.

## Things that are easy to get wrong

- The per-window budget bound includes carried credit: `10 × retries <= first attempts in the window + level carried in <= first attempts + cap`. Stored credit does not expire with time, so the largest retry burst after a quiet period is `retry.bucket_cap`, not `retry.floor`.
- `MatrixRow.NotRetried` records why a specific fault path never retries even when its error row allows retries in general (for example, deadline equality or a loss that opens the breaker); exactly one matrix row has `Retried() == true`.
- Losing a lent connection marks every idle sibling suspect (chunk 7), so subsequent lends check first and absorb other dead connections without client errors. One quiet period therefore produces at most one zero-byte refusal; `test/e2e/c12_relay.go` dooms every idle connection at a quiet instant to trigger one by construction.
- In tier S, a zero-byte write refusal comes from applying `simnet.FaultHalfOpen` (`Arg1 = 0`) to the proxy's own end of an idle connection: the end disappears without notification, the next write fails with zero bytes accepted, and `proxysim` posts `EvDBWriteRefused`. Applying that fault while a reset reply is still in flight drops the reset reply instead and triggers no client error.
- `retry.Snapshot.Check` enforces the withdrawal and from-start budget bounds only for `KindPreForwardOnce`; the test-only ablation policy reports kind `2` so engine invariants stay green while it deliberately misbehaves.

## Behind the fair dispatcher (join with chunk 9, 2026-11-28)

- Every admission queue runs chunk 9's fair dispatcher, so a granted retry waits behind it like any other request of its tenant. `leaseLost` or `coreDirty` calls `fairEnded` before `statementLost` offers the retry, and `lend` calls `fairStarted` once when the retry receives its next lease.
- A retry held back by the dispatcher—because its tenant is at its share on a reserved last slot, or because the fair limit is reached—is excluded from pool demand (`Queues.Releasable`) and triggers no dial. Its queue-timer clamp still enforces `arrival + timeouts.statement`, and the breaker re-check at the top of `dispatch` covers lends triggered by `fairRedispatch` or another tenant's lease end.
- Because a retry enters through `Queue.Push`, it joins the tail of its tenant's queue and spends a new round-robin turn. A tenant cannot gain service through retries and pays one extra turn per retried request. Chunk 8's `pushes` and `served` counters count a retried request twice.
- Fairness adds no row to `docs/fault-matrix.md` or `docs/error-contract.md`.
- Coverage: `internal/proxy/join_retry_fair_test.go` and `TestScenarioRetryStormTwoTenantsOfUnequalWeight` (`test/e2e/c12_c09_join_test.go`).

## Forcing budget exhaustion in a tier S scenario (2026-11-28)

- The policy deposits only at a first-attempt forward (`Engine.forward` -> `attempted`), and a write to a doomed (`FaultHalfOpen`) connection is refused and decided in the same instant as that forward. Each doom burst therefore produces exactly one eligible zero-byte refusal before the pool marks the remaining idle siblings suspect and checks them before reuse.
- To guarantee a budget refusal with a one-token bucket, place two doom bursts around a short spend phase whose length is shorter than `retryClientGap` (so each client issues at most one operation) and keep the relay's fault window out of that phase. The first lend after burst 1 spends the one token. At most `clients - 1` deposits follow during the spend phase, and the first lend after burst 2 deposits one more; with `8` clients, the bucket reaches at most `8` tenths (`< 10`), forcing `RefusedBudget` on every seed. `RetryStormSpec` uses this schedule (`stormSpendAt`/`stormExhaustAt`, `RetrySpec.Exhausts`) with a margin of `10 - clients` tenths, so it requires fewer than `10` clients.
- Pinning seeds that happen to exhaust is fragile because any scheduling change—such as the fair dispatcher, or later the adaptive limit and read sharing—shifts which seeds hit two eligible losses within ten first attempts.
