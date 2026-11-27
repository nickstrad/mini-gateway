# Chunk 8 — Admission queues: prose pass notes

Date: 2026-11-27  
Branch: `work/c08-admission` (pre-prose commit `0291b4f`)  
Method: Clarity skill (`/root/.agents/skills/clarity/SKILL.md`, `references/edit.md`, `references/medium.md`, `references/longform.md`), Nick's making-of cut ruling (`/root/briefs/zgateway-making-of-cut-ruling.md`), and `tools/lint-rules/prose-paths.txt`.

## What was written

### A. Go code comments (comments only; AST-verified against `0291b4f`)

- `internal/admission/config.go`: package doc comment, doc comments on all exported types (`Time`, `RuleKind`, `LIFOMode`, `Plant`, `Config`), constants, `ErrConfig`, functions/methods, and inline invariant note on `pendingRefuseAt` / `pendingCap`.
- `internal/admission/ring.go`: inline comments on fixed-capacity ring order and `removeAt` vs planted `removeSwapTail`.
- `internal/admission/rule.go`: doc comments on `RuleState`, `QueueRule`, `TimeoutRule`, `ControlLaw`, `FollyRule`, `Law`, `IntervalOverSqrt`, and all exported methods/constants; inline comments on `ControlLaw.advance` deferral, 16-interval re-entry delta, 128-bit `fitsUnder`, and `FollyRule` constructor parity with upstream folly.
- `internal/admission/tenant.go`: doc comment on `LoadFromDelay`; inline comments on `loadGauge` (LIFO blindness; gauge only) and `tenantQueue.next` deadline coalescing.
- `internal/admission/queues.go`: doc comments on `Reason`, `Shed`, `TenantView`, `Dispatcher`, `OldestFirst`, `Queues`, and all exported methods/constants; inline comments on `advance`, `compactPending`, `statsFor` single `"other"` fold row, `Push` pre-overflow `wasEmpty` capture, `shedOldest` arriving-tenant guard, and `Pop`/`Unpop` rule-state save/restore.
- `internal/admission/snapshot.go`: doc comments on `TenantSnapshot`, `Snapshot`, `ShedTotal`, `Check`, `Snapshot`; inline comments on `depthLimit` (`"other"` bounded by total capacity) and `light()`.
- `internal/obs/admsrc/admsrc.go`: package doc comment and doc comments on `MetricShedding`, `CheckShedding`, `Metrics`, `Map`, `Source`, `HealthRules`.
- `internal/proxy/admission.go`, `seams.go`, `config.go`, `engine.go`, `lease.go`: doc comments on `AdmissionQueue` and its exported methods, `Engine.AdmissionSnapshot`, `ShedQueue`, `Config.AdmissionConfig`; inline comments on `at`, `queueTouched`, `Shutdown` sweep-remove-close ordering, and skipping direct lend for `ShedQueue`.
- `internal/config/queue.go`, `config.go`, `queue_export_test.go`: doc comments on `DefaultQueueRule`, `DefaultQueueLIFO`, updated `Queue` struct doc comment, and `ProxyCaps`.
- `test/e2e/c08_admission.go`: doc comments on exported constants, types, variables, and functions; inline comment on `PerKind` `KindRead` service-time placement.
- Test files added by chunk 8 (`internal/admission/{classic,property,fuzz,repair}_test.go`, `internal/obs/admsrc/{admsrc,fold}_test.go`, `internal/config/queue_test.go`, `internal/proxy/{admission,admission_shutdown}_test.go`, `test/e2e/{c08_admission,c08_h2h_script,c08_tierr}_test.go`): brief one-sentence comment per test/fuzz function stating the property proved.

### B. Book chapter and manifest

- `book/chapters/08-admission-queues.md` ("8. Two CoDels, measured against each other"): explains standing-queue bufferbloat, the three CoDel variants and why both `timeout` and `control-law` ship behind `QueueRule`, ring/deadline/engine wiring with real excerpts from `internal/admission/rule.go`, the exact committed head-to-head table and how one DuckDB query (`head-to-head.query`) builds it, what the numbers show for each arm (including `timeout`'s 7.6% idle-capacity cost at 1.2×), testing/fuzzing/tier S/tier R evidence, and stated limits. No making-of prose (no agents, seats, reviews, attacks, or repair rounds). Chapter 6a untouched; Chunk 7 pool not described as present.
- `book/manifest.json`: set Chapter 8's `status` to `live`.
- `book/index.md`: updated the live-chapters sentence to include Chapter 8 while keeping Chapter 7 planned.

### C. Other reader-facing pages

- `README.md`: added Chunk 8 to the landed-chunks list and added Chapter 8 to the live-chapters note and book links (keeping Chunk 7 and Chunk 9 as pass-through seams).
- `docs/knowledge/admission-queues.md`: checked against the final code and notes; no sentence was wrong, so left untouched per the brief.

## Narrowed claims and claims left out

- **Claim 4 (no starvation of capacity, narrowed):** written as a plain limit of the software in "Stated limits" #1 — the ≥95% capacity figure at 10× load holds for evenly spread arrivals whose inter-arrival gap is shorter than `queue.target` (~0.55 ms gap vs 5 ms target in the benchmark) and does not hold for clustered bursts separated by idle gaps exceeding the allowed wait (bounded per burst by `allowed_wait / service_time + 1`, e.g. 100/1,000 slots = 10%).
- **Tier R throughput numbers:** cited as observed post-repair rates against the same-host overflow-only baseline (81/s timeout, 86/s control-law vs 85/s baseline, 70% floor) rather than claiming a universal throughput figure.
- **Left out:** no claim of fair per-tenant scheduling (deferred to Chapter 9 on `Dispatcher`), no claim of dynamic pooling or circuit breaking (deferred to Chapter 7), and no claim of cross-seed exactness for the heavy overload scenarios beyond seed 1 (stated as limit #8).

## Verification commands run

- `gofmt -l .` (empty)
- `cd tools/detlint && go run ./cmd/astdiff <0291b4f-tree> .` (zero AST diffs; comments-only proof)
- `go build ./... && go vet ./...` (pass)
- `go test -count=1 ./internal/admission ./internal/obs/admsrc ./internal/proxy ./internal/config` (pass)
- `go test -count=1 -v -run '^(TestFirstBurst|TestCanary|TestLIFO|TestFolly|TestControlLaw)' ./internal/admission` (pass)
- `go test -count=1 -v -run '^(TestScenarioAdmissionFirstBurst|TestCanaryAdmissionFirstBurst)' ./test/e2e` (pass)
- `scripts/admission-head-to-head.sh --check` (exact match with `test/analysis/admission-head-to-head/head-to-head.md`)
- `scripts/site.sh` (site built cleanly; `book/08-admission-queues.html` present and linked from `book/index.html`)
- `go test -short -count=1 ./...` and path-rule check against `tools/lint-rules/prose-paths.txt` (pass)
