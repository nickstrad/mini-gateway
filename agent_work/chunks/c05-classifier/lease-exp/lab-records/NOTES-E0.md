# Lease experiment — stage 0 notes (piece E0, run R0)

Written by the E0 builder (Opus) on 2026-11-26, on branch `exp/lease-lab`. Plan:
[../LEASE-EXPERIMENT-PLAN.md](../LEASE-EXPERIMENT-PLAN.md) §1, §7, §8 row R0, §11 row E0.
Scope set by Nick's ruling: the **deploy variant only** (SCRAM-SHA-256 with TLS on); stage 0
only — probe, R0 run, calibration file, notes. No pool, simulator model or report was built.

This note reports numbers and observations. **It does not give the verdict on P2**; the
reviewer writes that from the data.

## 1. What is on disk

| Path | What |
| --- | --- |
| `cmd/mgcalib/` | The probe: `mgcalib run` (measurements) and `mgcalib summarize` (derives calibration.json) |
| `scripts/lease-calib.sh` | Starts the throwaway containers, runs the probe, leaves a run directory |
| `runs/r0-20261126T031313Z-asc/` | Full run 1, M7 sustained levels ascending |
| `runs/r0-20261126T032236Z-desc/` | Full run 2, levels descending |
| `runs/r0-20261126T033237Z-desc/` | Full run 3, levels descending (added because parts of run 2 were contended, §6) |
| `runs/r0-20261126T040044Z-asc-only-M2I-M2H/` | Repair run 4: M2I and M2H only (review finding 1) |
| `runs/r0-20261126T040145Z-asc-only-M2I-M2H/` | Repair run 5: M2I and M2H only |
| `runs/r0-20261126T040237Z-asc-only-M2I-M2H/` | Repair run 6: M2I and M2H only |
| `calibration.json` | `mgcalib summarize` over all six run directories |

Each run directory holds `env.txt` (environment, settings, start/end, what else ran),
`samples.csv` (one row per sample), `cells.csv` (one row per M7 cell, with CPU accounting),
`verify.csv` (the M2 / M2I taint checks), `series.csv` (M7 sustained per-second completions; a
convenience copy — summarize recomputes it from `samples.csv`) and the probe's stdout. The
stdout file is `probe.out` in runs 1–3 and `probe.log` in runs 4–6: `.gitignore` has `*.out`,
so the first three were left out of the original commit; they were added with `git add -f` in
the repair, and the script now writes `probe.log`, which is not ignored. Runs 4–6 hold only
M2I/M2H rows, so their `cells.csv` and `series.csv` are header-only.

Calibration id: `sha256:bc6197988c0de9578534a59c5ac1bdbb80b17af929d9a2669a974360ca3e6edd`
(it changed in the repair because the three new run directories are hashed in).

## 2. Conditions

- Host: the shared VM, 4 vCPUs, 7.9 GiB, Linux 6.8.0-139, Docker 29.7.2 with the userland
  proxy enabled (`/usr/bin/docker-proxy`), Go 1.26.8, GOMAXPROCS 4. Probe and server share
  the 4 vCPUs.
- Server: `postgres:17` (digest `sha256:2d2b8998…f9e3`), server_version 17.11, started
  fresh for every run by `lease-calib.sh`: `POSTGRES_HOST_AUTH_METHOD=scram-sha-256`,
  `ssl=on` with the image's snakeoil certificate (RSA 2048, sha256WithRSA),
  `shared_buffers=32MB`, `max_connections=600`. The postgres role's verifier is
  `SCRAM-SHA-256$4096`. Full non-default settings are in each `env.txt`.
- TLS as negotiated (client and `pg_stat_ssl` agree): **TLS 1.3, TLS_AES_256_GCM_SHA384**,
  256 bits. Client: `sslmode=require` semantics — `InsecureSkipVerify`, no session
  resumption (no `ClientSessionCache`), so every connect does a full handshake.
- SCRAM: the server offers `SCRAM-SHA-256-PLUS` over TLS; pgwire answers with plain
  `SCRAM-SHA-256` and GS2 header `n,,` (no channel binding). This is pgwire's behaviour as
  landed, not a probe choice.
- **pgwire does not cache the salted password**: `ScramClient.Final` calls
  `SaltedPassword` (PBKDF2, 4,096 iterations) on every connect. Per the brief, no caching
  was added; the probe measures what exists.
- Two network paths, every M3 and M7 row labelled: `published` = `127.0.0.1:<free port>`
  through docker-proxy (the tier-r.sh pattern); `bridge` = container IP `172.17.0.x:5432`
  directly. M1 ran on both. M2 (old), M2I, M2H, M4, M5, M6 ran on `bridge` only (decision D4).
- Clock: Go's monotonic clock (`time.Now`/`time.Since`), nanoseconds.
- Runs: 03:13–03:22, 03:22–03:32, 03:32–03:43 UTC on 2026-11-26, about 10 minutes each.
  Repair runs 4–6 (M2I, M2H only, about 20–35 s each): 04:00:50, 04:01:50 and 04:02:42 UTC,
  each against freshly started containers, each started only once the 1-minute load average
  was below 1.5 (it was 1.36 / 1.21 / 1.25 at the start; the 5-minute average was still
  5.6–7.7 from earlier work). The script's one-second quiet reading of host CPU just before
  the probe was 238 / 89 / 289 milli-CPUs (`quiet_host_busy_cpus_milli_start` and
  `uptime_start`/`uptime_end` in each `env.txt`).

## 3. Method, per measurement

All database work goes through `cmd/mgcalib/wire.go`: a blocking-socket driver for
`pgwire.BackendSession` (the proxy's own database-leg state machine: SSLRequest, TLS,
StartupMessage, SCRAM, ParameterStatus/BackendKeyData, ReadyForQuery). After
ReadyForQuery the probe speaks simple Query itself (`pgwire.AppendQuery`, frames read to
ReadyForQuery); close is `Terminate` then socket close. pgproto3 was not needed.

Warm-up rows are written with `excluded=1` and never enter the statistics; nothing else is
removed. Failed rows carry an `err` value and are counted, not timed.

**Connect phases** (M3, M4 slot, M7), each sample: `tcp` = dial start → TCP connected;
`tls` = SSLRequest written → TLS handshake done, with its first part `answer` = SSLRequest
written → server's `S` byte read; `scram` = StartupMessage written → AuthenticationOk read;
`ready` = AuthenticationOk read → ReadyForQuery read; `total` = dial start → ReadyForQuery.
To time AuthenticationOk the driver splits the byte stream into frames itself and feeds
`BackendSession.Receive` one frame at a time.

- **PBKDF2.** `pgwire.SaltedPassword(pw, 16-byte salt, 4096)` timed 320 times per run in
  the probe process, first 20 excluded. Client CPU only.
- **M1.** 10 sessions per run, alternating paths (5 each). Each session: 1,100 pairs of
  `DISCARD ALL` and `SELECT 1`, order alternating per pair (A,B then B,A); first 100 pairs
  excluded. A sample = Query written → ReadyForQuery read. Every `DISCARD ALL` reply was
  checked for tag `DISCARD ALL` and status `I`. 5,000 timed samples per path per run.
- **M2 (old, now `M2R`) — superseded, order-confounded.** Runs 1–3: 5 sessions on
  `bridge`, 70 rounds each (first 10 excluded) → 300 per kind per run. Each round applied
  every kind once in the same session, the kind order rotated by one per round. A rotation
  keeps each kind's predecessor fixed, so every kind was always measured straight after the
  same other kind, and its "reset cost" mostly measured the distance from the last temp-table
  drop (`all` and `temp_table` both drop one): `set` (always after `all`) and `set_role`
  (after `temp_table`) read 250–255 µs, kinds one step later about 190–205, two steps later
  135–150, down to `listen` 118 µs — and `setseed`, which leaves DISCARD ALL nothing to do,
  read 189 µs. Review finding 1 identified this. The rows stay in the raw files under id `M2`
  and in calibration.json under `M2R` with a `conditions.M2R` entry saying they are
  order-confounded; they are not per-kind costs (decision D10). `-only M2` still runs it.
- **M2I — reset after one taint kind, isolated (replaces M2).** Runs 4–6, `bridge`. 15
  kinds: the 12 of plan §7, `all` (every non-transaction kind together, `SET ROLE` last), the
  control `none` (taint = `SELECT 1`: the cost of a reset after an ordinary statement), and
  `temp_table_rows` (a temp table with 1,000 rows of `(int, md5 text)` and a btree index — a
  real temp table is rarely empty; decision D12). Each kind has 3 sessions of its own that
  never see another kind; all 45 are opened first and then served round-robin, one statement
  at a time on the whole probe, 110 rounds each (first 10 excluded) → 300 timed samples per
  kind per run, 900 pooled. The round-robin starting point rotates by one kind per round
  (across separate backends order cannot carry over; this only spreads drift). A round in a
  session: apply the taint (separate Query messages); time the reset — `discard`, and for
  `in_tx`/`failed_tx` also `rollback` and `total` as before; then time 5 more consecutive
  `DISCARD ALL` on the now-clean session as parts `after1`…`after5` (the carry-over curve).
  Every reply is checked (tag, status `I`). Between two rounds of one session the other 44
  sessions each run a round, so a session sits idle for about 0.15–0.27 s (M2I took 19, 30
  and 22 s for 110 rounds) before its taint is applied — see §6.
  Verification (untimed, once per kind per run, on the kind's first session before its timed
  rounds, so it too never sees another kind): apply, read the state, reset, read again;
  `verify.csv`. All 15 kinds **ok in all three repair runs** (and the 13 old kinds ok in runs
  1–3), with the two residues OPTIONS.md predicts observed as predicted:
  - `setseed`: after `DISCARD ALL` the `random()` sequence continues from the old seed
    (residue item 1).
  - custom setting `lab.secret`: after `DISCARD ALL` the value is gone but
    `current_setting('lab.secret', true)` returns `''`, not NULL as on a fresh session —
    the name stays visible (residue item 2). (`pg_settings` does not list placeholder
    settings at all, so it cannot show this; the first development run's check used it and
    was replaced.)
  - `none` reads `1` before and after (nothing to clear); `temp_table_rows` shows 2 temp-schema
    relations (table and index) before and 0 after.
- **M2H — does a backend that once held a temp table pay more later?** Runs 4–6, `bridge`,
  6 pairs per run. Per pair two fresh sessions: A never has a temp table; B runs
  `CREATE TEMP TABLE lab_h(x int)`. Then both run 21 untimed `DISCARD ALL` (B's first one
  drops its table; so both have had 21 resets and B 20 after the drop), and the probe checks
  B's temp schema is empty. Then 1,100 timed `DISCARD ALL` on each (first 100 excluded →
  1,000 per session, 6,000 per side per run), alternating A,B sample by sample with the order
  flipped on every other sample and every other pair.
- **M3.** Per round, for each level c ∈ {1,4,16,64} still needing rounds, on each path
  (path order alternating per round): c goroutines block on a gate; the gate opens; each
  connects; wait for all; Terminate and close all; then the monitor session polls
  `pg_stat_activity` (client backends other than itself) until zero. Rounds: c=1 310 (10
  excluded), c=4 162 (2), c=16 41 (1), c=64 11 (1) → 300 / 640 / 640 / 640 samples per path
  per run.
- **M4, method 1 (poll).** 310 samples per run (10 excluded), `bridge`. Connect, note the
  backend pid, then t0, Terminate + close, then the monitor repeats
  `SELECT count(*) FROM pg_stat_get_activity(<pid>)` back to back until 0. Upper bound =
  t0 → end of the first poll returning 0; lower bound = t0 → start of the last poll still
  returning 1 (0 if the first poll already saw it gone). Resolution = the poll round trip,
  recorded per sample as `poll_rtt`. (The first development run polled the
  `pg_stat_activity` view; its round trip was about 3.7 ms, so it was replaced by the
  per-pid function, about 0.7 ms.)
- **M4, method 2 (slot).** A second container, same settings but `max_connections=1`,
  `superuser_reserved_connections=0`. Per sample (310, 10 excluded): connect (taking the
  only slot), t0, Terminate + close, then immediately retry connects; each refused attempt
  (`53300 too many clients`) is counted; record t0 → ReadyForQuery of the first successful
  attempt, its phases, and the rejection count.
- **M5 / M6.** 930 fresh backends per run on `bridge`; backend i serves kind i mod 3
  (first 30 excluded → 300 per kind per run): `SELECT 1`, a primary-key lookup
  `SELECT v FROM lab_kv WHERE id = 42` (table of 1,000 rows, created and analysed at
  setup), and `SELECT lab_fn(42)` (plpgsql, three SELECTs on `lab_kv`). On each backend:
  `fresh` = the first statement after ReadyForQuery; then 20 untimed executions; `reused`
  = the 21st; then `DISCARD ALL`; `after_reset` = the next execution.
- **M7 burst.** B ∈ {100, 500}, both paths, 6 repetitions per run (repetition 0 excluded),
  sizes and paths interleaved, path order alternating per repetition. Before each cell: wait
  for zero client backends, then the quiet check (below). Then B goroutines behind a gate,
  gate opens, all connect. Rate = B / (gate open → last ReadyForQuery). CPU accounting
  covers exactly that window; connections are closed after it.
- **M7 sustained.** Levels c ∈ {1,2,4,8,16,32,64}, ascending in run 1 and descending in runs
  2 and 3; at each level both paths, path order alternating per level. c workers loop
  connect → ReadyForQuery → Terminate/close with no pause (1 ms back-off only after a
  failure, which never happened). 3 s warm-up, then a 20 s window; a connect counts in the
  window if its ReadyForQuery falls inside it (others `excluded=1`). Per-second completions,
  latency, failures by SQLSTATE or kind. CPU accounting covers exactly the 20 s window.
- **M7 attribution, every cell.** Before/after the window: Postgres container
  `cpu.stat usage_usec` (cgroup v2, `system.slice/docker-<id>.scope`), probe `getrusage`
  user+sys, docker-proxy `/proc/<pid>/stat` utime+stime (published port's proxy; 10 ms
  ticks), host `/proc/stat` busy (all but idle and iowait), and the 1-minute load average
  before the cell. `other` = host busy − Postgres − probe − proxy.
- **Quiet check before every M7 cell.** With the server drained and the probe idle, one
  second of host CPU not used by Postgres or the probe is measured; while it exceeds one CPU
  the probe waits and measures again, at most 10 times, then proceeds and records the last
  value (`quiet_other_cpus_milli`). The brief's "load average above about 1.0" test could
  not be used directly: the 1-minute load average is dominated by the probe's own previous
  cell (it reached 15–38 after the c=64 and B=500 cells), so waiting for it would add
  minutes per cell and measure ourselves. The load average is still recorded per cell.

## 4. calibration.json schema (`mgcalib-calibration/1`)

Integers only. Times in ns, counts as counts, rates as milli-units per second
(`rate_milli_per_s` = 1000 × connects per second, truncated). Derived by
`mgcalib summarize <run-dir>...` from `samples.csv`, `cells.csv` and `verify.csv`; the run
directories are sorted by name, so argument order does not matter and the output is
byte-for-byte reproducible.

- `schema`, `calibration_id` — `sha256:` over, per run in name order and per file
  (`samples.csv`, `cells.csv`, `verify.csv`), a line `<run>/<file> <bytes>\n` then the
  file's bytes.
- `runs` — run directory names. `conditions` — fixed strings (auth, TLS, server, host,
  clock, path meanings, exclusion rule).
- `costs[]` — one entry per (measurement, path, cond, part, field):
  `stats {n, min, p50, p90, p99, max, mean}` over all included rows of all runs
  (nearest-rank percentiles: the ⌈p·n/100⌉-th smallest; mean truncated);
  `per_run [{run, n, p50}]`; `failed` and `fail_kinds` (`kind:count`). `field` is `ns` (the
  sample), a connect phase (`tcp_ns`, `tls_ns`, `answer_ns`, `scram_ns`, `ready_ns`),
  M4's `lo_ns` (lower bound) or `polls` (unit `count`: polls needed, or rejected attempts
  for `slot_retry`).
- `measurement` values: `PBKDF2`, `M1`, `M2R`, `M2I`, `M2H`, `M3`, `M4`, `M5`, `M6`, `M7B`,
  `M7S`. `M2R` is the raw id `M2` renamed by `summarize` (the old, order-confounded M2; see
  `conditions.M2R`); `M2I` parts are `discard`, `rollback`, `total`, `after1`…`after5`; `M2H`
  conds are `A_never_temp` and `B_had_temp`. `conditions` gained `M2R`, `M2I` and `M2H`
  entries. No field changed meaning, so the schema id stays `mgcalib-calibration/1`.
- `cells[]` — every M7 cell including excluded warm-up repetitions: wall, ok, failed,
  `rate_milli_per_s`, CPU in µs (Postgres, probe, proxy, host busy, other), CPU use in
  milli-CPUs (`pg_cpus_milli`, `probe_cpus_milli`, `other_cpus_milli`), CPU ns per connect
  (Postgres, probe, proxy; −1 where no proxy was read), `load1_milli_before`,
  `quiet_other_cpus_milli`, `contended`, and for sustained cells `per_second` (20 counts).
- `contended` = `other_cpus_milli > 500 || quiet_other_cpus_milli > 1000` — other work held
  more than half a CPU during the cell, or more than one CPU in the last quiet second
  before it. Decision D7; it is a label, nothing is dropped.
- `rates[]` — per (M7B|M7S, path, cond) over the non-excluded cells: `rate_milli_per_s`
  stats over all cells, `rate_milli_per_s_uncontended` over the uncontended ones,
  `per_run` p50, `per_second` stats (sustained: every second of every cell, milli-units),
  `failed`.

## 5. Results

Numbers below are copied from calibration.json (µs or ms as headed; per-run columns are
run 1 asc / run 2 desc / run 3 desc). The full set, including every phase at every level,
is in calibration.json.

### Headline figures

| Quantity | bridge | published |
| --- | --- | --- |
| M1 `DISCARD ALL` on a clean session, p50 (p99) | 85.5 µs (253 µs) | 174 µs (441 µs) |
| M1 `SELECT 1`, same sessions, p50 | 106 µs | 199 µs |
| M2I reset after the control `none` (`SELECT 1`), p50 (p99) | 177 µs (2,068 µs) | — |
| M2I reset after a non-temp-table kind, p50 range over the 11 kinds (`discard` part) | 133–186 µs | — |
| M2I reset after `temp_table` / `temp_table_rows` / `all`, p50 | 3.07 / 3.89 / 3.05 ms | — |
| M2H clean `DISCARD ALL`, never had a temp table / once had one, p50 | 114 / 140 µs | — |
| M3 connect at c=1, p50 (p99) | 16.0 ms (37.5 ms) | 17.7 ms (48.6 ms) |
| M7 burst B=100, rate p50 (min–max, 15 cells; 14 uncontended) | 246 /s (144–268; uncontended 212–268) | 219 /s (157–242; uncontended 188–242) |
| M7 burst B=500, rate p50 (min–max, 15 cells) | 233 /s (208–284; 14 uncontended: 221–284) | 224 /s (184–247; 12 uncontended: 188–247) |
| M7 sustained, highest level p50 across levels (uncontended cells) | 197 /s at c=32 (195 at c=16) | 173 /s at c=32 (170 at c=16) |
| M7 sustained, highest single uncontended cell | 199 /s (c=16, run 2) | 180 /s (c=32, run 1) |
| M7 failures (any kind, any cell) | 0 | 0 |

### Detailed tables

#### M1 — clean-session round trips (µs)

| | n | min | p50 | p90 | p99 | max | mean | per-run p50 (asc / desc / desc) |
|---|---|---|---|---|---|---|---|---|
| bridge DISCARD ALL | 15000 | 46.2 | 87.7 | 126.0 | 258.8 | 3238.3 | 96.4 | 87.5 / 83.6 / 90.9 |
| bridge SELECT 1 | 15000 | 56.0 | 108.6 | 152.3 | 300.7 | 2845.2 | 117.1 | 108.8 / 103.6 / 112.5 |
| published DISCARD ALL | 15000 | 83.9 | 177.7 | 259.8 | 578.7 | 16665.0 | 198.1 | 192.7 / 156.4 / 183.3 |
| published SELECT 1 | 15000 | 95.8 | 202.4 | 288.7 | 576.8 | 3327.6 | 219.4 | 218.4 / 180.0 / 208.7 |

#### M2I — reset after each taint kind, isolated sessions, bridge path (µs)

Runs 4–6 (per-run columns are run 4 / 5 / 6). `discard` = the `DISCARD ALL` right after the
taint; for the two in-transaction kinds `rollback` + `discard` = `total`.

| | n | min | p50 | p90 | p99 | max | mean | per-run p50 (run 4 / 5 / 6) |
|---|---|---|---|---|---|---|---|---|
| none — discard | 900 | 74.1 | 177.0 | 351.4 | 2068.2 | 3587.7 | 239.8 | 199.4 / 162.9 / 170.4 |
| set — discard | 900 | 66.5 | 164.1 | 310.7 | 1409.8 | 5197.7 | 223.3 | 194.8 / 152.9 / 156.4 |
| set_config_fn — discard | 900 | 94.0 | 186.2 | 357.6 | 1442.3 | 9487.9 | 271.5 | 212.3 / 178.9 / 176.9 |
| prepare — discard | 900 | 69.8 | 162.9 | 312.2 | 709.7 | 3085.8 | 202.0 | 184.8 / 152.6 / 158.2 |
| cursor_hold — discard | 900 | 71.3 | 165.0 | 313.0 | 972.7 | 12819.9 | 222.7 | 189.5 / 150.8 / 158.2 |
| listen — discard | 900 | 71.5 | 149.4 | 263.5 | 707.8 | 2928.2 | 181.9 | 168.4 / 129.1 / 150.6 |
| advisory_lock — discard | 900 | 72.3 | 175.2 | 311.0 | 1088.5 | 8331.7 | 229.8 | 198.5 / 170.2 / 160.8 |
| temp_table — discard | 900 | 1689.5 | 3072.4 | 6904.2 | 24499.7 | 102958.8 | 4495.4 | 3998.4 / 2801.6 / 2842.9 |
| set_role — discard | 900 | 65.9 | 168.6 | 327.5 | 2568.9 | 13529.1 | 266.0 | 190.4 / 172.8 / 140.8 |
| setseed — discard | 900 | 67.6 | 172.4 | 322.5 | 969.1 | 14026.6 | 245.2 | 185.3 / 167.5 / 167.3 |
| custom_setting — discard | 900 | 68.7 | 155.8 | 288.7 | 2072.0 | 9214.2 | 230.7 | 177.9 / 151.5 / 140.4 |
| in_tx — rollback | 900 | 64.4 | 139.8 | 275.0 | 834.6 | 10311.4 | 196.6 | 157.9 / 141.0 / 129.9 |
| in_tx — discard | 900 | 66.5 | 132.6 | 260.6 | 942.0 | 24320.7 | 202.3 | 148.9 / 129.7 / 122.6 |
| in_tx — total | 900 | 130.9 | 279.6 | 524.7 | 1723.5 | 24998.3 | 398.9 | 314.4 / 276.9 / 258.9 |
| failed_tx — rollback | 900 | 68.7 | 173.7 | 341.9 | 1828.1 | 9473.9 | 258.4 | 182.9 / 165.0 / 176.2 |
| failed_tx — discard | 900 | 69.7 | 151.3 | 289.8 | 816.4 | 13729.1 | 212.3 | 167.5 / 149.2 / 141.6 |
| failed_tx — total | 900 | 143.4 | 340.8 | 585.5 | 2796.3 | 13920.4 | 470.7 | 364.6 / 338.8 / 324.4 |
| all — discard | 900 | 1680.1 | 3049.3 | 6652.6 | 17236.7 | 151050.2 | 4329.3 | 3952.3 / 2842.8 / 2700.7 |
| temp_table_rows — discard | 900 | 2409.4 | 3894.4 | 7605.9 | 27436.6 | 119647.5 | 5291.3 | 4856.5 / 3651.1 / 3535.1 |

#### M2I — carry-over curve: p50 of the reset and of the next five `DISCARD ALL` (µs)

Same sessions and rounds as above (n = 900 per cell). `after1` is the first `DISCARD ALL`
on a session that the reset has just cleaned.

| kind | discard | after1 | after2 | after3 | after4 | after5 |
|---|---|---|---|---|---|---|
| none | 177.0 | 142.9 | 127.1 | 120.6 | 107.3 | 104.5 |
| set | 164.1 | 133.6 | 120.7 | 111.3 | 107.9 | 104.4 |
| set_config_fn | 186.2 | 150.4 | 132.9 | 117.7 | 108.5 | 103.2 |
| prepare | 162.9 | 131.4 | 122.3 | 113.4 | 105.4 | 100.5 |
| cursor_hold | 165.0 | 132.0 | 120.5 | 111.5 | 104.9 | 102.9 |
| listen | 149.4 | 123.1 | 112.9 | 107.5 | 105.9 | 105.0 |
| advisory_lock | 175.2 | 138.1 | 124.3 | 115.4 | 106.8 | 102.6 |
| temp_table | 3072.4 | 393.1 | 233.4 | 191.4 | 168.1 | 153.7 |
| set_role | 168.6 | 136.6 | 122.4 | 112.4 | 105.9 | 103.4 |
| setseed | 172.4 | 141.2 | 126.1 | 116.4 | 105.9 | 102.8 |
| custom_setting | 155.8 | 129.0 | 118.6 | 110.7 | 103.5 | 101.8 |
| in_tx | 132.6 | 117.6 | 108.6 | 101.6 | 102.2 | 99.6 |
| failed_tx | 151.3 | 122.2 | 109.9 | 103.5 | 100.9 | 97.6 |
| all | 3049.3 | 392.9 | 240.9 | 189.4 | 170.1 | 156.7 |
| temp_table_rows | 3894.4 | 428.9 | 256.9 | 201.5 | 171.1 | 155.0 |

#### M2H — clean `DISCARD ALL` by backend history, bridge path (µs)

6 pairs per run, 1,000 timed samples per session (runs 4 / 5 / 6). B > A in every one of the
18 pairs: per-pair medians differ by +18 to +50 µs.

| | n | min | p50 | p90 | p99 | max | mean | per-run p50 (run 4 / 5 / 6) |
|---|---|---|---|---|---|---|---|---|
| A_never_temp | 18000 | 47.9 | 114.1 | 192.0 | 392.3 | 6096.4 | 132.6 | 101.3 / 121.4 / 121.4 |
| B_had_temp | 18000 | 60.0 | 140.1 | 237.8 | 488.6 | 9011.4 | 164.3 | 123.4 / 146.8 / 155.6 |
[('advisory_lock', 'discard', 127280), ('all', 'discard', 2682843), ('cursor_hold', 'discard', 138640)] []
sha256:bc6197988c0de9578534a59c5ac1bdbb80b17af929d9a2669a974360ca3e6edd


#### M2R — the old rotated M2 (runs 1–3)

Not reproduced here as a table: its rows are order-confounded (§3) and are not per-kind
costs. They remain in calibration.json under `M2R` (and in the git history of this file).

#### M3 — connect at concurrency c (ms; dial start → ReadyForQuery, and phases)

| | n | min | p50 | p90 | p99 | max | mean | per-run p50 (asc / desc / desc) |
|---|---|---|---|---|---|---|---|---|
| bridge c=1 total | 900 | 12.41 | 16.02 | 21.16 | 37.50 | 79.51 | 17.21 | 16.42 / 15.59 / 15.90 |
| bridge c=1 tcp | 900 | 0.26 | 0.39 | 1.11 | 2.26 | 6.55 | 0.54 | 0.39 / 0.39 / 0.37 |
| bridge c=1 tls | 900 | 4.29 | 6.93 | 8.94 | 18.36 | 29.27 | 7.50 | 7.19 / 6.85 / 6.81 |
| bridge c=1 answer | 900 | 0.22 | 1.85 | 2.64 | 8.53 | 24.24 | 2.06 | 1.91 / 1.80 / 1.84 |
| bridge c=1 scram | 900 | 5.89 | 8.41 | 10.68 | 21.50 | 60.81 | 9.11 | 8.74 / 8.27 / 8.26 |
| bridge c=1 ready | 900 | 0.02 | 0.03 | 0.04 | 0.07 | 0.28 | 0.03 | 0.03 / 0.03 / 0.03 |
| bridge c=4 total | 1920 | 11.94 | 21.35 | 30.93 | 45.49 | 84.82 | 22.82 | 21.81 / 21.40 / 20.85 |
| bridge c=4 tcp | 1920 | 0.07 | 0.30 | 0.65 | 2.99 | 5.88 | 0.43 | 0.29 / 0.31 / 0.29 |
| bridge c=4 tls | 1920 | 3.97 | 9.09 | 16.09 | 26.29 | 42.43 | 10.64 | 9.52 / 9.22 / 8.60 |
| bridge c=4 answer | 1920 | 0.10 | 3.14 | 5.80 | 12.88 | 33.66 | 3.61 | 3.28 / 3.18 / 3.01 |
| bridge c=4 scram | 1920 | 5.74 | 11.06 | 15.73 | 24.83 | 62.78 | 11.70 | 11.27 / 11.01 / 10.87 |
| bridge c=4 ready | 1920 | 0.01 | 0.02 | 0.04 | 0.08 | 5.51 | 0.03 | 0.02 / 0.02 / 0.02 |
| bridge c=16 total | 1920 | 14.25 | 53.44 | 69.24 | 82.94 | 111.02 | 51.94 | 53.63 / 53.70 / 52.67 |
| bridge c=16 tcp | 1920 | 0.05 | 0.26 | 0.75 | 3.08 | 5.82 | 0.39 | 0.24 / 0.25 / 0.29 |
| bridge c=16 tls | 1920 | 4.94 | 30.51 | 52.66 | 65.83 | 89.05 | 31.12 | 30.05 / 30.49 / 30.95 |
| bridge c=16 answer | 1920 | 0.15 | 13.85 | 35.79 | 49.01 | 69.24 | 16.64 | 12.63 / 14.14 / 14.43 |
| bridge c=16 scram | 1920 | 5.97 | 19.33 | 30.26 | 42.03 | 64.77 | 20.33 | 20.27 / 20.40 / 17.92 |
| bridge c=16 ready | 1920 | 0.00 | 0.02 | 0.03 | 0.50 | 10.68 | 0.07 | 0.02 / 0.02 / 0.02 |
| bridge c=64 total | 1920 | 17.23 | 153.45 | 239.95 | 285.85 | 302.31 | 151.60 | 153.45 / 160.04 / 145.79 |
| bridge c=64 tcp | 1920 | 0.05 | 0.70 | 2.34 | 4.58 | 5.70 | 1.01 | 0.67 / 0.70 / 0.71 |
| bridge c=64 tls | 1920 | 5.96 | 120.15 | 218.16 | 257.88 | 279.14 | 122.46 | 119.33 / 121.42 / 118.56 |
| bridge c=64 answer | 1920 | 0.31 | 97.18 | 195.79 | 236.53 | 251.17 | 100.37 | 96.81 / 94.88 / 99.80 |
| bridge c=64 scram | 1920 | 6.36 | 25.24 | 44.59 | 77.50 | 99.18 | 27.95 | 25.48 / 26.36 / 24.08 |
| bridge c=64 ready | 1920 | 0.00 | 0.02 | 0.03 | 4.93 | 32.64 | 0.16 | 0.02 / 0.02 / 0.02 |
| published c=1 total | 900 | 13.41 | 17.67 | 24.91 | 48.59 | 83.70 | 19.50 | 18.65 / 17.25 / 17.30 |
| published c=1 tcp | 900 | 0.22 | 0.36 | 0.69 | 1.79 | 10.80 | 0.47 | 0.37 / 0.37 / 0.34 |
| published c=1 tls | 900 | 5.39 | 8.00 | 11.32 | 27.33 | 61.99 | 9.05 | 8.38 / 7.89 / 7.78 |
| published c=1 answer | 900 | 0.48 | 2.45 | 3.66 | 9.55 | 52.74 | 2.91 | 2.52 / 2.40 / 2.41 |
| published c=1 scram | 900 | 6.74 | 9.11 | 12.20 | 24.23 | 39.63 | 9.91 | 9.57 / 8.87 / 8.86 |
| published c=1 ready | 900 | 0.02 | 0.03 | 0.04 | 0.15 | 0.59 | 0.03 | 0.03 / 0.03 / 0.03 |
| published c=4 total | 1920 | 13.88 | 25.03 | 34.71 | 62.57 | 109.06 | 26.70 | 25.49 / 25.30 / 24.43 |
| published c=4 tcp | 1920 | 0.06 | 0.28 | 0.65 | 1.88 | 11.01 | 0.37 | 0.27 / 0.29 / 0.27 |
| published c=4 tls | 1920 | 5.69 | 10.77 | 18.01 | 32.89 | 69.46 | 12.41 | 11.27 / 10.97 / 10.33 |
| published c=4 answer | 1920 | 0.59 | 3.82 | 6.84 | 13.77 | 64.22 | 4.40 | 4.00 / 3.82 / 3.69 |
| published c=4 scram | 1920 | 6.52 | 13.00 | 18.83 | 34.86 | 65.73 | 13.88 | 13.26 / 12.86 / 12.86 |
| published c=4 ready | 1920 | 0.01 | 0.02 | 0.04 | 0.12 | 1.99 | 0.03 | 0.02 / 0.02 / 0.02 |
| published c=16 total | 1920 | 17.17 | 59.08 | 77.25 | 96.10 | 110.37 | 58.06 | 58.75 / 60.97 / 57.87 |
| published c=16 tcp | 1920 | 0.05 | 0.27 | 0.98 | 2.37 | 3.84 | 0.43 | 0.25 / 0.32 / 0.26 |
| published c=16 tls | 1920 | 6.39 | 32.93 | 57.80 | 73.24 | 92.29 | 34.81 | 32.29 / 33.38 / 33.06 |
| published c=16 answer | 1920 | 1.01 | 15.41 | 37.54 | 52.50 | 80.57 | 18.24 | 14.78 / 16.48 / 14.71 |
| published c=16 scram | 1920 | 7.26 | 21.67 | 33.66 | 45.35 | 73.14 | 22.73 | 22.17 / 21.79 / 20.87 |
| published c=16 ready | 1920 | 0.00 | 0.02 | 0.03 | 1.72 | 8.96 | 0.07 | 0.02 / 0.02 / 0.02 |
| published c=64 total | 1920 | 23.75 | 169.94 | 264.79 | 308.93 | 333.76 | 167.48 | 162.92 / 176.29 / 171.43 |
| published c=64 tcp | 1920 | 0.05 | 0.54 | 2.62 | 5.69 | 6.53 | 0.99 | 0.57 / 0.48 / 0.55 |
| published c=64 tls | 1920 | 8.18 | 135.27 | 240.67 | 281.70 | 315.96 | 136.48 | 128.74 / 142.66 / 135.48 |
| published c=64 answer | 1920 | 1.46 | 110.26 | 216.65 | 261.08 | 297.88 | 113.75 | 104.99 / 116.92 / 109.75 |
| published c=64 scram | 1920 | 6.62 | 28.03 | 46.64 | 71.66 | 121.89 | 29.88 | 28.25 / 27.52 / 28.22 |
| published c=64 ready | 1920 | 0.00 | 0.02 | 0.03 | 2.84 | 14.04 | 0.11 | 0.02 / 0.02 / 0.02 |

#### PBKDF2 — client SaltedPassword, 4096 iterations (ms)

| | n | min | p50 | p90 | p99 | max | mean | per-run p50 (asc / desc / desc) |
|---|---|---|---|---|---|---|---|---|
| pgwire.SaltedPassword | 900 | 2.28 | 3.08 | 3.66 | 4.93 | 5.15 | 3.07 | 3.05 / 3.01 / 3.15 |

#### M4 — close until the slot is free, bridge path

| | n | min | p50 | p90 | p99 | max | mean | per-run p50 (asc / desc / desc) |
|---|---|---|---|---|---|---|---|---|
| poll: Terminate → first poll seeing the pid gone, upper bound (µs) | 900 | 571.8 | 816.1 | 1579.5 | 3843.6 | 20004.6 | 1065.5 | 795.0 / 834.8 / 819.9 |
| poll: lower bound (start of last poll still seeing it; 0 if none) (µs) | 900 | 0.0 | 0.0 | 0.0 | 120.0 | 2098.7 | 5.8 | 0.0 / 0.0 / 0.0 |
| poll: polls needed (count) | 900 | 1 | 1 | 1 | 2 | 6 | 1 | 1 / 1 / 1 |
| poll round trip = resolution (µs) | 900 | 423.3 | 685.3 | 1233.3 | 3520.8 | 19884.2 | 900.8 | 666.0 / 695.9 / 689.1 |
| slot: Terminate → next connect at ReadyForQuery (µs) | 900 | 11017.1 | 15087.9 | 17587.7 | 23567.4 | 74737.5 | 15590.4 | 15374.0 / 15192.4 / 14680.5 |
| slot: rejected attempts (count) | 900 | 0 | 0 | 0 | 0 | 0 | 0 | 0 / 0 / 0 |

#### M5 / M6 — first statement and plpgsql call (µs)

| | n | min | p50 | p90 | p99 | max | mean | per-run p50 (asc / desc / desc) |
|---|---|---|---|---|---|---|---|---|
| select1 fresh | 900 | 407.3 | 630.5 | 850.1 | 2796.4 | 4951.4 | 700.3 | 602.0 / 638.8 / 659.1 |
| select1 reused | 900 | 57.3 | 107.1 | 158.6 | 397.8 | 2508.0 | 122.1 | 101.8 / 105.4 / 113.2 |
| select1 after_reset | 900 | 62.2 | 117.3 | 197.1 | 395.0 | 2785.6 | 138.2 | 109.5 / 115.5 / 126.1 |
| pk_lookup fresh | 900 | 1136.3 | 1750.4 | 2091.6 | 3658.5 | 7467.7 | 1812.5 | 1674.4 / 1753.1 / 1820.8 |
| pk_lookup reused | 900 | 97.8 | 177.9 | 256.4 | 705.8 | 4552.8 | 207.6 | 167.1 / 181.8 / 185.2 |
| pk_lookup after_reset | 900 | 101.6 | 184.8 | 262.8 | 523.1 | 3426.9 | 203.2 | 176.1 / 187.8 / 192.2 |
| plpgsql fresh | 900 | 3166.3 | 4395.4 | 5147.2 | 7525.2 | 39525.3 | 4553.1 | 4208.5 / 4406.5 / 4548.2 |
| plpgsql reused | 900 | 112.7 | 218.5 | 309.1 | 821.1 | 2526.9 | 243.3 | 211.7 / 218.0 / 230.7 |
| plpgsql after_reset | 900 | 371.7 | 611.8 | 765.4 | 1452.6 | 2667.0 | 641.5 | 585.8 / 603.7 / 642.9 |

#### M7 burst — per-connect latency (ms)

| | n | min | p50 | p90 | p99 | max | mean | per-run p50 (asc / desc / desc) |
|---|---|---|---|---|---|---|---|---|
| bridge B=100 total | 1500 | 24.07 | 238.76 | 387.22 | 610.97 | 689.15 | 238.82 | 224.49 / 234.00 / 254.07 |
| bridge B=100 tls | 1500 | 5.86 | 202.55 | 362.56 | 569.43 | 666.98 | 207.67 | 191.08 / 198.47 / 210.56 |
| bridge B=100 answer | 1500 | 0.39 | 179.31 | 345.32 | 542.94 | 647.08 | 185.30 | 173.98 / 179.19 / 182.08 |
| bridge B=100 scram | 1500 | 5.96 | 26.99 | 44.66 | 70.01 | 120.13 | 28.91 | 25.22 / 27.31 / 28.67 |
| bridge B=500 total | 7500 | 41.12 | 1046.31 | 1862.58 | 2182.74 | 2375.05 | 1063.13 | 1054.79 / 1093.85 / 990.12 |
| bridge B=500 tls | 7500 | 7.09 | 997.80 | 1818.96 | 2135.65 | 2325.09 | 1013.99 | 1006.94 / 1047.92 / 948.55 |
| bridge B=500 answer | 7500 | 0.13 | 977.69 | 1796.47 | 2113.05 | 2296.74 | 990.28 | 985.67 / 1025.96 / 925.31 |
| bridge B=500 scram | 7500 | 5.91 | 28.37 | 48.07 | 74.51 | 142.65 | 30.46 | 26.97 / 29.39 / 28.44 |
| published B=100 total | 1500 | 31.45 | 264.42 | 442.41 | 563.71 | 625.50 | 266.95 | 260.02 / 276.83 / 256.78 |
| published B=100 tls | 1500 | 11.36 | 225.36 | 405.45 | 521.86 | 603.18 | 231.22 | 218.35 / 232.11 / 222.18 |
| published B=100 answer | 1500 | 1.23 | 195.67 | 376.55 | 491.65 | 581.76 | 204.82 | 187.36 / 208.80 / 198.96 |
| published B=100 scram | 1500 | 8.02 | 31.44 | 54.02 | 90.33 | 142.23 | 34.06 | 31.24 / 32.99 / 30.27 |
| published B=500 total | 7500 | 65.69 | 1232.26 | 2077.15 | 2570.05 | 2691.98 | 1210.91 | 1248.72 / 1192.36 / 1249.25 |
| published B=500 tls | 7500 | 27.42 | 1165.41 | 2027.81 | 2514.54 | 2645.05 | 1152.29 | 1174.55 / 1137.08 / 1185.11 |
| published B=500 answer | 7500 | 1.70 | 1116.03 | 2003.51 | 2480.03 | 2616.47 | 1121.77 | 1105.78 / 1111.81 / 1154.38 |
| published B=500 scram | 7500 | 6.85 | 32.30 | 59.09 | 309.19 | 577.23 | 41.03 | 32.46 / 31.46 / 32.87 |

#### M7 sustained — per-connect latency (ms)

| | n | min | p50 | p90 | p99 | max | mean | per-run p50 (asc / desc / desc) |
|---|---|---|---|---|---|---|---|---|
| bridge c=1 | 3517 | 11.18 | 16.00 | 20.41 | 29.11 | 61.45 | 16.83 | 16.03 / 15.68 / 16.72 |
| bridge c=2 | 5535 | 11.28 | 19.45 | 30.18 | 45.29 | 93.63 | 21.42 | 18.71 / 28.22 / 18.10 |
| bridge c=4 | 8441 | 12.28 | 26.20 | 38.79 | 63.65 | 126.85 | 28.21 | 25.49 / 33.18 / 24.39 |
| bridge c=8 | 10740 | 15.62 | 43.43 | 58.07 | 77.51 | 141.55 | 44.49 | 41.57 / 43.00 / 46.43 |
| bridge c=16 | 11590 | 22.66 | 81.77 | 105.08 | 130.61 | 184.12 | 82.64 | 85.31 / 79.35 / 81.05 |
| bridge c=32 | 11600 | 81.92 | 163.24 | 196.40 | 231.58 | 344.41 | 165.31 | 172.66 / 158.37 / 160.04 |
| bridge c=64 | 11575 | 107.87 | 328.35 | 377.37 | 448.45 | 697.37 | 331.50 | 317.53 / 337.28 / 329.69 |
| published c=1 | 3212 | 12.44 | 17.56 | 21.36 | 34.64 | 276.66 | 18.49 | 18.08 / 17.73 / 16.99 |
| published c=2 | 5086 | 13.74 | 22.65 | 28.86 | 38.67 | 88.06 | 23.36 | 21.25 / 23.31 / 23.77 |
| published c=4 | 7893 | 14.38 | 29.53 | 37.98 | 48.48 | 88.98 | 30.19 | 29.79 / 28.51 / 30.54 |
| published c=8 | 9487 | 16.68 | 48.63 | 65.76 | 90.49 | 359.21 | 50.35 | 47.78 / 47.16 / 51.09 |
| published c=16 | 10106 | 27.32 | 93.35 | 122.28 | 153.50 | 225.25 | 94.81 | 96.82 / 92.78 / 90.82 |
| published c=32 | 10437 | 53.17 | 182.13 | 221.32 | 265.65 | 412.51 | 183.76 | 175.23 / 183.04 / 187.91 |
| published c=64 | 9745 | 220.28 | 380.15 | 478.95 | 626.38 | 863.03 | 392.91 | 405.46 / 383.56 / 369.55 |

#### M7 — rates (connects per second; integer part of the milli-unit values)

| kind | path | cond | cells | all: min / p50 / max | uncontended cells | uncontended: min / p50 / max | per-run p50 (asc / desc / desc) | per-second min / p50 / max (all cells) | failures |
|---|---|---|---|---|---|---|---|---|---|
| burst | bridge | B=100 | 15 | 144 / 246 / 268 | 14 | 212 / 246 / 268 | 246 / 247 / 245 | — | 0 |
| burst | bridge | B=500 | 15 | 208 / 233 / 284 | 14 | 221 / 233 / 284 | 232 / 231 / 260 | — | 0 |
| burst | published | B=100 | 15 | 157 / 219 / 242 | 14 | 188 / 219 / 242 | 228 / 210 / 228 | — | 0 |
| burst | published | B=500 | 15 | 184 / 224 / 247 | 12 | 188 / 226 / 247 | 226 / 224 / 204 | — | 0 |
| sustained | bridge | c=1 | 3 | 54 / 60 / 61 | 2 | 60 / 60 / 61 | 60 / 61 / 54 | 38 / 61 / 67 | 0 |
| sustained | bridge | c=2 | 3 | 67 / 103 / 106 | 2 | 103 / 103 / 106 | 103 / 67 / 106 | 54 / 100 / 114 | 0 |
| sustained | bridge | c=4 | 3 | 110 / 152 / 159 | 2 | 152 / 152 / 159 | 152 / 110 / 159 | 74 / 152 / 181 | 0 |
| sustained | bridge | c=8 | 3 | 167 / 181 / 188 | 3 | 167 / 181 / 188 | 188 / 181 / 167 | 123 / 180 / 206 | 0 |
| sustained | bridge | c=16 | 3 | 184 / 195 / 199 | 3 | 184 / 195 / 199 | 184 / 199 / 195 | 163 / 193 / 221 | 0 |
| sustained | bridge | c=32 | 3 | 184 / 197 / 198 | 3 | 184 / 197 / 198 | 184 / 198 / 197 | 163 / 194 / 218 | 0 |
| sustained | bridge | c=64 | 3 | 188 / 192 / 198 | 3 | 188 / 192 / 198 | 198 / 188 / 192 | 146 / 193 / 230 | 0 |
| sustained | published | c=1 | 3 | 51 / 53 / 55 | 2 | 51 / 51 / 55 | 51 / 53 / 55 | 31 / 55 / 61 | 0 |
| sustained | published | c=2 | 3 | 79 / 83 / 91 | 1 | 91 / 91 / 91 | 91 / 83 / 79 | 67 / 85 / 97 | 0 |
| sustained | published | c=4 | 3 | 126 / 131 / 136 | 3 | 126 / 131 / 136 | 131 / 136 / 126 | 103 / 132 / 153 | 0 |
| sustained | published | c=8 | 3 | 149 / 160 / 164 | 3 | 149 / 160 / 164 | 160 / 164 / 149 | 101 / 157 / 189 | 0 |
| sustained | published | c=16 | 3 | 162 / 170 / 172 | 3 | 162 / 170 / 172 | 162 / 170 / 172 | 140 / 168 / 196 | 0 |
| sustained | published | c=32 | 3 | 168 / 173 / 180 | 3 | 168 / 173 / 180 | 180 / 173 / 168 | 150 / 174 / 204 | 0 |
| sustained | published | c=64 | 3 | 149 / 164 / 173 | 2 | 164 / 164 / 173 | 149 / 164 / 173 | 98 / 165 / 206 | 0 |

#### M7 — CPU attribution per cell (every measured cell; contended ones marked)

| run | kind | path | cond | rep | rate/s | Postgres CPUs | probe CPUs | other CPUs | PG CPU ms/connect | probe CPU ms/connect | docker-proxy CPU ms/connect | load1 before | quiet other mCPU | contended |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 20261126T031313Z-asc | burst | bridge | B=100 | 1 | 257 | 2.30 | 1.37 | 0.08 | 8.96 | 5.32 | — | 3.94 | 97 |  |
| 20261126T031313Z-asc | burst | published | B=100 | 1 | 231 | 2.16 | 1.26 | 0.03 | 9.32 | 5.45 | 1.00 | 3.94 | 365 |  |
| 20261126T031313Z-asc | burst | bridge | B=500 | 1 | 232 | 2.26 | 1.25 | 0.32 | 9.71 | 5.39 | — | 3.94 | 63 |  |
| 20261126T031313Z-asc | burst | published | B=500 | 1 | 185 | 1.95 | 1.05 | 0.62 | 10.53 | 5.66 | 1.26 | 5.23 | 155 | **yes** |
| 20261126T031313Z-asc | burst | published | B=100 | 2 | 228 | 2.18 | 1.23 | 0.05 | 9.54 | 5.36 | 1.30 | 5.53 | 729 |  |
| 20261126T031313Z-asc | burst | bridge | B=100 | 2 | 240 | 2.31 | 1.38 | 0.09 | 9.57 | 5.74 | — | 5.53 | 147 |  |
| 20261126T031313Z-asc | burst | published | B=500 | 2 | 245 | 2.22 | 1.18 | 0.08 | 9.03 | 4.81 | 1.00 | 5.97 | 322 |  |
| 20261126T031313Z-asc | burst | bridge | B=500 | 2 | 254 | 2.38 | 1.29 | 0.11 | 9.33 | 5.08 | — | 5.97 | 621 |  |
| 20261126T031313Z-asc | burst | bridge | B=100 | 3 | 266 | 2.38 | 1.33 | 0.01 | 8.94 | 5.01 | — | 6.05 | 737 |  |
| 20261126T031313Z-asc | burst | published | B=100 | 3 | 240 | 2.19 | 1.22 | 0.00 | 9.12 | 5.08 | 1.10 | 6.05 | 132 |  |
| 20261126T031313Z-asc | burst | bridge | B=500 | 3 | 255 | 2.35 | 1.33 | 0.11 | 9.23 | 5.23 | — | 5.57 | 219 |  |
| 20261126T031313Z-asc | burst | published | B=500 | 3 | 211 | 2.17 | 1.18 | 0.18 | 10.26 | 5.59 | 1.22 | 5.12 | 165 |  |
| 20261126T031313Z-asc | burst | published | B=100 | 4 | 221 | 2.09 | 1.18 | 0.29 | 9.45 | 5.34 | 1.20 | 5.12 | 111 |  |
| 20261126T031313Z-asc | burst | bridge | B=100 | 4 | 246 | 2.34 | 1.37 | 0.07 | 9.47 | 5.56 | — | 4.71 | 326 |  |
| 20261126T031313Z-asc | burst | published | B=500 | 4 | 228 | 2.14 | 1.19 | 0.17 | 9.36 | 5.20 | 1.14 | 4.71 | 115 |  |
| 20261126T031313Z-asc | burst | bridge | B=500 | 4 | 224 | 2.31 | 1.33 | 0.20 | 10.28 | 5.95 | — | 4.33 | 215 |  |
| 20261126T031313Z-asc | burst | bridge | B=100 | 5 | 212 | 2.12 | 1.21 | 0.41 | 9.95 | 5.70 | — | 4.33 | 117 |  |
| 20261126T031313Z-asc | burst | published | B=100 | 5 | 188 | 1.99 | 1.18 | 0.26 | 10.57 | 6.24 | 1.30 | 5.35 | 169 |  |
| 20261126T031313Z-asc | burst | bridge | B=500 | 5 | 226 | 2.23 | 1.23 | 0.31 | 9.85 | 5.41 | — | 5.35 | 132 |  |
| 20261126T031313Z-asc | burst | published | B=500 | 5 | 226 | 2.14 | 1.18 | 0.23 | 9.47 | 5.22 | 1.10 | 6.76 | 380 |  |
| 20261126T031313Z-asc | sust. | published | c=1 | 0 | 51 | 0.84 | 0.37 | 0.30 | 16.28 | 7.12 | 2.93 | 6.22 | 448 |  |
| 20261126T031313Z-asc | sust. | bridge | c=1 | 0 | 60 | 0.95 | 0.42 | 0.23 | 15.92 | 7.10 | — | 5.44 | 95 |  |
| 20261126T031313Z-asc | sust. | bridge | c=2 | 1 | 103 | 1.55 | 0.66 | 0.16 | 15.02 | 6.37 | — | 4.24 | 315 |  |
| 20261126T031313Z-asc | sust. | published | c=2 | 1 | 91 | 1.37 | 0.57 | 0.09 | 14.93 | 6.28 | 2.13 | 4.50 | 153 |  |
| 20261126T031313Z-asc | sust. | published | c=4 | 2 | 131 | 1.95 | 0.79 | 0.08 | 14.84 | 5.99 | 1.92 | 3.99 | 172 |  |
| 20261126T031313Z-asc | sust. | bridge | c=4 | 2 | 152 | 2.22 | 0.89 | 0.09 | 14.53 | 5.83 | — | 4.25 | 147 |  |
| 20261126T031313Z-asc | sust. | bridge | c=8 | 3 | 188 | 2.59 | 1.04 | 0.12 | 13.76 | 5.52 | — | 5.20 | 245 |  |
| 20261126T031313Z-asc | sust. | published | c=8 | 3 | 160 | 2.31 | 0.91 | 0.19 | 14.41 | 5.67 | 1.74 | 7.20 | 127 |  |
| 20261126T031313Z-asc | sust. | published | c=16 | 4 | 162 | 2.46 | 0.96 | 0.18 | 15.18 | 5.95 | 1.80 | 8.45 | 312 |  |
| 20261126T031313Z-asc | sust. | bridge | c=16 | 4 | 184 | 2.67 | 1.07 | 0.18 | 14.50 | 5.82 | — | 11.52 | 634 |  |
| 20261126T031313Z-asc | sust. | bridge | c=32 | 5 | 184 | 2.65 | 1.07 | 0.22 | 14.38 | 5.84 | — | 12.22 | 156 |  |
| 20261126T031313Z-asc | sust. | published | c=32 | 5 | 180 | 2.48 | 0.97 | 0.19 | 13.76 | 5.41 | 1.62 | 12.73 | 185 |  |
| 20261126T031313Z-asc | sust. | published | c=64 | 6 | 149 | 2.14 | 0.85 | 0.71 | 14.29 | 5.68 | 1.72 | 13.17 | 94 | **yes** |
| 20261126T031313Z-asc | sust. | bridge | c=64 | 6 | 198 | 2.66 | 1.08 | 0.21 | 13.42 | 5.46 | — | 14.92 | 129 |  |
| 20261126T032236Z-desc | burst | bridge | B=100 | 1 | 257 | 2.44 | 1.34 | 0.02 | 9.50 | 5.22 | — | 4.13 | 221 |  |
| 20261126T032236Z-desc | burst | published | B=100 | 1 | 214 | 2.19 | 1.27 | 0.22 | 10.26 | 5.93 | 1.20 | 3.80 | 118 |  |
| 20261126T032236Z-desc | burst | bridge | B=500 | 1 | 231 | 2.37 | 1.33 | 0.17 | 10.26 | 5.75 | — | 3.80 | 340 |  |
| 20261126T032236Z-desc | burst | published | B=500 | 1 | 188 | 2.12 | 1.16 | 0.30 | 11.26 | 6.17 | 1.40 | 31.84 | 144 |  |
| 20261126T032236Z-desc | burst | published | B=100 | 2 | 208 | 2.19 | 1.22 | 0.10 | 10.55 | 5.85 | 1.30 | 29.29 | 228 |  |
| 20261126T032236Z-desc | burst | bridge | B=100 | 2 | 252 | 2.37 | 1.39 | 0.07 | 9.41 | 5.52 | — | 29.29 | 277 |  |
| 20261126T032236Z-desc | burst | published | B=500 | 2 | 213 | 2.15 | 1.19 | 0.23 | 10.09 | 5.56 | 1.22 | 29.29 | 394 |  |
| 20261126T032236Z-desc | burst | bridge | B=500 | 2 | 258 | 2.39 | 1.32 | 0.20 | 9.24 | 5.10 | — | 27.91 | 142 |  |
| 20261126T032236Z-desc | burst | bridge | B=100 | 3 | 216 | 2.15 | 1.25 | 0.42 | 9.94 | 5.79 | — | 25.67 | 59 |  |
| 20261126T032236Z-desc | burst | published | B=100 | 3 | 202 | 2.18 | 1.25 | 0.25 | 10.77 | 6.21 | 1.60 | 25.67 | 161 |  |
| 20261126T032236Z-desc | burst | bridge | B=500 | 3 | 226 | 2.33 | 1.29 | 0.23 | 10.30 | 5.70 | — | 25.67 | 128 |  |
| 20261126T032236Z-desc | burst | published | B=500 | 3 | 237 | 2.26 | 1.21 | 0.07 | 9.50 | 5.10 | 1.10 | 24.34 | 529 |  |
| 20261126T032236Z-desc | burst | published | B=100 | 4 | 219 | 2.10 | 1.18 | 0.23 | 9.55 | 5.39 | 1.20 | 23.51 | 312 |  |
| 20261126T032236Z-desc | burst | bridge | B=100 | 4 | 239 | 2.34 | 1.36 | 0.06 | 9.74 | 5.69 | — | 23.51 | 273 |  |
| 20261126T032236Z-desc | burst | published | B=500 | 4 | 224 | 2.23 | 1.20 | 0.16 | 9.92 | 5.35 | 1.14 | 23.51 | 193 |  |
| 20261126T032236Z-desc | burst | bridge | B=500 | 4 | 221 | 2.33 | 1.30 | 0.26 | 10.50 | 5.88 | — | 24.19 | 294 |  |
| 20261126T032236Z-desc | burst | bridge | B=100 | 5 | 247 | 2.40 | 1.35 | 0.11 | 9.70 | 5.46 | — | 23.77 | 74 |  |
| 20261126T032236Z-desc | burst | published | B=100 | 5 | 210 | 2.12 | 1.18 | 0.36 | 10.07 | 5.62 | 1.10 | 23.77 | 258 |  |
| 20261126T032236Z-desc | burst | bridge | B=500 | 5 | 233 | 2.29 | 1.30 | 0.29 | 9.81 | 5.58 | — | 21.87 | 130 |  |
| 20261126T032236Z-desc | burst | published | B=500 | 5 | 243 | 2.20 | 1.22 | 0.18 | 9.04 | 5.01 | 1.00 | 21.87 | 49 |  |
| 20261126T032236Z-desc | sust. | published | c=64 | 0 | 164 | 2.46 | 0.97 | 0.22 | 14.96 | 5.88 | 1.79 | 21.48 | 126 |  |
| 20261126T032236Z-desc | sust. | bridge | c=64 | 0 | 188 | 2.67 | 1.09 | 0.17 | 14.20 | 5.81 | — | 21.46 | 144 |  |
| 20261126T032236Z-desc | sust. | bridge | c=32 | 1 | 198 | 2.69 | 1.09 | 0.17 | 13.54 | 5.48 | — | 20.72 | 581 |  |
| 20261126T032236Z-desc | sust. | published | c=32 | 1 | 173 | 2.51 | 0.97 | 0.16 | 14.45 | 5.62 | 1.70 | 18.65 | 235 |  |
| 20261126T032236Z-desc | sust. | published | c=16 | 2 | 170 | 2.50 | 0.97 | 0.17 | 14.69 | 5.68 | 1.71 | 19.39 | 65 |  |
| 20261126T032236Z-desc | sust. | bridge | c=16 | 2 | 199 | 2.71 | 1.07 | 0.15 | 13.61 | 5.37 | — | 18.57 | 236 |  |
| 20261126T032236Z-desc | sust. | bridge | c=8 | 3 | 181 | 2.62 | 1.03 | 0.18 | 14.40 | 5.70 | — | 18.42 | 155 |  |
| 20261126T032236Z-desc | sust. | published | c=8 | 3 | 164 | 2.36 | 0.92 | 0.20 | 14.35 | 5.60 | 1.71 | 15.88 | 166 |  |
| 20261126T032236Z-desc | sust. | published | c=4 | 4 | 136 | 1.98 | 0.80 | 0.09 | 14.49 | 5.84 | 1.86 | 13.47 | 245 |  |
| 20261126T032236Z-desc | sust. | bridge | c=4 | 4 | 110 | 1.90 | 0.78 | 0.86 | 17.23 | 7.05 | — | 10.64 | 145 | **yes** |
| 20261126T032236Z-desc | sust. | bridge | c=2 | 5 | 67 | 1.13 | 0.49 | 1.94 | 16.82 | 7.28 | — | 10.01 | 590 | **yes** |
| 20261126T032236Z-desc | sust. | published | c=2 | 5 | 83 | 1.34 | 0.57 | 0.18 | 16.17 | 6.86 | 2.48 | 8.41 | 1829 | **yes** |
| 20261126T032236Z-desc | sust. | published | c=1 | 6 | 53 | 0.82 | 0.36 | 0.68 | 15.39 | 6.74 | 2.56 | 6.89 | 136 | **yes** |
| 20261126T032236Z-desc | sust. | bridge | c=1 | 6 | 61 | 0.95 | 0.42 | 0.18 | 15.53 | 6.95 | — | 5.14 | 899 |  |
| 20261126T033237Z-desc | burst | bridge | B=100 | 1 | 144 | 1.47 | 0.85 | 1.86 | 10.14 | 5.90 | — | 6.52 | 3729 | **yes** |
| 20261126T033237Z-desc | burst | published | B=100 | 1 | 157 | 1.55 | 0.85 | 1.30 | 9.86 | 5.41 | 1.30 | 6.59 | 1669 | **yes** |
| 20261126T033237Z-desc | burst | bridge | B=500 | 1 | 208 | 2.05 | 1.15 | 0.68 | 9.80 | 5.51 | — | 6.21 | 319 | **yes** |
| 20261126T033237Z-desc | burst | published | B=500 | 1 | 204 | 2.09 | 1.12 | 0.42 | 10.24 | 5.47 | 1.20 | 38.00 | 1209 | **yes** |
| 20261126T033237Z-desc | burst | published | B=100 | 2 | 242 | 2.18 | 1.20 | 0.23 | 8.99 | 4.94 | 1.00 | 38.00 | 370 |  |
| 20261126T033237Z-desc | burst | bridge | B=100 | 2 | 243 | 2.22 | 1.24 | 0.35 | 9.14 | 5.12 | — | 34.96 | 356 |  |
| 20261126T033237Z-desc | burst | published | B=500 | 2 | 184 | 1.80 | 0.95 | 0.88 | 9.75 | 5.14 | 1.16 | 34.96 | 717 | **yes** |
| 20261126T033237Z-desc | burst | bridge | B=500 | 2 | 260 | 2.35 | 1.33 | 0.17 | 9.01 | 5.10 | — | 33.68 | 335 |  |
| 20261126T033237Z-desc | burst | bridge | B=100 | 3 | 268 | 2.35 | 1.36 | 0.08 | 8.75 | 5.06 | — | 31.78 | 389 |  |
| 20261126T033237Z-desc | burst | published | B=100 | 3 | 238 | 2.20 | 1.23 | -0.00 | 9.26 | 5.15 | 1.10 | 31.78 | 337 |  |
| 20261126T033237Z-desc | burst | bridge | B=500 | 3 | 237 | 2.31 | 1.30 | 0.24 | 9.73 | 5.47 | — | 29.24 | 209 |  |
| 20261126T033237Z-desc | burst | published | B=500 | 3 | 191 | 2.13 | 1.18 | 0.30 | 11.11 | 6.16 | 1.28 | 29.24 | 238 |  |
| 20261126T033237Z-desc | burst | published | B=100 | 4 | 205 | 2.08 | 1.17 | 0.25 | 10.11 | 5.68 | 1.20 | 30.18 | 287 |  |
| 20261126T033237Z-desc | burst | bridge | B=100 | 4 | 245 | 2.37 | 1.33 | 0.02 | 9.68 | 5.42 | — | 30.18 | 222 |  |
| 20261126T033237Z-desc | burst | published | B=500 | 4 | 226 | 2.19 | 1.21 | 0.17 | 9.64 | 5.34 | 1.12 | 29.36 | 97 |  |
| 20261126T033237Z-desc | burst | bridge | B=500 | 4 | 284 | 2.39 | 1.32 | 0.13 | 8.41 | 4.65 | — | 27.09 | 166 |  |
| 20261126T033237Z-desc | burst | bridge | B=100 | 5 | 247 | 2.27 | 1.30 | 0.19 | 9.17 | 5.25 | — | 27.09 | 180 |  |
| 20261126T033237Z-desc | burst | published | B=100 | 5 | 228 | 2.20 | 1.21 | 0.09 | 9.63 | 5.29 | 1.10 | 27.09 | 70 |  |
| 20261126T033237Z-desc | burst | bridge | B=500 | 5 | 267 | 2.38 | 1.34 | 0.12 | 8.90 | 5.02 | — | 24.92 | 203 |  |
| 20261126T033237Z-desc | burst | published | B=500 | 5 | 247 | 2.23 | 1.24 | 0.12 | 8.98 | 5.00 | 1.00 | 24.92 | 114 |  |
| 20261126T033237Z-desc | sust. | published | c=64 | 0 | 173 | 2.50 | 0.97 | 0.16 | 14.42 | 5.64 | 1.72 | 22.93 | 405 |  |
| 20261126T033237Z-desc | sust. | bridge | c=64 | 0 | 192 | 2.70 | 1.09 | 0.16 | 14.00 | 5.67 | — | 22.81 | 162 |  |
| 20261126T033237Z-desc | sust. | bridge | c=32 | 1 | 197 | 2.70 | 1.09 | 0.17 | 13.67 | 5.51 | — | 22.14 | 103 |  |
| 20261126T033237Z-desc | sust. | published | c=32 | 1 | 168 | 2.46 | 0.96 | 0.21 | 14.67 | 5.75 | 1.74 | 20.20 | 97 |  |
| 20261126T033237Z-desc | sust. | published | c=16 | 2 | 172 | 2.50 | 0.96 | 0.17 | 14.46 | 5.59 | 1.70 | 18.08 | 440 |  |
| 20261126T033237Z-desc | sust. | bridge | c=16 | 2 | 195 | 2.71 | 1.07 | 0.15 | 13.87 | 5.51 | — | 17.83 | 273 |  |
| 20261126T033237Z-desc | sust. | bridge | c=8 | 3 | 167 | 2.60 | 1.03 | 0.18 | 15.60 | 6.15 | — | 18.07 | 74 |  |
| 20261126T033237Z-desc | sust. | published | c=8 | 3 | 149 | 2.29 | 0.89 | 0.21 | 15.28 | 5.96 | 1.88 | 15.90 | 95 |  |
| 20261126T033237Z-desc | sust. | published | c=4 | 4 | 126 | 1.96 | 0.79 | 0.13 | 15.43 | 6.20 | 2.06 | 14.69 | 203 |  |
| 20261126T033237Z-desc | sust. | bridge | c=4 | 4 | 159 | 2.23 | 0.90 | 0.09 | 14.02 | 5.68 | — | 11.20 | 154 |  |
| 20261126T033237Z-desc | sust. | bridge | c=2 | 5 | 106 | 1.58 | 0.66 | 0.14 | 14.82 | 6.23 | — | 9.45 | 204 |  |
| 20261126T033237Z-desc | sust. | published | c=2 | 5 | 79 | 1.27 | 0.54 | 0.60 | 15.96 | 6.75 | 2.50 | 7.04 | 396 | **yes** |
| 20261126T033237Z-desc | sust. | published | c=1 | 6 | 55 | 0.85 | 0.37 | 0.19 | 15.26 | 6.61 | 2.54 | 5.79 | 249 |  |
| 20261126T033237Z-desc | sust. | bridge | c=1 | 6 | 54 | 0.85 | 0.38 | 1.35 | 15.62 | 6.88 | — | 4.29 | 255 | **yes** |


### What the numbers say about where connect time goes (observations)

- **Phase shares at c=1** (bridge p50): TCP 0.4 ms; `answer` 1.85 ms (this is the
  postmaster forking the backend and the backend reading the SSLRequest — the server
  answers `S` only from the new child); rest of TLS ≈ 5 ms (TLS 1.3 handshake with an RSA
  2048 certificate signature on the server, key exchange on both sides); `scram` 8.4 ms;
  `ready` 0.03 ms.
- **`ready` is not observable as a separate phase.** AuthenticationOk arrives only
  about 0.03 ms before ReadyForQuery. That fits Postgres not flushing AuthenticationOk
  (and SASLFinal) on its own but together with ParameterStatus, BackendKeyData and
  ReadyForQuery (my reading of the server's design, not verified in its source here), so everything the backend does after verifying the proof (the rest of
  its initialisation) lands inside `scram`. `scram` therefore = two round trips + client
  PBKDF2 (3.1 ms p50, measured separately) + server proof check + post-authentication
  backend start-up.
- **As concurrency rises, the growth is in `answer`**, i.e. before the backend has even
  answered the SSLRequest: at c=64 `answer` p50 is 97 ms of a 153 ms total (bridge), at
  B=500 978 ms of 1,046 ms, while `scram` grows only from 8.4 ms to 25–28 ms. Connects
  queue in front of the server's TLS answer.
- **CPU per connect** (Table "CPU attribution"): Postgres about 8.4–11.3 ms CPU per connect
  in the burst windows (which end at the last ReadyForQuery, so they exclude backend exit)
  and 13.4–16.3 ms in the sustained windows (which include the exit of every closed
  backend); the probe about 4.6–7.1 ms per connect, of which about 3 ms is the PBKDF2;
  docker-proxy about 1.0–1.6 ms (burst) and 1.6–2.9 ms (sustained) per connect on the
  published path.
- **The host is CPU-saturated from c=8 up**: in the uncontended sustained cells at
  c ≥ 8, Postgres uses 2.3–2.7 CPUs, the probe 0.9–1.1 CPUs, and other 0.12–0.22 CPUs
  (kernel networking and the rest of the VM): together about 4 of the 4 vCPUs. The
  plateau from c=8 to c=64 coincides with that saturation. In the bursts the same split
  holds (uncontended: Postgres 2.0–2.4 CPUs, probe 1.2–1.4 CPUs). So in this set-up the ceiling is the
  shared host's CPU, of which the client side (TLS + SCRAM + Go runtime + docker-proxy on
  the published path) takes roughly 30–40 %. These figures let the reader estimate what
  Postgres alone would do with the whole machine; the probe does not make that estimate.
- **Paths**: the published path is slower per round trip (M1: +89 µs on `DISCARD ALL`,
  +93 µs on `SELECT 1`) and its sustained plateau is about 12 % lower (≈ 170–173/s
  against 195–197/s at c=16–32); docker-proxy's own CPU (1.6–2.9 ms per connect in sustained cells) explains
  most of that on a saturated host. Burst rates differ less (B=500: 224 against 233/s,
  overlapping ranges).

### Other observations

- **Reset cost by taint kind** (M2I, bridge p50). Measured in isolation, every kind that
  leaves no temp table resets in 133–186 µs, the same band as the control `none` (177 µs):
  the per-kind differences (`listen` 149, `set` 164, `setseed` 172, `set_config_fn` 186) are
  about the size of one kind's run-to-run spread (e.g. `none` 163–199 µs across runs), and
  no kind's pooled p50 is more than 10 µs above `none`. The
  transaction kinds' `ROLLBACK` adds 140 µs (`in_tx`) and 174 µs (`failed_tx`), so their
  totals are 280 and 341 µs. Three kinds stand apart: `temp_table` 3.07 ms, `all` 3.05 ms
  (which includes a temp table) and `temp_table_rows` 3.89 ms (1,000 rows and an index: about
  0.8 ms more than an empty one). Their spread is wide (p99 17–27 ms) and run 4 was slower
  (4.0 / 4.0 / 4.9 ms against 2.7–3.7 ms in runs 5 and 6). That dropping a temporary table
  (catalog row deletes, file unlinks, a commit) is the cost is an inference, not measured
  separately.
- **What the old M2 measured.** With the kinds isolated the old M2's ordering (`set` 250 µs,
  `set_role` 255, `setseed` 189, … `listen` 118) disappears: it was the carry-over from the
  temp-table drop that preceded each kind by a fixed distance, not the kind (§3, D10).
- **Carry-over after a temp-table drop** (`after1`…`after5`). After the three temp-table
  kinds the next clean `DISCARD ALL` costs 393–429 µs, then 233–257, 189–202, 168–171 and
  154–157 µs: about +250 µs on the first, decaying, and still about +50 µs above the other
  kinds' `after5` (≈ 98–105 µs) five resets later. So a temp table costs the lease that
  created it about 3 ms and the next few leases on that backend a few hundred µs in total.
- **Carry-over without a temp table.** Every other kind, `none` included, shows a smaller
  curve of its own: `discard` 133–186 µs, `after1` 118–150, down to 98–105 at `after5`. Since `none` has it too, it is not left by the taint; it fits a backend that has
  been idle for 0.15–0.27 s (the round-robin, §3) running its first few statements slower
  (scheduler wake-up, cold CPU caches — inference, not measured). This is why M2I's resets
  read about twice M1's 88 µs.
- **History** (M2H). A backend that once held a temp table pays more on every later clean
  `DISCARD ALL`: p50 140 µs against 114 µs (+26 µs, +23 %), measured 120–1,120 resets after
  the drop, in all 18 pairs (+18 to +50 µs per pair). Within the 1,000 timed samples the gap
  did not visibly close (not tested formally). Why is not established here. This
  confirms the reviewer's psql observation (+20–50 µs).
- **M1 is a best case.** M1's `DISCARD ALL` (88 µs bridge) is a tight loop on a busy, warm
  backend that has never had a temp table. A reset at the end of a real lease follows the
  lease's statements but may follow idle time, may land on a backend that once had a temp
  table (+26 µs, M2H) or just dropped one (+250 µs on the next reset, decaying); M2I's
  `none` (177 µs) is the closer figure for a reset after an ordinary lease on an otherwise
  idle-between-uses backend.
- **First statement** (M5): a fresh backend's first primary-key lookup costs 1.75 ms
  against 0.18 ms reused and 0.18 ms just after `DISCARD ALL`; `SELECT 1` 0.63 ms fresh,
  0.11 / 0.12 ms. `DISCARD ALL` does not empty the catalog caches, so the just-reset
  penalty on a simple Query is small.
- **plpgsql** (M6): first call on a fresh backend 4.40 ms; warm 0.22 ms; first call
  after `DISCARD ALL` 0.61 ms — the reset costs this function about 0.39 ms of re-planning
  on its next call.
- **Close** (M4): in 888 of 900 samples the first poll already saw the pid gone; upper bound
  p50 0.82 ms at a poll resolution (round trip) of p50 0.69 ms. The slot method never saw a
  rejection in 900 samples: by the time the retry's backend asked for a slot (after TCP,
  fork and the TLS handshake of the retry, ≈ 7 ms) the slot was always free; it bounds the
  release only from above, much more loosely than the poll.

## 6. Limits and confounders

- **Shared VM, foreign load.** Other agents' work ran on the VM during all three runs
  (`env.txt` lists `agy` and two `claude` sessions; the quiet checks saw up to 3.8 CPUs of
  other work at times). 13 of 102 measured M7 cells are marked contended (calibration.json
  `contended`); they are kept and also summarised separately. Run 2's sustained bridge
  c=2 and c=4 cells (67/s and 110/s) are the clearest cases: other work used 1.9 and 0.9
  CPUs during them. Run 3 was added because of this; its bursts at repetitions 1–2 (and the
  excluded warm-up repetition 0) were contended too; once the quiet check exhausted its 10
  waits at 3.7 CPUs of other work.
- **Probe and server share 4 vCPUs**, so the rates measured are rates of this pair, not of
  Postgres alone; see the CPU attribution. The client's PBKDF2 per connect is real proxy
  work as pgwire stands (no caching), but a proxy on its own host would not take it from
  the server's CPUs.
- **Loopback only.** No real network; a real link adds its round trip to every phase that
  has one (TCP 1, TLS 1 + 1, SCRAM 2, M1 1).
- **docker-proxy on the published path** adds per-round-trip latency and CPU; the bridge
  path has none. Neither is a production network path.
- **Phase boundaries are client-side timestamps**: `answer` includes fork and the server's
  read; `scram` includes post-auth backend start-up (above); `ready` is a flush artefact.
- **M4 poll measures disappearance from the activity view**, which Postgres clears during
  backend exit; whether it precedes or follows the PGPROC slot's release by microseconds is
  not resolved at a 0.7 ms poll resolution. Method 2 bounds the slot only loosely.
- **Sustained rate includes the probe's own close** (Terminate + socket close) in each
  worker's loop, as the brief specifies; the Postgres CPU in sustained cells includes
  backend exit, the burst CPU does not.
- **Load average** is recorded but mostly measures the probe's own preceding cells.
- **M2I sessions idle between rounds** (0.15–0.27 s, §3). Every M2I sample, the control
  included, is the first few statements after that idle time, so absolute M2I figures are
  above M1's tight-loop figures; compare kinds with `none`, not with M1. A pool whose
  backends are reused back to back would see less of this, one that parks them for seconds
  possibly more; neither was measured.
- **M1 is a best case**: a tight two-statement loop on a warm backend with a clean history
  (§5 "Other observations").
- **M2H's cause is not established**: the probe shows a persistent +26 µs on a backend that
  once had a temp table, measured up to 1,120 resets after the drop, but not what in the
  backend keeps it (no profiling was done), nor whether it ever ends.
- **M2I/M2H were run only on `bridge`** and only in runs 4–6, about 40–60 minutes after
  runs 1–3, on the same host; runs 4–6 started at 1-minute load ≈ 1.2–1.4 with the 5-minute
  average still 5.6–7.7 from earlier work. Run 4 read higher than 5 and 6 on most M2I
  figures (cause not established; its quiet reading was 238 milli-CPUs).
- **M2R** (the old M2) is order-confounded and should not be read as per-kind costs (§3).
- **USER_HZ = 100** assumed for docker-proxy and `/proc/stat` (checked with `getconf
  CLK_TCK` on this host); docker-proxy CPU has 10 ms granularity per cell (cells run
  0.4–20 s, so ±2.5 % on the smallest burst cell's proxy figure).
- Run 1's `env.txt` line `userland_proxy` captured the wrong `docker info` field (shows
  only `"UserlandProxyPath"`); the script was corrected afterwards (runs 2 and 3 used the
  same uncorrected script). Docker reported `EnableUserlandProxy: true`,
  `UserlandProxyPath: /usr/bin/docker-proxy` during this session, and the docker-proxy
  pid used for accounting is recorded on the same line in every `env.txt`.

## 7. Decisions taken (nobody to ask)

- **D1 Driver.** `pgwire.BackendSession` was drivable from blocking sockets without
  difficulty (the same pattern as `backend_tierr_test.go`), so it is used for every
  connect; pgproto3 is not used. Frames are fed to `Receive` one at a time only to timestamp
  AuthenticationOk.
- **D2 Connect deadline** 30 s (not pgwire's 2 s default), so slow connects in the 500
  bursts are measured, not failed. None came near it (max 2.7 s).
- **D3 M5/M6 design.** One kind per fresh backend (i mod 3), so every `fresh` sample really
  is the backend's first statement.
- **D4 Paths.** M3 and M7 on both paths (required); M1 on both (cheap, shows the proxy's
  round-trip cost); M2 (old), M2I, M2H, M4, M5, M6 on `bridge` only, which has no extra hop.
- **D5 Quiet check** instead of the load-average test (method, §3).
- **D6 Third run.** Added because run 2 was contended; its order is desc so that each order
  has an uncontended replicate for most cells.
- **D7 Contended label** and its thresholds (§4).
- **D8 Probe size.** About 1,450 lines (main 342, measure 552, summarize 335, wire 220)
  against the plan's guide of 250. The summarizer, CPU accounting, taint verification and
  two-path interleaving account for most of it. The repair added `m2iso.go` (142 lines: M2I,
  M2H) and a few lines elsewhere; 1,612 lines in all.
- **D10 Old M2 kept as `M2R`.** Its rows stay in `costs` under measurement id `M2R`, with
  a `conditions.M2R` entry stating they are order-confounded and superseded by M2I, rather
  than being dropped: the raw rows are on file and the reviewer's finding was derived from
  them, so the summary keeps them visible but renamed so nothing reads them as `M2`. `summarize`
  does the rename; the raw `samples.csv` files of runs 1–3 are unchanged.
- **D11 M2I layout.** 3 sessions per kind (the minimum), 110 rounds each with 10 warm-up
  (300 timed per kind per run), all 45 sessions open at once and served round-robin with a
  rotating start; verification on each kind's first session before its timed rounds. Raw id
  `M2I`, parts `discard`/`rollback`/`total`/`after1`…`after5`.
- **D12 `temp_table_rows` included.** It was cheap (about 4 ms per round, 900 rounds per
  run), so the variant with 1,000 rows (`int`, `md5` text) and a btree index runs inside M2I
  as a 15th kind, labelled by its name.
- **D13 M2H details.** 6 pairs per run (≥ 5 asked). A also runs the 21 settling resets B
  runs, so both sides have the same number of earlier resets; per-sample alternation with
  the order flipped on odd samples and odd pairs.
- **D14 New default measurement list.** `-only` defaults to
  `PBKDF2,M1,M2I,M2H,M5,M4,M3,M7B,M7S`; `M2` is no longer in it but still selectable.
- **D15 probe stdout file.** `probe.log` from the repair on; runs 1–3's `probe.out` files
  were force-added rather than renamed, so the committed run directories stay as produced.
- **D9 Raw data in git.** `samples.csv` is about 7.5 MB per run (22 MB for three), kept
  uncompressed so `summarize` and DuckDB read it directly; this is a lab branch.

## 8. Deviations from the brief

- The "load above about 1.0 → wait or note" rule is implemented as the quiet check (D5);
  the load average is still noted on every cell.
- `ready` (AuthenticationOk → ReadyForQuery) is reported as specified but is not a real
  server phase on Postgres 17 (§5).
- M4's second method gives only a loose upper bound (no rejection was ever observed).
- Three full runs instead of two (D6). Partly contended cells are labelled, not re-run.
- The probe is much longer than the plan's 250-line estimate (D8).
- Repair: the repair runs 4–6 are named `…-asc-only-M2I-M2H` because the script appends
  `ORDER` (default `asc`) to every run id; M2I and M2H do not depend on the order.
- Repair: the old M2 (`M2R`) was not re-run; its rows remain only from runs 1–3.

## 9. Reproduce

Prerequisites: Docker, the `postgres:17` image, Go on PATH (`/usr/local/go/bin`). The
script never uses port 5432 and touches no other container; it removes its own
containers on exit or interrupt.

Whole run (each about 10 minutes; do it at least twice, once per order; since the repair the
default list runs M2I and M2H instead of the old M2):

```
ORDER=asc  scripts/lease-calib.sh
ORDER=desc scripts/lease-calib.sh
go run ./cmd/mgcalib summarize agent_work/chunks/c05-classifier/lease-exp/runs/r0-* \
  > agent_work/chunks/c05-classifier/lease-exp/calibration.json
```

Regenerate the committed calibration.json byte-for-byte from the committed raw files:

```
go run ./cmd/mgcalib summarize agent_work/chunks/c05-classifier/lease-exp/runs/r0-* \
  | cmp - agent_work/chunks/c05-classifier/lease-exp/calibration.json
```

One measurement on its own, against fresh containers (`ONLY` takes any of `PBKDF2, M1,
M2I, M2H, M2` (old)`, M5` (includes M6)`, M4, M3, M7B, M7S`, comma-separated; the run directory gets an
`-only-…` suffix; `RUNS_ROOT=/tmp/x` puts it elsewhere):

```
ONLY=M1          scripts/lease-calib.sh   # clean-session reset (M1), about 15 s
ONLY=M2I,M2H     scripts/lease-calib.sh   # reset per isolated taint kind (with verify.csv) and history, about 25-35 s
ONLY=M2I         scripts/lease-calib.sh   # isolated kinds and the after1..after5 curve only
ONLY=M2H         scripts/lease-calib.sh   # never-had vs once-had a temp table only, about 3 s
ONLY=M2          scripts/lease-calib.sh   # the OLD order-confounded M2 (summarised as M2R)
ONLY=M7B,M7S     scripts/lease-calib.sh   # P2: burst and sustained, both paths, about 7 min
ORDER=desc ONLY=M7S scripts/lease-calib.sh
go run ./cmd/mgcalib summarize <that run dir>   # its own summary
```

Against an existing server instead of the script's containers (the database must accept
`postgres` with SCRAM over TLS; the probe creates role `lab_r`, table `lab_kv`, functions
`lab_setcfg` and `lab_fn`; the M2I/M2H temp tables are per-session):

```
go run ./cmd/mgcalib run -out /tmp/r -addr published=127.0.0.1:PORT,bridge=IP:5432 \
  -password PW [-small-addr IP:5432] [-pg-cgroup .../cpu.stat] [-proxy-pid N] \
  [-order asc|desc] [-only M1,M2I,M2H,M7S]
```
