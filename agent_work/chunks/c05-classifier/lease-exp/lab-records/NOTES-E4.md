# E4 — session-state isolation checker

Seat: Codex (independent test author), branch `exp/lease-lab-e4`.
Brief: `/root/briefs/zgateway-lease-e4-retry.md`; governing rules:
`/root/briefs/zgateway-lease-lab-rules.md`. The pool, engine, real shell, frozen
packages and root STATE.md are unchanged. This note is the piece checkpoint.

## Implementation and decisions

- `hostile.go`: registered `hostile` source/profile, 21-kind shuffled deck drawn
  from the engine's named seeded stream. Each complete deck covers every kind.
  The 12 required kinds, function-hidden variant, client gone, all-at-once, and
  error-after-state, committed SET/SET LOCAL, session authorization, multi-statement
  Query, 32 prepared statements, and a temporary sequence are included.
- Secrets identify tenant and lease. Advisory locks encode them as the two key
  integers; sequence values encode the globally unique lease ID. Seed probes use
  16 precomputed two-value random sequences, not string secrets. Secret-bearing
  settings use application_name; custom names also encode tenant and lease.
- Setup uses a separate session and creates the role and functions idempotently
  once per container. The stock image's plpgsql suffices. A SECURITY DEFINER SQL
  helper reads ONLY the calling backend's start timestamp: pg_stat_activity hides
  that timestamp from the changed role otherwise. The helper does not SET any
  configuration. The catalog independently checks PID against BackendKeyData and
  start time against E5's query identity.
- `oracle.go`: the catalog SELECT runs first, outside any explicit transaction,
  then front-door probes. Settings count only session overrides whose value differs
  from reset_val (RESET ROLE can leave a source label without a changed value).
  The role column also detects changed session authorization. Temp relations include
  tables, sequences, indexes and TOAST objects. A temp table in the all-at-once
  script grants SELECT to PUBLIC so SET ROLE cannot hide its secret behind a
  permission error. No privilege reset occurs before inspection.
- Held cursor FETCH and random() consume state after the catalog snapshot. NOTIFY
  comes from a short-lived side session before the catalog query; notifications
  collected across all replies count only when both channel and payload match.
  Advisory-lock read-back uses the current backend's lock view, not a new lock.
- Existing connections probe their own previous lease, checking the engine's
  tenant and taint metadata against inspector history. Fresh backends probe the
  last inspected lease as a negative control. The target is captured at Statements
  time, so concurrent Row completions cannot change the selected secret.
- `note` records scope=fresh/cross_tenant/same_tenant, allowed=true only for a
  same-tenant on-switch grant, probe_lease, every hit, and ERROR for unexpected
  replies, SQLSTATEs, shapes, identity mismatches or predecessor mismatches.
  `HostileViolation` treats errors as failures, excludes the two declared residues,
  and enforces the policy's lease/tenant boundary. The engine's existing metrics
  hook emits oracle_checks{clean,dirty} and canary_reads{clean,leak}. These metrics
  alone do not treat ERROR notes as failures: use the supplied SQL validator too.
- No reset-completion hook is exposed to this inspector: reset_between stays -1.
  This is deliberately not inferred from a clean result. R3 uses PID/start and
  connection identities; R1/R2 check actual state. E6's S1 reset-history row is
  therefore not checked by E4.
- Bounds: scripts at most 33 statements (engine cap 64), inspection at most 13
  queries, 21-entry deck, 16 seed references; history at most run.max_requests
  entries (overflow yields ERROR). Pending probe targets are at most one per lent
  connection, removed on Row; abandoned inspection targets remain bounded by the
  finite request count. Side sessions close immediately. The server-side baseline
  intentionally accumulates state, bounded by the finite campaign request count.
  Campaign IDs stay within signed 32-bit advisory-lock key range.

## Checks and retained evidence

`oracle_tierr_test.go` contains real Postgres tests, skipped without E5's container
variables. `hostile_tierr_test.go` contains deterministic source/schedule tests.
All test-owned sessions use the throwaway postgres:17 image, SCRAM and TLS through
E5's shell; no host Postgres or long-lived container is used.

| Requirement | Test/evidence |
| --- | --- |
| R1/R2/R4, baseline has teeth | TestRealHostileR1R4Matrix: 24 cells, 189 leases each; each kind written six times and probed three times same-tenant and three times across tenant boundaries (T/E/gone are closed instead) |
| R3 | Same matrix checks every PID/start against prior tenants/leases; remake-always forbids any reuse, remake-on-switch forbids cross-tenant reuse |
| R5 | TestRealHostileR5: each policy × T/E/client-gone, plus refused reset and timed-out reset under both reset policies; TestRealHostileR5PatienceZero adds the three endings with patience 0 |
| Slow successful reset | TestRealHostileSlowResetWaited: two requests queued up front, actual temp-table reset reply held for 100 ms at the test DB seam; any inspection before delivery is a failure |
| Catalog does not hide state | TestRealHostileCatalogDoesNotHideState: two successive catalog queries produce identical rows, all seven state columns positive |
| Planted skipped reset | TestRealHostileJudgeCanaries/skip_one_in_three: replace every third reset with SELECT 1 and forge its tag; exactly three subsequent leaks detected among 12 leases |
| Planted wrong connection | TestRealHostileJudgeCanaries/wrong_connection: redirect inspection to an independent clean connection; identity mismatch must fail even under baseline |
| Queued tenants / warm stock | TestRealHostileWarmStock: 30 queued leases, 12 tenants, fixed warm target 1, cap 1, both patience values; no backend crosses tenants |
| Named extra cases | TestRealHostileEdges: 9 kinds × 5 policies; each prints its oracle row; baseline must read the secret, clean policies must not |
| Seeded coverage / bounds | TestHostileScriptsBoundedAndSeeded, 1,000 scripts compared byte-for-byte; TestHostileCampaignSchedule verifies exact request counts |

Matrix baseline (each of four affinity/reseed cells): cross-tenant positive counts
in [prepared, cursors, listens, advisory locks, changed settings, temp relations,
changed role] = **[52, 52, 52, 48, 54, 52, 9]**. Every one of the 17 secret-bearing
kinds leaks in 3/3 cross-tenant probes. Seed is checked separately; T/E/client-gone
are required to close even in the baseline and are not expected leaks.

The reset-always cells have zero dirty columns and zero secret reads. Reset-on-switch
has zero cross-tenant dirty columns/reads and 51 allowed same-tenant reads per cell.
Both reset arms show six cross-tenant seed hits with reseed off, zero with reseed on,
and six custom-name hits either way. Remake-on-switch (auto and 0) has zero
cross-tenant dirt, reads or residue, and 51 allowed same-tenant reads per cell.
Remake-always has zero dirt, reads or residue on every grant.

HELD/BROKEN list and final counts are recorded below. Intermediate
logs are retained: initial failures were checker/configuration mistakes, not pool
findings (role-restricted identity metadata, duplicate reseed option, temp-table
permissions, and missing required warm.mode). They are superseded by the verified
run. No pool/engine/shell repair is included.

## Campaign entry and reproduction

Full manifest: `e4-campaign.txt`: 24 arms = five policies plus labelled
remake-on-switch patience 0, × affinity on/off × reseed on/off. Remake-on-switch
uses fixed warm stock 1, others cold; two connections, 12 tenants, load 5%, seed
741, oracle.rate=1, identity=query, obs.leases_csv=on. At 120 s per arm the exact
seeded count is **2,429 leases per arm**, at least 115 writes of every kind when
all requests are granted. Expected duration about 49 minutes plus drain/setup.
Run only when no timing campaign holds the machine lock. Functional campaign
lateness bounds are deliberately generous (30 s); no latency conclusion is drawn.

```sh
export PATH=/usr/local/go/bin:$PATH
# From this repository worktree:
E4_EVIDENCE="$PWD/agent_work/chunks/c05-classifier/lease-exp/e4-evidence/tests" \
  scripts/lease-exp.sh test -run TestRealHostile -v
go test ./internal/lab/leaselab -run '^TestHostile' -v
# Coordinator's full E7 isolation campaign (shell takes the machine lock):
scripts/lease-exp.sh campaign agent_work/chunks/c05-classifier/lease-exp/e4-campaign.txt \
  out=agent_work/chunks/c05-classifier/lease-exp/e4-full
# Reduced functional rehearsal, no timing evidence:
MGLEASE_NOLOCK=1 scripts/lease-exp.sh campaign \
  agent_work/chunks/c05-classifier/lease-exp/e4-reduced.txt \
  out=agent_work/chunks/c05-classifier/lease-exp/e4-evidence/campaign-mon
# Mandatory checker health / isolation check before reading table 2:
/root/.local/bin/duckdb -bail -c "SET VARIABLE e4_root='agent_work/chunks/c05-classifier/lease-exp/e4-evidence/campaign-mon';" \
  -f test/analysis/lease/oracle/e4.sql
# Standard report; its table 2 consumes these oracle.csv files:
scripts/lease-report.sh agent_work/chunks/c05-classifier/lease-exp/e4-evidence/campaign-mon \
  agent_work/chunks/c05-classifier/lease-exp/e4-evidence/report-mon
```

Reduced manifest: 3 s, exactly 60 requests per arm (two complete decks); 1,440
leases total. It exercises the production shell's concurrent scheduling; the
serial matrix supplies positive per-kind probe coverage and same-tenant pairs.
The reduced campaign does not replace the full E7 run. Observed counts follow below.

## Limits and deviations

- Residue items 3/4 (extension/interpreter memory and replication origins) remain
  untested, exactly as the plan requires. These results are not a universal claim
  about all PostgreSQL session state.
- The NOTIFY inspector opens a side session and blocks the real shell briefly;
  this campaign measures isolation only. It cannot supply throughput/latency
  comparisons. application_name taints also make E5's application-name monitor
  unsuitable; monitor is enabled at 100 ms only to satisfy the report file contract; its
  counts can undercount tainted backends, so R6 is not claimed here.
- The skipped-reset plant is at the existing DB seam, not a production engine knob.
  Slow-reset timeout uses `pg_sleep; DISCARD ALL`, which would also be refused as
  a multi-statement Query if allowed to finish; the timed-out counter distinguishes
  timeout from the separate immediate-refusal case. The successful delayed reply
  test uses a real, standalone DISCARD ALL and tests the wait separately.
- One role/session is shared just as in the lab deployment. Role checks assume the
  throwaway container's postgres login. Prepared statement multiplicity is 32,
  not an unbounded stress test. No extension or external module is added.
- No edits to STATE.md (explicitly frozen by this brief's rules), no push/merge,
  and independent review/landing remains with the coordinator.

## Final checkpoint — observations and results

Completed. Full real suite: eight top-level tests, 98 leaf cases including the
catalog test, **4,747 oracle rows**, `-race`, 306.481 s under concurrent host load.
Two deterministic tests also pass (100 leaf cases total). The later R5 confirmation
adds explicit assertions/counters distinguishing refused and timed-out resets; it
passes in 8.402 s. See `e4-evidence/real-tests-verified.log`, `r5-confirm.log`,
`schedule.log`, and `tests/<test-name>/oracle.csv`.

**HELD:** last-statement SET, state then division-by-zero, function-hidden state,
SET LOCAL then committed session SET, SET SESSION AUTHORIZATION, multi-statement
Query, 32 PREPAREs, LISTEN/NOTIFY, temporary sequence; slow successful reset waits;
refused/timed-out reset closes; T/E/client-gone closes under every policy; queued
warm stock preserves tenant identity. Named subtests print the actual oracle rows.
**BROKEN as required:** baseline isolation in all seven catalog categories and all
17 secret-bearing kinds; planted skipped reset (three detections); planted wrong
inspection connection (identity mismatch). No unexpected pool/engine/shell defect
was found; there is no suppressed failing pool test or requested core repair.

Final monitored reduced campaign: 24 runs × 60 = **1,440 grants**, zero refusals,
all runner-valid, all seven catalog columns checked on every grant, zero inspector
ERROR notes. SQL checker passes, including baseline-positive assertions. Standard
report produces 12 tables with **table 00 empty**; table 02 includes every policy.
`report-mon/02-isolation.csv` is the report table 2 input/result demonstration.
The first unmonitored rehearsal is retained in `campaign/`; its report flags 12
missing server.csv files, so it was superseded by `campaign-mon/` (all rows retained,
not silently removed). Monitoring is for that file contract, not R6 evidence.

Observed matrix below: each row has 60 oracle rows. D = cross-tenant positive
counts in prepared/cursors/listens/locks/settings/temp/role order. U = secret reads
not labelled allowed (intentional baseline leaks); A = allowed same-tenant reads.
S/N = all seed/name hits, including allowed same-tenant residue. The one S/N hit
under rms affinity-on/reseed-off is same-tenant, not a cross-tenant residue.

| Arm (affinity, reseed in name) | D | U | A | S | N |
| --- | --- | ---: | ---: | ---: | ---: |
| base_aoff_roff | 31,24,28,27,44,31,7 | 47 | 0 | 6 | 6 |
| base_aoff_ron | 31,26,28,28,41,32,8 | 48 | 0 | 6 | 6 |
| base_aon_roff | 34,26,29,27,41,32,7 | 47 | 0 | 6 | 6 |
| base_aon_ron | 35,25,27,25,45,31,7 | 47 | 0 | 6 | 6 |
| ra_aoff_roff | 0,0,0,0,0,0,0 | 0 | 0 | 6 | 6 |
| ra_aoff_ron | 0,0,0,0,0,0,0 | 0 | 0 | 0 | 6 |
| ra_aon_roff | 0,0,0,0,0,0,0 | 0 | 0 | 6 | 6 |
| ra_aon_ron | 0,0,0,0,0,0,0 | 0 | 0 | 0 | 6 |
| rma_aoff_roff | 0,0,0,0,0,0,0 | 0 | 0 | 0 | 0 |
| rma_aoff_ron | 0,0,0,0,0,0,0 | 0 | 0 | 0 | 0 |
| rma_aon_roff | 0,0,0,0,0,0,0 | 0 | 0 | 0 | 0 |
| rma_aon_ron | 0,0,0,0,0,0,0 | 0 | 0 | 0 | 0 |
| rms0_aoff_roff | 0,0,0,0,0,0,0 | 0 | 1 | 0 | 0 |
| rms0_aoff_ron | 0,0,0,0,0,0,0 | 0 | 1 | 0 | 0 |
| rms0_aon_roff | 0,0,0,0,0,0,0 | 0 | 3 | 0 | 0 |
| rms0_aon_ron | 0,0,0,0,0,0,0 | 0 | 3 | 0 | 0 |
| rms_aoff_roff | 0,0,0,0,0,0,0 | 0 | 1 | 0 | 0 |
| rms_aoff_ron | 0,0,0,0,0,0,0 | 0 | 2 | 0 | 0 |
| rms_aon_roff | 0,0,0,0,0,0,0 | 0 | 3 | 1 | 1 |
| rms_aon_ron | 0,0,0,0,0,0,0 | 0 | 3 | 0 | 0 |
| ros_aoff_roff | 0,0,0,0,0,0,0 | 0 | 2 | 6 | 6 |
| ros_aoff_ron | 0,0,0,0,0,0,0 | 0 | 2 | 0 | 6 |
| ros_aon_roff | 0,0,0,0,0,0,0 | 0 | 1 | 6 | 6 |
| ros_aon_ron | 0,0,0,0,0,0,0 | 0 | 2 | 0 | 6 |

Verification: gofmt empty; `go build ./...`, `go vet ./...` pass;
`go test ./internal/lab/... ./cmd/mglease/...` passes. Imported sim, pgwire and obs
package suites pass after moving the new SQL into `lease/oracle/`: the initial
regression's only failure was the existing baseline test counting the direct
`lease/*.sql` file as a scenario SQL. Its focused rerun passes unchanged
(`regression-recheck.log`). No existing file was modified to fix this.

Reusable project knowledge is contained in this note (probe ordering, role-masked
identity metadata, concurrent target capture, report/monitor contract) and the
indexed commands/manifests above; no VM knowledge was added. The normal kb command
failed before producing a search ID; the repository's older kb binary worked,
search 829 was judged not useful. No independent review, push or merge performed;
coordinator owns those next steps. Full E7 campaign is prepared, not run here.

Size: 395 lines of Go implementation, 519 lines of Go tests (914 total), plus
39 lines of independent SQL validation and the manifests/evidence. This exceeds
the test estimate to cover per-arm positive controls, explicit queued grants and
probe failures. Final cleanup confirmed no test-owned mglease container remains.

Implementation and evidence commit: `53de977`. Post-commit assessment: no additional
VM knowledge or reusable script is needed; the reusable oracle SQL and the two
campaign manifests are indexed above. Final deterministic tests also pass under
`-race` (2.992 s), and the final vet/gofmt checks are clean. Ready for coordinator review.
