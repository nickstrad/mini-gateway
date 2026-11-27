# agent_work index

Scripts and research artifacts for mini-zgateway. Every script any agent produces on this project is saved here and indexed: name, purpose, phase/chunk, author seat, what it proved / last result.

## Phase 0 — influences research (Fable orchestrator, 2026-11-24)

| Path | Purpose | Author seat | What it proved / last result |
| --- | --- | --- | --- |
| `phase0/briefs/_common.md` | Shared rules for every research brief: honesty labels (read directly / secondary only / could not verify), the per-influence entry format, no repo edits | Fable | Appended to each bucket brief at dispatch |
| `phase0/briefs/b1-spec-named.md` | Research brief: verify every reference in spec §6 from the real source (ZGateway, Hyperdrive, Canva, Fail at Scale, CoDel paper, Apollo) | Fable | See `phase0/research/b1-spec-named.md` |
| `phase0/briefs/b2-prior-projects.md` | Research brief: Nick's prior repos on vm-1 (analytics-edge-gateway, zeromq-go, object-store, mansio, sqlite-internals) — reusable ideas, non-reusable code, carried rulings | Fable | See `phase0/research/b2-prior-projects.md` |
| `phase0/briefs/b3-industry-systems.md` | Research brief: unnamed industry systems (PgBouncer, Envoy, Netflix concurrency-limits, Finagle, SRE book, McRouter/Twemproxy, HAProxy, Go singleflight, Vitess…) | Fable | See `phase0/research/b3-industry-systems.md` |
| `phase0/briefs/b4-foundations.md` | Research brief: foundational algorithms reduced to exact mechanisms (CoDel, Adaptive LIFO, GCRA, token bucket, DRR, AIMD/Vegas/gradient, Little's Law, balls-into-bins, coordinated omission, HDR, benchmark statistics, DST lineage) | Fable | See `phase0/research/b4-foundations.md` |
| `phase0/dispatch-research.sh` | Conductor: launches the bucket research agents headlessly (fresh `claude-fable-5-2` each, scratch cwd under `/tmp/zgateway-phase0/`, brief copies in `/root/briefs/zgateway-phase0-*.md`), waits with a deadline, writes `/root/runs/zgateway-phase0-<bucket>.exit`, fails closed if an output is missing | Fable | Ran once 2026-11-24: all four agents exited 0, each wrote its notes file (47–78 KB); no re-dispatch needed |
| `phase0/research/b1-spec-named.md` | Research notes: spec §6 sources read from the originals, with a verification log of every URL tried | Fable research agent | 20 entries. All six spec-named posts exist; the 19× is a model output; "CoDel" is three different algorithms across the sources; Canva session-revocations post has no mechanism to borrow |
| `phase0/research/b2-prior-projects.md` | Research notes: Nick's prior repos — reusable ideas, carried rulings, recorded failures | Fable research agent | 17 entries + 8 skimmed. Projects 2 and 4 have no product code (plans and probes only); mansio holds the closest working code; shared-kernel ruling unresolved |
| `phase0/research/b3-industry-systems.md` | Research notes: 31 industry systems reduced to mechanisms, including the Variant A command-classification table (entry 20) | Fable research agent | 31 entries + 8 ranked one-liners. Confirms the 10% retry budget comes from the Google SRE book; supports Variant A |
| `phase0/research/b4-foundations.md` | Research notes: foundational algorithms with pseudo-code sketches, invariants and classic bugs | Fable research agent | 34 entries. RFC CoDel is the wrong algorithm for a request queue; folly's detector goes blind under LIFO; "converges to target" is not what the service variants do |
| `phase0/probes/codelsim.py` | Probe: toy single-seed queue simulation (20 servers × 50 ms, Poisson arrivals, 30 s) comparing plain FIFO, RFC CoDel, folly's rule and the "Fail at Scale" timeout rule, each with and without LIFO. Run: `python3 -I agent_work/phase0/probes/codelsim.py` | Fable research agent (b4); re-run by the orchestrator | At 10× overload: RFC CoDel left 85,292 queued; folly + LIFO shed zero; timeout rule held max wait at 100.0 ms with depth ≤ 580; timeout rule + LIFO without a head sweep left 5,450 stale requests. Identical output on re-run. A toy — a reason to test in Commit 0, not a result |
| `phase0/probes/calc.py` | Probe: arithmetic behind quoted numbers — HDR histogram sizes, Zipf top-1% shares by exponent, balls-into-bins maxima, distinct-server counts. Run: `python3 -I agent_work/phase0/probes/calc.py` | Fable research agent (b4); re-run by the orchestrator | HDR 1 µs–60 s: 20.5 KB at 2 digits, 139 KB at 3; Zipf over 100k keys: top 1% of keys take 63% / 85% / 98% of traffic at s = 1.01 / 1.2 / 1.5 |

## Phase 1 — plan, C4, review site (Fable orchestrator, 2026-11-24)

| Path | Purpose | Author seat | What it proved / last result |
| --- | --- | --- | --- |
| `phase1/probes/pgwire/main.go.txt` (own `go.mod`; not product code; kept as `.txt` since chunk 2's repair round so that no Go file outside `tools/` imports `pgproto3` beyond the allowed packages, claim 11) | Probe for steering answers 1 and 9. Part A: `pgproto3` message structs used without its blocking reader, behind a 15-line framer of our own, fed one byte at a time. Part B: a real Postgres — startup with SCRAM-SHA-256 written on the standard library, transaction status bytes, pipelined queries, session state, empty query, a 20 MB reply, a backend killed mid-query, a read that misses a concurrent acknowledged write. Run: `docker run -d --rm --name mg-probe-pg -e POSTGRES_PASSWORD=probe-pw -p 127.0.0.1:55439:5432 postgres:17`, then `d=$(mktemp -d) && cp agent_work/phase1/probes/pgwire/{main.go.txt,go.mod,go.sum} "$d" && mv "$d/main.go.txt" "$d/main.go" && (cd "$d" && go run . 127.0.0.1:55439)`, then `docker rm -f mg-probe-pg` | Fable | Run 2026-11-24 against PostgreSQL 17.11; output in `output-2026-11-24.txt`. `BEGIN; SELECT 1` in one message ends with status `T`; three pipelined queries get three independent replies; a query pipelined after `BEGIN` runs inside that transaction; `SET` outlives its exchange and `SET application_name` triggers an unsolicited `ParameterStatus`; a killed backend sends `FATAL 57P01` and no end marker; a running read returned the old value after a write was acknowledged. `go list -deps`: `pgproto3` compiles in with only two other packages, both internal to pgx |
| `phase1/probes/pgtls/main.go` (own `go.mod`, standard library only; not product code) | Probe for Nick's ruling 5 (TLS): a TLS-terminating relay — the 8-byte request exchange before startup, `crypto/tls` on the client leg, optional TLS on the database leg, then a byte relay — plus a handshake CPU measurement and a smuggled-bytes check. Run: `docker run -d --name mg-tls-probe-pg -e POSTGRES_PASSWORD=probe -p 127.0.0.1:55432:5432 postgres:17 -c ssl=on -c ssl_cert_file=/etc/ssl/certs/ssl-cert-snakeoil.pem -c ssl_key_file=/etc/ssl/private/ssl-cert-snakeoil.key`; `go build -buildvcs=false -o tlsprobe . && ./tlsprobe -ca-out ca.pem` (add `-backend-tls` or `-no-client-tls` for runs 2 and 3), drive it with `psql "host=127.0.0.1 port=6543 user=postgres sslmode=require"`; `./tlsprobe -mode garbage`; `./tlsprobe -mode bench`; then `docker rm -f mg-tls-probe-pg` | Fable | Run 2026-11-24 (amendment 2), output in `output-2026-11-24.txt`. 59 TLS-specific lines. `psql` with `require`, `verify-full` and the default `prefer` connect over TLS 1.3; `pgbench -S -M simple` 800/800 over TLS; database leg `ssl = true`; passing Postgres's SCRAM through the relay fails on channel binding; smuggled plaintext is refused; at most 3.3 ms CPU per ECDSA handshake under host load 24. Verdict: include TLS (PLAN.md section 3.1; `docs/knowledge/tls-cost-assessment.md`) |
| `phase1/research/q8-object-store-simulator-evaluation.md` | Evidence for steering answer 8: the object-store simulator (`/root/Software/object-store`, commit `379e622`) read file by file and judged against this project's needs | Fable research agent; three load-bearing claims re-checked in the code by the orchestrator | Not a fit as a whole: no byte-stream connections; about 8% of its code survives unchanged, about 26% with edits. Clean pieces to copy: named streams, seed plumbing, heap, time helpers, determinism lint. Its tests pass (`go test ./internal/sim/... ./internal/env/... ./cmd/objsim/...`) |
| `phase1/research/release-it-mining.md` | *Release It!* notes (`/root/Software/book-notes/books/release-it/`) mined for the build plan: anti-pattern checklist questions, pattern verdicts, a 22-item fault catalog, 15 insights on build order | Fable research agent | Source is a teaching project derived from the book, not the book; the knowledge store has no entry (ten searches, all misses, recorded). Gap: nothing on pooled connections dying silently |
| `../docs/knowledge/release-it-pooled-connections.md` | Ruling 6, the step before chunk 7: the book itself (`/root/briefs/release-it-book.pdf`, first edition, 2007) read for pooled connections that die silently — the 5 a.m. problem, what TCP reports, stale connections after a failover, waits at checkout. Four parts with printed page numbers: what the book says, what PLAN.md already covers, ten gaps written as changes to the chunk 7 brief, points not adopted | Fable research agent | 2026-11-24. The first edition never mentions TCP keepalive; its cure was a server-side ping that kept the firewall entry alive. Gaps: three half-open fault modes plus an idle-triggered drop; keepalive in both directions with defaults; idle timeout 180 s under a stated path limit; an empty-query check with a 250 ms deadline run off the request's path; a suspect flag on sibling connections; "nothing reached the database" holds only when zero bytes were written. Keepalive and PgBouncer facts checked against the Postgres 17, PgBouncer and AWS docs and `go doc` |
| `phase1/c4-check.mjs` | Validates `docs/c4/model.json` with `c4-sdk@0.1.0`: decodes it and builds a scene for every diagram. Run from a scratch folder outside `/tmp` that has `npm init -y && npm install c4-sdk@0.1.0`: copy the script into that folder, then `node c4-check.mjs <repo>/docs/c4/model.json` | Fable | 2026-11-24, after amendment 2 (rulings applied): decode ok, 0 errors, 0 warnings; 12 of 12 scenes built |
| `phase1/check-mermaid.py` | Opens every page listed in a site's `pages.txt` in headless Chromium (the Playwright install of `/root/Software/web-tools`) and reports HTTP status and whether every Mermaid picture drew. Run: `agent_work/phase1/check-mermaid.py <base-url>` | Fable | 2026-11-24, local build after amendment 2: 17 pages 200, 12 of 12 pictures drew. Live result: see `/root/runs/zgateway-phase1-done.txt` |
| `../docs/c4/gen.py` | Source of the C4 set: writes `model.json`, the 12 diagram pages, the Mermaid sources and the table in `docs/c4/README.md`. Run: `python3 docs/c4/gen.py` | Fable | 116 elements, 166 relationships, 12 diagrams (after amendment 2: batcher removed, TLS added) |
| `../.github/scripts/build_site.py` | Builds the Pages site (review page, plan, C4, influences, and since chunk 0 the book: front page with a contents list from `book/manifest.json`, one page per live chapter). Needs the `markdown` package; locally run it through `scripts/site.sh`. Fails on a broken relative link (every `href` and `src`, raw HTML included, any quote style) or a manifest that disagrees with `book/chapters/` | Fable | 2026-11-24, chunk 0: 18 pages (the 17 earlier pages and `book/index.html`) |

## Chunk 0 — foundation (Fable, 2026-11-24)

Notes: `chunks/c00-foundation/NOTES.md`. Durable facts: `../docs/knowledge/foundation-gates.md`. Results below are from the repair round after the adversarial pass (see "Repair round" in the notes); the gated commit is the `sha` in `chunks/c00-foundation/gate/result.json`.

### Gates and scripts

| Path | Purpose and how to run | Author seat | What it proved / last result |
| --- | --- | --- | --- |
| `../scripts/gate.sh` | The local landing gate, G0 to G10, on the host; the landing authority. Run: `scripts/gate.sh <chunk-dir>` (or `make gate CHUNK=<chunk-dir>`); `GATE_STAGE=build\|final\|foundation`, `GATE_ONLY=G0,G3` for a subset. Writes `chunks/<chunk-dir>/gate/result.json`; a refused run removes an earlier one. At the final stage it also holds the prose pass to `tools/lint-rules/prose-paths.txt`; `G4 markers` rejects a not-built marker that was added or restored | Fable | `GATE_STAGE=foundation scripts/gate.sh c00-foundation`: overall pass, counts_for_landing true, 8 s; G4 to G8 skip-not-built; result in `chunks/c00-foundation/gate/result.json` |
| `../scripts/ci-fast.sh` | The CI set (G0, G1, G3 with `-short`), run by `.github/workflows/ci.yml` and `make ci`. Run: `scripts/ci-fast.sh` | Fable | Green in 8 s on vm-1 (target under five minutes). Not yet run on GitHub: nothing is pushed from the builder's worktree |
| `../scripts/gate-canaries.sh` | Tests the gate: plants one violation per check in a throwaway copy under `/tmp` and requires the right check to go red with the expected message. Run: `scripts/gate-canaries.sh` (`CANARY_ONLY=<id>,<id>` for a few) | Fable | 51 of 51 caught in 116 s (42 red, 5 refused with exit 2, 4 controls); `chunks/c00-foundation/gate/canaries.json` |
| `../scripts/site.sh` | Builds the site locally; fetches the pinned `markdown` wheel into `.cache/pylib` when Python has no such package. Run: `scripts/site.sh [output-dir]` or `make site` | Fable | 18 pages built; runs as gate check G9 |
| `../scripts/sweep.sh` | Seed sweep of every simulator scenario test (gate G4): `go test -run ^TestScenario ./...` with `DST_SEEDS`; reprints the first REPLAY line. Run: `SWEEP_SEEDS=200 RUN_DIR=<dir> scripts/sweep.sh` or `scripts/sweep.sh [-seeds N] [-run REGEXP]` | Fable (skeleton); built by Opus in chunk 1 | Chunk 1: 200 seeds x 11 toy scenarios green in 5 s; 1,000 seeds in 17 s. Found a real flaw in an ablation check while being built (seed 6), fixed |
| `../scripts/fuzz.sh` | Timed fuzzing of every `func FuzzXxx` in the module (gate G5); no target found is a failure. Run: `FUZZ_SECONDS=20 scripts/fuzz.sh` | Fable (skeleton); built by Opus in chunk 1 | Chunk 1: 2 targets (`FuzzScheduleFromBytes`, `FuzzToySchedule`), 20 s each, green in 47 s |
| `../scripts/tier-r.sh` | Tier R (gate G6): starts two throwaway `postgres:17` containers on 127.0.0.1 (trust auth; `main` with `max_connections=600` and `pg_stat_statements`, `burst` with `max_connections=100`), runs `go test -run ^TestTierR ./...` with `MG_TIER_R=1`, removes the containers on exit. Run: `RUN_DIR=<dir> scripts/tier-r.sh`; `MG_TIER_R_UPDATE=1` also rewrites `internal/sim/world/testdata/fidelity/postgres17.txt` | Fable (skeleton); built by Opus in chunk 2 | Chunk 2, 2026-11-25, Postgres 17.11: 4 tests green in about 30 s. The fake matches Postgres 17 on the probe script after one fix (Postgres sends `RowDescription` when a SELECT starts); 500 clients held 500 connections; a 500-client burst against 100 slots: 100 accepted, 400 refused with `53300`; 1,000 identical reads = 1,000 executions |
| `../scripts/csv-assert.sh` | Skeleton: DuckDB assertions over flushed CSV files (gate G7). Run: `scripts/csv-assert.sh <run-dir>...` | Fable | Not built yet (exit 3); marker `csv-assert`; built in chunk 3 |
| `../scripts/gen-docs.sh` | Skeleton: generated contract documents; `--check` diffs against the committed files (gate G8). Run: `scripts/gen-docs.sh [--check]` | Fable | Not built yet (exit 3); marker `gen-docs`; built in chunk 5 |
| `../scripts/race-long.sh` | Skeleton: repeated race run of the shell and engine tests, on demand. Run: `RACE_COUNT=20 scripts/race-long.sh` | Fable | Not built yet (exit 3); marker `race-long` |
| `../scripts/soak.sh` | Skeleton: long steady-state run, on demand. Run: `SOAK_MINUTES=30 scripts/soak.sh` | Fable | Not built yet (exit 3); marker `soak` |
| `../scripts/bench.sh` | Skeleton: benchmarks (PLAN.md section 11), on demand; `make bench` runs it. Run: `scripts/bench.sh` | Fable | Not built yet (exit 3); marker `bench`; built in chunk 14 |
| `../scripts/obs-chain.sh` | Skeleton: OTLP push through a Collector into Prometheus, on demand. Run: `scripts/obs-chain.sh` | Fable | Not built yet (exit 3); marker `obs-chain`; built in chunk 15 |
| `../Makefile` | `make test`, `chaos`, `bench` (fail with "not built yet" while their markers exist), `gate CHUNK=…`, `ci`, `lint`, `site` | Fable | `make test`, `make chaos`, `make bench` each fail with `not built yet`; `make ci`, `make lint`, `make site` green |
| `../.github/workflows/ci.yml` | CI: push to main, pull requests, manual dispatch; `actions/setup-go` with the version from `go.mod`; runs `scripts/ci-fast.sh`; 15-minute timeout | Fable | Parses as YAML; first real run happens when the orchestrator pushes |

### Lint tools (`../tools/detlint`, nested module, standard library only)

Build all: `BIN=$(mktemp -d); (cd tools/detlint && go build -o "$BIN" ./...)`. Test: `cd tools/detlint && go vet ./... && go test ./...` (green, 8 packages). Rules in full: `../tools/detlint/README-tools.txt`. Origin: `../tools/detlint/SOURCE.md` (object-store commit `060bb3e`).

| Path | Purpose and how to run | Author seat | What it proved / last result |
| --- | --- | --- | --- |
| `../tools/detlint` (`detlint`) | Determinism lint: wall clock, global random, concurrency, floats, map order, process state, imports. Run: `$BIN/detlint -root . -allow tools/lint-rules/detlint-allow.txt <pkg>...` (gate G1, packages from `tools/lint-rules/det-packages.txt`) | Copied from object-store | Tests pass here; canary `g1-wallclock` caught a planted `time.Now()` |
| `../tools/detlint/cmd/nocomments` | The no-comments rule for product Go, with a baseline. Run: `$BIN/nocomments -root . -baseline <old-tree>` (gate G2, build stage) | Copied; skips `agent_work .git vendor tools`, and since the repair round reads `testdata` folders | Canaries `g2-comment`, `g2-comment-testdata` caught |
| `../tools/detlint/cmd/astdiff` | Proves two trees have the same code (comments and formatting aside). Run: `$BIN/astdiff <tree-a> <tree-b>` (gate G2, final stage, beside the path list of `tools/lint-rules/prose-paths.txt`) | Copied | Canary `g2-prose-changed-code` caught; control `g2-prose-only` passes |
| `../tools/detlint/cmd/paniccheck` | Every `panic(` needs an allow line. Run: `$BIN/paniccheck -root . -allow tools/lint-rules/panic-allow.txt internal cmd` (gate G1) | Copied | Canary `g1-panic` caught |
| `../tools/detlint/cmd/importcheck` | Standard library and own module only, from a file list on standard input | Copied | Kept with its tests; the gate uses `importlint` instead |
| `../tools/detlint/cmd/importlint` | Import rules of PLAN.md section 6.3 and the go.mod requirement rule, from `tools/lint-rules/imports.txt`; reads `testdata` folders and forbids importing a package in one. Run: `$BIN/importlint -root . -rules tools/lint-rules/imports.txt` (gate G1) | Fable | Canaries caught: core imports wire layer, engine imports adapter, pgproto3 outside its packages, third-party import, second requirement, testdata package imports the wire layer, product Go imports a testdata package |
| `../tools/detlint/cmd/boundlint` | Bounded-channel lint for the shell: unbuffered channels, unnamed capacities, growth in unbounded loops (fields, pointers, package variables, maps not made locally, and through calls within the package). Run: `$BIN/boundlint -root . -allow tools/lint-rules/boundlint-allow.txt internal/shell/...` (gate G1) | Fable | Canaries caught: unbuffered channel, literal capacity, append in a receive loop, append through a local pointer, append in a helper the loop calls |
| `../tools/lint-rules/` | The rules and package lists the gate reads: `imports.txt`, `det-packages.txt`, `race-packages.txt`, `shell-packages.txt`, the three allow files, and `prose-paths.txt` (what the prose pass may change; G2 at the final stage) | Fable | Package lists empty at chunk 0 except `shell-packages.txt`; canaries `g2-prose-changed-script`, `-testdata`, `-generated` caught |

### Orchestration (`orch/`, written by the orchestrator before chunk 0; indexed here)

| Path | Purpose and how to run | Author seat | What it proved / last result |
| --- | --- | --- | --- |
| `orch/launch.sh` | Launches one headless agent, detached, in a worktree; checks 15 s in that it is alive and in the right folder; writes `/root/runs/zgateway-p2-<run>.{log,pid,exit}`. Run: `agent_work/orch/launch.sh <run-name> <seat> <workdir> <brief-file>` (seats: opus, fable, neon, neon-oc, astra) | Fable (orchestrator) | Launched the chunk 0 builder |
| `orch/wait.sh` | Waits for launched runs to stop, with a deadline; exit 0 all zero, 1 a nonzero exit, 3 a run died with no exit file, 4 still running. Run: `agent_work/orch/wait.sh <deadline-seconds> <run-name>...` | Fable (orchestrator) | Not run by the builder |
| `orch/land.sh` | Lands a chunk on main from the main checkout. Requires a gate result on the branch with chunk = the chunk landed, overall pass, counts_for_landing true, partial false, dirty_tree false, stage final (foundation for `c00-foundation` only), a gated commit on the branch after which only `chunks/<chunk-dir>/gate/result.json`, `chunks/<chunk-dir>/*.md`, `INDEX.md`, `STATE.md` and `docs/knowledge/*.md` changed; plus the review verdict and `ADVERSARIAL.md`; merges `--no-ff` and pushes. Run: `agent_work/orch/land.sh <chunk-dir> <branch> <message-file>` (`LAND_NO_REVIEW=1` is honoured for `c00-foundation` only) | Fable (orchestrator); tightened in the chunk 0 repair round | Never run for real by a builder. All 14 landing attacks of `adversarial/c00-foundation/attack.sh` are refused, in throwaway clones |
| `orch/pages-check.sh` | After a push: waits for the Pages run of a commit, then requires HTTP 200 for each site path. Run: `agent_work/orch/pages-check.sh <commit-sha> <site-path>...` | Fable (orchestrator) | Not run by the builder |
| `orch/usage.sh` | Usage gauges before a wave: Neon through `agy -p "/usage"`, limit messages in the newest run logs for the others. Run: `agent_work/orch/usage.sh` | Fable (orchestrator) | Not run by the builder |

### Adversarial pass on the gate (`adversarial/c00-foundation/`)

| Path | Purpose and how to run | Author seat | What it proved / last result |
| --- | --- | --- | --- |
| `adversarial/c00-foundation/attack.sh` | 103 attacks on the gate, the lints, the site build and `orch/land.sh`, each in a throwaway clone under `/tmp/zgw-c00-adv/`. Run from the repository root: `agent_work/adversarial/c00-foundation/attack.sh [attack-id ...]`. It rewrites `RESULTS.md`, `evidence.json` and `STATE.md` beside it, so to try a change without touching them, run it in a scratch copy of the tree | Adversary seat | Before the repair round: 87 held, 16 broken (`RESULTS.md`). After it, run in a scratch copy of the repaired tree: 102 held, 1 broken (`foundation-reuse`: the gate now refuses with exit 2 where the attack expects a red G2); see "Repair round" in `chunks/c00-foundation/NOTES.md` |
## Chunk 1 — simulator kernel (Opus, 2026-11-24)

Notes: `chunks/c01-sim-kernel/NOTES.md`. Durable facts: `../docs/knowledge/sim-kernel.md`, `../docs/knowledge/sim-kernel-provenance.md`.

| Path | Purpose and how to run | Author seat | What it proved / last result |
| --- | --- | --- | --- |
| `../cmd/mgsim` | Runs a named simulator scenario outside `go test`; prints the trace and state digests and the REPLAY line. Run: `go run ./cmd/mgsim list`, `go run ./cmd/mgsim run <scenario> -seed N [-trace]` | Opus | All 11 toy scenarios pass; two processes print identical digests (`TestCrossProcessReplay`) |
| `chunks/c01-sim-kernel/gate/extra.sh` | Gate G10 for chunk 1: every `mgsim list` scenario passes `TestScenario` on the pinned seeds, the planted-bug canary and the shrinker test pass, and two processes of the built `mgsim` agree for every scenario. Run: `agent_work/chunks/c01-sim-kernel/gate/extra.sh` | Opus | Green in 2 s |
| `../internal/sim/simtoy/testdata/golden-digests.txt` | Golden trace and state digests of every toy scenario on the pinned seeds. Update after review: `go test ./internal/sim/simtoy -run TestGoldenDigests -update` | Opus | 55 lines (11 scenarios x 5 seeds) |


### C4 as-built mechanism (2026-11-24)

| Path | Purpose and how to run | Author seat | What it proved / last result |
| --- | --- | --- | --- |
| `../docs/c4/gen.py` | Source of the C4 set, now with the AS BUILT block at its top (`LANDED`, `BUILT`, `PARTIAL`, `REL_BUILT`): the one edit at a chunk landing. Built parts are written with status `existing` and drawn solid, the rest `planned` and dashed. Run: `python3 docs/c4/gen.py`. How to mark a chunk: `../docs/knowledge/c4-as-built.md` | Fable | Chunk 1 marked: 118 elements (14 built), 177 relationships (16 built), 12 diagrams. `c4-check.mjs`: decode ok, 0 errors, 0 warnings, 12 of 12 scenes. Site built (18 pages); `check-mermaid.py` against a local server: 12 of 12 pictures drew |
| `phase1/check-mermaid.py` | Also works on a local build: `scripts/site.sh /tmp/c4-site`, then `python3 -m http.server 8765 --bind 127.0.0.1` in that folder, then `agent_work/phase1/check-mermaid.py http://127.0.0.1:8765` | Fable | 2026-11-24, local build with chunk 1 marked: 18 pages ok, 12 of 12 pictures drew |

## Chunk 5 canvas — how other projects handle the classifier problem (Fable coordinating Opus research agents, 2026-11-25)

Research only; chunk 5 stays parked. The synthesis is `chunks/c05-classifier/CANVAS.md`.

| Path | Purpose and how to run | Author seat | What it proved / last result |
| --- | --- | --- | --- |
| `chunks/c05-classifier/canvas/briefs/` | The mission (`00-mission.md`) and the twelve per-cluster research briefs (shared rules, the five questions, seven probe statements, the findings format) | Fable | Dispatched once; no brief needed a retry |
| `chunks/c05-classifier/canvas/conductor.sh` | Rolling conductor: keeps at most `MAXPAR` agents running through `orch/launch.sh`, one re-dispatch per failed agent with the log tail appended to its brief, a deadline per agent, fails closed if a findings file is missing. Run: `MAXPAR=5 ./conductor.sh <slug>...` from a scratch folder holding `findings/` (paths inside point at `/root/scratch/c05-canvas` and `/root/briefs/zgateway-c05-canvas-<slug>.md`) | Fable | 2026-11-25: twelve Opus agents, five at a time, all exit 0 on the first try in 20 minutes (`canvas/conductor-run.log`) |
| `chunks/c05-classifier/canvas/findings/` | One findings file per cluster: sources with commit SHAs, answers to the five questions with file-and-line citations, a probe table, mechanisms the assessment did not consider, and what could not be verified | Opus research agents | 12 files, about 5,500 lines. Holes reported in other projects are read from code, not executed, unless marked as run |
| `chunks/c05-classifier/canvas/experiments/postgres-server/` | SQL scripts, a raw wire client (`wire.py`) and their outputs (`*.out.txt`; the repository ignores `*.out`) against a throwaway PostgreSQL 17.11 container: what `DISCARD ALL` resets and leaves, what a read-only transaction allows, which privileges can be revoked, the `CommandComplete` tag per statement, cost of `DISCARD ALL` against a catalog "is this session dirty" query (`dirty-check.sql`). Run: `docker run --rm -d --name c05canvas-pg -e POSTGRES_PASSWORD=x postgres:17`, then `docker exec -i c05canvas-pg psql -U postgres < e1-discard-residue.sql` and so on; `pgbench -n -c 1 -T 8 -f q-discard.sql` for the timings | Opus research agent (cluster 12) | `DISCARD ALL` on a clean session 0.044 ms against `SELECT 1` 0.056 ms; the random seed survives it; a client `COMMIT` escapes a `BEGIN READ ONLY` wrapper; `SELECT … INTO TEMP` is tagged `SELECT` |
| `chunks/c05-classifier/canvas/experiments/libpg-query/` | Go benchmarks and probes of `pg_query_go` (cgo) and `wasilibs/go-pgquery` (WASM), saved as `.go.txt` with `go.mod.txt` so that the repository's Go tooling ignores them; outputs beside them. To run: copy into a scratch folder, rename to `.go` under the folder named by each file's prefix, `go test -bench . -benchmem -cpu 1` | Opus research agent (cluster 8) | Short statement: split 3.4 µs, parse 10–47 µs with cgo; WASM 2–6× slower with a 3.2 s, 280 MB first call; the scanner assumes `standard_conforming_strings = on` and disagrees with a server where it is off (`probe_out.txt`, `live_pg_probe.sh`) |

## Chunk 5 — statement classifier and the contracts, reworked under ruling 9 (Opus, 2026-11-26)

Notes: `chunks/c05-classifier/NOTES.md` (the first build's are under `chunks/c05-classifier/parked/`). Durable facts: `../docs/knowledge/classifier.md`.

| Path | Purpose and how to run | Author seat | What it proved / last result |
| --- | --- | --- | --- |
| `../scripts/gen-docs.d/c05-contracts.sh` | Part of `scripts/gen-docs.sh`: generates `docs/error-contract.md`, `docs/reject-list.md` and `docs/safe-shape.md` from `internal/pgwire`; `--check` diffs (gate G8) | Opus | Green on the rework; the three documents match the code |
| `../docs/safe-shape.md` | Generated: the safe-shape rules, stop list, parenthesis keywords and share allow-list (what `coalesce: auto` shares and what counts as a read for `08006`) | Opus | Held by `TestClassifierContractDocsMatchTheCode` |
| `../docs/error-contract.md`, `../docs/reject-list.md` | Generated error contract and reject list (a best-effort courtesy) | Opus | Held by the same test |
| `../internal/pgwire/testdata/classifier-corpus.txt` | The classifier corpus: 599 records (flags `R:`/`F`, `fits`, `was-co`, `new`, `hint`, `bad`, `noexec`) | Opus | 60 fit the safe shape; 40 of the first build's 116 coalescable records still fit; no record the first build refused to coalesce fits |
| `../internal/pgwire/pgwiretest/clsworld/` | The reject-list scenario world (gateway on chunk 4's framer, fake Postgres, probes), importable by the flush harness | Opus | Both scenarios pass on 200 sweep seeds; replay digests equal across processes |
| `../internal/obs/clssrc/` | Maps the classifier, reply and discard-until-Sync snapshots to the `classifier` component | Opus | Every snapshot field reaches an exported value; every metric moves |
| `../test/analysis/classifier-reject-only/`, `../test/analysis/classifier-reject-list/` | SQL over flushed files (gate G7): rejects = statements sent, fake statements = 0; forwarded = fake statements started | Opus | Zero rows on correct runs; each file fails on a planted wrong number (`TestClassifierSQLCatchesPlantedNumbers`) |
| `../test/oracles/dirty-check.sql` | Shipped session-cleanliness oracle (prepared statements, cursors, listens, own advisory locks, session-sourced settings, temp relations, role change); compare before and after. Copied from the canvas experiment; chunk 6 reuses it | Opus | Catches every session-change canary in `TestTierRSafeShapeDifferential` |
| `chunks/c05-classifier/gate/extra.sh` | Gate G10 for chunk 5: both reject-list scenarios (pinned seeds + 20) in two processes with equal digests, the planted `SET` and alphabet canaries, the classifier SQL files with DuckDB required | Opus | 42 scenario runs replayed with identical digests; both canaries caught; SQL files catch planted numbers |
| `chunks/c05-classifier/parked/ADVERSARIAL-RESULTS.md` | The first adversary's results on the first build (its harness was hoisted into the shipped suite and deleted) | Astra (adversary) | History: 29 held, 3 broken against the first build |

### Chunk 5 rework — verification record (two adversarial passes, Nick's ruling of 2026-11-26)

| Path | Purpose and how to run | Author seat | What it proved / last result |
| --- | --- | --- | --- |
| `adversarial/c05-classifier/` | Astra's attacks on the 11 claims. Run: `go test -count=1 ./agent_work/adversarial/c05-classifier/...` (starts its own Postgres 17 container) | Astra | 31 held / 5 broken, then 32 pass / 4 settled |
| `adversarial/c05-classifier-neon/` | Neon's independent attacks on the same claims. Run: `go test -count=1 ./agent_work/adversarial/c05-classifier-neon/...`; real server: `./run-tier-r.sh` | Neon (separate from the reviewer) | 33 held / 5 broken, then 35 pass / 3 settled |
| `chunks/c05-classifier/RECONCILIATION.md` | The two passes and review 1 reconciled into 11 decisions for the repair round | Fable | Nothing dropped; three Astra-only and three Neon-only findings judged real |
| `chunks/c05-classifier/ADVERSARIAL.md`, `ARBITRATION-1.md` | The adversarial record after repair; Astra's arbitration after the second review FAIL | Fable; Astra | Arbitration PASS after a one-line fix (`f914f7c`) |

## Change request CR-1 — shrinker value lowering and the fuzz rules (Opus, 2026-11-26)

Record: `chunks/cr01-shrink-fuzz-rules/` (change request, brief, notes, two reviews, adversarial record).

| Path | Purpose and how to run | Author seat | What it proved / last result |
| --- | --- | --- | --- |
| `../scripts/fuzz.sh` (changed) | Now prints and, with `FUZZ_REPORT`, writes a rate table; classes each target `fuzz` or `seed-sweep` by `FUZZ_RATE_FLOOR` (100/s); fails closed without a readable rate. Test: `go test ./scripts/` | Opus | 12 targets; the rate is read from the last progress line only (attack fixed) |
| `../docs/knowledge/shrunk-scenarios.md` | How to turn a failing fault schedule into a committed scenario file | Opus | Used by `internal/sim/simtoy`'s planted bug |
| `adversarial/cr01-shrink-fuzz-rules/` | Astra's 24 attacks on the seven claims; `ADV_LONG=1 ./long-fuzz.sh` for the long fuzz campaign. Report assembled by the orchestrator (Astra's run was cut twice) | Astra; report by Fable | 22 held / 2 broken, then 23 pass / 1 settled; found the 13-second `FuzzWorldSchedule` input |

## Phase 2, chunk 6 — walking skeleton (Opus, 2026-11-26)

Record: `chunks/c06-walking-skeleton/` (brief, notes, gate extra).

| Path | Purpose and how to run | Author seat | What it proved / last result |
| --- | --- | --- | --- |
| `../scripts/chaos.sh` | `make chaos`: the named e2e scenarios, tier S (`TestScenarioE2E`, `CHAOS_SEEDS` seeds) then tier R (`TestTierR*` in `test/e2e`) when Docker is present, each with the SQL files of `test/analysis/<scenario>/`; one line per scenario, `VERBOSE=1` for the raw output | Opus | 21 tier S scenarios green at 3 seeds; tier R 14 tests green in about 30 s |
| `../scripts/fuzz-plant.sh` | Plants the queue-slot ordering bug (`MG_FUZZ_PLANT=queued-vanish`) and requires `FuzzEngineOps` to fail with the queue invariant within `FUZZ_SECONDS` (default 20); removes the crasher it writes. Run by the chunk's gate extra | Opus | Found in 1–4 s in 5 of 5 runs (3 clients over 1 connection, empty fuzz cache); the minimised input is `test/e2e/testdata/shrunk/queued-vanish.faults` (4 operations) |
| `../scripts/gen-docs.d/c06-contracts.sh` | Generates `docs/timeout-budget.md` and `docs/capacity-table.md` from `internal/proxy/contracts.go` and `internal/shell/contracts.go`; `--check` diffs | Opus | Part of `scripts/gen-docs.sh` (gate G8) |
| `../scripts/tier-r.sh` (changed) | Passes `MG_TIER_R_PREFIX` and `RUN_DIR` to the tests and also removes every container named `<prefix>-*` (chunk 6 starts `<prefix>-c06` itself) | Opus | Full tier R (chunks 2, 4, 5, 6) green |
| `../docs/knowledge/walking-skeleton.md` | Facts for the builders of chunks 6a–13: the engine API and read protocol, seams, plants, shell, e2e harness | Opus | — |
| `chunks/c06-walking-skeleton/gate/extra.sh` | G10: canaries and ablations, the shrunk fuzz scenario, `scripts/fuzz-plant.sh`, the simrun baseline SQL with DuckDB required | Opus | — |
| `adversarial/c06-walking-skeleton/` | Astra's 46 attacks on the 15 claims. Run: `go test -count=1 ./agent_work/adversarial/c06-walking-skeleton/...`; long and real-server: `ADV_LONG=1` | Astra | 43 held / 3 broken, then 44 pass / 2 settled (`chunks/c06-walking-skeleton/ADVERSARIAL.md`) |
| `../scripts/counted-growth.sh` | Gate G3 `counted` (and so `scripts/ci-fast.sh`): runs the linear-growth tests with work counting compiled in, `go test -tags clscount -count=1 -run Linearly -v ./internal/pgwire/`. Red when the run fails or when one of the five named growth tests did not pass. `COUNTED_GO` replaces `go`. Test: `go test ./scripts/` (`counted_growth_test.go`) | Opus | CR-3 repair, 2026-11-27: 5 tests pass in about 1 s; the stub tests show it red on a failing run, on "no tests to run" and on each missing test |

## Phase 2, chunk 7 — pool (Opus, 2026-11-27)

Record: `chunks/c07-pool/` (brief in `/root/briefs/zgateway-p2-c07-pool.md`, notes, gate extra).

| Path | Purpose and how to run | Author seat | What it proved / last result |
| --- | --- | --- | --- |
| `../scripts/fuzz-plant.sh` (changed) | Now also plants the pool core's ordering bug (`MG_FUZZ_PLANT=reset-clears-suspect`: a reset clears a suspect flag raised while the connection was lent) and requires `FuzzPoolOps` to find it within `FUZZ_SECONDS`; one NOTE line for both targets | Opus | `FuzzPoolOps` found it in 18–20 s on a host at load 18 (3 runs, 2 found); see NOTES "G9 results" for the gate's run |
| `chunks/c07-pool/gate/extra.sh` | G10: the pool core's property tests at full size, the pool canaries and ablations (cap, creation rate, check deadline, suspect, on-switch tag, breaker, jitter, the night trickle all-off and each remedy alone), the fault driver's self-test, the engine core-path tests, `scripts/fuzz-plant.sh` | Opus | — |
| `../test/e2e/c07_faultnet.go` | The G1 fault driver: a relay between the proxy and the fake (blackhole, peer_forgot, idle_drop with modelled keepalive, slots held, polite close, the `setseed` rewrite). Self-test: `go test ./test/e2e -run TestPathFault` | Opus | Blackhole on one connection leaves the other unchanged; clearing idle_drop restores a never-faulted run |
| `../docs/knowledge/pool-core.md` | Facts for the builders of chunks 8–13: the pool core's API and rules, the engine's core path, the cancel transport action, the tests to copy | Opus | — |
## Phase 2, chunk 6a — operator tools (Opus, 2026-11-27)

Record: `chunks/c06a-operator-tools/` (brief, notes with the promotion ledger, gate extra).

| Path | Purpose and how to run | Author seat | What it proved / last result |
| --- | --- | --- | --- |
| `../cmd/loadgen` | The open-loop load tool: `go run ./cmd/loadgen -addr 127.0.0.1:6432 -password <pw> -rate 200 -duration 30s -profile errors -seed 1 [-csv-dir d]`; `-plan N` prints the operations without a network | Opus | 2026-11-27 on the dev stack: 200/s errors profile, 1,000 intended, `0A:72 22:65 23:85 42:76`, SHORTFALL (230 late, 38 could not send: the proxy's back pressure under host load 6–20); Prometheus completed = the tool's ok + db-error (890) |
| `../cmd/mgctl` | Read-only operator client: `mgctl -admin <host:port> health \| stats [-watch] \| metrics \| tenants \| config show`, `mgctl config check <file>`, `mgctl verifier -password -`, `mgctl -addr <host:port> query -user U -password - "<sql>"`; `MGCTL_TRANSCRIPT=<file>` | Opus | Model tests green in tier S (4 seeds, replayed) and tier R (real binary, mgctl-made verifier) |
| `../deploy/dev/` (`make run-up`, `make load`, `make run-down`) | The dev stack: Postgres 17.11 + Prometheus v3.9.1 (127.0.0.1 only, `mg-` names, tmpfs), the gateway on the host, the wait for the scrape, URLs; settings in `deploy/dev/env.sh` | Opus | `TestTierRRunTargetsUpLoadDown` green: up in about 60 s on a loaded host, nothing left after run-down |
| `../scripts/report.sh` (`make report RUNS=… \| SEEDS=N`) | The story report over flushed CSV runs; SQL in `../test/analysis/report/sections/` | Opus (helper agent) | Golden, planted-failure, empty, truncated and 3-vs-300-seed tests green with DuckDB 1.5.x |
| `../scripts/sweep.sh`, `../scripts/chaos.sh` (changed) | Concise by default (one line per scenario, failures with their REPLAY lines), `VERBOSE=1` raw; `SWEEP_GO`, `CHAOS_GO` test seams | Opus (helper agent) | `scripts/c06a_report_scripts_test.go` green |
| `../docs/knowledge/operator-tools.md` | Facts for later builders: the load tool's core and runner, mgctl's surface, the dev stack, the report's SQL placement | Opus | — |
| `chunks/c06a-operator-tools/gate/extra.sh` | G10: the load tool and mgctl packages in full (binary differential, cross-process repeatability, canary), the mgctl tier S model, the report and script tests with DuckDB required | Opus | — |
## Phase 2, chunk 8 — admission queues (Opus, 2026-11-27)

Record: `chunks/c08-admission/` (brief, notes, gate extra).

| Path | Purpose and how to run | Author seat | What it proved / last result |
| --- | --- | --- | --- |
| `../scripts/admission-head-to-head.sh` | Prints the admission head-to-head table (one DuckDB query, `test/analysis/admission-head-to-head/head-to-head.query`) over a root of flushed runs; `--run [root]` runs the scenarios first; `--check` reruns them and diffs with the committed `head-to-head.md` | Opus | Table reproduced from seed 1 (both rules, both arms, 1.2×/2×/10×) |
| `../scripts/fuzz-plant.sh` (changed) | Also plants `admission.PlantRemoveSwapsTail` (`MG_FUZZ_PLANT=admission-remove-order`) and requires `FuzzAdmissionOps` to find it within `FUZZ_SECONDS` | Opus | Found after 7 s of 20 s (empty cache) |
| `chunks/c08-admission/gate/extra.sh` | G10: `fuzz-plant.sh` and `admission-head-to-head.sh --check` | Opus | — |
| `../docs/knowledge/admission-queues.md` | Facts for chunks 9, 10, 13, 14: the core API, the seam, plants, the dispatcher hook, scenario cost | Opus | — |

## Phase 2, chunk 9 — fairness (Opus, 2026-11-27)

Record: `chunks/c09-fairness/` (brief, notes, gate extra).

| Path | Purpose and how to run | Author seat | What it proved / last result |
| --- | --- | --- | --- |
| `../scripts/fuzz-plant.sh` (changed) | Also plants `admission.FairPlantUnpopKeepsTurn` (`MG_FUZZ_PLANT=fairness-unpop-turn`) and requires `FuzzFairnessOps` to find it within `FUZZ_SECONDS`; no committed seed finds it on its own | Opus | Found after 2 s of 20 s (empty cache) |
| `chunks/c09-fairness/gate/extra.sh` | G10: `fuzz-plant.sh`, then the fairness canaries in `internal/admission`, `internal/proxy` and `test/e2e` on their own | Opus | — |
| `../docs/knowledge/fairness.md` | Facts for chunks 10, 13, 14: the fair rule as built, where chunk 10 sets the limit (`Queues.SetLimit`, `AdmissionQueue.SetLimit`), the metrics, scenario cost | Opus | — |
