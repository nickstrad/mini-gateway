# Lease lab — piece E2 notes: lab driver, simulator shell, connect model, metrics, sidecars, runner (U5)

Builder: Opus, 2026-11-26. Brief `/root/briefs/zgateway-lease-e2-lab-driver.md`, rules
`/root/briefs/zgateway-lease-lab-rules.md`. Branch `exp/lease-lab`; not pushed.

E3 (scenarios), E4 (hostile workload, oracle), E5 (real shell) and E6 (report SQL) build against
sections 2–4 of this file. Those sections are the contract; anything not written there may change.

## 1. What is built

| Where | What |
| --- | --- |
| `internal/lab/leaselab/config.go` | `Config`: every knob K1–K19 (not K10, not `warm.rule`) plus `run.*` labels; `Parse` (key=value and `config=<file>`), `Validate`, `Rows` (lab.csv order), `Args` (replay) |
| `calib.go` | `LoadCalibration`: reads `calibration.json`, derives `Costs`, the fit targets and the host's cores, fits the connect model |
| `cpumodel.go` | The shared-CPU connect model (`CPUModel`, `CMParams`), `Predict` per calibration cell, `FitConnectModel` |
| `gen.go` | `Generator`: seeded open-loop arrivals, profiles, lease-time distributions |
| `seam.go` | The three seams: `DB`/`Sink`, `ScriptSource`, `Inspector`; their registries; the `plain` and `marker` scripts |
| `engine.go` | `Engine`: the event-driven state machine that runs leases over the DB seam (shared with E5) |
| `metrics.go` | The metrics under component `pool` and the histograms the driver keeps |
| `simdb.go` | `SimDB`: the simulator's DB (simnet + fakepg via `pgwire.BackendSession`, connect model, calibrated costs, faults) |
| `siminspect.go` | The built-in simulator inspector |
| `sidecar.go` | `lab.csv`, `oracle.csv`, `leases.csv` writers (`OpenSidecars`, `LabRows`, `WriteLab`) |
| `run.go` | `RunSim`: one simulated run into a run directory, with every invariant check |
| `cmd/mglease/` | The runner; `main.go` (subcommand registry), `sim.go` |
| `test/analysis/lease/checks/conservation.sql` | Conservation, as SQL for `scripts/csv-assert.sh` |
| `internal/lab/leasepool/pool.go` | One change: `pump` stops when no warm or idle connection is left (the E1 review's carried item) |

## 2. Seams (Go names)

All in package `leaselab` unless marked.

### 2.1 Database seam — `DB`, `Sink`

```go
type DB interface {
    Open(c leasepool.ConnID)            // result later: Sink.OpenDone
    Exec(c leasepool.ConnID, s Stmt)    // one simple Query; result later: Sink.ExecDone
    Close(c leasepool.ConnID)           // abort what is in flight (no ExecDone follows); Sink.CloseDone exactly once, when the backend is gone
    Now() int64                         // the shell's clock, ns since the run began
}
type Sink interface {                   // *Engine implements it
    OpenDone(c, OpenResult)             // OpenResult{OK, Fail leasepool.ConnectFail, PID, StartNs}
    ExecDone(c, Result)                 // Result{Rows [][]string, Tag, Status byte, Err string, Notifications []Notification}
    CloseDone(c)
    Lost(c)                             // the connection died with nothing in flight
}
type ServerStats interface { ServerBackends() (now, peak int64) }   // optional (S11)
```

- `Stmt{SQL, Purpose}`; `Purpose` is `PurposeLease`, `PurposeReset`, `PurposeReseed`,
  `PurposePrime`, `PurposeInspect`. A real shell ignores it; the simulator charges costs by it.
- `Result.Err` is the SQLSTATE of an ErrorResponse, `""` when none, or `ErrLost` ("lost") when the
  connection died under the statement. `Tag` is the last CommandComplete tag, `Status` the
  ReadyForQuery byte. `Result.OK()` = no error. `Notifications` (repair round) are the
  `NotificationResponse` messages read while the statement ran, plus any read since the
  previous statement's reply, in order: `Notification{PID int64; Channel, Payload string}`, at
  most `MaxNotifications` = 1,024 per Result (later ones dropped). The simulator's fake never
  sends one, so there it is always empty; E5's shell must fill it the same way.
- Rules a DB must keep: every call returns at once and **never calls the Sink from inside the
  call**; at most one statement in flight per connection; after a **failed** `OpenDone` the
  connection is gone (no `Close` follows); `Close` is valid on a lost connection; a connection
  that dies with a statement in flight answers `ExecDone{Err: ErrLost}`, otherwise `Lost`.
- The pool core's actions map onto it in `Engine.act`: open → `Open`; prime → `SELECT 1`
  (Purpose prime); reset → `DISCARD ALL` as its own Query, ok only on tag `DISCARD ALL` then
  status `I` (the core's `ReplyOK`); reseed → `Config.ReseedSQL` (key `db.reseed_sql`, default
  `SELECT setseed(0)`, one row, tag `SELECT 1`); close → `Close`; lend → the inspector (if
  drawn), then the script.

### 2.2 Engine — `Engine` (written once; E5 pumps it from a wall-clock loop)

```go
func NewEngine(cfg *Config, db DB, src ScriptSource, insp Inspector, stream func(label string) *rand.Rand, rec Records) (*Engine, error)
(e *Engine) Request(l LeaseInfo)       // at (or after) l.IntendedNs
(e *Engine) Tick()                     // every TickNs = 100 ms
(e *Engine) OpenDone / ExecDone / CloseDone / Lost   // Sink
(e *Engine) Source(extra func() Extra) obs.Source    // component pool
(e *Engine) Snapshot() leasepool.Snapshot; Settled() bool; InFlight() int; OpenConns() []ConnID; Err() error
type Records struct { Oracle func(OracleRow); Lease func(LeaseRecord) }   // sidecar sinks (OpenSidecars gives them)
func CheckConservation(s leasepool.Snapshot) error
```

No goroutines, no clock: time is `db.Now()`; events from inside an action are queued and run
after it (no re-entry into the core). Streams used: `lease.scripts` (ScriptSource),
`lease.oracle` (inspection draw). `Engine.Err()` is the first invariant violation or driver bug
(cap exceeded, K14 exceeded, the core refusing an event, a result for a connection with nothing
in flight, more leases in flight than cap + wait cap + 1).

### 2.3 ScriptSource — `ScriptSource`, `Script`, `LeaseInfo`

```go
type LeaseInfo struct { ID leasepool.LeaseID; Tenant leasepool.TenantID; IntendedNs, ServiceNs int64 }
type Script struct { Stmts []Stmt; Taint string; Abandon bool; AbandonAfter int }  // ≤ MaxScriptStmts = 64 statements
type ScriptSource interface { Script(l LeaseInfo, r *rand.Rand) Script }
func RegisterScriptSource(name string, f ScriptFactory)    // from init; ScriptFactory func(*Config) (ScriptSource, error)
func RegisterScriptProfile(name, base, script string)      // e.g. ("hostile", "steady", "hostile")
```

`workload.script` names the source (`plain` default, `marker`); a profile registered with
`RegisterScriptProfile` runs `base`'s arrivals and forces its script source. The engine sends
the statements one by one, regardless of errors, and releases with the **last** reply's status
(`I`, `T`, `E`); a lost connection ends the script and releases in `E` (the core closes it).

**Client gone** (repair round): `Abandon: true, AbandonAfter: n` (0 ≤ n ≤ len(Stmts); any
other n is an engine error) makes the client go away after statement n — at n = 0 before any
statement (after the inspection, if any). The engine sends nothing more and releases with
`ClientGone` and the **true server status**: the last ReadyForQuery status seen on that
connection (its last statement's, the inspector's, or `I` from the login). The core closes the
connection under every policy (`closes{client_gone}`, `leases_released{client_gone}`); such a
lease is not goodput. The zero value (`Abandon: false`) is a normal lease.

**`plain`, the default lease, is two statements** (repair round): a short statement first,
then the hold:

1. by `workload.stmt_class`: `pk_lookup` (default) → `SELECT v FROM lab_kv WHERE id = 42`;
   `plpgsql` → `SELECT lab_fn(42)`; `select1` and `off` → `SELECT 1` — exactly the statements
   calibration timed in M5/M6 (NOTES.md: `lab_kv` of 1,000 rows, `lab_fn` = three SELECTs on
   it). Constants `FirstPKLookup`, `FirstPLpgSQL`, `FirstSelect1`; `FirstStmtSQL(class)`.
2. `SELECT pg_sleep(<ServiceNs as s.nnnnnnnnn>)`.

Taint `""`. **E5 must run the same two statements on real Postgres** (and create `lab_kv` and
`lab_fn` as calibration did); R8 compares identical scripts. `first_stmt_ns` times statement 1.
`marker` is unchanged: `SET lab.marker = 't<tenant>'` then the sleep, taint `set`.

### 2.4 Inspector — `Inspector`, `InspectInput`, `OracleRow`

```go
type InspectInput struct { Lease; Tenant; Conn; HasPrev bool; PrevTenant; PrevTaint, Taint string; PID, StartNs int64 }
type Inspector interface {
    Statements(in InspectInput) []string                 // run in order, each its own Query (Purpose inspect), ≤ 64
    Row(in InspectInput, res []Result) OracleRow         // one Result per statement
}
func RegisterInspector(name string, f InspectorFactory)  // InspectorFactory func(*Config, DB) (Inspector, error)
```

Called at **grant**: after the pool lent the connection, before the script; `PrevTenant` /
`PrevTaint` are the previous lease on that connection (the engine's own record). The engine fills
`Lease, Tenant, PrevTenant (-1 if none), Conn, PID, StartNs, PrevTaint, Taint`; the inspector the
rest. `oracle.rate` (K18) is a fraction; each lease is drawn from stream `lease.oracle` (no draw at
0 or 1). `lease_wait_ns` is taken at lend, before the inspection; `lease_total_ns` subtracts the
inspection time. `oracle.inspector` names it (`builtin` default, `none`, or a registered one).
A row is `dirty` (metric `oracle_checks{outcome=dirty}`) when any of the seven category columns
is > 0; `canary_reads{outcome=leak}` counts rows with `canary_read = 1`, `clean` rows with 0.

### 2.5 The rest

- `Generator`: `NewGenerator(cfg, stream)`, `Next() (LeaseInfo, bool)`, `ExpectedMilli()`,
  `BaseRateMilli()`. Streams `lease.arrivals`, `lease.tenants`, `lease.times`.
- `Config`: `Parse(args)`, `Keys()`, `(*Config).Rows()`, `Args()`, `Validate()`; `Resolve(cfg, cal)`
  fills every `derived` value; `Fingerprint`, `Replay(c, out)`.
- `Calibration`: `LoadCalibrationFile`, `LoadCalibration(bytes)`, `FindCalibration(dir)`; fields
  `ID`, `HostCores`, `Costs`, `Targets`, `CM`.
- Connect model: `CMParams`, `NewCPUModel`, `Predict(p, FitTarget)`, `FitConnectModel`, `ErrPPM`.
- Run directory: `OpenSidecars(dir, cfg) (*Sidecars, error)` → `Records()`, `Counts()`, `Close()`;
  `LabRows(cfg, tier, cal, RunFacts)`, `WriteLab(dir, rows)`; `PoolMetrics()`.
- `RunSim(cfg, cal, out) (*RunResult, error)` — one simulated run.
- `cmd/mglease`: `Register(Command{Name, Usage, Run func(args []string, stdout, stderr io.Writer) int})`
  from `init` in your own file; exit codes `ExitOK` 0, `ExitViolation` 1, `ExitUsage` 2.
  `splitRunArgs` (takes `out=` and `calib=`) and `loadCalibration` are reusable in package main.

## 3. Run directory (`out=<dir>`)

`<dir>`'s last element is the run id (`[A-Za-z0-9._=-]`); the flushed CSV files go to
`<dir>` itself (`csvflush` with `Dir = parent`, `RunID = base`). Files:

| File | Written | Schema |
| --- | --- | --- |
| `metrics.csv`, `histograms.csv`, `run.csv` | `csvflush`, unchanged, schema version 1 | `docs/csv-schema.md` |
| `lab.csv` | last, after the quiesce checks | below; versioned by its row `sidecar.version` |
| `oracle.csv` | during the run (one row per inspected lease, as it is inspected) | below |
| `leases.csv` | only with `obs.leases_csv=on`; at most `obs.leases_cap` rows, the rest counted as dropped | below |

`run.csv` (one row): scenario `lease-lab`, tier `S`, seed = `seed`, arm = `run.arm` if it is a
valid id element else `base`, instances 1, build `lab-sim`, config_fingerprint
`lab-<16 hex of sha256 over the resolved key=value rows>`. Example:
`1,ex,lease-lab,S,1,base,1,lab-sim,lab-391e8264eeb87cc0,4,2100000000`.
Flushes: at 0, every `obs.interval` (default 1 s, virtual time), and once after the drain.
Sources registered: `pool` (this piece), `sim`, `simnet`, `fakepg` (`internal/obs/simsrc`).

Example rows (`metrics.csv`, `histograms.csv`):
```
1,ex,0,0,sim-1,fakepg,bad_hellos,counter,,,,,0
1,ex,1,1000000000,sim-1,pool,lease_wait_ns,,miss_connect,,,ns,18350080,18481152,3
```

### 3.1 `lab.csv` — columns `key,value` (both text; values are integers where the key says `_ns`, `_ppm`, `_milli`, or a count)

Rows, in this order: `sidecar.version` (1), `tier` (`sim`; E5 writes `real`), `calibration.id`,
then **every config key of section 5 with its resolved value** (no `derived` left), then:

| Key | Meaning |
| --- | --- |
| `cm.model` | `fcfs-c-server` |
| `cm.host_cores` | host CPUs read from the calibration (busy CPUs at the sustained plateau, rounded) |
| `cm.fit.cores`, `cm.fit.work_ns`, `cm.fit.floor_ns`, `cm.fit.close_work_ns` | the fitted model (the run's own values are the `cm.*` config keys) |
| `cm.fit.worst_err_ppm` | the fit's largest relative error over the calibration cells, ppm |
| `cost.reset_after_work_ns`, `cost.short_stmt_ns`, `cost.fresh_penalty_ns.<class>`, `cost.reset_penalty_ns.<class>` | section 6 |
| `run.duration_ns` | arrival window |
| `run.end_ns` | virtual time at the final flush (arrivals + drain) |
| `requests.intended_milli` | expected leases from the rate and duration, milli-units |
| `requests.generated` | leases the generator produced |
| `requests.achieved` | leases the pool core saw (`leases_requested`) |
| `oracle.rows`, `leases_csv.rows`, `leases_csv.dropped` | sidecar row counts |
| `run.result` | `ok` or `violated` |
| `run.violations` | number of violated checks |
| `run.replay` | the REPLAY line **without** `out=` (append `out=<dir>`), so lab.csv does not depend on the path |

Example: `sidecar.version,1` · `tier,sim` · `release.policy,none` · `cm.fit.cores,3` ·
`requests.achieved,4801` · `run.result,ok`.

### 3.2 `oracle.csv` — fixed header (`OracleHeader`)

| # | Column | Type | Meaning |
| --- | --- | --- | --- |
| 1 | `lease` | int | lease id |
| 2 | `tenant` | int | tenant id |
| 3 | `prev_tenant` | int | tenant of the previous lease on this connection, -1 if none |
| 4 | `conn` | int | pool connection id |
| 5 | `backend_pid` | int | BackendKeyData pid (fake: from 10,000) |
| 6 | `backend_start_ns` | int | backend start time; 0 in the simulator |
| 7 | `prev_taint` | text | taint kind of the previous lease's script, `""` if none |
| 8 | `taint` | text | taint kind of this lease's script |
| 9–15 | `prepared`, `cursors`, `listens`, `advisory_locks`, `settings_changed`, `temp_relations`, `role_changed` | int | dirty-check categories; -1 = not checked |
| 16 | `reset_between` | int | 1: a completed reset reached the database since the previous lease; 0: none; -1: unknown |
| 17 | `canary_kind` | text | kind of state the canary tried to read |
| 18 | `canary_read` | int | 1: the previous tenant's secret was read; 0: not; -1: not tried |
| 19 | `probe_seed_inherited` | int | residue probe: random sequence inherited (1/0/-1) |
| 20 | `probe_custom_name` | int | residue probe: custom setting name visible (1/0/-1) |
| 21 | `note` | text | free |

Example (baseline `none`, marker script): `6,5,4,3,10003,0,set,set,-1,-1,-1,-1,1,-1,-1,0,marker,1,-1,1,marker=t4`.
`NotChecked()` gives a row with every inspector column at -1.

The built-in simulator inspector runs `SHOW lab.marker` and fills `settings_changed` and
`probe_custom_name` (1 if the setting exists, 0 on 42704), `canary_kind = marker`,
`canary_read` (1 when the value is the previous, different tenant's marker), `note =
marker=<value>`, and `reset_between` from the fake's own statement records (section 7, item 8).

### 3.3 `leases.csv` — fixed header (`LeasesHeader`)

`lease, tenant, intended_ns, granted_ns, released_ns` (int; -1 when it did not happen),
`outcome` (`reuse`, `warm_hit`, `miss_connect`, `miss_slot`, or `refused`), `switch` (`none`,
`warm`, `connect`, `reset`, `ready`; empty when refused), `conn`, `prev_tenant` (int, -1 none),
`status` (`I`, `T`, `E`; empty when refused). One row per lease at release or refusal. Example:
`4,4,945608,20578637,23162898,miss_slot,ready,1,2,I`.

## 4. Metrics (component `pool`; labels only outcome / reason / mode)

Every declared counter and gauge has a row in every flush; labelled ones have a row for every
label value. Histograms: `obs.NewDurationHistogram` (1 µs – 60 s); values under 1 µs (e.g.
`switch_ready_ns` for modes without a wait) land in `<name>_clamped_low`.

| Metric | Kind | Labels / values |
| --- | --- | --- |
| `leases_requested`, `leases_refused`, `leases_goodput` | counter | — (goodput = every statement ok and released in `I`) |
| `leases_granted` | counter | outcome: reuse, warm_hit, miss_connect, miss_slot |
| `leases_released` | counter | outcome: clean, in_tx, failed_tx, client_gone, db_lost |
| `leases_switch` | counter | mode: none, warm, connect, reset, ready |
| `miss_connect_cause` | counter | reason: no_stock (no warm connection and a slot free, at request), no_slot (no slot free at request), other (stock present at request, taken by an earlier waiter) |
| `switch_arrivals` | counter | — ; repair round: leases that needed a fresh connection, each counted once (took a never-lent connection, or started to count toward the deficit); the rule's λ̂ input |
| `stock_empty_arrivals` | counter | — ; of those, found no warm connection ready |
| `stock_empty_free_slot` | counter | — ; of those, while a slot was free or a replacement was under way: the warm-stock model's miss (report table 6: miss rate = this / `switch_arrivals`) |
| `patience_waits`, `patience_expired` | counter | — ; `remake-on-switch`: leases that waited for their own tenant's connection; of those, whose patience ran out |
| `release_actions` | counter | mode: idle, reset, close |
| `resets` | counter | outcome: started, ok, failed, timeout |
| `reseeds`, `primes` | counter | outcome: started, ok |
| `connects` | counter | outcome: started, ok, refused, timeout, auth, other |
| `closes` | counter | reason: remake, switch, shrink, in_tx, failed_tx, client_gone, bad_status, reset_failed, reset_timeout, reseed_failed, reseed_timeout, prime_failed, prime_timeout, db_lost |
| `closes_done`, `stale_events`, `db_lost` | counter | — |
| `warm_target_changes` | counter | reason: grow, shrink, clamp |
| `oracle_checks` | counter | outcome: clean, dirty |
| `canary_reads` | counter | outcome: clean, leak |
| `lent_ns_total`, `warm_ns_total`, `connecting_ns_total`, `priming_ns_total`, `resetting_ns_total`, `closing_ns_total`, `idle_ns_total`, `conn_ns_total` | counter (ns) | — ; the first seven sum to `conn_ns_total` |
| `open` (includes closing), `lent`, `connecting`, `priming`, `warm`, `idle`, `resetting`, `closing`, `waiters`, `pending`, `warm_target`, `lambda_hat_milli`, `lead_hat_ns` | gauge | — |
| `open_connecting_hw`, `waiters_hw`, `db_backends_hw` | high-water gauge | — |
| `db_backends` | gauge | server-side backends now (fake: counted sessions) |
| `cpu_running`, `cpu_queued` | gauge | connect model jobs |
| `lease_wait_ns` | histogram | outcome; intended start → lend |
| `switch_ready_ns` | histogram | mode; match → lend (request → lend for `connect`) |
| `reset_ns`, `connect_ns`, `close_lag_ns` | histogram | — ; DISCARD ALL sent → reply; open → connect done (ok only); close issued → close done |
| `first_stmt_ns` | histogram | mode: fresh (first lease on the connection), reused, after_reset; send → reply of the lease's first statement |
| `lease_total_ns` | histogram | — ; intended start → release, inspection excluded |

Conservation as built (and as `test/analysis/lease/checks/conservation.sql` asserts it in every
flush): `leases_requested = Σ leases_granted + waiters + pending + leases_refused`;
`Σ leases_granted = Σ leases_released + lent`; `connects{ok} = closes_done + open`;
`Σ *_ns_total (seven states) = conn_ns_total`. `CheckConservation` checks the same on the core's
snapshot plus `connects started = ok + failed + connecting`.

The registry: `Collector.Register` keeps a per-collector registry, so the lab's declarations under
`pool` are accepted with no change to `internal/obs` or its catalog. The catalog
(`docs/csv-schema.md`) does not list them; that is lab-only by design.

## 5. Configuration keys (lab.csv order; `derived` = filled by `Resolve`)

| Key | Default | Plan | Notes |
| --- | --- | --- | --- |
| `release.policy` | required | K1 | none, reset-always, reset-on-switch, remake-always, remake-on-switch |
| `reset.reseed` | off | K2 | |
| `reset.timeout` | 1s | K3 | also bounds prime and reseed (core) |
| `warm.mode` | required | K4 | cold, fixed, dynamic |
| `warm.size` | 4 | K5 | W / W_min |
| `warm.epsilon` | 1% | K7 | 5%, 1%, 0.1% |
| `warm.ewma_fast`, `warm.ewma_slow` | 1s, 10s | K8 | |
| `warm.lead` | p95 | K9 | p95, mean |
| `warm.initial_lead` | 21ms | — | L̂ before the first connect |
| `warm.prime` | none | K11 | none, select1 |
| `remake.order` | close-then-open | K12 | |
| `remake.patience` | auto | — | repair round; `remake-on-switch` only: `auto` (= L̂), `0` (none: the plan's literal rule, the thrash), or a duration |
| `pool.cap` | 20 | K13 | |
| `pool.wait_cap` | 1000 | — | E1 review item 5; keep modest in overload runs |
| `pool.connect_retry` | 100ms | — | core's back-off after a failed connect |
| `connect.max_concurrent` | 4 | K14 | |
| `connect.timeout` | 2s | — | shell's connect deadline |
| `affinity` | on | K15 | |
| `workload.profile` | steady | K16 | steady, skewed, bursty, step, faulty, or a registered profile |
| `workload.tenants` | 100 | K16 | 1 … 1,000,000 |
| `workload.skew` | 1100 | K16 | Zipf exponent × 1000 (> 1000), skewed only |
| `workload.lease` | medium | K16 | short (exp., mean 0.5 ms), medium (exp., 5 ms), heavy (Pareto shape 2, mean 5 ms, cut at 5 s) |
| `workload.load` | 60 | K16 | percent of cap / mean lease time; decimals allowed (`95.5`) |
| `workload.step_factor` | 4 | K16 | step: middle third at load × factor |
| `workload.burst_period`, `workload.burst_active` | 1s, 100 | K16 | bursty: period; active tenants per mille of `tenants` (≥ 1) |
| `workload.script` | plain | K16 | ScriptSource name |
| `workload.stmt_class` | pk_lookup | — | first-statement penalty class of lease statements: select1, pk_lookup, plpgsql, off |
| `run.duration` | 10s | — | arrival window |
| `run.max_requests` | 5,000,000 | — | the run fails if the generator wants more |
| `obs.interval` | 1s | — | flush period |
| `obs.leases_csv`, `obs.leases_cap` | off, 100000 | — | |
| `db.kind` | sim | K17 | `real` is E5's; `mglease sim` refuses it |
| `db.max_connections` | 600 | K17 | the fake's limit (calibration server's value) |
| `db.net_one_way` | derived | — | simnet one-way latency |
| `db.close_lag` | derived | — | close → backend gone |
| `db.reseed_sql` | `SELECT setseed(0)` | K2 | must return one row, tag `SELECT 1` |
| `cm.cores`, `cm.work`, `cm.floor`, `cm.close_work` | derived | — | connect model (section 6) |
| `cm.avail_pm` | 1000 | — | "CPU already busy": per mille of each core left to connects (E7) |
| `fault.restart_at`/`_for`, `fault.stall_at`/`_for`, `fault.refuse_at`/`_for`, `fault.slow_at`/`_for` | derived | — | faulty: at 20/40/60/80% of the run, for min(1 s / 3 s / 1 s / 2 s, duration/10); other profiles 0 (off) unless set |
| `fault.slow_factor_pm` | 5000 | — | connect CPU work × 5 while slow |
| `oracle.rate` | 0 | K18 | fraction 0–1 (written with 6 decimals) |
| `oracle.inspector` | builtin | — | |
| `seed` | 1 | K19 | one seed fixes everything |
| `run.campaign`, `run.test`, `run.arm`, `run.rep` | "" | — | free labels, recorded only |

Unknown keys, repeated keys (also across a config file and the command line), values with a
comma, quote or newline, and missing `release.policy` / `warm.mode` are errors. Runner-only
arguments: `out=<dir>` (required), `calib=<file>` (default: `calibration.json` found upwards).

## 6. Simulator shell: what is modelled, exactly

### 6.1 Shared-CPU connect model (Nick's ruling) — the fit

A connect spends `floor` off the CPU, then needs one of `cores` servers for `work` (first come,
first served); each close puts `close_work` of CPU (the backend's exit) in the same queue,
holding no pool slot. Jobs run at `cm.avail_pm`/1000 of a core's speed. In the shell the
connect is: dial (the fake counts the backend from accept) → floor → CPU → login through
`pgwire.BackendSession` (trust, no TLS: the calibrated TLS + SCRAM time is inside the model).

**Derivation, by code** (`FitConnectModel`, run when the calibration is loaded, 0.3 s): the
targets are read from `calibration.json` (bridge path; M3 `connect ns` p50 at c = 1/4/16/64;
M7S and M7B `rate_milli_per_s_uncontended` p50, falling back to all cells); the host's cores
from the uncontended sustained cells (`host_busy_us / wall_ns`, max, rounded: 4). For every core
count 1…4 a grid in 1 ms steps (work 4–30 ms, floor 0–10, close work 0–8), then 0.25 ms steps
around the best, minimising the **largest** relative error over all 13 cells. The model is
simulated per cell exactly as measured: M3 = c connects released at once from idle, p50 of the
per-connect times; M7S = c workers in a closed loop (connect, close, reconnect), 0.5 s warm-up,
2 s window; M7B = B released at once, B / time of the last.

Result: **cores 3, work 13.75 ms, floor 4.5 ms, close work 3.5 ms** (`TestConnectModelFit`):

| Cell | Predicted | Measured | Error |
| --- | --- | --- | --- |
| M3 per-connect p50, 1 at once | 18.25 ms | 16.02 ms | +13.8% |
| M3, 4 at once | 18.25 ms | 21.35 ms | −14.5% |
| M3, 16 at once | 45.75 ms | 53.44 ms | −14.3% |
| M3, 64 at once | 155.75 ms | 153.45 ms | +1.4% |
| M7S sustained, 1 worker | 54.5 /s | 60.0 /s | −9.1% |
| M7S, 2 workers | 109.0 /s | 103.0 /s | +5.8% |
| M7S, 4 workers | 174.0 /s | 152.65 /s | +13.9% |
| M7S, 8 workers | 174.5 /s | 181.6 /s | −3.9% |
| M7S, 16 workers | 173.5 /s | 195.35 /s | −11.1% |
| M7S, 32 workers | 174.5 /s | 197.25 /s | −11.5% |
| M7S, 64 workers | 174.0 /s | 192.65 /s | −9.6% |
| M7B burst of 100 | 211.9 /s | 246.7 /s | −14.1% |
| M7B burst of 500 | 217.3 /s | 233.6 /s | −6.9% |

Band: ±20% on every cell (`FitBandPPM`); all 13 cells are inside it (worst 14.5%). The brief's
list (M3 ×4, M7S at 1/4/8/16/32/64, M7B ×2) is covered; M7S at 2 workers is included as well.

**Why a c-server queue, not processor sharing.** `TestConnectModelPSFitsWorse` fits a
processor-sharing model (each connect in the CPU phase runs at min(1, cores/n)) over a 2 ms grid:
best worst-cell error **40.3%** (cores 3, work 10, floor 2, close work 8 ms: M3 at 64 at once
+40%, M7S at 1 worker +39%, burst +28%), against 14.5% for the queue. Processor sharing finishes a
batch all at once, so per-connect time at 64 at once is far too long while bursts run too fast;
the measured per-connect times spread like a queue's.

**What it gets wrong, stated.** The sustained plateau is 174/s against a measured 195/s (−11%),
the single connect 18.25 ms against 16.0 ms (+13.8%), and the slowdown at 4 at once is too small
(18.25 against 21.35 ms): a work-conserving queue with 3 free cores cannot slow 4 connects that
fit. The 3 fitted cores are the 4 vCPUs minus the client's share (the client held about 1.1 CPUs
at the plateau), and work 13.75 ms is close to the measured Postgres CPU per connect-and-close
(13.4–15.6 ms) — but these are fitted numbers, not measured ones. A model with fewer than 3 cores
or a different shape would trade these cells against each other; the fit picks the minimax.

`cm.avail_pm` scales every job's speed: at 500 the ceiling halves (`TestConnectModelAvailability`).
`fault.slow_*` multiplies connect work (not close work) while on. A connect whose deadline
passes while queued leaves the queue; one already on a core runs on (the server keeps working).
The model holds at most `MaxCPUJobs` = 65,536 jobs; a connect beyond that fails as `other`,
close work beyond it is dropped.

### 6.2 Other costs (all derived from `calibration.json` by `LoadCalibration`)

| Cost | Value | Source | How it is charged |
| --- | --- | --- | --- |
| Network one-way | 43.8 µs | half of M1 `discard rt` p50 (87.7 µs) | simnet latency each way, no jitter: every statement pays one round trip |
| Reset (clean) | 87.7 µs total | M1 discard | the round trip alone (by construction) |
| Reset after a lease | +89.3 µs | M2I `none discard` p50 − M1 discard | delay before the DISCARD ALL is written, when a lease statement ran since the last reset/connect |
| Other short statements (SELECT 1, SET, SHOW, reseed, inspector's) | +21.0 µs | M1 select1 − M1 discard | delay before writing; not for `pg_sleep` |
| Lease service time | drawn | generator | `SELECT pg_sleep(t)`: the fake sleeps t |
| The short first statement | round trip + 21.0 µs | M1 | as "other short statements"; the fake has no `lab_kv`/`lab_fn`, so the shell sends `/* <the statement> */ SELECT 0` for those two (one row, tag `SELECT 1`) |
| First statement on a fresh backend | +0.52 / 1.57 / 4.18 ms | M5/M6 fresh − reused, for select1 / pk_lookup / plpgsql | added to the first **lease** statement after connect (with `plain`: the short statement); class = `workload.stmt_class` |
| First statement after a reset | +10 / 7 / 393 µs | M5/M6 after_reset − reused | added to the first lease statement after a reset |
| Prime (`warm.prime=select1`) | 21 µs + 0.52 ms | | the prime pays the select1 fresh penalty and **removes that much** from the pending fresh penalty: pk_lookup keeps 1.05 ms, plpgsql 3.65 ms, select1 0. Calibration measured each class alone on a fresh backend, never "SELECT 1 then X"; subtracting is my assumption and the most a SELECT 1 can remove |
| Close lag | 816 µs | M4 poll `close_to_gone` p50 (upper bound) | Terminate + FIN at close; `close done` (slot free) after the lag |
| Close CPU | 3.5 ms | fitted (above) | queued on the connect model's cores |

Not modelled: the 3 ms reset after a temp table (M2I temp kinds), the slower reset on a backend
that once held a temp table (M2H), resets getting cheaper over consecutive resets (after1–5),
jitter of any cost, and the CPU of lease statements (they do not compete with connects; use
`cm.avail_pm` for a busy server). The fake models only `SET`/`RESET`/`SHOW`/`DISCARD ALL` state.
The reseed's `SELECT setseed(...)` is unknown to the fake: the shell sends
`/* <the reseed SQL> */ SELECT 0` instead (one row, tag `SELECT 1`, the original text in the
fake's statement tag).

### 6.3 Faults (`faulty`, via `sim.InjectSchedule`)

| Fault | Mechanism | Effect |
| --- | --- | --- |
| Database restart | `fakepg.FaultRestart` (Arg2 = duration) | every backend reset; connects during the window get FATAL 57P03 → `connects{refused}`; the engine reports one `db_lost` |
| Connect stall | `fakepg.FaultConnectStall` (→ simnet dial stall), cleared by `fakepg.FaultClear` | dials get no answer; `connect.timeout` fails them as `timeout` |
| Connect refused | `fakepg.FaultRefuse` (→ simnet listener refuse), cleared | `connects{refused}` |
| Slow connects | the lab's own kind `FaultSlowConnect` = 112 (`lab.slow-connect`), Arg1 = factor per mille, 1000 clears | connect CPU work × factor |

S11: `db_backends` / `db_backends_hw` are the fake's counted sessions (`fakepg` `Sessions`
gauge and peak); `db.max_connections` sets the fake's limit (beyond it: FATAL 53300 → refused).

## 7. Decisions the brief left open

1. **Load** = `workload.load` % × `pool.cap` / mean lease time, the mean of the distribution
   (0.5 ms short, 5 ms medium and heavy), as the brief says; per-lease overheads (round trips,
   resets) are not in it, so 100% is already over a reset policy's capacity. Since the repair
   round each `plain` lease also holds its connection for the short first statement: one more
   round trip + 21 µs (about 109 µs; +1.57 ms on a fresh backend for `pk_lookup`). The load
   definition is unchanged; at medium leases that is about 2% more connection time per lease
   (about 22% at short leases).
2. **step**: `workload.load` is the base level; the middle third of the run is at load ×
   `workload.step_factor` (4). **bursty**: every `burst_period` a new random set of
   `burst_active`‰ of the tenants is active; at the period's first instant each active tenant
   issues one lease (they arrive together), then Poisson arrivals over the active set at the base
   rate less the batch's share, so the mean rate stays the base rate. **skewed**: Zipf
   `P(k) ∝ k^-s` over tenants 1…N (`randdist.NewZipf`, v = 1). **faulty** = steady + section 6.3.
3. **Lease times**: exponential via `randdist.ExpGap` (capped at 1000× the mean by randdist);
   heavy = Pareto with shape 2 (finite mean, infinite variance), `x = (mean/2) / sqrt(U)` in
   integers, cut at 1000× the mean (5 s).
4. **`lease_wait_ns` is measured from the intended start**, not from the core's `RequestedAt`
   (the E1 review's wording). In the simulator they are the same instant; in a wall-clock shell
   the difference is the shell's dispatch lag, which open-loop measurement must include.
5. **Any connection loss is "database lost"** for the core (`EvDBLost`), once per loss: a loss is
   covered (not reported again) when the connection was already open at the last report. The core
   has no event for one connection dying; reporting it as a database loss closes every idle and
   warm connection, which is right for a restart and conservative for a single killed backend.
6. **Inspector runs before the script** on the same connection; its statements cost a round
   trip + 21 µs each and do not consume the first-statement penalty, so the lease's own first
   statement still shows it. Timing runs use `oracle.rate=0`.
7. **Quiesce**: after `run.duration` the run continues (ticks, no arrivals) until the engine is
   settled — no lease in flight, nothing lent, waiting, pending, connecting, priming, resetting or
   closing — or 60 s (`DrainNs`). Idle and warm connections are not leaks: at that point the fake
   must hold exactly `open` backends. After the final flush the shell closes them outside the
   core, runs the simulator until quiet and runs every registered quiesce check (simnet, fakepg,
   no pending timers). Anything else is a violation.
8. **Structural reset check in the simulator**: the shell installs the kernel's trace tap and
   counts, per backend, the fake's own `pg.done` records of DISCARD ALL with no error code (the
   same records the statement log keeps; the logs themselves are set to count-only to bound
   memory). `reset_between` = such a DISCARD ALL after the reply to the previous lease's last
   statement on that backend.
9. **first_stmt_ns modes**: `fresh` = the first lease on a connection (primed or not; priming
   reduces the penalty, it does not change the mode), `after_reset` = the first lease after a
   reset that succeeded, `reused` otherwise. It times the lease's first statement, which with
   `plain` is the short statement (repair round), not the hold.
10. **miss_connect cause**: from the core's snapshot taken just before each request (no core
    change): `no_stock` = no warm connection while a slot was free; `no_slot` = open + connecting
    at the cap; `other` = stock existed but an earlier waiter took it. The warm-stock miss rate is
    `miss_connect_cause{no_stock} / (warm_hit + miss_connect)` restricted as the E1 review says.
11. **Run id** = the last element of `out`; the REPLAY line printed by `mglease sim` includes
    `out=` (and `calib=` when given); lab.csv's `run.replay` omits `out=` so U5 can compare runs
    in different directories byte for byte.
12. **Simulated login**: trust auth, no TLS (costs are in the model). User and database `lab`.
13. **Histograms** use the obs duration range (1 µs – 60 s); values below go to `_clamped_low`.
14. **Generator bound**: `run.max_requests` (5 M); beyond it the run stops generating and
    reports a violation.
15. **WaitCap**: default 1,000, as E1's default; it is K-less, so it is a key and in lab.csv.

## 8. Claims and the tests that prove them

All in `internal/lab/leaselab` unless marked.

| Claim (brief) | Test |
| --- | --- |
| **U5**: same seed and config twice → byte-identical run directory, every file (`metrics.csv`, `histograms.csv`, `run.csv`, `lab.csv`, `oracle.csv`, `leases.csv`); another seed differs (`metrics.csv`, `leases.csv`) — for all five policies, fixed and dynamic warm modes, every profile (steady, skewed, bursty, step, faulty), affinity off, short/heavy leases, reseed and prime on | `u5_test.go` `TestU5SameSeedSameBytes` (8 configurations × 3 runs) |
| Replay line reproduces the run byte for byte through the runner | `cmd/mglease/main_test.go` `TestSimReplayReproduces` |
| Connect model held to calibration, ±20%, table printed | `fit_test.go` `TestConnectModelFit` |
| Processor sharing fits worse (model choice) | `TestConnectModelPSFitsWorse` |
| "CPU already busy" knob scales the ceiling | `TestConnectModelAvailability` |
| Generator: achieved against intended (±3%) and mean lease time (±5%, heavy ±15%) for every profile × distribution over 60 s | `gen_test.go` `TestGeneratorAchievedRate` |
| Step shape (1× / 4× / 1×) | `TestGeneratorStepShape` |
| Zipf shape for 10, 100, 10,000 tenants: f(1)/f(k) = k^1.1 within 10% for k = 2…4, counts fall with rank | `TestGeneratorZipfShape` |
| Bursty: a period's tenants arrive at one instant | `TestGeneratorBurstyArrivesTogether` |
| Open loop: a pool of 1 at 300% load gets every lease at the instant the generator alone produces; waits grow past 1 s instead | `TestGeneratorOpenLoop` |
| A reset reaches the fake as DISCARD ALL (fake's count = resets ok = started), and a `SET` marker is gone for the next lease under `reset-always`, still there and read by another tenant under `none` | `engine_test.go` `TestResetReachesFakeAndClearsMarker` |
| reset-on-switch resets before every switch; remake policies never put two tenants (remake-always: two leases) on a connection | `TestPoliciesStructurally` |
| Only `DISCARD ALL` + `I` makes a connection lendable, through the seam (7 bad replies: error, error in E, T, E, other tag, empty tag, lost) | `TestEngineResetReply` (scripted DB, no simulator) |
| Conservation in every flush, by SQL in DuckDB through `scripts/csv-assert.sh`, on runs of four policies including a faulty one; a planted break is found | `duckdb_test.go` `TestConservationInDuckDB` |
| Quiesce: nothing lent, queued, connecting, resetting, closing; the fake holds exactly the open connections, then none; no CPU jobs; world quiesce checks pass (overload, dynamic, two faulty runs) | `TestQuiesce` |
| The quiesce check can fail (database refusing past the drain) | `TestQuiesceCheckFails`; runner exit 1: `cmd/mglease` `TestExitCodes` |
| **Canary 1**: the inspector told the wrong previous tenant → the baseline leak check finds nothing (the check fails) | `TestCanaryWrongPrevTenant` |
| **Canary 2**: the reset reply not checked → every bad reply (6 of 6 that reach the check) leads to a lend | `TestCanaryResetUnchecked` |
| Config: errors (unknown, repeated, required, bad values), round trip through lab.csv rows, config files, durations | `config_test.go` |
| Core early exit keeps behaviour: U1–U4 green; a faulty overload run's files byte-identical before and after | `internal/lab/leasepool` (all), and by hand (87 s → 17 s for that run) |
| Repair round A1–A3, B4, B6 | see section 13 |

Totals (after the repair round): 28 top-level tests in `leaselab` and `cmd/mglease` (with
subtests 51); `go test` of both about 60 s.

## 9. Deviations

- **Size**: about 3,300 lines of code in `leaselab` + 150 in `cmd/mglease` + 36 lines of SQL,
  and 1,000 lines of tests, against the 700 / 250 estimate. The config table, the simulator
  shell's wire handling and the documentation-carrying seam file are most of it.
- **Core change** (allowed by the brief): `pump`'s early exit. No accessor was needed.
- `lease_wait_ns` from the intended start (decision 4), not from `RequestedAt` — equal in the
  simulator.
- The reseed statement is rewritten in the simulator only (6.2).
- Commit trailer: the rules file's `Co-Authored-By: Claude <noreply@anthropic.com>`.

**Change request: none.** Nothing frozen was touched.

## 10. Limits

- The connect model misses the cells listed in 6.1 by up to 14.5%; the plateau is low by 11%.
- One network latency, no jitter; one close lag (an upper bound); costs are p50s, never tails.
- The CPU model sees connects and backend exits only; lease work does not slow connects.
- The fake has no state beyond settings: the built-in inspector can only see the marker and the
  structural reset; every other oracle column is -1 in the simulator (E4 / tier R).
- Fault defaults are short in short runs (each window at most a tenth of the run).
- Speed: about 10–15 thousand leases per wall second; a run whose wait list is full most of the
  time is slower (17 s for 240,000 leases at 120% with a 1,000 wait list and a restart).
- R1's lead time is the connect alone (E1 review item 2), unchanged and recorded here.
- Determinism holds for the same commit, toolchain and linux/amd64 (randdist's floats).

## 11. For the parallel pieces

- **E3**: run with `RunSim(cfg, cal, out)` or `mglease sim`; load the calibration once per
  process (`LoadCalibrationFile` fits the model, 0.3 s). Add `mglease sweep` as `cmd/mglease/sweep.go`
  with `Register` in `init`. The miss-rate split is `miss_connect_cause`. S11 reads `db_backends_hw`.
- **E4**: `RegisterScriptSource("hostile", …)`, `RegisterScriptProfile("hostile", "steady",
  "hostile")`, `RegisterInspector(name, …)` from `init` in your own files of package `leaselab`;
  fill `OracleRow` (leave engine-filled columns alone). Your statements must be single Query
  messages; multi-statement strings are allowed by the protocol but `Result` keeps only the last
  tag and status. Client gone: `Script.Abandon`/`AbandonAfter` (section 2.3). LISTEN/NOTIFY:
  `Result.Notifications` (section 2.1) — in the simulator always empty.
- **E5**: run the same two-statement `plain` lease (section 2.3; create `lab_kv`/`lab_fn`), fill
  `Result.Notifications` from `NotificationResponse` messages, and never call the Sink from
  inside a DB call (REVIEW-E2-1 finding 4: `SimDB` does on a write error; do not copy it).
- **E5**: implement `DB` with wall-clock I/O in your own file(s) and pump the engine from one
  goroutine (it is not safe for concurrent use; every Sink call and `Request`/`Tick` from that
  loop). Reuse `Parse` (`db.kind=real`), `NewGenerator`, `NewEngine`, `Engine.Source`,
  `OpenSidecars`, `LabRows(…, "real", …)`, `WriteLab`, `CheckConservation`. Register
  `mglease real` / `campaign` in your own files.
- **E6**: SQL that `scripts/csv-assert.sh` runs in its root mode must not sit directly in
  `test/analysis/lease/` (root mode would take `lease` for a scenario with no runs and fail, which
  also breaks `internal/obs/simrun`'s tests); use a subdirectory as `checks/` does. Join
  `lab.csv` by run directory; numbers in it are text in the CSV — cast.

## 12. Commands

```sh
export PATH=$PATH:/usr/local/go/bin
cd /root/Software/mini-gateway-work/lease-lab
go test ./internal/lab/... ./cmd/mglease/... -count=1            # all, about 60 s
go test ./internal/lab/leaselab -run U5 -v                        # U5
go test ./internal/lab/leaselab -run 'ConnectModel' -v            # the fit table and the PS comparison
go test ./internal/lab/leaselab -run 'Generator' -v
go test ./internal/lab/leaselab -run 'Reset|Polic|Engine|Canary|Quiesce|Conservation' -v
go run ./cmd/mglease sim release.policy=reset-on-switch warm.mode=cold run.duration=5s out=/tmp/lease-run/r1
scripts/csv-assert.sh /tmp/lease-run test/analysis/lease/checks/conservation.sql
```

## 13. Repair round (REVIEW-E2-1 findings 1–3 and C1; brief `zgateway-lease-e12-repair.md`)

Pool-core part (B1–B5): NOTES-E1.md §8. Driver part:

| Item | Commit | What | Test |
| --- | --- | --- | --- |
| **A1** client gone reachable | `d636fde` | `Script.Abandon` / `AbandonAfter` (section 2.3); engine `abandons` | `repair_test.go` `TestClientGoneEachPolicy` (scripted DB; every policy × n = 0, 1, 2: only n statements sent, released client-gone with the server's status, `closes{client_gone}` = 1, not goodput, the next lease opens a new connection and the abandoned one runs nothing more); `TestClientGoneSim` (simulator, every policy, every fifth lease abandoned after 0/1/2 statements: `closes{client_gone}` = `leases_released{client_gone}` = abandoned, and from `leases.csv` no lease is ever lent on such a connection afterwards) |
| **A2** `first_stmt_ns` times a short first statement | `d636fde` | `plain` = short statement of `workload.stmt_class`, then the hold (section 2.3); the simulator charges it the round trip + 21 µs + the P5 penalty | `TestDefaultScriptFirstStatement`: the script per class; and per mode, p50 and p99 of `first_stmt_ns` within 2% of round trip + costs: fresh pk_lookup 1,687 µs (model 1,681), fresh plpgsql 4,292 µs (4,285), reused 109 µs (108), after_reset 115 µs (115) — no longer the 5 ms lease time |
| **A3** notifications in `Result` | `d636fde` | `Result.Notifications`, `Notification{PID, Channel, Payload}` (section 2.1); the simulator parses `A` messages | `TestResultNotifications` (two in order, a malformed one dropped, one between statements kept for the next Result) |
| **B4** counters exported | `9fabab8` | `switch_arrivals`, `stock_empty_arrivals`, `stock_empty_free_slot`, `patience_waits`, `patience_expired` (section 4) | flushed in every run (the C table below is read from them) |
| **B6** `remake.patience` | `9fabab8` | key `remake.patience=auto\|0\|<duration>`, default `auto`, in `lab.csv` after `remake.order` | `config_test.go` `TestConfigPatience` (default, values, round trip through the lab.csv rows), errors in `TestConfigErrors` |

**Golden values changed:** only `engine_test.go`'s scripted-database expectations
(`resetLendsBadly`): a lease now sends `SELECT 1` and then the sleep, so the test answers both.
No stored output file exists; U5 compares runs with each other and stays green (byte-identical
replays, every file). Every run's numbers change, since every lease now has two statements.

### C. The C1 table, re-run

Config as REVIEW-E2-1 C1: `workload.profile=skewed workload.tenants=10 pool.cap=20
workload.lease=medium seed=1 run.duration=20s`, `warm.size=4`; baseline =
`release.policy=none warm.mode=fixed`; the rest `release.policy=remake-on-switch` with
`warm.mode=fixed` (`fixed:4`) or `dynamic`, `remake.patience=0` or `auto`. Same leases as the
review (23,963 / 47,751 / 63,867 requested). Connects/s = `connects{started}` / 20 s. Waits are
`lease_wait_ns` over every outcome, quantiles as the upper edge of the histogram bucket (about
0.7% wide); `≤ 1 µs` = in the clamped lowest bucket. No lease was refused; goodput = requested
in every run. Run ids: `base`, `ros-<warm>-p<patience>`, `-<load %>`.

|         run          | connects/s | reuse | warm_hit | miss_connect | miss_slot | p50_wait | p99_wait | goodput | requested | closes_switch | switch_arr | stock_empty | empty_free | pat_waits | pat_expired |
|----------------------|------------|------:|---------:|-------------:|----------:|----------|----------|--------:|----------:|--------------:|-----------:|------------:|-----------:|----------:|------------:|
| base-30              | 1          | 23871 | 7        | 13           | 72        | ≤ 1 µs   | ≤ 1 µs   | 23963   | 23963     | 0             | 92         | 85          | 85         | 0         | 0           |
| ros-dynamic-p0-30    | 172        | 16428 | 1379     | 2062         | 4094      | ≤ 1 µs   | 13.6 ms  | 23963   | 23963     | 3425          | 7535       | 6156        | 6156       | 0         | 0           |
| ros-dynamic-pauto-30 | 8          | 16524 | 62       | 103          | 7274      | ≤ 1 µs   | 20.3 ms  | 23963   | 23963     | 149           | 189        | 45          | 45         | 7360      | 93          |
| ros-fixed-p0-30      | 163        | 16634 | 1301     | 1964         | 4064      | ≤ 1 µs   | 14.4 ms  | 23963   | 23963     | 3249          | 7329       | 6028        | 6028       | 0         | 0           |
| ros-fixed-pauto-30   | 8          | 16524 | 62       | 103          | 7274      | ≤ 1 µs   | 20.3 ms  | 23963   | 23963     | 149           | 189        | 45          | 45         | 7360      | 93          |
| base-60              | 1          | 46007 | 0        | 20           | 1724      | ≤ 1 µs   | 1.4 ms   | 47751   | 47751     | 0             | 1744       | 1744        | 245        | 0         | 0           |
| ros-dynamic-p0-60    | 174        | 10862 | 0        | 3471         | 33418     | 6.1 ms   | 35.7 ms  | 47751   | 47751     | 3453          | 36889      | 36889       | 36889      | 0         | 0           |
| ros-dynamic-pauto-60 | 64         | 15805 | 30       | 1067         | 30849     | 3.5 ms   | 34.6 ms  | 47751   | 47751     | 1078          | 3362       | 3118        | 3112       | 31339     | 2242        |
| ros-fixed-p0-60      | 174        | 10922 | 2        | 3473         | 33354     | 5.7 ms   | 36.4 ms  | 47751   | 47751     | 3459          | 36829      | 36827       | 36827      | 0         | 0           |
| ros-fixed-pauto-60   | 126        | 10987 | 513      | 2013         | 34238     | 6.2 ms   | 35.1 ms  | 47751   | 47751     | 2510          | 5223       | 4098        | 4098       | 35219     | 2895        |
| base-80              | 1          | 42660 | 0        | 20           | 21187     | ≤ 1 µs   | 20.6 ms  | 63867   | 63867     | 0             | 21207      | 21207       | 320        | 0         | 0           |
| ros-dynamic-p0-80    | 132        | 1914  | 0        | 2646         | 59307     | 28.7 ms  | 88.6 ms  | 63867   | 63867     | 2628          | 61953      | 61953       | 56582      | 0         | 0           |
| ros-dynamic-pauto-80 | 132        | 1936  | 0        | 2647         | 59284     | 26.5 ms  | 88.6 ms  | 63867   | 63867     | 2629          | 33829      | 33828       | 30820      | 56027     | 27435       |
| ros-fixed-p0-80      | 132        | 1914  | 0        | 2646         | 59307     | 28.7 ms  | 88.6 ms  | 63867   | 63867     | 2630          | 61953      | 61953       | 56582      | 0         | 0           |
| ros-fixed-pauto-80   | 133        | 1857  | 0        | 2647         | 59363     | 28.8 ms  | 90.7 ms  | 63867   | 63867     | 2631          | 36074      | 36074       | 33007      | 55975     | 29575       |

For reference, before the repair (REVIEW-E2-1, fixed:4, one statement per lease): 30% 173
connects/s, reuse 221, warm hits 6, p50 24 ms, p99 61 ms; 60% 168/s, reuse 0, p50 28 ms, p99
73 ms; 80% 129/s, reuse 0, p50 37 ms, p99 110 ms.

**What I see.**

- **B1 alone (`patience=0`, with the new two-statement lease)** removes the runaway queue at
  30% (p99 61 → 14 ms, reuse 221 → 16,634) but not the churn: still 163–174 connects/s at 30%
  and 60%, the connect model's ceiling (174/s). At 60% the median wait is 6 ms against ≤ 1 µs
  for the baseline.
- **Patience `auto` at 30%** stops the thrash: 8 connects/s (169 connects in 20 s, 20 of them
  the initial fill), 149 switch closes in 20 s against 3,249, demand
  (`switch_arrivals`) 189 against 7,329, p50 ≤ 1 µs. The p99 wait rises from 14 to 20 ms: 7,360
  leases wait patiently (one in three), up to L̂ (about 18–21 ms), 93 of them run out.
- **At 60% it only partly helps**: `fixed:4` still makes 126 connects/s (reuse unchanged at
  about 11,000 of 47,751; p50 6.2 ms, p99 35 ms); `dynamic` 64/s, p50 3.5 ms, p99 35 ms. The
  baseline: p50 ≤ 1 µs, p99 1.4 ms.
- **At 80% it changes nothing that matters**: 132–133 connects/s with patience 0 or auto,
  reuse about 1,900 of 63,867, p50 27–29 ms, p99 89–91 ms, against the baseline's p50 ≤ 1 µs and
  p99 21 ms. Half the patient waiters run out (27,435 / 56,027 and 29,575 / 55,975).
- So, plainly: **with patience on, `remake-on-switch` still churns at 60–80%.** That is a
  finding about the policy, not the provisioning rule: with tenant-keyed connections under a
  hard cap of 20, ten Zipf-skewed tenants at 60–80% load hold more distinct tenant-connections
  than the cap allows, so a tenant switch has to remake a connection, at 130–175 connects/s.
  Per the brief I added no further mechanism.
- `stock_empty_free_slot` / `switch_arrivals` (the model's miss rate) under `remake-on-switch`
  `fixed:4`: 30% 82% (p0) and 24% (auto, 45 of 189); 60% 100% and 78%; 80% 91% both. With the
  cap full the warm stock is drained as fast as it is made.
- `fixed` and `dynamic` give the same pool counters at 30% with `auto` (the files differ only
  in the rule's gauges and the run id): the rule's target stays at W_min = 4 (λ̂ 7.9/s at the
  end). At 80% the target is 0 at the end (λ̂ 2,020/s with `auto`, 3,199/s with 0; the clamp
  cap − lent high-water reaches 0 with 20 lent), and the numbers are nearly those of `fixed:4`
  because with the cap full the stock cannot be refilled either way. At 60% with `auto` the
  target ends at 1 (λ̂ 138/s) — which is why `dynamic` closes fewer idle connections there
  than `fixed:4` (64 against 126 connects/s). I read these from the final gauges only.

Reproduce (scratch, not committed): for each row,
`go run ./cmd/mglease sim <the row's keys> workload.profile=skewed workload.tenants=10 pool.cap=20 warm.size=4 run.duration=20s obs.interval=20s seed=1 out=<dir>/<run id>`,
then the DuckDB query over `<dir>/*/metrics.csv` and `histograms.csv` (last flush; quantiles
from the summed `lease_wait_ns` buckets).

**Corrected in this file by the repair:** sections 2.1 (`Result`), 2.3 (`Script`, `plain`),
4 (counters), 5 (`remake.patience`), 6.2 (the short statement's cost), 7.1 (load) and 7.9
(`first_stmt_ns`), 8 (totals), 11 (E4, E5).

