# Chunk 7 — Pool: prose notes (Neon seat, 2026-11-27)

Branch: `work/c07-pool` (worktree `/root/Software/mini-gateway-work/c07`, pre-prose commit `d4ab627`).
Written with the Clarity skill (`/root/.agents/skills/clarity`) and no making-of passages.

## What was written

- **Go comments (comments only, AST unchanged):**
  - `internal/pool/`: package doc comment in `pool.go`, doc comments on every exported type, constant, variable, function, and method across `pool.go`, `config.go`, `breaker.go`, `rate.go`, and `snapshot.go`, plus inline invariants (ceiling GCRA division, time-before-event, breaker demand reset, non-trial check failure rule).
  - `internal/proxy/`: doc comments and key inline comments in `corepool.go`, plus comments on chunk 7 and join additions in `admission.go` (`Refuse`), `api.go` (`EvDBCancelDone`, `ActDBCancel`), `config.go` (`CloseClientIdleInTx`..`CloseDBCancelCap`, `ResetOnSwitch`, `PlantBreakerFlushPops`), `seams.go` (`ShedQueue`), and `lease.go` (leaving `dpResetting` before `StepDone`).
  - `internal/config/`: doc comments in `pool.go` and updated `Pool` and `Lease` struct comments in `config.go`.
  - `internal/obs/poolsrc/poolsrc.go`: package doc comment and doc comments on every exported symbol.
  - `internal/shell/`: `cancel.go` (`closeWriteWait`, `cancelRequest`), `conn.go` (100 ms TLS close-notify write deadline), `shell.go` (`keepAliveDialer`).
  - `internal/admission/queues.go`: updated `Reason` doc comment and added doc comments on `ReasonBreaker` and `Queues.Refuse`.
  - `internal/pgwire/errors.go`: doc comment on `ErrRowLostMidStatementInTransaction`.
  - Chunk 7 test and e2e files: brief property comment on each test function and exported helper across `internal/pool/{pool,repair,arbitration,allow_arbitration}_test.go`, `internal/proxy/{core,core_repair,core_arbitration,trial_arbitration,join}_test.go`, `internal/admission/join_test.go`, `internal/config/{pool,join}_test.go`, `internal/obs/poolsrc/poolsrc_test.go`, `internal/shell/{cancel,keepalive}_test.go`, and `test/e2e/c07_{faultnet,scenarios,checks,scenario_test,canary_test,arbitration_test,tierr_test}.go`.
- **Book chapter:** `book/chapters/07-pool.md` ("The pool: a hard cap, a creation rate, and connections that go bad"), set to `status: "live"` in `book/manifest.json`. Covers the 500-client headline benchmark on Postgres 17, hard cap / GCRA creation rate / jittered lifetimes / fleet budgets, `always` vs `on-switch` release policies and `lease.reseed`, the 5 a.m. silent-death scenario and all three remedies, the circuit breaker (non-trial idle check failures do not open it), the dirty-connection table and cancel forwarding, the pool-admission queue boundary and full exit table, testing/canaries/fuzzing, and stated limits.
- **Reader-facing docs:**
  - `docs/knowledge/pool-core.md`: corrected the `pool-admission-restart` explanation from 5 breaker failures to 3 (inherited from `corePool()`; all 4 idle connections close politely at t = 2.000 s adding 0 failures, so 3 refused dials take ~100.8 ms at 20/s vs <= 2.84 ms at 1,000/s against the 100 ms interval).
  - `docs/knowledge/admission-queues.md`: added `Refuse` to the `proxy.ShedQueue` method list.
  - `README.md`: added the Chunk 7 bullet to the landed list and marked Chapter 7 live (removing Chunk 7 from the remaining pass-through seams list).

## Claims narrowed, qualified, or left out

- **Narrowed claims 1, 3, 6, and 8** are written in `book/chapters/07-pool.md` under "Stated limits" as plain limits of the software:
  1. Cap accounting holds until `ActClose` is issued (synchronous shell close with 100 ms TLS close-notify deadline; physical overlap at most one closing descriptor per replacement dial).
  2. Jitter spreads expiries uniformly per nanosecond (minimum spread 1 ms) without guaranteeing zero collisions; measured bounds are stated (at most 6 of 200 in 1 s; at most 4 in 100 ms in `pool-lifetimes`).
  3. After a silent mass death, client errors are bounded by lent connections plus non-suspect connections idle for less than `pool.check_after_idle` (30 s), at most one error per dead connection before `suspect` catches siblings.
  4. `lease.reseed: on` runs with resets; under `lease.reset: on-switch`, same-tenant reuse skips both reset and re-seed, so random-seed residue is gone across tenants and remains within a tenant's consecutive same-connection leases.
- **Headline benchmark 1 direct count:** our live run of `TestTierRPoolHeadlineDatabaseConnectionsDirectAgainstProxy` reported `196 connected, 304 refused, 196 database backends, 196 queries ok` direct and `500 connected, 0 refused, 16 database backends, 500 queries ok` proxied (the chapter quotes our exact run and notes that Postgres's `max_connections = 200` with 3 superuser-reserved slots allows at most 197 non-superuser backends, 196 here while the prior setup connection finished exiting).
- **Tier R TLS reconnection storm:** our live run peaked at 5 database backends (cap 16) and 64 clients in startup (exact `limits.max_startup` cap), 0 refused, 500 queries ok; the chapter quotes our live figure (5) rather than an older run's 12.
- **Not edited (out of prose-pass scope):** `docs/timeout-budget.md` (its check-before-reuse row still says "counts toward the breaker", generated from `internal/proxy/contracts.go`, denied by `tools/lint-rules/prose-paths.txt`) and `docs/extension-seams.md` (excluded from prose rewrites per `PLAN.md` 19.4 and not requested in the brief).
- **No making-of content:** no agents, seats, reviews, attacks, arbitration, repair rounds, build rounds, or join history appear in `book/chapters/07-pool.md` or `README.md`.

## Commands run

1. `go test -count=1 -v -run '^(TestNightTrickleEachRemedyAloneCarriesTheRamp|TestAblationNightTrickleWithEveryRemedyOffHangsTheRamp|...)$' ./test/e2e` — pass (5.667 s): all remedies off = 4 client errors, 4 no-reply deadlines, 9 idle drops; keepalive alone = 0 errors, 0 drops; idle alone = 0 errors, 10 idle closes, 0 drops; check alone = 0 errors, 4 checks failed, 0 breaker opens, 9 idle drops.
2. `MG_TIER_R=1 go test -count=1 -v -run '^TestTierRPool' ./test/e2e` — pass (16.862 s): all 5 tier R pool tests green against Postgres 17 (keepalive `30/10/3` + `ss -tnoi`, all 4 release-policy arms with 0 cross-tenant crossings, targeted cancel, headline 500-client benchmark, 500-client TLS storm).
3. `go test -count=1 -v -run '^(TestCanaryPool|TestCanaryJoin|TestAblationColdStart|TestAblationRestart|TestPathFault)' ./test/e2e` and `go test -count=1 -v -run '^(TestEveryPlantedBug|TestAFailedCheck|TestBreakerOpens|TestDirtyTable|TestOnSwitch)' ./internal/pool` — pass (3.950 s and 0.044 s).
4. `gofmt -l .` (empty), `go build ./... && go vet ./...` (pass), `scripts/site.sh /tmp/c07-site` (built 27 pages, `book/07-pool.html` listed in `pages.txt` and `book/index.html`), and `go test -count=1 ./...` — all packages pass.
5. Clarity diagnostic: `python3 /root/.agents/skills/clarity/scripts/strip_markdown.py book/chapters/07-pool.md > /tmp/c07-clarity.txt && python3 /root/.agents/skills/clarity/scripts/prose_stats.py /tmp/c07-clarity.txt`, followed by one focused edit pass splitting overloaded sentences and re-running `scripts/site.sh`.
6. Final-stage mechanical check (after commit, into `/tmp/c07-final-check` without running the full gate): `GATE_STAGE=final GATE_PREPROSE=d4ab627 GATE_ONLY=G0,G2,G9 GATE_OUT=/tmp/c07-final-check scripts/gate.sh c07-pool` — verifies `gofmt`, `build`, `vet`, `G2 astdiff` + `prose-paths.txt`, and `G9 site`.
