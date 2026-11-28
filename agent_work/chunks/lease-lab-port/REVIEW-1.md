VERDICT: FAIL

## Findings

1. **MAJOR** — `test/e2e/tierr_test.go:384-394`, `test/e2e/c07_tierr_test.go:150-152`
   - **What is wrong:** Brief deliverable 1b requires that *"After a custom-setting lease the next lease reads the setting back (`current_setting('mg_hostile.secret', true)`) and must not see the secret"*. Under `ResetAlways` (default in `sessionRun` and the `always/off` and `always/on` arms of `policyRun`), `secretReadBack` does not run on the next lease after `do_custom`: in `sessionRun` it runs on the **3rd** lease after `h` (`dirty-check.sql` -> `DISCARD ALL` -> `SHOW search_path` -> `DISCARD ALL` -> `secretReadBack`), and in `policyRun` it runs on the **2nd** lease after `h` (`dirty-check.sql` -> `DISCARD ALL` -> `secretReadBack`, confirmed by `reseeds 45` = 16 hostile + 16 check + 9 read-back + 4 seed/random). Because `pg_settings` does not list placeholder settings, `dirty-check.sql` on lease 1 is blind to `mg_hostile.secret`, and the `DISCARD ALL` triggered at the end of lease 1 scrubs the leaked secret before `secretReadBack` ever executes. Any proxy bug that skips reset on `do_custom`'s lease (for instance on a closing client, a specific tenant, or a specific release path) while still resetting after `checker`/`o`'s `dirty-check.sql` lease escapes `TestTierRSessionStateNeverCrossesClients` and both `always` arms of `TestTierRPoolSessionStateUnderEachReleasePolicy`.
   - **How I know:** Mutation M7 in `/tmp/zgw-lease-lab-port-review1/mut/work` replaced `DISCARD ALL` with `SELECT 1` only on the reset immediately following a query containing `do_custom`, leaving `mg_hostile.secret` live on the next lease handed to `checker`/`o`. `TestTierRSessionStateNeverCrossesClients` passed (`0 dirty observations over 24 hostile leases`), and `TestTierRPoolSessionStateUnderEachReleasePolicy` passed both `always/off` and `always/on` (`crossings 0, same-tenant carry-over 0`), failing only on `on-switch/off` and `on-switch/on` (`2 session-state crossings between tenants`, where no reset runs between `o`'s two statements).
   - **What a fix must achieve:** `secretReadBack` must observe the backend on the immediately following lease after `h.Query(sql)` with no intervening `DISCARD ALL` — for example by executing `dirty-check.sql`, `SHOW search_path` (in `sessionRun`), and `secretReadBack` inside one transaction (`BEGIN` ... `COMMIT`) on `checker`/`o` so the entire check shares a single lease, or by running `secretReadBack` before `dirty-check.sql` when `secret != ""` (after a `do_custom` lease).

2. **MAJOR** — `test/e2e/tierr_test.go:345-352`
   - **What is wrong:** `(*sessionResult).observe` marks every column `i` as moved whenever `i >= len(row) || i >= len(clean) || row[i] != clean[i]`. If `checker.Query(check)` returns an error (`E:...`) or closed connection (`closed:...`), `dataRow(got)` returns `nil` (`len(row) == 0`), making `i >= len(row)` true for all seven columns. A single failed `dirty-check.sql` query during `TestTierRSessionAblationWithoutResetCrosses` therefore increments `r.moved[i]` for every column and allows the permanent positive-control guard (deliverable 1c, orchestrator decision 1) to pass even when columns are completely blind.
   - **How I know:** Mutation M6 in `/tmp/zgw-lease-lab-port-review1/mut/work` removed both `do_cursor()` and `do_role()` from `hostileCalls` and substituted `[]string{"E:ERROR:42501", "ZI"}` for `checker.Query(check)` on `round == 0 && i == 0`. `TestTierRSessionAblationWithoutResetCrosses` passed (`prepared 16, cursors 1, listens 15, adv_locks 17, session_gucs 18, temp_rels 18, role_changed 1, secret read back 13`) instead of failing with `cursors` and `role_changed` named.
   - **What a fix must achieve:** `observe` must only credit `r.moved[i]++` when `len(row) == len(clean)` and `row[i] != clean[i]`. A missing or malformed `dirty-check.sql` row must never increment per-column `moved` counts (while still incrementing `r.dirty` and, in `TestTierRSessionAblationWithoutResetCrosses`, failing the test if a check query errored).

3. **MINOR** — `agent_work/chunks/c05-classifier/lease-exp/REPORT.md:255` (and `agent_work/chunks/lease-lab-port/EVALUATION.md:260-268`)
   - **What is wrong:** `REPORT.md` line 255 still reads `Review files are on the lab branch.` even though the review files were ported to `lab-records/` and `exp/lease-lab` is to be deleted after this port. `EVALUATION.md` section 6 missed this third stale pointer in `REPORT.md`, so the builder's brief listed only lines 6 and 144.
   - **How I know:** `grep -n -E 'branch|lease-lab' agent_work/chunks/c05-classifier/lease-exp/REPORT.md` returns line 255.
   - **What a fix must achieve:** Update `REPORT.md` line 255 to point to `[lab-records/](lab-records/)` (e.g. `Review files are in [lab-records/](lab-records/).`).

4. **MINOR** — `test/e2e/c07_tierr_test.go:138`, `test/e2e/tierr_test.go:380`
   - **What is wrong:** Hostile-call failure checks test only `HasToken(got, "E:")`. `RealClient.Query` (`test/e2e/realclient.go:178-198`) returns `closed:write` or `closed:eof` without an `E:` token if the gateway drops the connection before answering, so a silently closed hostile call would not increment `r.hostileErrors` (in `policyRun`) or `r.dirty` (in `sessionRun`).
   - **How I know:** Code inspection of `test/e2e/realclient.go:178-198` against `test/e2e/c07_tierr_test.go:138` and `test/e2e/tierr_test.go:380`.
   - **What a fix must achieve:** Treat a hostile call as failed unless it succeeded cleanly (`HasToken(got, "E:") || HasToken(got, "closed:") || !HasToken(got, "D:")`).

## Extra port checks

- **EVALUATION.md verdicts:** Checked all 833 files on `exp/lease-lab` (`f7b2ec8..5ffc7a3`): every path falls under the six judged prefixes; all five evidence manifests (`e7/{r6,r8,r7a,r7b,isolation}/manifest.txt`) match their counterparts on `exp/lease-lab` (`manifests/e7-*.txt`, `e4-campaign.txt`) by blob hash; all 21 kinds in `exp/lease-lab:internal/lab/leaselab/hostile.go` and `oracle.go` were inspected. No DROP is wrong (dropped lab engine/harness tests do not exercise `internal/proxy`, R5 contradicts main's D7 rollback-reset-reuse, `session_auth` requires superuser, and the remaining kinds duplicate existing `dirty-check.sql` columns or `setseed` in `c07_tierr_test.go`). No KEEP is misplaced. Only gap in `EVALUATION.md` is missing the stale pointer at `REPORT.md:255` (Finding 3).
- **Copies and figures:** All 44 of 44 copied files match their blob hashes in `NOTES.md` and `git rev-parse exp/lease-lab:<source>` at commit `23221ba`; at `HEAD` (`9f0378f`), only `report-sql/lease-report.sh` differs (commit `a2f023b`, 7 lines for defaults/header, mode `0755`). Every figure in `docs/knowledge/lease-experiment-method.md` (12%, 2x, 1.6–2.9 ms, 195/s, 235/s, 8–64, 14 ms, 5.5 ms, 4 vCPUs, 177/104/88 µs, 5/5.3 ms, 109%, `h0`, `r`, ceiling, 79.7%, 79.1–79.4%, 73.9%), in `REPORT.md` lines 6–10 and 145–151, and in `CALIBRATION-NOTE.md` lines 120–127 matches its named source (`CALIBRATION-NOTE.md`, `REPORT.md`, `lab-records/NOTES-E0.md`, `lab-records/NOTES-E3.md`, `report-sql/09-verdict.sql`). `e4.sql` reproduces `e7/isolation/e4-oracle-check.txt` identically (58 lines); `report-sql/lease-report.sh` runs from its new folder over `e7/tier-r-per-run.tar.gz` (`1-r6`) and reproduces `e7/r6/tables/01-calibration.csv` byte for byte (46 lines including header).
- **Extra `policyRun` changes (`test/e2e/c07_tierr_test.go`):** Neither change weakens an existing assertion for the five older hostile kinds. Alternating the observer tenant by `(round+i)%2 == 1` instead of `i%2 == 1` strengthens coverage: previously `make_temp` and `do_prepare` were only ever checked by tenant 2 (same-tenant carry-over) and never by tenant 1 (cross-tenant), while `hide_set`, `take_lock`, and `do_listen` were never checked for same-tenant carry-over; now all five older kinds (and all three new ones) are checked for both crossing and same-tenant carry-over across the two rounds. Failing the test on `r.hostileErrors != 0` strictly strengthens the test against silently failing hostile calls.
- **Fuzz targets (PLAN.md 6.6):** Not applicable; no fuzz target was added or modified.

## Mutation checks

All mutations were applied to a clean `git archive HEAD` copy in `/tmp/zgw-lease-lab-port-review1/mut/work` and tested with `GOFLAGS=-buildvcs=false MG_TIER_R=1 go test -count=1 -timeout 5m ./test/e2e/ ...`:

1. **M1 — Partial reset omitting `CLOSE ALL` and `RESET ROLE`** (`RESET ALL; DEALLOCATE ALL; UNLISTEN *; DO $$BEGIN PERFORM pg_advisory_unlock_all(); END$$; DISCARD TEMP` in `internal/proxy/lease.go`): **CAUGHT** (exit 1, 17.2 s). `TestTierRSessionStateNeverCrossesClients` failed (`21 dirty observations ... cursors 19 ... role_changed 18`); `TestTierRPoolSessionStateUnderEachReleasePolicy` failed in all four arms (`5 session-state crossings`, `1 hostile calls failed`).
2. **M2 — Partial reset omitting `RESET ALL` (keeping `RESET search_path`, `CLOSE ALL`, `RESET ROLE`, etc.)** in `internal/proxy/lease.go`: **CAUGHT** (exit 1, 16.3 s). `TestTierRSessionStateNeverCrossesClients` failed (`17 dirty observations ... secret read back 17`, all 7 catalog columns 0); `TestTierRPoolSessionStateUnderEachReleasePolicy` failed in all four arms (`4 session-state crossings`).
3. **M3 — Remove `do_cursor()` from `hostileCalls`** in `test/e2e/tierr_test.go`: **CAUGHT** (exit 1, 8.7 s). `TestTierRSessionAblationWithoutResetCrosses` failed naming `cursors` (`dirty-check column cursors never differed ...`).
4. **M4 — Remove `do_role()` from `hostileCalls`** in `test/e2e/tierr_test.go`: **CAUGHT** (exit 1, 12.7 s). `TestTierRSessionAblationWithoutResetCrosses` failed naming `role_changed`.
5. **M5 — Remove `do_custom(...)` from `hostileCalls`** in `test/e2e/tierr_test.go`: **CAUGHT** (exit 1, 9.4 s). `TestTierRSessionAblationWithoutResetCrosses` failed naming `mg_hostile.secret`.
6. **M6 — Remove `do_cursor()` and `do_role()` AND inject one `E:ERROR:42501` reply on `checker.Query(check)` (`round == 0 && i == 0`)** in `test/e2e/tierr_test.go`: **MISSED** (exit 0, 8.3 s). `TestTierRSessionAblationWithoutResetCrosses` passed with `cursors 1, role_changed 1` (Finding 2).
7. **M7 — Skip `DISCARD ALL` (send `SELECT 1`) only on the reset immediately following a query containing `do_custom`** in `internal/proxy/lease.go` and `internal/proxy/dbconn.go`: **MISSED by `TestTierRSessionStateNeverCrossesClients` and by both `always` arms of `TestTierRPoolSessionStateUnderEachReleasePolicy`** (14.8 s total; caught only by `on-switch/off` and `on-switch/on`, `2 session-state crossings`; `TestTierRSessionStateNeverCrossesClients` passed with `0 dirty observations`, and `always/off` and `always/on` passed with `crossings 0`) (Finding 1).

## Release It! checklist

This port adds no product code (`internal/` and `cmd/` are untouched); every anti-pattern is evaluated against the test code in `test/e2e/` and the kept record script `report-sql/lease-report.sh`:

- **Integration points:** Not applicable to product code (untouched); in `test/e2e`, new queries run through `RealClient.Query`, which already uses `realOpTimeout` deadlines on TCP/TLS sockets.
- **Blocked threads:** Not applicable to product code (untouched); `sessionRun` and `policyRun` close every dialed `RealClient` and `t.Cleanup` stops every `Gateway` and container.
- **Slow responses:** Not applicable here because no proxy request/cancel/timeout path changed.
- **Cascading failures:** Not applicable here because no database-outage or health-check code changed.
- **Chain reactions:** Not applicable here because no multi-instance shedding/queueing code changed.
- **Dogpile:** Not applicable here because no dial/reconnect/recovery path changed.
- **Self-denial attacks:** Not applicable here because no batch dial, expiry, flush, or retry code changed; the added tier R queries are serial on a 1-connection pool.
- **Force multiplier:** Not applicable here because no adaptive or automatic control loop was added or modified.
- **Unbalanced capacities:** Not applicable here because no producer/consumer buffer or queue was added or modified.
- **Scaling effects:** Not applicable here because no fleet-wide guarantee or exported metric label changed.
- **Users:** Not applicable here because no per-client or per-tenant resource limit changed (`hostileRole` is a single test-setup Postgres role).
- **Unbounded result sets:** Not applicable here because no relay or result-forwarding code changed (`dirty-check.sql` and `current_setting` each return one 1-row scalar result).
- **Compounds:** Not applicable here because no product paths were added or modified; the two test-harness defects (Findings 1 and 2) are in `test/e2e/` only.

## Observability

- **Product components (PLAN.md 7.6):** No component in `internal/` or `cmd/` was added or changed (`git diff --stat 98cee87..9f0378f -- internal cmd` is empty), so no new `Snapshot()`, exporter mapping, metric registry row, or health rule is owed.
- **Existing observability exercised and verified:** `TestTierRSessionStateNeverCrossesClients` and `TestTierRPoolSessionStateUnderEachReleasePolicy` still invoke `stopAndRead`, which runs `csvflush.RoundTrip`, `realConservation` (requests, leases, resets), and counter-movement checks (`resets_completed`, `leases_ended`, `tenant_switches`, `reseeds`). Running `scripts/csv-assert.sh` over the tier R run directory verified all 30 tier R SQL assertions pass, including `test/analysis/tierr-session-state/conservation.sql` and `resets.sql`; `G8 gen-docs` verified `docs/csv-schema.md` is current.

## What I ran

- `gofmt -l test/e2e/ && go build ./... && go vet ./...`: pass (clean output).
- `git diff --stat 98cee87..9f0378f -- internal/ cmd/ go.mod go.sum`: empty.
- Blob-hash check over all 44 copied files in `NOTES.md` against `23221ba` and `exp/lease-lab`: 44/44 matched (`mismatches=0`); only `report-sql/lease-report.sh` edited at `HEAD` (`a2f023b`, mode `0755`).
- `GATE_OUT=/tmp/zgw-lease-lab-port-review1/gate GATE_BASE=98cee87ce9552b6544ef74f67e778489250ecb28 GATE_ONLY=G0,G1,G2,G8,G9 scripts/gate.sh lease-lab-port`: overall pass (G0 tools 4s, gofmt 1s, build 4s, vet 2s, tools-vet 0s, tools-test 7s, tidy 1s; G1 detlint 6s, importlint 0s, boundlint 8s, paniccheck 1s; G2 comments 1s; G8 gen-docs 6s; G9 site 4s).
- `duckdb -bail -c "SET VARIABLE e4_root='...';" -f agent_work/chunks/c05-classifier/lease-exp/e7/isolation/e4.sql` over unpacked `per-run.tar.gz`: exit 0, 58 lines, empty `diff` vs `e4-oracle-check.txt`.
- `report-sql/lease-report.sh` over unpacked `e7/isolation/per-run.tar.gz` (exit 1, 12 tables, table 00 = 108 rows) and `e7/tier-r-per-run.tar.gz` `1-r6` with `server_extra=0` (exit 1, 12 tables, table 00 = 62 rows; `01-calibration.csv` byte-identical to `e7/r6/tables/01-calibration.csv`, 46 lines).
- `go test -count=1 -timeout 2m ./scripts/ -v`: pass (90.5 s).
- `go test -count=1 -timeout 2m ./internal/obs/simrun/ -run 'TestClassifierSQLFilesAreTheOnesInTheTree' -v`: pass (0.00 s). (Full `./internal/obs/simrun/` hit the 5m timeout under host load 19–31 and was not needed for this diff.)
- `go test -short -count=1 -timeout 3m ./test/e2e/ -run 'Session' -v`: pass (0.12 s; tier S `TestAblationWithoutResetSessionStateCrosses` passed).
- `MG_TIER_R=1 go test -count=1 -timeout 10m ./test/e2e/ -run 'TestTierRSession|TestTierRPoolSessionState' -v`: pass (12.8 s; 3 tests; exact counts matching `NOTES.md`).
- `MG_TIER_R=1 RUN_DIR=/tmp/zgw-lease-lab-port-review1/tierr-runs go test -count=1 -timeout 10m ./test/e2e/ -run '^TestTierR' -v`: pass (152.0 s; all 22 `TestTierR*` tests in `test/e2e` passed).
- `scripts/csv-assert.sh /tmp/zgw-lease-lab-port-review1/tierr-runs`: all 30 tier R SQL files passed (`ok:`, including `tierr-session-state/conservation.sql` and `resets.sql`; 88 scenarios total, confirming nothing under `agent_work/` is picked up).
- `MG_TIER_R=1 go test -count=5 -timeout 10m ./test/e2e/ -run 'TestTierRSession|TestTierRPoolSessionState' -v`: pass (47.2 s; 15/15 runs passed with identical counts on every run).
- Mutations M1–M7 in `/tmp/zgw-lease-lab-port-review1/mut/work`: results recorded under `## Mutation checks` above.
