# Review 2 (confirm, final) of pieces E1 and E2 — the repair round

Reviewer: Fable, 2026-11-26. Scope: commits `dabb85f..b4ee95c` on `exp/lease-lab` against
`/root/briefs/zgateway-lease-e12-repair.md` (items A1–A3, B1–B6, C) and
[REVIEW-E2-1.md](REVIEW-E2-1.md); regressions. No code edited.

**Verdict: NITS.** Every repair item is done and tested; nothing found corrupts evidence. No
further repair. E3, E5 and E6 may fork from `b4ee95c`. The findings below are carried forward.

## What I verified, and how

- `go test ./internal/lab/... ./cmd/mglease/... -count=1` green (leaselab 62 s, leasepool 9 s,
  mglease 3 s); `gofmt -l .` empty; `go vet` clean. `git diff 593bd25..HEAD --stat`: 16 files,
  all under `internal/lab/` and the two notes files — nothing frozen touched.
- Read the whole diff of `pool.go`, `types.go`, `ref_test.go`, `engine.go`, `seam.go`,
  `metrics.go`.
- **(a) Patience.** `patient(l)` = patience > 0, the tenant holds a lent or idle connection
  (`tagged`, maintained in `setState`/`setTenant`), and waited < patience. A tenant with no
  connection is never patient and takes warm stock in the step of its request. Expiry is by
  time alone, re-evaluated on every event and tick, so a waiter cannot be held forever; the
  wait list is not reordered. On the two new match paths (affinity on: return before the
  warm pick; affinity off: own-tenant idle only) a patient waiter can receive only its own
  tenant's connection. `grant`/`lend` are unchanged apart from the counters, so the isolation
  argument of review 1 stands. Patience is 0 for every policy but `remake-on-switch`.
- **(b) B1.** Room-making closes = min(deficit − free − closing-not-replacing,
  K14 − connecting − closing), none while the connect-retry gate is shut. Holds for both
  `remake.order` values (the bound does not depend on the order; `closing` counts every
  closing connection, which is conservative). Canary `TestU1CanaryUnboundedRoom` bites.
- **(c) Counters, by hand on a run I made** (`skewed`, 10 tenants, cap 20, `fixed:4`, load
  30%, seed 1, 20 s): patience 0 → `switch_arrivals` 7,329 = warm_hit 1,301 + miss_connect
  1,962 + 4,066 leases that began to count toward the deficit and were then served by a used
  connection; `auto` → 189 = 62 + 103 + 24, `patience_waits` 7,360, `patience_expired` 93,
  connects 169, switch closes 149. These are the notes' C-table cells exactly.
- **(d) Reference model.** `ref_test.go` keeps its own state and derives patience, L̂ (own
  window, own sort), `holds`, demand and the counters by scanning; U1 compares the counters at
  every event. All three canaries and `TestC1PatienceStopsTheThrash` pass.
- **(e)** A1: `Script.Abandon`/`AbandonAfter`, the engine releases with `ClientGone` and the
  server's last status; tests per policy on a scripted DB and in the simulator. A2: `plain` is
  a short statement then the hold; `first_stmt_ns` no longer times the hold. A3:
  `Result.Notifications` (bounded at 1,024).
- **(f)** Each of the two cells run twice: all five files byte-identical per pair.
- **(g)** Notes: "Repair round" sections present in both files; the corrected sections read true.

## Findings (all minor; none needs a repair)

1. **`switch_arrivals` is "fresh-connection demand", not "tenant switches".** Under `none` and
   the reset policies it counts every lease that waited (baseline at 60%: 1,744; at 80%:
   21,207). Under `remake-on-switch` it also counts a lease that started to count toward the
   deficit and was then served by its own tenant's returning connection (4,066 of 7,329 in the
   patience-0 cell above). It is the right λ for the sizing rule and the wrong number for the
   switch-rate budget.
2. **`stock_empty_free_slot / switch_arrivals` understates nothing but mixes denominators.**
   Arrivals that found the stock empty with NO slot free are outside the Poisson model; the
   model's miss rate is `stock_empty_free_slot / (switch_arrivals − (stock_empty_arrivals −
   stock_empty_free_slot))`. The two agree only below the cap.
3. **`miss_slot` is not a miss under patience.** It labels "waited, then got a used
   connection", which now includes every patient waiter served by its own tenant (7,271 of
   23,963 at 30%). Read as a miss it would make `remake-on-switch` look worse than it is.
4. **The reference model mirrors the core's structure** for the new rules (same decomposition:
   patient / demand / give-up). Both come from one rule text, so a shared misreading of the
   brief would pass U1. Mitigated by the outside-world checks, the C1 regression test and my
   reading of the semantics above; stated, not repaired.
5. **Patience `auto` raises p99 wait to about L̂** (14 → 20 ms at 30%) by design: one lease in
   three waits for its own connection. And the churn at 60–80% remains (64–133 connects/s).
   Both are findings about the policy for the report, not defects.
6. `mglease` finds `calibration.json` only when run from inside the repository tree.

## Carry-forward (one list for the downstream briefs; supersedes review 1's)

- **E3:** set `obs.interval` to the run length for bulk sweeps (1 s flushes cost about 6 MB of
  histograms per 20 s run); keep it short only where a time series is needed (`step`). Run S3
  below the cap, and report the miss rate by finding 2's formula. S8 tabulates connects/s,
  reuse share and switch closes so thrash is visible. Run `remake-on-switch` with
  `remake.patience=auto` as the arm under test and `0` as a labelled extra, never mixed.
  Prefill under `none`/reset is one-shot (no refill after a restart) — say so in S9. Run
  `mglease` from inside the worktree.
- **E4:** client-gone is `Script.Abandon`/`AbandonAfter`; notifications arrive in
  `Result.Notifications`. `DB` offers no second session: open a side connection of your own in
  the real tier. A lost connection is reported to the core as "database lost" and recycles the
  whole pool — expect that when you kill a backend.
- **E5:** use the same two-statement `plain` script (create `lab_kv` and `lab_fn` as
  calibration did). Never call the `Sink` from inside a `DB` call. A timing run with
  `db_lost > 0` is invalid. Honour `remake.patience` (it is the core's; nothing to do but pass
  it and record it).
- **E6:** conservation includes `pending`: requested = granted + waiters + pending + refused;
  connects ok = `closes_done` + open. Print refusals beside every wait quantile. The
  switch-rate budget counts tenant switches from the lend labels (`switch_ready_ns` by mode:
  `warm` + `connect` + `reset` + `ready`) or from `connects{started}` for the remake policies —
  NOT from `switch_arrivals` (finding 1). Table 6's miss rate uses finding 2's formula.
  `miss_slot` is not a warm-stock miss (finding 3). Give the budget against both 195/s
  (measured) and 174/s (the model's ceiling).
- **E8:** state that the simulator's connect ceiling is 11% low (174/s against 195/s), that
  lease work does not load the connect CPUs in the model, that the simulator's prime, reset
  and first-statement numbers are calibration inputs replayed (not findings), that
  `remake.patience` is a knob added in repair beyond the plan (0 = the plan's literal
  behaviour), and findings 4 and 5.
