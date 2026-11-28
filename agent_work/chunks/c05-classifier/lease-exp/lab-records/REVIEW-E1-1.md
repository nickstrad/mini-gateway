# Review 1 of piece E1 (pool core, policies, rule R1, U1–U4)

Reviewer: Fable, 2026-11-26. Commits `36d27ee..92c968c` on `exp/lease-lab`. Brief
`/root/briefs/zgateway-lease-e1-pool-core.md`.

**Verdict: NITS.** No finding corrupts the experiment's evidence or blocks E2. No repair
round is needed; the items below are carried into the E2/E3/E6 briefs.

## What I verified, and how

- Ran `go test ./internal/lab/leasepool/ -count=1` (ok, 6.8 s), `gofmt -l`, `go vet`: clean.
- Read `pool.go`, `rule.go`, `types.go` in full and the world model of `u1_test.go`.
- **Isolation, by reading every path to `lend`** (`pool.go:476`): a connection reaches `lend`
  only from `grant` (state warm or idle) or from `ready` after a checkout reset. Warm is
  entered only from connect/prime, never after a lend, so the warm stock cannot hand out a
  used connection. `reset-always`: release goes to resetting, idle only after reset (and
  reseed) ok. `reset-on-switch`: `grant` sends every used connection of another tenant
  through the reset, affinity on or off (`pool.go:462`). `remake-on-switch`: with affinity
  on other tenants' idle connections are never candidates (`pool.go:390`); with affinity off
  one is closed, not lent (`pool.go:400`). `remake-always`: release always closes. A release
  in `T`/`E`, client gone or doomed closes under every policy (`pool.go:212`). Failed or
  timed-out resets close and put the lease back in its place. Late replies are stale by
  state or phase; connection ids are never reused.
- **Cap and K14:** `free = Cap − len(conns)` counts closing connections until `close done`;
  opens are bounded by `MaxConnecting − connecting` (`pool.go:545–549`).
- **Head of the list:** wrote a throwaway probe (cap full of idle connections tagged for
  tenant 1, tenant 2 asks, `remake-on-switch`, affinity on and off): tenant 2 is served
  after one close and one connect. Removed afterwards.
- **Rule:** tail convention P(N ≥ W) ≤ ε matches the plan's examples. Recomputed five table
  thresholds with my own 60-digit arithmetic (ε 5%/W 7 and 43, 1%/W 16 and 47, 0.1%/W 10):
  each threshold holds ε and threshold + 1 µ does not. EWMA units and the clamp read correct.
- **U1 independence:** the isolation, cap and conservation checks live in the outside-world
  model (`u1_test.go:361`), not in the reference model, so they do not share the core's
  logic; both canaries are judged by those checks with the reference off.
- **Determinism:** the only map (`leases`) is used for lookup, never iterated; no floats,
  clock, goroutines; ties broken by connection id.
- **Accounting:** time integrals advance from per-state counts before each event and sum to
  `ConnNs` by construction; U1 checks them against the world's own open-to-gone time.

## Findings (all minor)

1. **Step cost grows with the wait list** (`pool.go:371–378`). `pump` tries every waiter
   against every connection on every event, ticks included. Measured: 78 µs per step with
   1,000 waiters and cap 20; 1.7 ms with 20,000. Overload runs (120% load) will sit at the
   wait cap, so a 60 s run costs minutes. A behaviour-preserving fix is to stop the loop when
   no warm or idle connection is left; U1 would prove it unchanged. Carry: E2 may make that
   change (U1–U4 green) and should keep `WaitCap` modest in overload runs.
2. **The rule's lead time is the connect alone** (`pool.go:263`). Under `close-then-open`
   the true replenishment time is close lag + connect (+ prime). With the calibrated 0.8 ms
   and 0.6 ms against 16–21 ms, m is under-estimated by 5–10%; p95 lead covers some of it.
   Carry: S5 and the report state it; no change.
3. **`miss_connect` mixes two causes** (`pool.go:481–488`, notes decision 10): no warm
   connection in stock, and no free slot under the cap. The warm-stock miss rate is
   recoverable as `miss_connect / (warm_hit + miss_connect)` only while slots are free; a
   switch that waited and then got a same-tenant connection is `miss_slot`. Carry: S3 runs
   below the cap and says so; S4 reports the mixed number as the limit it maps.
4. **Conservation needs `Pending`.** Requested = granted + `Waiters` + `Pending` + refused;
   connects ok = `ClosesDone` + `Open` (open includes closing). Carry: E2 exports `Pending`
   and `ClosesDone`; E6's SQL uses them.
5. **A full wait list refuses even a request that could be served at once** (decision 5),
   and `WaitCap` is a knob the plan does not list. Carry: E2 exposes it in the config and
   in `lab.csv`.
6. **Prefill under `none`/reset policies is one-shot** (decision 8): no refill after a
   database restart. Carry: S9 states it.
7. Timeouts have tick resolution (100 ms), and K3 also bounds prime and reseed. Stated in
   the notes; fine for the lab.

## Notes for the driver (E2)

- Reply events must carry the server's tag and status: reset ok is tag `DISCARD ALL` then
  `I`; prime and reseed expect tag `SELECT 1`, so the reseed statement must be a one-row
  `SELECT setseed(...)`.
- On `close` the driver aborts whatever is in flight on that connection and reports
  `close done` exactly once; the core never closes a lent or connecting connection.
- `lease_wait_ns = lend time − RequestedAt`; `switch_ready_ns` from `MatchedAt` (from
  `RequestedAt` for mode `connect`). Modes `ready` and `none` are extra to the plan's three.
