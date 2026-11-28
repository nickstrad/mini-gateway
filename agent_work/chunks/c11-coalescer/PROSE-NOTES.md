# Chunk 11 — Coalescer: prose pass notes

- Date: 2026-11-28
- Branch: `work/c11-coalescer` (pre-prose commit `43fcbdc`)
- Method: Clarity skill (`/root/.agents/skills/clarity/SKILL.md`, `references/edit.md`, `references/medium.md`, `references/longform.md`), Nick's making-of cut ruling (`/root/briefs/zgateway-making-of-cut-ruling.md`), and `tools/lint-rules/prose-paths.txt`.

## What was written

### A. Go code comments (comments only; AST-verified against `43fcbdc`)

- `internal/coalesce/config.go`, `lists.go`, `snapshot.go`, and `table.go`: package doc comment, doc comments on every exported type, constant, variable, function, and method (`Instant`, `RequestID`, `FlightID`, `Millisecond`, `Second`, `Mode` and constants/methods, `JoinMode` and constants/methods, `Plant` and all six `Plant*` constants, all `Default*`/`Max*`/`OtherTenant` constants, `Config`, `WithDefaults`, `ErrConfig`, `Validate`, `Cap` and constants/methods, `TenantHits`, `HotKey`, `Snapshot`, `Table.Snapshot`, `Counters`, `Snapshot.Check`, `Table.Check`, `Quiesce`, `Verdict` and constants/methods, `Joined`, `NoticeKind` and constants/methods, `Notice`, `Result`, `LeaveWhy`, `LeftKind`, `Left`, `Table`, `New`, `Config`, `Join`, `Started`, `Stored`, `Finish`, `Leave`, `Sweep`, `TakeNotice`, `Next`, `Shutdown`, `Owner`, `Followers`, `Member`, `Running`), plus targeted inline comments on `after` saturation, FIFO `noShareList` expiry at equality, Space-Saving `hotKeys`, bounded `tenantStats`, `eligible` SQL-free check, 8-byte length-prefixed `keyOf`, `joinTarget` strict vs relaxed selection, creation-order deadline list, and time-passes-before-event in `advance`.
- `internal/obs/coalsrc/coalsrc.go`: package doc comment and doc comments on `MetricWaiting`, `CheckCoalescer`, `Metrics`, `Map` (noting rank-only hot keys and `obs.detail` gating), `Source`, and `HealthRules`.
- `internal/config/coalesce.go` and `validate.go`: doc comments on `DefaultCoalesce*`, `CoalesceScope*`, and `Coalesce`; inline comments on validating bounds under `mode: "off"`, restricting the `on-switch` + `all-tenants` refusal to active coalescing, and writing resolved `MaxReplyBytes` back onto `File` for fingerprint consistency.
- `internal/proxy/coalesce.go`, `client.go`, `lease.go`, `corepool.go`, and `retry.go`: doc comments on `FlightTable`, `CoreCoalescer`, `NewCoreCoalescer`, all `CoreCoalescer` methods, and `Engine.CoalesceSnapshot`; inline comments on `noticeBusy` re-entrancy and insertion-ordered `ends` cleanup, `coalStart` shutdown and `breakerOpen` guards, `expireQueued` vs `unqueue` in `coalExpireOwner`, single-copy slice + per-follower `ReadyForQuery 'I'` in `coalAnswer`, `coalStoring` detach-and-stream, held-owner `arrived` propagation in `coalLeave`, `coalHandoff`, `request.arrived` and `request.tenant` in `client.go`, `flightGone` before `offerRetry` in `statementLost`, `d.lastTenant` lease ends in `release` and `coreRelease`, and `requestDeadline` anchored on `req.arrived`.
- Test files and tier S scenario helpers added by chunk 11 (exported identifiers in `test/e2e/c11_coalesce.go` and `c11_join.go`; brief per-test comment stating what each test proves): `internal/coalesce/{table,key,narrowed,property}_test.go`, `internal/obs/coalsrc/coalsrc_test.go`, `internal/config/{coalesce,join_coalesce}_test.go`, `internal/proxy/{coalesce_export,coalesce,coalesce_repair,join_coalesce,join_coalesce_shed,join_repair,join_repair_ops}_test.go`, and `test/e2e/{c11_coalesce_test,c11_tierr_test}.go`.

### B. Book chapter, manifest, and index

- `book/chapters/11-coalescer.md` ("11. One read for a thousand callers"): explains the hot-key stampede problem, SQL-free eligibility (`auto`, `hinted`, `off`), length-prefixed keys, strict vs relaxed joins and read-your-own-write, reference counting and owner handoff, creation-ordered flight deadlines, single-copy reply buffering and all four memory caps, composition with fairness (Chapter 9) and pre-forward retries (Chapter 12), real code excerpts (`Table.Join`/`joinTarget` and `Engine.coalLeave`), configuration and observability, real command outputs and tier S/R numbers (including Zipf Headline 2), and all 10 stated limits. Describes the software only (no making-of prose; Chapter 10 is not referred to as existing).
- `book/manifest.json`: set Chapter 11's `status` to `live`.
- `book/index.md`: added Chapter 11 (the request coalescer) to the live-chapters sentence (leaving Chapter 10 planned).

### C. Other reader-facing pages

- `docs/knowledge/coalescer.md`: rewritten in Clarity rewrite mode, preserving all five section headings, every symbol, path, number, and caveat, and removing process-history references.
- `docs/knowledge/fairness.md`: rewrote only the `## Coalesced flights and the limit (chunk 11 join, 2026-11-28)` section in Clarity rewrite mode, keeping every fact, caveat, and link intact.
- `README.md`: added Chunk 11 to the landed-chunks list, added Chapter 11 to the live-chapters note and Book links, and removed coalescer (Chunk 11) from the pass-through stage seams sentence.

## Narrowed claims and claims left out

All narrowed claims and stated limits from `NOTES.md`, `ADVERSARIAL.md`, and `ARBITRATION-1.md` are written directly as limits of the software (without process history):

- **"At most one in-flight read per key" (Claim 1 / N1):** stated as holding among flights the coalescer owns, with all three counted exceptions named alongside their metrics (`flight_ends{outcome="cancelled"}`, `passed{reason="no-share"}`, and `passed{reason="full"}` / `overflows{reason="flights"}`).
- **Same-instant leave vs result (Claim 6 / N2):** stated that event order at a single instant decides — result before leave delivers the reply (`left_unknown`), leave before result withholds it (`57014` for cancel, nothing for gone).
- **Default sharing trust limit:** stated that `coalesce.mode` defaults to `auto` (`strict`, `all-tenants`) and the safe shape trusts named database objects (views, user-defined operators, shadowed allow-listed functions, row-level security policies) unless the operator sets `off` or `hinted`, and that `hinted` shares any marked statement including writes.
- **Cross-tenant riding and handoff keep-alive:** stated that under `all-tenants`, cross-tenant followers spend no share, and cross-tenant handoff keeps an abandoned read (and a zero-byte-loss retry of it) alive on the leaving tenant's share until the lease ends (`key_scope: "tenant"` gives strict isolation).
- **Flight retries and arrival bound:** stated that a zero-byte loss before reaching Postgres retries once for all waiters when retries are on, bounded by the original leader's arrival instant (`arrived + timeouts.statement`, preserved across strict-mode promotions and repeated handoffs) and never offered after the flight deadline; strict-mode arrivals behind a queued retry wait in a held flight.
- **Open or half-open breaker promotion refusal:** stated that a held flight promoted while the breaker is open or half-open is refused `53M03` with all its waiters (`engine.coalesce_promoted_refused`, counted as failed rather than shed).
- **Vanished clients on real TCP sockets (D5):** stated that the real shell notices a reset client only at reply time (or flight deadline) unless it sends a `CancelRequest`.
- **Tier R Zipf latency caveat:** stated explicitly that the single-run Zipf p50/p99 latencies are indicative only while the read counts are exact; no unmeasured fleet numbers or Chapter 10 references were added.

## Commands run

- `gofmt -l .` (clean: 0 files)
- `go build ./... && go vet ./...` (pass)
- `go test -count=1 -v -timeout=3m ./internal/coalesce ./internal/obs/coalsrc` (pass: `1.334s`, `0.009s`)
- `MG_C11_HEAVY=1 go test -count=1 -v -timeout=5m -run '^TestCoalesceHotKeyNumbers$' ./test/e2e` (pass: `10.015s`; strict 2 reads p99 `101.705708ms`, relaxed 1 read p99 `51.47203ms`, off 1,000 reads p99 `12.652920921s`, direct 1,000 reads p99 `50.294794ms`)
- `RUN_DIR=/tmp/opencode/c11-prose-run MG_C11_HEAVY=1 DST_SEEDS=1 go test -count=1 -v -timeout=5m -run '^TestScenarioCoalesce/coalesce-(hot-key-strict|hot-key-relaxed|read-your-write-strict|read-your-write-relaxed|cancel-99-strict|vanish-all-strict|shared-error-strict|caps-strict|hot-key-weighted-strict|hot-key-retry-strict)$' ./test/e2e -seed=1` (pass: `30.857s`)
- `scripts/csv-assert.sh /tmp/opencode/c11-prose-run/coalesce-hot-key-strict test/analysis/coalesce-hot-key-strict/*.sql` and `scripts/csv-assert.sh /tmp/opencode/c11-prose-run/coalesce-hot-key-retry-strict test/analysis/coalesce-hot-key-retry-strict/*.sql` (0 failures; DuckDB v1.5.5)
- `go test -count=1 -v -timeout=5m -run '^(TestFollowerGets|TestStrictHolds|TestALeaverAtTheInstant|TestTheOwnerLeaving|TestInvariantsCoverFlights|TestEngineQuiescesWithAnEmpty|TestAQueuedFlightAtItsDeadline|TestShutdownSendsNothing|TestJoin|TestRepair)' ./internal/proxy` (pass: `12.900s`, including all 400 randomized sequences)
- `GOMAXPROCS=2 go test -count=1 -timeout 5m ./internal/coalesce/ ./internal/obs/coalsrc/ ./internal/config/ ./internal/proxy/` (pass: `1.328s`, `0.009s`, `0.425s`, `11.150s`)
- `GOMAXPROCS=2 go test -count=1 -short -timeout 5m -run '^TestScenarioCoalesce$' ./test/e2e/` (pass: `83.769s`)
- `(cd tools/detlint && go run ./cmd/astdiff /tmp/opencode/c11-preprose /root/Software/mini-gateway-work/c11)` (pass: zero AST differences)
- `scripts/gen-docs.sh --check` (pass)
- `scripts/site.sh /tmp/opencode/c11-site` (built 30 pages cleanly; `book/11-coalescer.html` present, listed in `pages.txt`, and linked in `book/index.html`)
- Path check of all changed paths against `tools/lint-rules/prose-paths.txt` (pass)
