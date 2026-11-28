VERDICT: PASS

## Findings

None.

Verification of review 1's four findings and Astra's three broken attacks on tip `af1554b4ba551aec9e1eb48f2a1efd3384cf29cd`:

1. **Review 1 Finding 1 (MAJOR — read-back two or three leases late):** Fixed in `d3e8d4b` (`test/e2e/tierr_test.go:335-344`, `406-449`; `test/e2e/c07_tierr_test.go:117-129`, `164-179`). `observeOneLease` runs `BEGIN`, `secretReadBack`, `dirty-check.sql`, `SHOW search_path`, and `COMMIT` inside one transaction on the lease immediately following each hostile lease; `directBaseline` runs the same one-lease sequence so `ReadyForQuery` (`ZT`) matches. Confirmed by mutation M7 below (`TestTierRSessionStateNeverCrossesClients` and all four arms of `TestTierRPoolSessionStateUnderEachReleasePolicy` fail).
2. **Review 1 Finding 2 (MAJOR — unreadable check credits every column) and `TestAdv_3_*`:** Fixed in `d3e8d4b` (`test/e2e/tierr_test.go:320-322`, `372-394`, `432-449`, `475-477`; `test/e2e/c07_tierr_test.go:169-172`, `218-220`; unit tests `test/e2e/sessionoracle_test.go:12-49`). `completeRow` requires a clean reply and exact column width before `observe` credits any column; broken or wrong-width replies increment `unreadable` instead of `moved`, and both `TestTierRSessionAblationWithoutResetCrosses` and `TestTierRPoolSessionStateUnderEachReleasePolicy` fail on `unreadable != 0`. Confirmed by mutations M6a and M6b below and by `TestAdv_3_ErrorIsNotMovement`, `TestAdv_3_EmptyIsNotMovement`, and `TestAdv_3_TruncatedIsNotMovement` all passing.
3. **Review 1 Finding 3 (MINOR — stale pointer `REPORT.md:255`):** Fixed in `4307437` (`agent_work/chunks/c05-classifier/lease-exp/REPORT.md:255` now reads `Review files are in [lab-records/](lab-records/).`). Grepping `REPORT.md` and `CALIBRATION-NOTE.md` for `branch|lease-lab` finds only past-tense references to the deleted branch.
4. **Review 1 Finding 4 (MINOR — closed hostile call not counted):** Fixed in `d3e8d4b` (`test/e2e/tierr_test.go:324-326`, `422`; `test/e2e/c07_tierr_test.go:152`; unit test `test/e2e/sessionoracle_test.go:51-67`). `hostileFailed` fails on `E:`, `closed:`, missing `Z`, or missing `D:`. Confirmed by mutation M8 below.

Fuzz targets (PLAN.md 6.6): not applicable; no fuzz target was added or modified (`git diff --stat 98cee87..af1554b -- internal cmd go.mod go.sum` is empty).

## Mutation checks

All mutations were applied to fresh copies of `git archive HEAD` (`af1554b`) under `/tmp/zgw-lease-lab-port-review2/mut/work` and run with `GOFLAGS=-buildvcs=false MG_TIER_R=1 go test -count=1 -timeout 5m ./test/e2e/ ...`:

1. **M6a — Remove `do_cursor()` and `do_role()` from `hostileCalls` AND replace the first observation (`round == 0 && i == 0`) with `catalog = []string{"E:ERROR:42501", "ZE"}`, `broken = true`** (`test/e2e/tierr_test.go`): **CAUGHT** (exit 1, 10.5 s). `TestTierRSessionAblationWithoutResetCrosses` failed with `1 dirty-check observations were unreadable`, `dirty-check column cursors never differed`, and `dirty-check column role_changed never differed` (`cursors 0, role_changed 0, unreadable checks 1`).
2. **M6b — Same removal, injecting `catalog = []string{"E:ERROR:42501", "ZI"}` with `broken = false` (testing `observe()` directly)** (`test/e2e/tierr_test.go`): **CAUGHT** (exit 1, 10.0 s). `TestTierRSessionAblationWithoutResetCrosses` failed with `1 dirty-check observations were unreadable`, `dirty-check column cursors never differed`, and `dirty-check column role_changed never differed` (`cursors 0, role_changed 0, unreadable checks 1`).
3. **M7 — Skip `DISCARD ALL` (send `DO $$BEGIN END$$` and accept tag `DO`) only on the reset immediately following a query containing `do_custom`** (`internal/proxy/dbconn.go`, `internal/proxy/lease.go`): **CAUGHT** (exit 1, 19.1 s). `TestTierRSessionStateNeverCrossesClients` failed (`3 dirty observations ... secret read back 3, unreadable checks 0`), and `TestTierRPoolSessionStateUnderEachReleasePolicy` failed in all four arms (`always/off`: `1 session-state crossings`, `same-tenant carry-over 1`; `always/on`: `1 session-state crossings`, `same-tenant carry-over 1`; `on-switch/off`: `2 session-state crossings`; `on-switch/on`: `2 session-state crossings`).
4. **M1 — Partial reset omitting `CLOSE ALL` and `RESET ROLE`** (`internal/proxy/lease.go`): **CAUGHT** (exit 1, 17.8 s). `TestTierRSessionStateNeverCrossesClients` failed (`21 dirty observations ... cursors 19 ... role_changed 18`); `TestTierRPoolSessionStateUnderEachReleasePolicy` failed in all four arms (`5 session-state crossings`, `1 hostile calls failed`).
5. **M2 — Partial reset omitting `RESET ALL` while keeping `RESET search_path`, `CLOSE ALL`, `RESET ROLE`, etc.** (`internal/proxy/lease.go`): **CAUGHT** (exit 1, 16.9 s). `TestTierRSessionStateNeverCrossesClients` failed (`17 dirty observations ... secret read back 17`, all seven catalog columns 0); `TestTierRPoolSessionStateUnderEachReleasePolicy` failed in all four arms (`4 session-state crossings`).
6. **M3 — Remove `do_role()` from `hostileCalls`** (`test/e2e/tierr_test.go`): **CAUGHT** (exit 1, 10.4 s). `TestTierRSessionAblationWithoutResetCrosses` failed naming `role_changed` (`role_changed 0`).
7. **M8 — Return `[]string{"closed:eof"}` for `do_role()` in `policyRun`** (`test/e2e/c07_tierr_test.go`): **CAUGHT** (exit 1, 16.4 s). `TestTierRPoolSessionStateUnderEachReleasePolicy` failed in all four arms (`2 hostile calls failed, so their state was never planted`).

## Release It! checklist

No product code was added or modified (`internal/` and `cmd/` are untouched); answers apply to the changed `test/e2e/` harness and kept records:

- **Integration points:** Not applicable to product code (untouched); every new statement in `observeOneLease` and `directBaseline` runs over `RealClient.Query`, which enforces `realOpTimeout` deadlines on TCP/TLS sockets.
- **Blocked threads:** Not applicable to product code (untouched); `sessionRun`, `policyRun`, and `directBaseline` close every dialed `RealClient` and `t.Cleanup` stops every `Gateway` and Postgres container.
- **Slow responses:** Not applicable here because no proxy request, cancel, or timeout path changed.
- **Cascading failures:** Not applicable here because no database-outage or health-check path changed.
- **Chain reactions:** Not applicable here because no multi-instance shedding or queueing path changed.
- **Dogpile:** Not applicable here because no dial, reconnect, or recovery path changed.
- **Self-denial attacks:** Not applicable here because no batch dial, expiry, flush, or retry path changed; the tier R checks run serially on a 1-connection pool.
- **Force multiplier:** Not applicable here because no automatic or adaptive control loop changed.
- **Unbalanced capacities:** Not applicable here because no producer/consumer buffer or queue changed.
- **Scaling effects:** Not applicable here because no fleet-wide guarantee or exported metric label changed.
- **Users:** Not applicable here because no per-client or per-tenant limit changed (`hostileRole` is a single test-setup role).
- **Unbounded result sets:** Not applicable here because no relay or result-forwarding code changed (`dirty-check.sql`, `current_setting`, and `SHOW search_path` each return a single scalar row).
- **Compounds:** Not applicable here because no product paths changed, and review 1's test-oracle defects are closed.

## Observability

- **Product components (PLAN.md 7.6):** `git diff --stat 98cee87..af1554b -- internal cmd go.mod go.sum` is empty; no component was added or modified, so no new `Snapshot()`, exporter mapping, metric registry row, or health rule is owed.
- **Existing observability exercised and verified:** `TestTierRSessionStateNeverCrossesClients` and `TestTierRPoolSessionStateUnderEachReleasePolicy` run `stopAndRead`, verifying `csvflush.RoundTrip`, `realConservation`, and counter movement (`resets_completed`, `leases_ended`, `tenant_switches`, `reseeds` — `always-reseed` now 36 with the one-lease check, matching `NOTES.md`). `scripts/csv-assert.sh` passed both `test/analysis/tierr-session-state/conservation.sql` and `test/analysis/tierr-session-state/resets.sql`; `G8 gen-docs` passed (`docs/csv-schema.md is current`).

## What I ran

- `git rev-parse HEAD && git status --short`: `af1554b4ba551aec9e1eb48f2a1efd3384cf29cd`, clean tree before and after.
- `git diff --stat 98cee87..af1554b -- internal cmd go.mod go.sum`: empty.
- `gofmt -l test/e2e/ && go build ./... && go vet ./...`: pass (clean output).
- `go test -count=1 -timeout 3m ./test/e2e/ -run 'TestSessionOracle|TestHostileCall|TestAblationWithoutResetSessionStateCrosses' -v`: pass (4/4 tests, 0.063 s).
- `go test -short -count=1 -timeout 3m ./test/e2e/`: pass (62.447 s).
- `GATE_OUT=/tmp/zgw-lease-lab-port-review2/gate GATE_BASE=98cee87ce9552b6544ef74f67e778489250ecb28 GATE_ONLY=G0,G1,G2,G8,G9 scripts/gate.sh lease-lab-port`: overall pass (G0 tools 6s, gofmt 1s, build 9s, vet 4s, tools-vet 1s, tools-test 10s, tidy 0s; G1 detlint 8s, importlint 0s, boundlint 15s, paniccheck 1s; G2 comments 2s; G8 gen-docs 10s; G9 site 6s).
- `go test -count=1 -timeout 3m ./scripts/ -v`: pass (all tests, 111.441 s).
- Adversarial package in clean clone `/tmp/zgw-lease-lab-port-review2/adv-repo`:
  - `go test -count=1 -timeout 5m ./agent_work/adversarial/lease-lab-port/... -run '^TestAdv_(3|9|10|11|12)_' -v`: pass (15/15 tests including `TestAdv_3_ErrorIsNotMovement`, `TestAdv_3_EmptyIsNotMovement`, `TestAdv_3_TruncatedIsNotMovement`, 63.287 s).
  - `go test -count=1 -timeout 5m ./agent_work/adversarial/lease-lab-port/... -run '^TestAdv_(1|2)_' -v`: pass (6/6 live Postgres tests, 13.755 s).
- `MG_TIER_R=1 RUN_DIR=/tmp/zgw-lease-lab-port-review2/tierr-runs go test -count=1 -timeout 10m ./test/e2e/ -run 'TestTierRSession|TestTierRPoolSessionState' -v`: pass (3/3 tests, 11.692 s; `always-reseed` reseeds 36, ablation `prepared 21, cursors 19, listens 20, adv_locks 22, session_gucs 24, temp_rels 23, role_changed 18, secret read back 17, unreadable checks 0`).
- `scripts/csv-assert.sh /tmp/zgw-lease-lab-port-review2/tierr-runs`: `ok: test/analysis/tierr-session-state/conservation.sql over 1 runs`, `ok: test/analysis/tierr-session-state/resets.sql over 1 runs` (88 scenarios total, confirming nothing under `agent_work/` is picked up).
- `MG_TIER_R=1 RUN_DIR=/tmp/zgw-lease-lab-port-review2/tierr-loop go test -count=5 -timeout 10m ./test/e2e/ -run 'TestTierRSession|TestTierRPoolSessionState' -v`: pass (15/15 runs, 36.356 s, identical counts on every run).
- Mutations M6a, M6b, M7, M1, M2, M3, M8 in `/tmp/zgw-lease-lab-port-review2/mut/work`: 7/7 caught (details under `## Mutation checks`).
