# mini-gateway — chunk 6a: Operator tools — builder brief

Seat: Opus (product Go, scripts and tests, logic only). Chunk directory name:
`c06a-operator-tools`. Worktree: `/root/Software/mini-gateway-work/c06a` — branch
`work/c06a-operator-tools`.

Read, in this order, before writing anything:

1. `/root/briefs/zgateway-p2-builder-rules.md` — they bind you.
2. `PLAN.md`: "Chunk 6a — Operator tools" (your scope — every deliverable and test there
   is required); section 19.2 (the load-tool ruling and why Go, `cmd/loadgen`) and 19.3
   (the six developer-experience items, each with what was adopted); section 6.3 (concise
   and verbose output); 6.5 and 7.7–7.8 (the CSV flush and DuckDB); 11 (benchmarking:
   open-loop load, latency from intended send time — chunk 14 completes the tool, do not
   build its benchmark arms); 12.2 step 9 (the promotion rule).
3. `/root/briefs/zgateway-loadgen-ruling.md` and `/root/briefs/zgateway-dx-patterns-ruling.md`.
4. `agent_work/chunks/c06-walking-skeleton/NOTES.md`, `docs/knowledge/walking-skeleton.md`:
   the binary, its config, its admin endpoints (health, stats, metrics — read-only), the
   e2e harness and `make chaos`.
5. `internal/testclient` (the client path you reuse), `internal/obs` (collector, CSV
   flush), `scripts/csv-assert.sh`, `test/analysis/`.
6. `docs/knowledge/foundation-gates.md`.

## Purpose

Being able to watch the proxy work: start a gateway, a Postgres and a Prometheus with one
command, put a constant load on it, watch the graphs move; ask a running gateway how it
is; and read a test run as a short report instead of raw output.

## What is yours — and what is not

Yours: `cmd/loadgen`, `cmd/mgctl`, `internal/mgctl`, `internal/loadgen` (if you split the
logic out), `deploy/dev/`, the `Makefile` targets `run-up`, `load`, `run-down`, `report`,
`scripts/report.sh`, `test/analysis/report/`, the concise/verbose switch in
`scripts/sweep.sh` and `make chaos`, and the promotion pass.
NOT yours: `internal/proxy`, `internal/shell`, `internal/pool`, `internal/admission`,
`internal/pgwire`, `internal/sim` — you change no engine or core file. If `mgctl` needs
something the admin surface does not serve (per-tenant numbers, the config fingerprint),
first look for it in the stats JSON; if it is truly absent, add a read-only field through
`internal/obs` adapters only and say so in NOTES.md; if that is not enough, record the gap
under "Deviations" rather than edit the engine.

## Deliverables

As PLAN.md's chunk 6a lists them, all five:

- **`cmd/loadgen`** — open-loop at a constant rate (an intended send time per request;
  never waits for a reply before the next send); flags `-rate`, `-duration`, `-conns`,
  `-tenants`, `-profile`, `-seed`, TLS and SCRAM settings; the seed printed at the start
  and everything drawn from it; profiles `simple` and `errors`; the once-a-second status
  line; the end-of-run summary with the **SHORTFALL** line and the count it could not
  send; its own snapshot through a collector and the CSV flush. Its scheduling core is a
  pure function of (seed, rate, time) so it is testable without sockets.
- **`cmd/mgctl`** with logic in `internal/mgctl` (no flag or HTTP-framework imports
  there): `health`, `stats` (`--watch`), `metrics`, `tenants`, `config show`,
  `config check <file>` (the gateway's own validation, offline), `verifier`, `query`.
  Read-only. Standard-library `flag` with subcommands; `go.mod` keeps its one requirement.
- **`deploy/dev/`** and the run targets — docker-compose with Postgres 17 and Prometheus
  (images pinned by version, ports on 127.0.0.1, names prefixed `mg-`), Prometheus
  scraping the gateway's `/metrics`; `make run-up` waits until Prometheus reports the
  target up and prints the URLs and three ready-made graph links; `make load RATE=…
  DURATION=… PROFILE=… SEED=…`; `make run-down` leaves nothing behind (containers,
  networks, volumes, processes). The Prometheus image must be pulled once: if the pull
  fails on this machine, say so in NOTES.md and leave the smoke test behind its switch.
- **The story report** — `scripts/report.sh <run directory or root>` and `make report
  RUNS=…`, SQL under `test/analysis/report/`: what the run proves; how the data was
  collected; inputs; a legend per table; coverage by scenario, statement class and
  outcome; every failed assertion and oracle mismatch in full; a verdict table ending in
  one line and a non-zero exit on any failure. The same at 3 seeds and at 300.
  `scripts/sweep.sh` and `make chaos` print one line per scenario by default, raw output
  under `VERBOSE=1`.
- **The promotion pass for chunks 1–4** (PLAN.md 12.2 step 9): the adversarial tests of
  `agent_work/adversarial/c01-…` to `c04-…` that held and still describe the product move
  into the shipped suite beside the code they test (COPY them — the originals under
  `agent_work/adversarial/` stay untouched as evidence); anything that needs vm-1 tools
  (Docker, DuckDB) goes behind the tier R switch or `testing.Short()` so CI stays under
  five minutes. A ledger in NOTES.md: per chunk, promoted / left behind with the reason.

## Tests (write these first)

As PLAN.md's chunk 6a: the load tool (achieved = intended at a rate the fake can serve;
same seed, same operation sequence; a throttled server gives SHORTFALL and a non-zero late
or could-not-send count; a stalled server shows in latency from intended send time);
`mgctl` as a seeded model test in process against the simulator's world and in tier R
against the real binary, with `MGCTL_TRANSCRIPT=<file>`; `make run-up` → `make load` →
`make run-down` as a tier R smoke test (the Prometheus count of completed requests is
non-zero and equals the load tool's own count from its CSV flush); the report as golden
output over recorded run directories, with a planted failing assertion that appears in
full and makes the exit status non-zero.

## Observability

The load tool's snapshot (intended, sent, completed by outcome, late, could not send,
latency histograms), with counters-move and conservation tests (intended = sent + could
not send; sent = completed by outcome + in flight at the end). No new proxy metrics.

## Claims this chunk makes (an adversary will try to break each)

1. **Open loop**: the intended send times depend only on rate, duration and seed — never
   on replies; a slow or stalled server does not slow the schedule.
2. **Latency is measured from the intended send time**, so a stall shows as latency, not
   as a lower rate.
3. **It never slows down silently**: whenever achieved < target the summary has the
   SHORTFALL line, and late and could-not-send counts are exact.
4. **Repeatable**: the same seed and flags give the same sequence of operations, keys and
   payloads, in process and across processes.
5. **Conservation** in the load tool's snapshot, on every exit path including an
   interrupt.
6. **`errors` profile**: every outcome class it promises appears (database errors by
   class, proxy refusals) and is counted under the right SQLSTATE class.
7. **`mgctl` is read-only**: no subcommand changes a running gateway; `config check`
   accepts exactly what the gateway accepts and refuses exactly what it refuses (same
   validation code, shown by a differential test over generated configs).
8. **`mgctl verifier`** produces a verifier the gateway's SCRAM login accepts, and never
   prints the password.
9. **`mgctl query`** prints the rows, or the SQLSTATE with its error-contract row, for
   every row of the table that can be produced through a running gateway.
10. **The report's verdict is honest**: any failed assertion or oracle mismatch in any run
    directory makes the exit status non-zero and appears in full; an empty or truncated
    run directory is a failure, not a pass.
11. **The report is stable**: the same run directories give byte-identical output.
12. **Concise by default**: one line per scenario; `VERBOSE=1` gives the raw output; a
    failure is never hidden by the concise mode.
13. **The run targets clean up**: after `make run-down` no `mg-` container, network or
    volume and no gateway process remains; ports were bound on 127.0.0.1 only.
14. **The promotion ledger is true**: every promoted test exists in the shipped suite and
    asserts what its original asserted; CI's fast set stays under five minutes.
15. **`go.mod` still has one requirement.**

## Size

About 1,300 product lines. None of the on-demand parts runs in CI beyond unit tests.

## Wave B — you are one of three concurrent builds (binding)

Chunks 6a (operator tools), 7 (pool) and 8 (admission queues) build at the same time in
separate worktrees and land one at a time. To keep the three apart:

- Touch only the files your brief names as yours, plus the smallest wiring edits it lists.
  Where you must edit a shared file (`internal/config`, `internal/proxy/seams.go` wiring,
  `cmd/mini-gateway`, `tools/lint-rules/*`, `scripts/gen-docs.d/`, `agent_work/INDEX.md`,
  `test/e2e` shared helpers), ADD (a new file, a new block at the end, a new key section)
  rather than reorder or reformat what is there, so the later landing merges cleanly.
- New e2e scenarios, SQL assertions and gate extras go in files of your own
  (`test/e2e/<chunk>_*.go`, `test/analysis/<scenario>/`, `agent_work/chunks/<chunk-dir>/gate/extra.sh`).
- The machine runs three builds: the full gate takes about 40 minutes alone and longer
  now. Run fast checks often (`go test ./<your packages>/...`, `GATE_ONLY=G0,G1,G2`), and
  the full `scripts/gate.sh <chunk-dir>` ONCE at the end, detached, on a clean committed
  tree.

## Lessons from chunks 5, 6 and CR-1 (binding)

1. **Time passes before the event.** In the engine, timers due at or before an event's
   time fire before the event is processed. Your core follows the same rule: a deadline
   that equals `now` has expired. Test at equality.
2. **Leftover bytes and silence prove nothing.** Bytes after a `ReadyForQuery` in the same
   read are unread bytes (close, never lend). "Did not reach the database" is claimed only
   on the driver's explicit zero-byte refusal (`EvDBWriteRefused`).
3. **Every fix and every mechanism has a FAST regression test** (plain `go test ./...`, no
   Docker) that fails when the mechanism is removed. A behaviour checked only in tier R is
   not covered. The reviewer reverts your mechanisms one by one.
4. **Real-server tests share one Postgres cluster** and `scripts/tier-r.sh` runs packages
   one at a time. Server-wide views see every database: filter by your own database or
   connection.
5. **Bound the cost of one fuzz execution** (operation count, sizes, virtual time), with a
   test; a simulation-level fuzz target takes its simulator seed from a constant, never
   from a hash of its input (PLAN.md 6.6), and must run above 100 executions per second or
   be reported as a seed sweep.
6. **The fuzz inventory is pinned** in `agent_work/adversarial/cr01-shrink-fuzz-rules/adv_test.go`
   (15 simulator-level targets). Your new fuzz target makes two tests there fail until the
   orchestrator re-pins; you may not edit `agent_work/adversarial/`. List every fuzz
   target you add in NOTES.md with its seed source; those two tests
   (`TestAdv_6_GateSeedSweepCounts`, `TestAdv_7_InventoryAndHiddenHashImports`) are the
   only adversarial tests allowed to be red.
7. Go's fuzz tool sometimes ends a target with `context deadline exceeded` and no failing
   input. `fuzz.sh` fails closed on it. If your gate is red only for that, re-run the gate
   once and say so in NOTES.md; do not change `fuzz.sh`.
8. **A zero or omitted cap in config means its default; negative is refused naming the
   key.** Follow that for every key you add, and add each key to the generated timeout
   and capacity documents' source.
9. The simulator API is frozen (tag `freeze-sim`). If a scenario needs a fault the
   simulator lacks, first try a test-side wrapper (chunk 6 put a relay in front of the fake
   for bad reset replies). If a change to `internal/sim/...` is unavoidable it must be
   ADDITIVE (new fault kinds or options beside the old; every existing scenario's trace and
   digest unchanged, shown by a test), as small as possible, and listed at the top of
   NOTES.md under "Simulator change requests" with the reason — the reviewer and the
   adversary examine each one.

## Finish

As the builder rules say: `gofmt -l .` empty; build, vet and `go test -count=1 ./...`
green; your packages added to the determinism and race lists; the gate at the build stage
on a clean committed tree (delete the generated `gate/result.json` if untracked); NOTES.md
at `agent_work/chunks/<chunk-dir>/NOTES.md` complete — what you built, each open decision,
EACH CLAIM WITH ITS TEST, deviations, stated limits, commands, fuzz targets, simulator
change requests, index rows; a short knowledge note in `docs/knowledge/`; the summary and a
final line containing exactly AGENT-DONE.
