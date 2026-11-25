VERDICT: NITS

## Findings

1. **MINOR — `internal/obs/simrun/imports_test.go:54,79`** (`TestOneRawExportImportRule` does not mirror the adapter-to-`wiresrc`/`pgwire` or `pgwire`-to-adapter rules)
   - **What is wrong:** `TestOneRawExportImportRule` added `"internal/obs/wiresrc"` to the source-side loop (line 54), but did not add `"internal/pgwire"` there and did not add `"internal/obs/wiresrc"` or `"internal/pgwire"` to the adapter-side loop (line 79: `[]string{"internal/sim", "internal/obs/simsrc", "internal/testclient"}`). `agent_work/chunks/c04-wire-sessions/NOTES.md:374` states that `simrun/imports_test.go` checks the `wiresrc` rule too.
   - **How I know:** Adding an import of `internal/obs/wiresrc` in `internal/obs/csvflush/writer.go` in a scratch copy failed `importlint` (`rc=1`, `tools/lint-rules/imports.txt` line 36), while `go test -count=1 -run '^TestOneRawExportImportRule$' ./internal/obs/simrun` passed (`rc=0`).
   - **What a fix must achieve:** Add `"internal/pgwire"` to the source list at line 54 and `"internal/obs/wiresrc"`, `"internal/pgwire"` to the adapter deny list at line 79 so `TestOneRawExportImportRule` fails on both directions just as `importlint` does.

2. **MINOR — `test/analysis/wire-*/*.sql:1`** (five queried metrics are absent from `-- requires:`)
   - **What is wrong:** `handshake-failures.sql` compares `wire.client_tls_completed` against `workload.startup_handshakes` and `wire.database_tls_completed` against `fakepg.handshakes`, and `tls-requests.sql` compares `wire.database_tls_requests` against `fakepg.tls_answered` and `workload.startup_tls_answered` against `wire.client_prestartup_requests`, without listing those five metrics in any file's `-- requires:` header. If both sides of a `coalesce(..., 0) <> coalesce(..., 0)` comparison (e.g. `wire.client_tls_completed` and `workload.startup_handshakes`) were absent from `metrics.csv`, the query would pass silently. (`docs/knowledge/wire-sessions.md:90-91` documents keeping `-- requires:` short for DuckDB startup cost, and `TestEveryDeclaredMetricIsEmittedAndEveryEmittedOneDocumented` guards emission in Go.)
   - **How I know:** Inspection of `-- requires:` vs `metric =` / `metric IN` predicates across all 57 SQL files under `test/analysis/wire-*/`.
   - **What a fix must achieve:** Either list the remaining queried metrics in `-- requires:` (or batch the per-metric checks in `csv-assert.sh` into one DuckDB invocation so listing every queried metric is free).

## Mutation checks

All run in a scratch copy under `/tmp/zgw-c04-wire-sessions-review3/repo` (and flushed seed-99 runs under `/tmp/zgw-c04-wire-sessions-review3/sweep`):

1. **M1 — Drop a `WireSnapshot` field from mapping:** Replaced `c.CancelAfterTLS` with `0` in `internal/obs/wiresrc/wiresrc.go:99`. **Caught** by `go test -count=1 ./internal/obs/wiresrc` (`TestMappingMovesEveryMetricAndExportsEveryField`: `wire.client_cancel_after_tls never moves` and `snapshot fields not exported: [.Client.CancelAfterTLS]`).
2. **M2a — Emitted but undeclared:** Removed `reg.Declare(obs.ComponentWire, wiresrc.Metrics()...)` from `internal/obs/catalog/catalog.go:27`. **Caught** by both `./internal/obs/catalog` (`TestCSVSchemaDocumentIsGeneratedFromTheCode`: `docs/csv-schema.md is stale`) and `./internal/obs/simrun` (`TestEveryDeclaredMetricIsEmittedAndEveryEmittedOneDocumented`: `wire.client_auth_failures exported but not in the catalog`).
3. **M2b — Declared but never emitted:** Added `obs.CounterDesc("phantom_metric", ...)` to `wiresrc.Metrics()`. **Caught** by `./internal/obs/wiresrc` (`obs: declared metric not emitted: wire.phantom_metric`) and `./internal/obs/catalog` (`docs/csv-schema.md is stale`).
4. **M3 — Wrong mapped value vs snapshot:** Changed `c.StartupsCompleted` to `c.StartupsCompleted+1` in `wiresrc.go:84`. **Caught** by `TestScenarioWireFlush/wire-startup-tls/seed=1` (`checkWireCounts`: `wire.client_startups_completed ... exported 9, snapshot 8`).
5. **M4 — Corrupted flushed CSV on disk (round trip & byte-identity):**
   - Mutating an earlier flush in `metrics.csv` before `a.Check()` was **caught** by the byte-identical-files check in `TestScenarioWireFlush/wire-startup-tls/seed=1` (`flushed files differ between two runs of one seed`).
   - Mutating the final flush (`8` -> `9`) before `a.Check()` was **caught** directly by `a.Check()` (`csv round trip: csvflush: flushed files differ from the snapshot: wire.client_startups_completed ... file counter 9, snapshot counter 8`).
6. **M5 — SQL wrong-number mutations on flushed seed-99 runs (`scripts/csv-assert.sh`):**
   - `M5a` (`wire-db-bad-certificate/startup-failures.sql`, `database_startups_failed{tls-failed}` = 2): **Caught** (`rc=1`, 1 violating row: `injected 1, failed 2`).
   - `M5b` (`wire-startup-tls/startup-failures.sql`, uninjected `fakepg.faults{pg.reset}` = 1): **Caught** (`rc=1`, 1 violating row: `a fault this scenario does not inject`).
   - `M5c` (`wire-startup-tls/tls-requests.sql`, `client_prestartup_requests{mode=gss,outcome=N}` = 1): **Caught** (`rc=1`, 1 violating row: `1 GSS or cancel requests counted, none sent`).
   - `M5d` (`wire-startup-tls/tls-requests.sql`, `database_tls_answers{S}` = 99): **Caught** (`rc=1`, 1 violating row: `database answer S: proxy 99, fakepg 8`).
   - `M5e` (`wire-db-bad-certificate/handshake-failures.sql`, `database_tls_failed{failed-verification}` = 0): **Caught** (`rc=1`, 1 violating row: `injected 1, failed 0`).
   - `M5f` (`wire-startup-tls/handshake-failures.sql`, `database_tls_completed{1.3}` = 99): **Caught** (`rc=1`, 1 violating row: `wire.database_tls_completed 99 != fakepg.handshakes completed 8`).
7. **M6 — Import rules:**
   - `M6a` (import `internal/obs/csvflush` in `internal/obs/wiresrc/wiresrc.go`): **Caught** by both `importlint` (`rc=1`) and `TestOneRawExportImportRule` (`rc=1`).
   - `M6b` (import `internal/obs/wiresrc` in `internal/obs/csvflush/writer.go`): **Caught** by `importlint` (`rc=1`), **missed** by `TestOneRawExportImportRule` (`rc=0`, Finding 1).
8. **M7 — Moved Go assertions in `wireworld.Scenario.Verify`:** Removed fault injection (`Answer: 'N'`) from `wire-db-tls-refused` in `wireworld.go`. **Caught** by both `TestScenarioWireStartup/wire-db-tls-refused/seed=1` (`./internal/pgwire`) and `TestScenarioWireFlush/wire-db-tls-refused/seed=1` (`./internal/obs/simrun`).

## Observability

- **`Snapshot()` and mapping:** Every field of `pgwire.WireSnapshot` (21 client metrics + 26 database metrics = 47 `wire.*` metrics, covering all scalar counters, gauges, high-water marks, and fixed-size enum arrays) is declared in `wiresrc.Metrics()`, registered in `catalog.Registry()`, and generated into `docs/csv-schema.md` (`scripts/gen-docs.sh --check` passes). Chunk 4's simulator counters (`fakepg.AuthSnapshot`, `workload.StartupCounters`) are also mapped in `internal/obs/simsrc/simsrc.go` and covered by `TestMappingsMoveEveryMetric`.
- **Counters move & conservation:** `TestMappingMovesEveryMetricAndExportsEveryField` and `TestZeroSnapshotEmitsEveryLabelValue` pass; `TestEveryDeclaredMetricIsEmittedAndEveryEmittedOneDocumented` passes across all baseline + 19 wire runs; `wireworld.Scenario.Verify` asserts `WireSnapshot.Check()` (conservation) and non-zero counters on every run in both `TestScenarioWireStartup` and `TestScenarioWireFlush`.
- **CSV flush, round trip, replay, SQL:** `TestScenarioWireFlush` runs `Run.Check()` (CSV round trip, JSON decode, Prometheus grammar, log count, final seq) and byte-identical-files comparison on pinned seeds (1, 42) and extra seed 99. All 19 wire scenarios × 3 SQL files (`startup-failures.sql`, `tls-requests.sql`, `handshake-failures.sql`) pass under `scripts/csv-assert.sh` in root mode alongside the baseline SQL files (62 SQL files, 0 failures) and fail on planted wrong numbers (`TestWireSQLCatchesPlantedNumbers` and M5).
- **Scope check:** `git diff --name-only 35c58c3ec92503b0de1588c00266467218019bab..HEAD -- internal/pgwire/` touches only test/harness files (`client_analysis_test.go`, `client_canary_test.go`, `client_sim_test.go`, `pgwiretest/wireworld/wireworld.go`); no product behaviour in `internal/pgwire` changed. Line-by-line comparison of `35c58c3:internal/pgwire/client_sim_test.go` against `internal/pgwire/pgwiretest/wireworld/wireworld.go` confirms every pre-move Go assertion in `Verify` still runs unweakened.

## What I ran

- `gofmt -l . && go build ./... && go vet ./...` — clean, exit 0.
- `scripts/gen-docs.sh --check` — `NOTE: docs/csv-schema.md is current`, exit 0.
- `GATE_ONLY=G0,G1,G2,G3,G8,G10 GATE_OUT=/tmp/zgw-c04-wire-sessions-review3/gate scripts/gate.sh c04-wire-sessions` — `overall=pass`:
  - `G0` tools 1s, gofmt 0s, build 1s, vet 0s, tools-vet 1s, tools-test 3s, tidy 0s — all pass.
  - `G1` detlint 2s (16 package patterns), importlint 0s, boundlint 0s, paniccheck 0s — all pass.
  - `G2` comments 1s (baseline `0b86db71e99f2a5aa8abbf0da822418fa3cd4e80`) — pass.
  - `G3` unit (`go test -count=1 ./...`) 170s, race 16s — pass.
  - `G8` gen-docs 1s — pass.
  - `G10` extra (`agent_work/chunks/c04-wire-sessions/gate/extra.sh`) 113s (SQL assertions on 19 wire scenarios, early-bytes canary, 19 cross-process digests) — pass.
- `RUN_DIR=/tmp/zgw-c04-wire-sessions-review3/sweep DST_SEED=99 go test -count=1 -run '^TestScenarioWireFlush$' ./internal/obs/simrun` (9.97s, pass) and `DST_SEED=99 go test -count=1 -run '^TestScenarioWireStartup$' ./internal/pgwire` (0.77s, pass), followed by `scripts/csv-assert.sh` across all 19 seed-99 wire run dirs × 3 SQL files (all 19 passed).
- `RUN_DIR=/tmp/zgw-c04-wire-sessions-review3/sweep5 scripts/sweep.sh -seeds 5` (271 seed runs passed, 0 failures) followed by `scripts/csv-assert.sh /tmp/zgw-c04-wire-sessions-review3/sweep5` (48 run directories, 23 scenarios, `62 SQL files run, 0 failures; v1.5.5`).
- Mutation checks M1–M7 in `/tmp/zgw-c04-wire-sessions-review3/`.


NITS CLOSED by the orchestrator, 2026-11-25: this is the scoped review of the integration step. Both MINOR items (the Go import-rule test does not mirror two directions that importlint already enforces; five metrics queried by the wire SQL files are missing from their `-- requires:` headers) falsify no claim and are carried forward as required fixes in the chunk 6 brief (section "Carried from chunk 4").
