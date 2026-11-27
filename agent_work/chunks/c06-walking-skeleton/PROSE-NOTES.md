# Chunk 6 — Walking skeleton: prose pass notes

Prose seat: Gemini Neon, 2026-11-26, branch `work/c06-walking-skeleton` (on top of `5ac7dbb`).

## What was written

### 1. Go comments (comments only; AST-identical to `5ac7dbb`)

- **`internal/proxy/`**: package doc comment and doc comments on all exported types, constants, errors, functions, and methods in `api.go`, `config.go`, `seams.go`, `engine.go`, `contracts.go`, `snapshot.go`; focused inline invariant comments in `engine.go` (`clientList` determinism, deadline rule in `Step`), `client.go` (mid-message cancel regardless of phase, no `FATAL 08P01` splice after partial reply), and `lease.go` (`d.trailing` unread flag, rule G8 in `reachedFor`, trailing-byte rejection in `judgeReset`); per-test property comments in `deadline_test.go`, `engine_test.go`, `midmessage_test.go`, `reached_test.go`, `splice_test.go`, `unread_test.go`.
- **`internal/proxy/proxysim/`**: package doc comment, exported identifiers, and inline comments on `drain` and rule G8 in `gone` (`proxysim.go`, `world.go`); per-test comments in `flush_test.go`, `smoke_test.go`.
- **`internal/shell/`**: package doc comment, exported identifiers, and inline invariant comments on `engineExit` shutdown-before-publish, `deliver`/`readLoop` per-connection `inflight` read credit, exact 8-byte/1-byte unbuffered reads, and `readerExit` waiting on `<-c.writerDone` before `EvDBWriteRefused`/`EvDBClosed` (`shell.go`, `engine.go`, `io.go`, `admin.go`, `stats.go`, `contracts.go`); per-test comments in `contracts_test.go`, `credit_test.go`, `dialcancel_test.go`, `order_test.go`, `shutdown_test.go`, `synctest_test.go`, `tcp_test.go`.
- **`internal/config/` and `internal/config/certtest/`**: package doc comments and all exported types/constants/functions (`config.go`, `validate.go`, `build.go`, `certtest.go`), plus an inline note on `validity()` starting in 1990 for `testing/synctest`; per-test comments in `config_test.go`, `zerocap_test.go`.
- **`cmd/mini-gateway/`**: package doc comment (`main.go`) and per-test comments in `catalog_test.go`, `main_test.go`.
- **`internal/obs/enginesrc/` and chunk 6 additions under `internal/obs/`**: package doc comment and exported symbols in `enginesrc.go`, `simrun/custom.go`; per-test comments in `enginesrc_test.go`, `collector_repair_test.go` (the two chunk 6 tests), and `csvflush/repair_test.go` (the chunk 6 test).
- **`internal/pgwire/`**: doc comments on the three newly added `ErrRow*` constants and `AppendRowFatal` in `errors.go`; per-test comments in `client_tenants_test.go`; updated "modal" to "the table's" in `client_tenants.go` and `client_mock_test.go` per `NOTES.md` (since carried item 4.3 requires uniform verifier shapes).
- **`test/e2e/`**: package doc comment and exported identifiers across `spec.go`, `scenarios.go`, `checks.go`, `driver.go`, `simtier.go`, `tamper.go`, `opfuzz.go`, `realclient.go`, `realenv.go`, `realtier.go`, `workloadsnap.go`; per-test comments across `canary_test.go`, `driver_test.go`, `fuzz_test.go`, `scenario_test.go`, `shrunk_test.go`, `tierr_test.go`.

### 2. Book chapter and reader-facing pages

- **`book/chapters/06-walking-skeleton.md`** (title: *"The first working proxy, and lending connections by the transaction"*): covers the walking-skeleton scope, what is deliberately pass-through vs built, the sans-I/O architecture and alternatives turned down, the ten stage seams, transaction-lease lifecycle and bounded streaming back pressure, unconditional `DISCARD ALL` reset and ablation numbers (both tiers), the goroutine shell, TLS exact-read rules, the two shell bugs only tier R caught, JSON config and building/running `cmd/mini-gateway`, tier S / tier R testing, `make chaos`, `FuzzEngineOps` + shrunk scenario, Review 1's two blockers and the two repair rules (deadline rule and rule G8), Astra's adversarial results (43 held / 3 broken → 44 pass / 2 settled, including the content-filter cut and single re-dispatch), Review 2 PASS, gate status (no `result.json` after repair; last full gate run `dbc896b`), narrowed Claim 13, and all stated limits.
- **`book/manifest.json`**: set chapter 6 `status` to `live`.
- **`book/index.md`**: updated live chapter summary to Chapters 1 through 6.
- **`README.md`**: updated status for Chunk 6 and Chapter 6, added links to `docs/timeout-budget.md`, `docs/capacity-table.md`, and `docs/extension-seams.md`, added verified commands to build and run `cmd/mini-gateway`, updated `make chaos` (now live) and the fuzz target count (16 targets).
- **`docs/index.md`**: updated status for Chunk 6 and added Chapter 6 links.
- **`docs/extension-seams.md`**: lightly polished the intro paragraph and linked directly to `PLAN.md` section 4 while keeping every operational table untouched.

## Claims narrowed or left out for lack of evidence

- **Claim 13 narrowed:** written explicitly as narrowed in `NOTES.md` and `ADVERSARIAL.md` — explicit zero or omitted caps take their positive defaults (`docs/capacity-table.md`); negative or out-of-range caps fail closed naming the key.
- **Pass-through mechanisms not claimed built:** explicitly stated that the dynamic pool, circuit breaker, idle-in-transaction 5 s rule, active cancel/rollback, `on-switch` reset, `lease.reseed`, CoDel / adaptive LIFO admission, per-tenant fairness queues, adaptive concurrency limit, rate gate, coalescer, retries, and fleet drain orchestration are not built in Chunk 6.
- **No post-repair `gate/result.json`:** stated truthfully that no `result.json` was committed after the repair round; quoted the last full build-stage gate run (`dbc896b`) from `NOTES.md` and the gate logs (`G4-sweep.log`, `G5-fuzz.tsv`, `G6-tier-r.log`, `G7-csv.log`, `G10-extra.log`).
- **Tier R ablation and real-server numbers:** Docker is not invoked during the prose pass; every tier R figure quoted (`0 dirty over 15 hostile leases`, `32 dirty` without reset, `1.4 MB` vs `64 KiB` pre-fix slow-reader high-water, `800/800` `pgbench`, `23 passed` on Postgres 17.11) is attributed directly to `NOTES.md` and `gate/G6-tier-r.log`.
- **`mini-gateway -config internal/config/testdata/example.json -check`:** tested directly and found to fail closed with exit 2 because `example.json` requires `server.crt`/`server.key` (not committed in `testdata/`); documented accurately in both Chapter 6 and `README.md` instead of presenting a broken copy-paste command.

## Commands run

- `gofmt -l .` (clean after formatting `internal/proxy/seams.go`)
- `go build ./... && go vet ./...`
- `go build -o /tmp/mini-gateway ./cmd/mini-gateway`
- `/tmp/mini-gateway -help`
- `go run ./cmd/mini-gateway -verifier -password example-pw`
- `go run ./cmd/mini-gateway -config internal/config/testdata/example.json -check` (verified fail-closed exit 2 on missing cert/key)
- `/tmp/mini-gateway -config /tmp/mg-example-off.json -check` (verified exit 0, fingerprint `4a827a9f43a1174e`)
- `go test -count=1 -v -run '^(TestCanary|TestAblation|TestShrunk|TestResultsThroughTheProxyEqualDirectOnEveryPinnedSeed)' ./test/e2e`
- `go test -count=1 -v -run '^(TestFragmentedQuery|TestBytesAfter|TestResetReplyAtItsDeadline|TestDatabaseClosedBefore|TestWriteRefusedWithZeroBytes)' ./internal/proxy`
- `go test -race -count=1 ./internal/shell/... ./internal/config/...`
- `CHAOS_SEEDS=3 CHAOS_TIER_R=0 RUN_DIR=/tmp/c06-prose-chaos scripts/chaos.sh`
- `scripts/fuzz-plant.sh`
- `scripts/site.sh /tmp/c06-prose-site` (verified `book/06-walking-skeleton.html` built and listed in `book/index.html` and `pages.txt`)
- `scripts/gen-docs.sh --check`
- `go test -count=1 ./...`
- `GATE_ONLY=G0,G1,G2,G8,G9 GATE_STAGE=final GATE_PREPROSE=5ac7dbb GATE_OUT=/tmp/c06-prose-gate scripts/gate.sh c06-walking-skeleton` (after committing)
