# Chunk 9 — Fairness: prose pass notes

Date: 2026-11-28  
Branch: `work/c09-fairness` (pre-prose commit `7c0488d`)  
Method: Clarity skill (`/root/.agents/skills/clarity/SKILL.md`, `references/edit.md`, `references/medium.md`, `references/longform.md`), Nick's making-of cut ruling (`/root/briefs/zgateway-making-of-cut-ruling.md`), and `tools/lint-rules/prose-paths.txt`.

## What was written

### A. Go code comments (comments only; AST-verified against `7c0488d`)

- `internal/admission/fair.go`: doc comments on all exported constants (`DefaultWeight`, `MaxWeight`, `MaxFairLimit`, `MaxWeights`, `FairPlantNoShareBound`, `FairPlantNoRoundRobinStep`, `FairPlantUnpopKeepsTurn`), types (`FairPlant`, `TenantWeight`, `FairConfig`, `Fair`, `FairSnapshot`), functions and methods (`WithDefaults`, `Validate`, `NewFair`, `Config`, `Limit`, `InFlight`, `TenantInFlight`, `States`, `ShareOf`, `Share`, `Pick`, `Queues.Fair`, `LeaseStart`, `LeaseEnd`, `SetLimit`, `Limit`, `Releasable`, `Held`, `InFlight`, `TenantInFlight`, `Depth`); inline comments on `reserved`, `releasable` deficit tracking, `Pick` save-before-unpop, and `takeTurn` even/odd turn ordering.
- `internal/admission/queues.go`, `snapshot.go`: comments only on the fairness additions (`TenantView.Turn`, `fairAccount` before deadline steps, newcomer turn `2*q.round-1`, `TenantSnapshot.InFlight`/`Share`/`AtShare`, `Snapshot.Fair`).
- `internal/proxy/fairness.go`: doc comments on `PlantFairStatementInFlight`, `Config.FairConfig`, `AdmissionQueue.LeaseStart`/`LeaseEnd`/`SetLimit`, `Engine.TenantLeases`/`TenantQueued`; inline comments on `fairRedispatch`, deterministic deduplication in `fairnessCheck`, and `checkoutDemand` gating below `pool.connections`.
- `internal/proxy/corepool.go`, `engine.go`, `lease.go`: brief inline comments on `CorePool.Checkout` demand refresh, `Engine.corePump` releasable demand, `aq.installFair`, and lease-lifetime tracking in `Engine.lend`.
- `internal/config/fairness.go`: doc comments on `Fairness` and `FairWeight`.
- `internal/obs/admsrc/fair.go`: doc comments on `HeldLimit` and `HeldShare`.
- `test/e2e/c09_fairness.go`: doc comments on all exported constants, types, variables, and functions.
- Test files added by chunk 9 (`internal/admission/{fair,fair_repair,fair_model}_test.go`, `internal/proxy/fairness_test.go`, `internal/config/fairness_test.go`, `internal/obs/admsrc/fair_test.go`, `test/e2e/{c09_fairness,c09_tierr}_test.go`): brief comment per test/fuzz function stating the property proved (and a type doc comment on `fmModel`).

### B. Book chapter, manifest, and index

- `book/chapters/09-fairness.md` ("9. Fairness that binds only when it must"): explains why per-tenant queues alone do not protect a quiet tenant from a flooder or held transactions, the five-rule design and alternatives turned down, real excerpts of `Fair.reserved`, `Fair.Pick`, and `Fair.releasable`, engine wiring and observability, core/tier S/tier R test evidence with real command outputs run in this pass, and all eleven stated limits. Describes the software only (no making-of prose).
- `book/manifest.json`: set Chapter 9's `status` to `live`.
- `book/index.md`: updated the live-chapters sentence to cover Chapters 1 through 9 (including 6a, 7, 8, and 9).

### C. Other reader-facing pages

- `docs/knowledge/fairness.md`: rewritten in Clarity rewrite mode, keeping every symbol, path, number, heading, caveat, and chunk-10 handoff detail intact.
- `README.md`: added Chunk 9 to the landed-chunks list, added Chapter 9 to the live-chapters note and book links, and removed Chunk 9 from the remaining pass-through seams list.

## Narrowed claims and claims left out

All narrowed claims from `NOTES.md` and `ADVERSARIAL.md` are stated directly as limits or exact behaviour of the software (without process history):

- **When the last free connection is held back (Claim 3):** stated explicitly that at most one connection idles, only when it is the last free lease, no waiting tenant is below its share, and some active non-waiting tenant holds fewer leases than its share; floor-rounded remainders with no active below-share tenant are lent by round robin.
- **Within-group round robin (Claim 4):** stated that round-robin starvation bounds apply within a priority group and that an over-share tenant can wait while below-share tenants arrive until one of its own leases ends or its queue rule sheds the request.
- **Noisy-neighbour wait bound and scenario shapes (Claims 5 and 6):** stated that the general bound is one flooder lease (including think time for open transactions, plus reset); the 2× p99, exact standalone outcomes, and >99% tier R figures belong to the stated scenario shapes, and quiet shedding begins once a flooder lease reaches `queue.interval` (or `queue.target` if quiet arrivals form a standing queue), backed by the 40 ms / 90 ms / 150 ms tests run in this pass.
- **Many-tenant capacity condition (Claim 7):** stated that quiet tenants keep standalone outcomes while their combined demand stays within queue-wait and total-queue capacity (`n <= 18` in the 2-connection scenario; 16 synchronized verified).
- **No preemption of held transactions (Claim 8):** stated that shares bound new lends only and never revoke open leases when a tenant joins or `SetLimit` lowers the limit.
- **Checkout extra dial at `limit == pool.connections`:** stated in limit #9 that `CorePool.Checkout` refreshes demand only when `limit < pool.connections`, so at `limit == pool.connections` `Pool.Checkout` can still dial one connection within `pool.connections` that closes once at `pool.idle_timeout`.
- **Left out:** no claim of bounding database CPU/I/O (only database concurrency/leases), no claim of global cross-instance shares (fleet row stated as per-instance), and no claim of adaptive limiting (explicitly left to Chapter 10).

## Verification commands run

- `gofmt -l .` (empty)
- `go build ./... && go vet ./...` (pass)
- `cd tools/detlint && go run ./cmd/astdiff <7c0488d-tree> .` (zero AST diffs; comments-only proof)
- `go test -count=1 -timeout 5m ./internal/admission ./internal/proxy ./internal/config ./internal/obs/admsrc` (pass: 9.34s, 0.31s, 0.15s, 0.49s)
- `go test -count=1 -v -run '^(TestFairDispatcher|TestRoundingRemainders|TestTheLastLease|TestATenantAtItsShare|TestTheShareBoundsNewLends|TestCanaryNo|TestCanaryUnpop|TestPlantedUnpop)' ./internal/admission` (pass, 7.23s)
- `go test -count=1 -v -timeout 5m -run '^(TestScenarioFairNoisyNeighbour$|TestScenarioFairNoisyNeighbourAblation$|TestScenarioFairNarrowed|TestScenarioFairHeldTransactions$|TestScenarioFairManyTenants$|TestCanaryFair)' ./test/e2e -seed=1` (pass, 49.78s)
- `go test -count=1 -v -timeout 3m -run '^TestScenarioFairResultsEqualDirect$' ./test/e2e -seed=1` (pass, 2.15s)
- `MG_TIER_R=1 go test -count=1 -v -timeout 3m -run '^TestTierRFairNoisyNeighbour$' ./test/e2e` (pass, 20.86s; quiet 100/100, p99 56.77 ms vs 50.92 ms alone; flooder 994 ok, 39,064 shed)
- `scripts/site.sh /tmp/opencode/c09-site` (built 28 pages cleanly; `book/09-fairness.html` listed in `pages.txt` and linked in `book/index.html`)
- Path check of `git diff --name-only --no-renames 7c0488d HEAD` against `tools/lint-rules/prose-paths.txt` (pass)
