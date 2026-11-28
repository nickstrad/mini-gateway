# Chunk 12 — Retry policy: prose pass notes

Date: 2026-11-28  
Branch: `work/c12-retry` (pre-prose commit `25b4c13`)  
Method: Clarity skill (`/root/.agents/skills/clarity/SKILL.md`, `references/edit.md`, `references/medium.md`, `references/longform.md`), Nick's making-of cut ruling (`/root/briefs/zgateway-making-of-cut-ruling.md`), and `tools/lint-rules/prose-paths.txt`.

## What was written

### A. Go code comments (comments only; AST-verified against `25b4c13`)

- `internal/retry/config.go` and `internal/retry/retry.go`: package doc comment, doc comments on every exported type, constant, variable, function, and method (`Time`, `Kind`, `KindOff`, `KindPreForwardOnce`, `NumKinds`, `ParseKind`, `TenthsPerToken`, `DepositTenths`, `WithdrawTenths`, `DefaultCapTokens`, `DefaultFloorTokens`, `MaxTokens`, `MaxAttempts`, `Plant`, all six `Plant*` constants, `Config`, `ErrConfig`, `WithDefaults`, `Validate`, `Share`, `Failure`, `Reason`, all `Reason` constants, `Decision`, `Policy`, `Snapshot`, `RefusedTotal`, `Check`, `New`, `Off`, `PreForwardOnce` and methods), plus inline comments explaining why omitted floors clamp only against positive caps, why `Deposits` starts at `Floor`, why `Snapshot.Check` skips budget bounds for non-product kinds, and why `InTx` precedes `Reached` with deadline equality refusing.
- `internal/retry/retrytest/everything.go`: package doc comment and doc comments on `EverythingAttempts`, `Everything`, `KindEverything`, `NewEverything`, and methods.
- `internal/obs/retrysrc/retrysrc.go`: package doc comment and doc comments on `MetricLevel`, `Metrics`, `Map`, and `Source`.
- `internal/config/retry.go`: doc comments on `DefaultRetryPolicy`, `Retry`, and `RetryFloorPerInstance`, plus inline comments on omitted-floor clamping and skipping fleet floor division when the policy is off.
- `internal/pgwire/errors_retry.go`: doc comments on `ProxyRetry`, `ProxyRetryNever`, `ProxyRetryWhenNotReached`, `ErrRowID.ProxyRetry`, and `ProxyRetry.String`.
- `internal/proxy/retry.go`, `matrix.go`, `lease.go`, and `seams.go`: doc comments on `RetryEnd`, all `RetryEnd*` constants, `RetryCore`, `NewRetryCore`, `CoreBreaker.Open`, `ClosedBreaker.Open`, `Engine.RetrySnapshot`, `MatrixRow`, `Retried`, `RetriedCell`, `FaultMatrixRows`, `FaultMatrixMarkdown`, and updated `RetryPolicy` comment in `seams.go`; inline comments on deterministic ID sorting and non-refunded tokens in `refuseRetriesBehindOpenBreaker`, queue re-entry in `rerun`, dispatch breaker re-check in `dispatch`, and offer-before-failure ordering in `statementLost`.
- Test files added by chunk 12 (brief per-test comments stating what each test proves): `internal/retry/canary_test.go`, `internal/retry/retry_test.go`, `internal/obs/retrysrc/retrysrc_test.go`, `internal/config/retry_test.go`, `internal/config/join_test.go` (chunk 12's two join tests), `internal/pgwire/errors_retry_test.go`, `internal/admission/fair_quiesce_test.go`, `internal/proxy/matrix_test.go`, `internal/proxy/retry_internal_test.go`, `internal/proxy/retry_canary_test.go`, `internal/proxy/retry_repair_test.go`, `internal/proxy/retry_test.go`, `internal/proxy/join_retry_fair_test.go`, and `test/e2e/{c12_client,c12_relay,c12_retry,c12_retry_test,c12_c09_join_test,c12_tierr_test}.go` (including exported scenario types/helpers in `c12_client.go`, `c12_relay.go`, `c12_retry.go`, and `c12_c09_join_test.go`).

### B. Book chapter, manifest, and index

- `book/chapters/12-retry-policy.md` ("12. Retries, off by default"): explains why blind proxy retries cause duplicate writes and retry storms, the single-fact `Reached == work.No` rule, the integer tenths-of-a-token budget, queue/deadline/breaker/fairness composition, real code excerpts from `internal/retry/retry.go` and `internal/proxy/lease.go`, completed contracts (`docs/error-contract.md`, `docs/fault-matrix.md`), configuration and observability, real command outputs and DuckDB seed-1 numbers across tier S and tier R, and all 11 stated limits. Describes the software only (no making-of prose; Chapters 10 and 11 are not referred to as existing).
- `book/manifest.json`: set Chapter 12's `status` to `live`.
- `book/index.md`: updated the live-chapters sentence to include Chapter 12 (leaving Chapters 10 and 11 planned).

### C. Other reader-facing pages

- `docs/knowledge/retry-policy.md`: rewritten in Clarity rewrite mode, keeping every symbol, path, number, heading structure, and caveat intact.
- `README.md`: added Chunk 12 to the landed-chunks list, added Chapter 12 to the live-chapters note and book links, added `docs/fault-matrix.md` to the generated contracts line, and removed retries (Chunk 12) from the pass-through stage seams list.

## Narrowed claims and claims left out

All narrowed claims from `NOTES.md` and `ADVERSARIAL.md` are stated directly as exact software behaviour and limits (without process history):

- **Budget bound and burst after a quiet period (Claim 5):** stated as `10 × retries <= attempts + floor_in_tenths` from startup and `10 × Δretries <= Δattempts + credit_carried_in <= Δattempts + cap_in_tenths` over any window, explicitly noting that stored credit does not expire with time and that the largest retry burst after a quiet period is `retry.bucket_cap` tokens.
- **What a waiting retry holds (Claim 7):** stated that a waiting retry holds no database connection and holds one place in its tenant's admission queue, bounded by `arrival + timeouts.statement` (tested at equality).
- **Tier R produces zero retries:** stated with exact observed numbers (`off`: 2 offered / 0 retries; `on`: 4 offered / 0 retries) and the socket-level reason (`pg_terminate_backend` on idle backends is absorbed by check-before-reuse, and on active backends races kernel TCP writes to produce `08007` rather than a zero-byte write refusal).
- **Composition with fairness:** stated that a granted retry pushes to the tail of its tenant's queue and spends a new round-robin turn without refunding the first attempt's turn, so a tenant gains no service through retries and pays one extra turn per retried request; held-back retries trigger no pool dial (`Queues.Releasable()`).
- **Forward count bound:** stated as `attempts <= forwards <= attempts + retries - retry_ends{outcome="breaker"}`, noting the upper bound is exact only when no granted retry leaves the queue before forwarding.
- **Left out:** no claim of time-decaying credit, no claim of zero queue-slot usage while waiting, no claim of tier R exercising proxy retries directly, no reference to Chapters 10 or 11 as existing, and no multi-instance fleet measurement (deferred to its own chapter).

## Commands run

- `go test -count=1 -v ./internal/retry ./internal/obs/retrysrc` (pass: `1.442s`, `0.013s`)
- `MG_FUZZ_PLANT=retry-withdraw-first go test -count=1 -run '^FuzzRetryOps$' ./internal/retry` (expected fail in `0.008s` on `FuzzRetryOps/seed#2`: `10 tenths withdrawn for 0 retries`)
- `GOMAXPROCS=2 RUN_DIR=/tmp/opencode/c12-prose-run go test -count=1 -timeout 5m -v -run '^(TestScenarioRetry$|TestCanaryRetry|TestScenarioRetryStormTwoTenantsOfUnequalWeight$|TestScenarioRetryRerunsKillRowsAndResultsWithThePolicyOn$)' ./test/e2e -seed=1` (pass: `42.700s`)
- `scripts/csv-assert.sh /tmp/opencode/c12-prose-run/retry-storm test/analysis/retry-storm/*.sql` and `scripts/csv-assert.sh /tmp/opencode/c12-prose-run/retry-no-double-apply test/analysis/retry-no-double-apply/*.sql` (3 SQL files each, 0 failures; DuckDB v1.5.5)
- DuckDB seed-1 summary query over `/tmp/opencode/c12-prose-run/*/*/*/metrics.csv` (recorded in the chapter's seed-1 table)
- `MG_TIER_R=1 RUN_DIR=/tmp/opencode/c12-prose-tierr go test -count=1 -v -timeout 3m -run '^TestTierRRetryStorm$' ./test/e2e` (pass: `33.777s`; `off`: 30 kills, 2,330 ops, 2,328 ok, 705/705 after recovery, 2 offered, 0 retries; `on`: 28 kills, 3,673 ops, 3,669 ok, 1,046/1,046 after recovery, 4 offered, 0 retries) and `scripts/csv-assert.sh /tmp/opencode/c12-prose-tierr/tierr-retry-storm test/analysis/tierr-retry-storm/*.sql` (2 SQL files, 0 failures)
- `gofmt -l .` (empty), `go build ./... && go vet ./...` (pass)
- `(cd tools/detlint && go run ./cmd/astdiff /tmp/opencode/c12-preprose /root/Software/mini-gateway-work/c12)` (pass: zero AST differences)
- `GOMAXPROCS=2 go test -count=1 -timeout 5m ./internal/retry/... ./internal/obs/retrysrc/... ./internal/proxy/... ./internal/config/... ./internal/pgwire/... ./internal/admission/...` (all pass)
- `GOMAXPROCS=2 go test -count=1 -timeout 3m -run '^(TestCanaryRetry|TestScenarioRetryRerunsKillRowsAndResultsWithThePolicyOn$)' ./test/e2e` (pass after commenting `test/e2e`)
- `scripts/gen-docs.sh --check` (pass)
- `scripts/site.sh /tmp/opencode/c12-site` (built 29 pages cleanly; `book/12-retry-policy.html` listed in `pages.txt` and linked in `book/index.html`)
- Path check of all changed paths against `tools/lint-rules/prose-paths.txt` (pass)
