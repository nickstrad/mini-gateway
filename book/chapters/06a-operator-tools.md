# 6a. Watching it run: a load tool, a small client, and reports that say what a run proved

Chapter 6 produced a proxy binary that accepts Postgres connections, authenticates tenants, and forwards transactions to Postgres 17. Once the binary runs, a practical gap opens immediately: putting steady traffic on it means hand-rolling a driver, checking its health or configuration means typing `curl` against JSON and Prometheus endpoints, and reading what a multi-seed test sweep proved means scrolling through thousands of lines of `go test` output.

Chunk 6a builds the operator surface around that proxy without modifying a line of the engine or wire core:

- [`cmd/loadgen`](../../cmd/loadgen/main.go), [`internal/loadgen`](../../internal/loadgen), and [`internal/loadgen/live`](../../internal/loadgen/live): a constant-rate, **open-loop** load generator (a generator that schedules each request at a fixed intended time regardless of how slowly the server replies), with repeatable seeds, `simple` and `errors` profiles, live status lines, explicit `SHORTFALL` reporting, and its own CSV metric flush.
- [`cmd/mgctl`](../../cmd/mgctl/main.go) and [`internal/mgctl`](../../internal/mgctl): a read-only CLI client for querying a running gateway (`/healthz`, `/stats`, `/metrics`, tenant status, config fingerprint), validating config files offline, deriving SCRAM-SHA-256 verifiers, and running single statements through the proxy with error-contract decoding.
- [`deploy/dev`](../../deploy/dev/docker-compose.yml) and `make run-up` / `make load` / `make run-down`: a local stack of Postgres 17, Prometheus, and the host gateway bound strictly to `127.0.0.1`, ready to watch under load and guaranteed to clean up after itself.
- [`scripts/report.sh`](../../scripts/report.sh), [`test/analysis/report/sections`](../../test/analysis/report/sections), and concise output for [`scripts/sweep.sh`](../../scripts/sweep.sh) and [`scripts/chaos.sh`](../../scripts/chaos.sh): a DuckDB-backed story report that turns any set of flushed CSV run directories into a fixed-length summary of claims, coverage, full failures, and a single verdict line.
- Shipped regression suites beside the packages of Chapters 1 through 4: 132 durable property and regression tests (139 functions across 10 files) moved into the normal package suites behind `testing.Short()` or tier R switches so CI remains fast.

None of the interactive tools runs in CI beyond unit tests, and `go.mod` keeps its single external requirement (`github.com/jackc/pgx/v5`).

---

## Open-loop load without silent throttling (`cmd/loadgen`)

A shell loop calling `psql` cannot hold a constant rate, and a standard closed-loop benchmark tool such as `pgbench` waits for each reply before issuing the next request. When the server stalls for 600 ms, a closed-loop client stops sending during the stall and records one slow request instead of the dozens of requests that arrived while the server was frozen—a measurement failure known as **coordinated omission**.

`cmd/loadgen` avoids both problems in one Go tool that serves interactive exploration now and later benchmarking as well. Its design splits cleanly into two packages:

- [`internal/loadgen`](../../internal/loadgen) is a pure, determinism-linted core (no clocks, no goroutines, no floating point). A `Plan` holds `(Seed, Rate, DurationNs, Tenants, Keys, Profile)` and defines `Count()`, `At(i)`, `Due(now)`, and `Op(i)`. A `Tracker` owns the per-connection FIFO slot rings, latency histograms, and counters.
- [`internal/loadgen/live`](../../internal/loadgen/live/live.go) wraps the tracker in real sockets and goroutines: one coordinator goroutine owns `Tracker` and `obs.Collector`, while each open connection runs a dedicated writer and reader goroutine sharing the same SCRAM/TLS session path ([`internal/mgctl/session.go`](../../internal/mgctl/session.go)) built on `internal/testclient`.

**Why integer arithmetic and a per-index stream.** Computing intended send time as `i * NsPerSecond / Rate` overflows `int64` at high rates over long runs, and drawing operations from one shared RNG couples the operation mix to the schedule. [`internal/loadgen/plan.go`](../../internal/loadgen/plan.go) splits whole seconds from remainders and seeds an independent SplitMix64 stream from `(Seed, i)`:

```go
func (p Plan) At(i int64) int64 {
	return (i/p.Rate)*NsPerSecond + (i%p.Rate)*NsPerSecond/p.Rate
}

func (p Plan) Due(now int64) int64 {
	if now < 0 {
		return 0
	}
	whole, part := now/NsPerSecond, now%NsPerSecond
	n := whole*p.Rate + part*p.Rate/NsPerSecond + 1
	// Correct for integer truncation in the estimate so At(n-1) <= now < At(n) holds exactly.
	for n > 0 && p.At(n-1) > now {
		n--
	}
	for p.At(n) <= now && n < p.Count() {
		n++
	}
	return min(n, p.Count())
}
```

An operation whose intended time equals `now` is due immediately (matching the simulator's "time passes before the event" rule), and changing `-rate` or `-duration` shifts only `Op.At` without changing which tenant, key, SQL statement, or hex payload index `i` draws. Plans whose rate and duration produce zero operations (such as `-rate 1 -duration 500ms`) are refused at validation naming `-rate` and `-duration`.

Both profiles speak the `kv (k text primary key, v text)` dialect understood by `fakepg` and `deploy/dev/init.sql`:

- `simple` (default): 80% single-key reads (`SELECT v FROM kv WHERE k = 'k<n>'`) and 20% updates (`UPDATE kv SET v = '<hex>' WHERE k = 'k<n>'`).
- `errors`: 50% reads, 20% writes, 8% duplicate-key inserts (`23505`, class `23`), 8% missing-relation selects (`42P01` on Postgres or `42601` on the fake, class `42`), 7% division by zero (`22012`, class `22`), and 7% `LISTEN mg_load` (refused by the proxy's reject list with `0A000`, class `0A`).

Passing `-plan N` prints the first `N` operations and an FNV-64a digest without touching the network:

```sh
$ go run ./cmd/loadgen -plan 6 -seed 7 -profile errors
plan: seed 7 profile errors rate 100/s, 1000 operations planned; first 6:
0	at 0ns	tenant 1	missing-relation	SELECT v FROM mg_load_missing WHERE k = 'k175'
1	at 10.0ms	tenant 0	division-by-zero	SELECT 1 / 0
2	at 20.0ms	tenant 1	division-by-zero	SELECT 1 / 0
3	at 30.0ms	tenant 1	write	UPDATE kv SET v = 'f64922d1ddba007000961786e54a06b9532cea78df0accbedc646420a9941d' WHERE k = 'k247'
4	at 40.0ms	tenant 0	read	SELECT v FROM kv WHERE k = 'k349'
5	at 50.0ms	tenant 1	proxy-refusal	LISTEN mg_load
digest 8d16c9b3f53e6bfa
```

When `-seed` is omitted, `loadgen` draws a 63-bit seed from `crypto/rand` and prints it on the first line (`repeat with -seed N`) so any interesting run can be reproduced exactly.

### Back pressure, conservation, and `SHORTFALL`

Connections are assigned to tenants round-robin (`c mod -tenants`, requiring `-conns >= -tenants`). On each connection, requests may pipeline up to `-max-outstanding` (default 64, capped at 1,024). When an operation becomes due in `Tracker.Tick`:

- If the tenant has an open connection below `-max-outstanding`, the least-loaded connection takes the request (`Queued++`).
- Otherwise the tracker counts it `CouldNotSend++` immediately; future ticks stay on schedule.
- When a writer goroutine pulls a request, it posts `evSent` to the tracker *before* calling `Write` on the socket so a fast reply can never overtake its send event. If `now - dueAt` strictly exceeds `-late-after` (default 25 ms; equality is on time), `Late++` increments.
- On completion, the tracker records latency in two histograms: `latency` (`now - dueAt`, from intended send time) and `latency_from_send` (`now - sentAt`, as a coordinated-omission control). Error replies are grouped by their two-character SQLSTATE class (the standard five-character Postgres error code prefix), capped at 32 labels with overflow folded into `other`.

Every state transition, disconnect (`ConnDown`), signal interrupt (`Stop`), and end-of-run drain (`Finish`) preserves two conservation laws checked by `Snapshot.Check()` ([`internal/loadgen/tracker.go`](../../internal/loadgen/tracker.go)):

```go
if s.Intended != s.Sent+s.CouldNotSend+s.Queued {
	errs = append(errs, fmt.Errorf("%w: intended %d != sent %d + could not send %d + queued %d", ErrConservation,
		s.Intended, s.Sent, s.CouldNotSend, s.Queued))
}
if s.Sent != s.CompletedTotal()+s.InFlight {
	errs = append(errs, fmt.Errorf("%w: sent %d != completed %d + in flight %d", ErrConservation,
		s.Sent, s.CompletedTotal(), s.InFlight))
}
```

Achieved rate is `TargetRate * (Sent - Late) / Intended`, printed in tenths using overflow-safe integer division (`ns/unit*10 + ns%unit*10/unit`, so even `math.MaxInt64` nanoseconds formats without wrapping). Whenever `Late + CouldNotSend > 0`, the end-of-run summary prints a `SHORTFALL:` line and `loadgen` exits `3` (rather than `0`). If `-csv-dir` is set, the load tool flushes its own 14 metrics (component `loadgen`, declared in [`docs/csv-schema.md`](../../docs/csv-schema.md)) into `<csv-dir>/load-<profile>/<seed>/<start-ns>/`; any CSV flush error prints to `stderr` even under `-quiet` and exits `1`.

---

## A small, read-only client (`cmd/mgctl`)

`mgctl` answers the everyday questions an operator has against a running gateway without changing its state. Every admin subcommand sends HTTP `GET` only—to `/healthz`, `/stats`, or `/metrics`—while `config check` and `verifier` run offline, and `query` opens one Postgres connection to execute a single statement.

**Design choices and alternatives turned down.**
- **Standard-library `flag` instead of Cobra.** Although [PLAN.md section 19.3](../../PLAN.md#193-developer-experience-patterns-from-object-store-rootbriefszgateway-dx-patterns-rulingmd) permitted a CLI dependency for operator tools, `mgctl` and `loadgen` together have only a handful of subcommands and flags. Using `flag.NewFlagSet` per subcommand avoids adding third-party CLI packages and preserves the single-dependency rule in `go.mod`.
- **Logic separated from transport.** [`internal/mgctl`](../../internal/mgctl) imports neither `flag` nor `net/http` (enforced by `TestTheLogicPackagesImportNoFlagOrHTTPFramework`). Admin calls go through a one-method `Admin` interface (`Get(path string) (int, []byte, error)`), and queries open streams via a `Dialer` returning `io.ReadWriteCloser`. That lets the exact same command code run in process against the simulator's `proxysim.World` and collector.
- **Exact config parity.** `mgctl config check <file>` calls `config.Load` directly—the same function `mini-gateway -check` runs—so it cannot drift from the gateway's fail-closed rules:

```sh
$ go run ./cmd/mgctl config check internal/config/testdata/example.json
mgctl: internal/config/testdata/example.json: config: invalid: client_tls.cert_file: open internal/config/testdata/server.crt: no such file or directory
config: invalid: client_tls.key_file: open internal/config/testdata/server.key: no such file or directory
exit status 2

$ go run ./cmd/mgctl verifier -password example-pw
SCRAM-SHA-256$4096:cSvIgpo9mhCSvo/s+r27BQ==$BLaoRmg9Cohu6yTp8cuAfl8Mwmp6g5UglRYFqyrDDSw=:YrhLO/Bg1CIVibTCYKrhBaI8EFgmxc9FZyrG4rwFnoI=
```

Passwords passed via `-password -` (shared across `mgctl verifier`, `mgctl query`, and `loadgen` through `mgctl.ReadPasswordLine` in [`internal/mgctl/password.go`](../../internal/mgctl/password.go)) are read one byte at a time up to 1,024 bytes (excluding trailing `\r\n`), never echoed, and refused with exit `2` if longer instead of silently truncated. When `MGCTL_TRANSCRIPT=<file>` is set, `cmd/mgctl` logs each command, indented output, and exit code while redacting literal `-password` flags to `(password)`.

### Decoding errors against the error contract

When `mgctl query` receives an `ErrorResponse`, it looks up the reply in `pgwire.ErrorRows` ([`docs/error-contract.md`](../../docs/error-contract.md)) and prints whether the statement reached Postgres, whether a write may have applied, whether retrying is safe, and whether the client connection stayed usable.

A subtle trap sits inside code `08006`: `lost-after-read` (`ERROR`, `"mini-gateway: database connection lost"`) is a strict prefix of `lost-in-transaction` (`FATAL`, `"mini-gateway: database connection lost inside a transaction"`). Matching the first prefix in declaration order would tell an operator that a dropped transaction left the client connection usable. [`internal/mgctl/contract.go`](../../internal/mgctl/contract.go) ranks candidates by matching severity first, then exact text, then longest matching prefix:

```go
func messageRank(severity, message string, r pgwire.ErrorRow) int {
	rank := len(r.Message)
	if message == r.Message {
		rank += exactMatchBonus
	}
	if severity != "" && r.Severity() == severity {
		rank += severityMatchBonus
	}
	return rank
}
```

For `FATAL` replies whose wire text embeds dynamic names or differs from the table template (such as `tls-required` or `unknown-database`), `ContractRowFor` strips double-quoted identifiers and falls back to code plus word overlap. Replies not made by the proxy print as `pass-through`.

Here are real excerpts from a seeded `mgctl` transcript recorded against the simulated gateway (`MGCTL_TRANSCRIPT=... go test -count=1 -run '^TestMgctlSeededModelAgainstTheSimulatedGateway$' ./test/e2e`):

```text
$ mgctl health
  health: ok
    database   ok  database reachable
    collector  ok  publishing
[exit 0]

$ mgctl query -user tenant2 SELECT v FROM kv WHERE k = 'k4'
  v
  v4
  (1 rows)
  SELECT 1
[exit 0]

$ mgctl query -user tenant2 LISTEN jobs
  ERROR 0A000: mini-gateway: feature not supported by a transaction-pooling proxy
  error-contract row: unsupported-feature — Statement uses a feature on the reject list; reached the database: no; write applied: no; retry: no, change the client; client connection: stays usable
[exit 3]

$ mgctl query -user tenant2 SELECT 1 / 0
  ERROR 22012: division by zero
  error-contract row: pass-through (an error from Postgres itself, relayed unchanged)
[exit 3]

$ mgctl stats -match requests_
  instance gw-sim  build sim  config sim-fingerprint  seq 8
  COMPONENT  METRIC                   LABELS  KIND     VALUE
  engine     requests_accepted        -       counter  6
  engine     requests_completed       -       counter  6
  engine     requests_failed          -       counter  0
  engine     requests_in_flight       -       gauge    0
  engine     requests_in_flight_peak  -       gauge    1
  engine     requests_queued          -       gauge    0
  engine     requests_queued_peak     -       gauge    0
  engine     requests_received        -       counter  6
  engine     requests_rejected        -       counter  0
  engine     requests_shed            -       counter  0
[exit 0]

$ mgctl tenants
  instance gw-sim  build sim  config sim-fingerprint  seq 5
  no per-tenant numbers in this gateway's stats: no component it runs exports a tenant label yet
[exit 0]

$ mgctl config show
  running instance gw-sim
  build sim
  config fingerprint sim-fingerprint
  effective settings seen in its stats:
    pool.connections         2  (engine.database_capacity)
    queue.capacity           16  (engine.queue_capacity)
    limits.reply_buffer      262144  (engine.relay_buffer_capacity)
    (shell mailbox, fixed)   0  (engine.shell_mailbox_capacity)
  (give -config <file> to print that file's effective settings and compare its fingerprint)
[exit 0]
```

Exit status `3` means the gateway answered, but not with an ok result (health `degraded`/`failed`, or a query error); `1` means unreachable or runtime failure; `2` means usage or config error. `mgctl stats -watch` polls `/stats` on `-interval` and adds a signed `CHANGE` column between samples.

---

## One-command local stack (`deploy/dev/`)

`make run-up`, `make load`, and `make run-down` wrap [`deploy/dev/up.sh`](../../deploy/dev/up.sh), [`deploy/dev/load.sh`](../../deploy/dev/load.sh), and [`deploy/dev/down.sh`](../../deploy/dev/down.sh) around [`deploy/dev/docker-compose.yml`](../../deploy/dev/docker-compose.yml).

- **Images and isolation.** Postgres (`postgres:17.11` pinned by SHA-256 digest) initializes role `gateway` and table `kv` with rows `k1..k1000` via [`deploy/dev/init.sql`](../../deploy/dev/init.sql), publishes only on `127.0.0.1:55432`, and stores its data on `tmpfs` (in-memory filesystem). Prometheus (`prom/prometheus:v3.9.1`) runs with `network_mode: host` and `--web.listen-address=127.0.0.1:59090` so it can scrape the host gateway's admin port (`127.0.0.1:56433/metrics`) every second without waiting for the OTLP collector planned for Chapter 15. Every port and the `mg-`-prefixed project name can be overridden in [`deploy/dev/env.sh`](../../deploy/dev/env.sh).
- **Readiness and URLs.** `make run-up` builds `mini-gateway`, `mgctl`, and `loadgen` into `/tmp/mg-dev/bin`, derives SCRAM verifiers for tenants `t0..t3`, checks the generated config with `mgctl config check`, starts Postgres and Prometheus, launches `mini-gateway` on the host, waits for `mgctl health` to return `ok`, and polls Prometheus's `/api/v1/targets?state=active` until the gateway target reports `"health":"up"`. Only then does it print the Prometheus UI URL, targets URL, and three pre-filled graph links (`completed/s`, `replies/s` by reason, and `queued`).
- **Load and clean teardown.** `make load RATE=100 DURATION=30s PROFILE=errors SEED=1` runs `loadgen` against the stack and flushes its snapshot under `/tmp/mg-dev/load/`. `make run-down` stops the gateway process, runs `docker compose down -v --remove-orphans`, removes any remaining `mg-dev` containers, networks, or volumes, and fails closed with exit `1` if Docker cannot list those resources. CSV directories under `/tmp/mg-dev/csv` and `/tmp/mg-dev/load` are preserved so `make report RUNS=/tmp/mg-dev/load` can read them afterward.

---

## Concise runs and the story report (`scripts/report.sh`)

When a sweep runs 200 seeds across dozens of scenarios, raw `go test -v` output runs to tens of thousands of lines. [`scripts/sweep.sh`](../../scripts/sweep.sh) and [`scripts/chaos.sh`](../../scripts/chaos.sh) now default to concise output—one line per scenario showing the package, scenario name, seed count, verdict, and run directory—while `VERBOSE=1` streams the raw `go test` output alongside the summary. Concise mode never hides a failure: a failing seed prints its `--- FAIL`, error, and `REPLAY` lines beneath its scenario; a failed parent scenario or a non-zero test process exit after passing seeds still fails the run; and a SQL failure in `chaos.sh` marks the scenario `FAIL(sql)` and prints its violating rows.

```sh
$ RUN_DIR=/tmp/c06a-prose-sweep scripts/sweep.sh -seeds 3 -run '^TestScenarioE2E$/^(e2e-results|e2e-queue-full|e2e-reject-list|e2e-reset-error)$'
NOTE: 3 seeds per scenario from 1, tests matching ^TestScenarioE2E$/^(e2e-results|e2e-queue-full|e2e-reject-list|e2e-reset-error)$
./test/e2e             TestScenarioE2E/e2e-queue-full                   seeds 3     pass files /tmp/c06a-prose-sweep/e2e-queue-full
./test/e2e             TestScenarioE2E/e2e-reject-list                  seeds 3     pass files /tmp/c06a-prose-sweep/e2e-reject-list
./test/e2e             TestScenarioE2E/e2e-reset-error                  seeds 3     pass files /tmp/c06a-prose-sweep/e2e-reset-error
./test/e2e             TestScenarioE2E/e2e-results                      seeds 3     pass files /tmp/c06a-prose-sweep/e2e-results
sweep: 12 seed runs passed, 0 failures; log /tmp/c06a-prose-sweep/sweep.log
```

To read what those flushed CSV directories actually proved, `scripts/report.sh <dir...>` (or `make report RUNS=...` / `make report SEEDS=N`) loads `metrics.csv`, `histograms.csv`, and `run.csv` once into DuckDB (an embedded analytical SQL engine, pinned to `1.5.x`) using `test/analysis/views.sql`, then prints seven fixed sections regardless of whether the root holds 3 seeds or 300:

1. **What the runs prove** — every structural check plus the `-- claim:` line of every scenario assertion and oracle check that applies. A SQL file whose `-- requires:` line names one component is classified as an *assertion*; one naming multiple components (for instance, `engine` compared against `workload` or `fakepg`) is classified as an *oracle check*.
2. **How the data was collected** — tiers (`S`, `R`, `bench`), schema version, run/flush/row counts, and components seen.
3. **Inputs** — one aggregated row per scenario (reads `run.csv`'s `scenario` column so any directory layout works).
4. **Coverage by scenario, statement class and outcome** — nine tables with per-table legends covering engine outcomes, error-contract replies, classifier classes and rejects, simulated client outcomes and SQLSTATEs, and loadgen totals, outcomes, and error classes.
5. **Failed assertions, in full** — every violating row, with no 20-row cutoff.
6. **Oracle mismatches, in full** — every cross-component mismatch, in full.
7. **Verdict** — a table of `(check, claim, observed, expected, pass)` ending in one `VERDICT: PASS (...)` or `VERDICT: FAIL (...)` line, exiting `0` on pass, `1` on any failure, and `2` on usage errors.

The report's SQL lives at depth 3 under [`test/analysis/report/sections/`](../../test/analysis/report/sections/) so `scripts/csv-assert.sh`'s root-mode scan (`test/analysis/<scenario>/*.sql`) never mistakes it for a scenario. Five built-in load-tool checks (`check-loadgen-{conservation,answered,drained,moved,labels}.sql`) apply automatically to any run exporting component `loadgen` or matching `load-*`. All checks run in one DuckDB session (~7 s under load instead of ~50 s for one process per file) and fall back to per-file sessions only if DuckDB errors, so an error still names its source file.

Running `scripts/report.sh` on the 3-seed sweep above ends with:

```text
3. Inputs

| scenario        | tier | runs | seeds | arms | flushes | instances |
|-----------------|------|------|-------|------|---------|-----------|
| e2e-queue-full  | S    | 3    | 1-3   | base | 6       | 1         |
| e2e-reject-list | S    | 3    | 1-3   | base | 6       | 1         |
| e2e-reset-error | S    | 3    | 1-3   | base | 9       | 1         |
| e2e-results     | S    | 3    | 1-3   | base | 6       | 1         |

...

VERDICT: PASS (30 checks over 12 runs)
```

And on a flushed `load-errors` run (`scripts/report.sh test/analysis/report/testdata/load-errors`), tables 4.7 through 4.9 summarize the load tool's own counters alongside its built-in conservation checks:

```text
4.7 Load tool: what was intended and what was sent (loadgen)

| scenario    | value                                      | total | runs   |
|-------------|--------------------------------------------|-------|--------|
| load-errors | intended                                   | 200   | 1 of 1 |
| load-errors | sent                                       | 198   | 1 of 1 |
| load-errors | could_not_send                             | 2     | 1 of 1 |
| load-errors | late                                       | 3     | 1 of 1 |
| load-errors | queued (final)                             | 0     | 0 of 1 |
| load-errors | in_flight (final)                          | 0     | 0 of 1 |
| load-errors | achieved on time (sent - late)             | 195   | 1 of 1 |
| load-errors | shortfall runs (late + could_not_send > 0) | 1     | 1 of 1 |
```

The report fails closed on every structural defect: missing or wrong-version DuckDB, a non-directory argument, an empty directory argument even beside a good run, a missing `run.csv`/`metrics.csv`/`histograms.csv`, a bad schema header, any `guard.sql` violation, a `metrics.csv` whose distinct `flush_seq` count disagrees with `run.csv`'s `flushes` (`sections/flushes.sql`), or a required metric absent from a final flush. Paths are printed relative to the input arguments and rows are sorted under `LC_ALL=C`, so the same runs always produce byte-identical output.

---

## How it is tested and what the tests proved

- **Load tool core and live runner** ([`internal/loadgen/plan_test.go`](../../internal/loadgen/plan_test.go), [`tracker_test.go`](../../internal/loadgen/tracker_test.go), [`live/live_test.go`](../../internal/loadgen/live/live_test.go), [`cmd/loadgen/main_test.go`](../../cmd/loadgen/main_test.go)):
  - Constant-rate `Count`, `At`, and `Due` hold at equality and up to `MaxRate * MaxDurationNs` without overflow; `FormatNs` formats up to `math.MaxInt64` without overflow; zero-op plans fail validation.
  - Same seed produces identical operations, digests, and per-tenant SQL streams in process and across compiled binary processes.
  - A 400 ms send lag with 1 ms reply time shows in `Snapshot.Latency` (from intended send) while staying absent from `Snapshot.LatencyFromSend`; a test-only closed-loop plant (`PlantSlowSchedule`, verified unreachable from `cmd/` and `live/` by `TestNoConfigOrCommandSetsTheLoadToolsPlant`) collapses p99 below 100 ms against a 1 s stall, proving the open-loop check catches closed-loop regressions.
  - Late sends alone (zero drops) trigger `Shortfall()`, lower achieved rate, print `SHORTFALL:`, and exit `3`, while sends at exact `LateAfterNs` stay on time. Throttled and non-reading `loadgentest.Fake` peers produce `SHORTFALL` and conserve counts; a 200-seed random-event campaign and an interrupted live run both satisfy `Snapshot.Check()` on every transition; CSV flush round-trips equal the final in-memory snapshot; CSV flush errors force exit `1`.
- **`mgctl` unit, differential, and model tests** ([`internal/mgctl/mgctl_test.go`](../../internal/mgctl/mgctl_test.go), [`cmd/mgctl/main_test.go`](../../cmd/mgctl/main_test.go), [`test/e2e/c06a_mgctl_test.go`](../../test/e2e/c06a_mgctl_test.go), [`test/e2e/c06a_tierr_test.go`](../../test/e2e/c06a_tierr_test.go)):
  - Every admin command sends only `GET` to `/healthz`, `/stats`, or `/metrics`; `MGCTL_TRANSCRIPT` redacts `-password`; stdin password lines above 1,024 bytes are refused with exit `2`.
  - Differential config validation matches `config.Load` across 300 mutated configs in process and matches the compiled `mini-gateway -check` binary across 150 mutated configs (exit status, stdout, and error message after the program prefix).
  - `mgctl verifier` produces verifiers accepted by `pgwire.NewTenantTable` and `pgwire.ScramServer` without echoing the password. Every messaged row of `pgwire.ErrorRows` resolves to its own ID, including `FATAL 08006` `lost-in-transaction` over `lost-after-read`.
  - A 60-step seeded model test drives `mgctl` against `proxysim.World` in tier S (proving identical replayable transcripts and engine quiescence), and a 40-step tier R model test drives the compiled `mgctl` binary against a live TLS-enabled `mini-gateway` and Postgres 17, polling `/stats` for a fresh publish `seq` after each step to prove admin commands never move `requests_received`.
- **Dev stack and tier R smoke** ([`scripts/c06a_chaos_down_test.go`](../../scripts/c06a_chaos_down_test.go), `TestTierRRunTargetsUpLoadDown` in [`test/e2e/c06a_tierr_test.go`](../../test/e2e/c06a_tierr_test.go)):
  - A fast Docker-double test proves `deploy/dev/down.sh` fails closed if container, network, or volume listing fails.
  - End to end on free ports with an isolated `mg-dev-t<pid>` project, `make run-up` binds all four ports on `127.0.0.1` only (checked via `ss` and `docker port`); `make load RATE=40 DURATION=5s PROFILE=simple SEED=11` completes 200 requests and Prometheus's `mg_engine_requests_completed_total` increases by exactly 200 (matching the load tool's CSV flush); `make run-down` leaves zero containers, networks, volumes, or gateway processes behind.
- **Story report, scripts, and site checks** ([`test/analysis/report/report_test.go`](../../test/analysis/report/report_test.go), [`scripts/c06a_report_scripts_test.go`](../../scripts/c06a_report_scripts_test.go), [`scripts/c06a_site_links_test.go`](../../scripts/c06a_site_links_test.go), [`scripts/c06a_site_suffix_test.go`](../../scripts/c06a_site_suffix_test.go)):
  - Golden tests cover passing runs (`recorded.golden`), planted assertion + oracle + loadgen conservation failures (`planted.golden`), empty directories (`empty.golden`), and missing `run.csv` (`truncated.golden`).
  - Additional tests prove byte-identical output across paths with zero absolute paths, print all 25 of 25 violating rows, and verify identical report structure at 3 seeds and 300 seeds. They also check that `errors_by_class{reason="other"}` passes while malformed classes fail, that empty sibling arguments and dropped whole flushes fail closed, and that the `REPORT_PLANT=ignore-failures` canary is caught.
  - Site tests verify that suffixed chapters such as `06a` sort between whole-numbered chapters and that relative links in published pages must target files tracked by `git ls-files`.
- **Shipped regression suites for Chapters 1–4**: 132 tests (139 functions across `internal/sim`, `internal/sim/simnet`, `internal/sim/world`, `internal/obs`, `internal/obs/jsonstats`, `internal/obs/promtext`, `internal/obs/csvflush`, `internal/obs/simrun`, and `internal/pgwire`) run directly in `go test`, with 13 `TestTierRAdv*` functions running in `scripts/tier-r.sh` and slow multi-process/baseline tests skipped under `-short` (adding about 2 s to the short run).

---

## Stated limits

1. **No per-tenant numbers or effective-settings endpoint in `/stats` yet.** No engine component exports a `tenant` label yet (per-tenant accounting arrives with fairness in Chapter 9), and `/stats` exports only metric points. `mgctl tenants` reports that gap in one line; `mgctl config show` prints the fingerprint and the four capacity gauges carried by `/stats`. Passing `-config <file>` lets both commands read the local config file, list configured tenant users, show effective settings with `database.password` masked as `(set)`, and check whether that file's fingerprint matches the running gateway.
2. **`Late` mixes client scheduling delay with server back pressure.** On a busy shared VM, Go timers and goroutines can slip by several milliseconds. The 25 ms default `-late-after` threshold keeps ordinary timer jitter out of `Late`, while genuine stalls or a peer that stops reading still surface as late or unsent requests.
3. **Intended-time latency includes client-side pipelining wait.** Measuring from `dueAt` captures time a request spends queued behind earlier requests on its connection. That total wait is what open-loop latency is meant to show, not database service time alone; `latency_from_send` is recorded alongside it to isolate post-write duration.
4. **Outcome `refused` depends on the `mini-gateway:` message prefix.** `loadgen` classifies an error reply as `refused` only when it matches an error-contract row whose message begins with `mini-gateway:`. Errors whose text matches Postgres's own wording (such as `57014` statement cancel) are counted under `db-error`.
5. **End-to-end `mgctl query` row matrix.** Unit tests match every messaged row of `pgwire.ErrorRows`, while end-to-end `mgctl` runs exercise rows, `pass-through`, `unsupported-feature`, `auth-failed`, `tls-required`, and `unknown-database`; overload and connection-loss rows (`shed`, `lost-*`) are produced end to end by the proxy's scenario suite rather than by `mgctl`.
6. **Stdin password cap.** `-password -` refuses any first line longer than 1,024 bytes (after stripping trailing `\r`) rather than silently cutting it. Passwords supplied directly as flag values have no such limit.
7. **Dev stack defaults and single-host saturation.** Default ports (`55432`, `59090`, `56432`, `56433`) and project name (`mg-dev`) can collide with a second stack unless overridden via `deploy/dev/env.sh`. With 8 pooled connections and unconditional `DISCARD ALL` resets, the dev stack sustains roughly 150 statements/s on a loaded 4-vCPU host; higher rates such as `RATE=200` report `SHORTFALL` from real proxy back pressure.
8. **Report and script boundaries.** `scripts/report.sh` requires DuckDB `1.5.x` and checks that `metrics.csv` holds the exact number of distinct flushes declared by `run.csv`. The 3-seed vs 300-seed scale test uses synthetic copies of recorded runs, and a full 4,000-run gate sweep report has not been timed. `scripts/chaos.sh` reads SQL failure snippets from `csv-assert.sh`'s `.sql.log` (which caps output at 20 rows per file), whereas `scripts/report.sh` prints every violating row. `scripts/sweep.sh`'s `files <dir>` hint matches by the scenario's last path component under `RUN_DIR`.
