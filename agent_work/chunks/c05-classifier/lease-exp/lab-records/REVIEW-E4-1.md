# Review 1 of piece E4: session-isolation workload and checker

Reviewer: Claude Opus 5.5 (cross-family; builder Astra/Codex). Brief:
`/root/briefs/zgateway-lease-e4-review.md`. Reviewed commits `53de977` and `0634906` on
`exp/lease-lab-e4` (base `549e9c3`).

## Verdict: **NITS**

The checker has teeth, and its isolation evidence is honest. The baseline fails in all seven
catalog columns and for all 17 secret-bearing kinds. I re-ran the whole real suite: it passes,
and the matrix reproduces the notes' numbers exactly. Three mutations of my own were each
caught by a named test: a pool core that skips half the switch resets, an engine that sends a
partial reset (`RESET ALL` with a forged `DISCARD ALL` tag), and a catalog blind to advisory
locks. None of the findings below makes the isolation evidence wrong. They are test-assertion
gaps and documentation points that the coordinator can carry forward.

## Findings

1. **minor: the catalog's settings column cannot see custom (placeholder) settings.**
   `internal/lab/leaselab/oracle.go:130` (`pg_settings WHERE source='session' …`).
   Evidence: in my hand session, the `custom_setting` kind (`SET myapp_7.l4242 = 'secret…'`)
   gave catalog `[0 0 0 0 0 0 0]` before any reset, while `current_setting` returned the
   secret. Postgres does not list placeholder GUCs in `pg_settings`. So for this kind only the
   front-door canary sees the value, and the canary checks only the immediate predecessor's
   kind on that connection. A pool bug that left a custom value behind for two or more leases
   would be invisible to R1. Repair: state in NOTES-E4.md that the R1 settings column covers
   built-in GUCs only and that custom names and values rest on the canary and
   `probe_custom_name`. Optionally, have the inspector also probe every custom name written
   on that connection since its last reset, not only the predecessor's.

2. **minor: a latent false failure in the matrix's seed assertion on fresh rows.**
   `oracle_tierr_test.go:240-243`. Under reset arms the check fires on any row where
   `SeedInherit >= 0`, including fresh backends, which probe the previous lease (`h.last`)
   as a negative control. A fresh backend after a `setseed` lease with reseed off therefore
   fails with "seed residue" even though nothing is wrong. In the unmutated serial matrix
   this never happens, because fresh rows only follow T/E/gone leases. My first `RESET ALL`
   mutation hit it: the pool refused every reset, every lease got a fresh backend, and the
   test failed on this assertion rather than on a leak. `e4.sql:28` already guards with
   `prev_tenant>=0`. Repair: add `o.PrevTenant >= 0` to the Go condition.

3. **minor: R5 does not assert closure on the same-tenant path under the on-switch policies.**
   `oracle_tierr_test.go:315-331` always goes tenant 1 → 2. Under `remake-on-switch`,
   `a.PID != b.PID` is then true by construction, and under `reset-on-switch` a switch reset
   would happen anyway. The case where no reset is planned is a same-tenant follower on an
   on-switch policy. There, closing the connection is the only defence, and for
   `client_gone` (status `I`) a reused connection would be labelled `allowed=true` and pass.
   The behaviour does hold: across all 24 matrix cells in the builder's evidence, no oracle
   row has `prev_taint IN (in_tx, failed_tx, client_gone)` (my DuckDB query over
   `e4-evidence/tests/*Matrix*/oracle.csv` found 0 in every cell, including the 54
   same-tenant pairs per on-switch cell). But nothing asserts it. Repair: in the matrix and
   in `e4.sql`, fail any row whose `prev_taint` is one of the three closing kinds, and add
   same-tenant variants to `TestRealHostileR5`.

4. **minor: some coverage counts are logged, not asserted.** `oracle_tierr_test.go:277`
   logs `seed` and `custom_name`, but nothing requires them to be greater than zero under the
   reset arms, or `seed` greater than zero under the baseline. The per-row residue checks at
   lines 240-246 would pass vacuously if the `setseed` probe stopped being emitted.
   Repair: require `seed > 0` when reseed is off and `nameResidue > 0` in the reset cells.

5. **minor (carry-forward, documentation): the fresh-backend negative control is weak in the
   concurrent shell.** `oracle.go:201,370`. `h.last` is the last *inspected* lease, whose
   script may not have run yet on its own connection. In the campaign's remake arms, the
   front-door probes on fresh backends therefore prove little. The arm's isolation claim
   rests, correctly, on backend identity (`e4.sql:20-22`, report table 2 `backend_identity`)
   and on zero cross-tenant reuse. Say so in NOTES-E4.md.

6. **NITS (carry-forward for E7).**
   - `e4-campaign.txt` is not under `lease-exp/manifests/e7-*.txt`, so
     `scripts/lease-exp.sh e7` will not run it. The coordinator must run it with
     `lease-exp.sh campaign …` (about 48 minutes; it takes the machine lock) or copy it in as
     an `e7-0-r1r5.txt`.
   - The engine's `oracle_checks{clean,dirty}` metric counts not-checked (-1) columns as
     clean (`engine.go`, `oracle()`; E2 code, not E4's). NOTES-E4.md says so. Readers of the
     E7 run must gate on `test/analysis/lease/oracle/e4.sql`, not on the metric.
   - `HostileViolation` (`oracle.go:382`) exempts fresh rows under on-switch policies, but
     `e4.sql:18` checks them. The SQL is the stricter of the two; no harm, just inconsistent.
   - Readability is acceptable now: gofmt-clean, one statement per line. `hostileKinds`
     (`hostile.go:8`) is one 240-character line, and the `one`/`add` closures are terse.
     Cosmetic.

## What I verified and how

- **Build hygiene.** `gofmt -l internal cmd` is empty. `go vet ./internal/lab/...
  ./cmd/mglease/...` is clean. `go test ./internal/lab/... ./cmd/mglease/... -short
  -count=1` passes. `git diff --name-status 549e9c3 0634906` adds only new files (hostile*,
  oracle*, the two manifests, NOTES-E4, `test/analysis/lease/oracle/e4.sql`, evidence).
  The pool core, engine, real shell and frozen packages are untouched.
- **Real suite, re-run.** `E4_EVIDENCE=/tmp/… scripts/lease-exp.sh test -run TestRealHostile
  -v`: all 8 top-level tests PASS, with 105 `--- PASS` lines and exit 0 (269 s). Matrix
  logs: baseline cross-tenant non-zero counts `[52 52 52 48 54 52 9]` in all four cells,
  every one of the 17 secret-bearing kinds read 3/3 across tenants, seed 6 and custom name 6.
  `reset-always`/`reset-on-switch` cross-tenant columns are all zero, with seed 6 when
  reseed is off and 0 when it is on, and custom name 6 either way. On-switch arms have 51
  allowed same-tenant reads. Remake arms show zero residue. This matches NOTES-E4.md.
- **(1) Baseline fails.** Confirmed above, plus `e4.sql` on `campaign-mon`: "baseline
  exercises every catalog column" and "every secret-bearing kind" (cross-tenant only).
- **(2) Taints are real.** I read every script in `hostile.go:39-110` and each probe in
  `oracle.go:209-257`. By hand, in a container session (scratch test with
  `OpenSession`, run through `lease-exp.sh test` from a scratch copy), I checked ten kinds:
  catalog and front-door probe before `DISCARD ALL`, then again after. `temp_table` (temp=1,
  secret selected → 0, 42P01), `cursor_hold` (cursors=1, FETCH secret → 0, 34000),
  `advisory_lock` (locks=1, lock view 1 → 0), `listen` (listens=1, NOTIFY delivered → 0,
  none), `hidden` (`[1 1 1 0 1 1 0]` from a SELECT with no session keyword → all 0),
  `set_role`/`session_auth` (role=1, `e4_role:secret` → `postgres:…`), `temp_sequence`
  (temp=1, currval → 42P01), `setseed` (sequence equals the reference → differs), and
  `custom_setting` (value → `''`; the name survives: residue item 2; see finding 1).
- **(3) Catalog.** The engine calls `Statements` at lend and sends the queries
  `PurposeInspect` on the lent connection, then runs the script (`engine.go:309-327`,
  `479-487`). The catalog is the first query, and `Row` requires status `I`
  (`oracle.go:305`), so a connection lent inside a transaction fails. The query has no side
  effects (`TestRealHostileCatalogDoesNotHideState`; my hand runs give identical catalog
  values when repeated). Unchecked (-1) columns count as violations in both
  `HostileViolation` and `e4.sql:16`.
- **(4) Across- vs same-tenant.** `allowed` is true only for same-tenant rows under
  `reset-on-switch`/`remake-on-switch` (`oracle.go:296`). `HostileViolation` and
  `e4.sql:18` require clean same-tenant rows under `reset-always`/`remake-always`. My
  mutation run below was caught on a same-tenant `reset-always` row.
- **(5) R3/R4.** Catalog columns 8 and 9 are `pg_backend_pid()` and `backend_start` (through
  a SECURITY DEFINER helper), compared with the engine's identity (`oracle.go:317`).
  Remake-always forbids any PID/start reuse and remake-on-switch forbids reuse across
  tenants (`oracle_tierr_test.go:269-276`, `e4.sql:20-22`). Residue item 1 (seed) appears
  under reset arms with reseed off and disappears with reseed on. Item 2 (custom name)
  appears under reset arms either way. Neither appears under remake arms. This is asserted
  per row and visible in the report table 2 rows I regenerated.
- **(6) R5.** `in_tx`/`failed_tx`/`client_gone` × 5 policies, plus patience 0: a different
  backend follows, the status is T/E/I, and there are no canary reads. A refused reset
  (`BEGIN; DISCARD ALL` gives a real 25001) and a timed-out reset (`pg_sleep(0.3)` against
  `reset.timeout=50ms`) each close the connection, with the `ResetsFailed`/`ResetsTimedOut`
  counters asserted. A slow successful reset is waited for
  (`TestRealHostileSlowResetWaited`). Gap: finding 3.
- **(7) Planted bugs.** `skip_one_in_three` gives exactly 3 detections, and
  `wrong_connection` is caught by identity mismatch. Both pass in my re-run. My own
  mutations, each in a scratch copy, reverted and never committed:
  (a) `leasepool/pool.go` `grant()` skips the switch reset on odd lease IDs:
  `TestRealHostileR1R4Matrix/reset-on-switch-pauto-aoff-rfalse` FAILS, with a cross-tenant
  `settings_changed=1`, `hit=set` at lease 3.
  (b) The engine sends `RESET ALL` with a forged `DISCARD ALL` tag (a partial reset):
  `reset-always-pauto-aoff-rtrue` FAILS on a same-tenant row with prepared, cursors,
  listens and temp all at 1. Without the forged tag the pool refuses every reset and closes
  the connection, a good defence in its own right, and the test then trips finding 2.
  (c) The catalog's advisory-lock column is replaced by `0`: the baseline cell FAILS with
  "toothless column 3".
- **(8) Coverage.** Reduced campaign (`campaign-mon`, 60 leases per arm): baseline and
  reset arms have 48-50 cross-tenant, 1-2 same-tenant and 9-10 fresh rows, with 18 kinds
  (21 minus the 3 closing kinds) probed across tenants. Remake-on-switch arms have 0
  cross-tenant rows (as designed), 1-3 same-tenant and 57-59 fresh. Tenant switches are
  dominant, so a leak would be seen. Same-tenant carry-over is thin in the campaign, but
  the serial matrix covers it (54 pairs per cell). The full manifest gives 2,429 leases per
  arm (`TestHostileCampaignSchedule`). The NOTES-E4.md claims I checked (189 rows per cell,
  6 writes / 3 + 3 probes per kind, the HELD/BROKEN list, the reduced-campaign matrix)
  agree with the tests and evidence. What the notes say but the tests do not enforce is
  covered by findings 3 and 4.
- **(9) Manifest.** `e4-campaign.txt` has 24 arms: none, reset-always, reset-on-switch,
  remake-on-switch with patience auto and 0, and remake-always, each with affinity on/off ×
  `reset.reseed` off/on. It sets `oracle.rate=1`, `identity=query` and
  `obs.leases_csv=on`. The reduced twin differs only in duration. I re-ran `e4.sql` on
  `campaign-mon` (all seven gates pass) and `scripts/lease-report.sh` into scratch: rc 0,
  and `02-isolation.csv` is identical to the builder's `report-mon/02-isolation.csv`. E7
  placement: finding 6.
- **(10) Rules.** gofmt/vet clean, no frozen or core file touched, readable Go (finding 6).

Scratch (`/tmp/zgw-lease-e4-review/`, including the mutation copies) was removed after the
review. All test containers were `mglease-` throwaways started and stopped by
`scripts/lease-exp.sh`.
