# Lease-lab port (`lease-lab-port`) — builder notes

Builder: Opus, 2026-11-27, branch `work/lease-port` (cut from main `98cee87`; the evaluation
commit `fd3b553` came first). Brief: `/root/briefs/zgateway-p2-lease-port.md`. Evaluation:
`EVALUATION.md` in this folder; its "Port plan" steps 1–4 are the scope. Ruling:
`/root/briefs/zgateway-lease-lab-port-ruling.md`. No product code changed: `internal/` is
untouched (`git diff --stat 98cee87 HEAD -- internal cmd` prints nothing). The lab branch was
read with `git show` and `git ls-tree` only.

## Commits

| Commit | Subject |
| --- | --- |
| `b1d70df` | e2e: add held-cursor, role-change and custom-setting hostile kinds to tier R |
| `23221ba` | lease-port: copy the lab notes, reviews, report SQL and e4.sql as records |
| `a2f023b` | lease-port: point lease-report.sh at its own folder |
| `c7b1801` | lease-port: point REPORT.md and CALIBRATION-NOTE.md at the kept records |
| `8d21be2` | knowledge: add the lease experiment's method facts and index the kept records |
| `1563c68` | e2e: fail the per-policy tier R test when a hostile call errors |
| (this file) | lease-port: add the port's notes |
| `d3e8d4b` | e2e: observe each check in one lease and credit only complete dirty-check rows (repair) |
| `4307437` | lease-port: point REPORT.md's review-file line at lab-records (repair) |
| (this file) | lease-port: record the repair round |

## What was built (deliverable 1)

- `test/e2e/realenv.go` `setupSQL`: a role `mg_hostile_role` (NOLOGIN, constant `hostileRole`)
  granted to `RealBackendUser`, and three plpgsql functions, all `GRANT EXECUTE` to the
  backend role:
  - `do_cursor()`: `EXECUTE 'DECLARE hostile_cursor CURSOR WITH HOLD FOR SELECT 1'`;
  - `do_role()`: `PERFORM set_config('role', 'mg_hostile_role', false)`;
  - `do_custom(secret text)`: `PERFORM set_config('mg_hostile.secret', secret, false)`.
- `test/e2e/tierr_test.go`:
  - `hostileCalls` has eight entries: the five of main plus `do_cursor()`, `do_role()` and
    `do_custom('LEASE_SECRET')`. `hostileLease(call, lease)` replaces the marker
    `LEASE_SECRET` with a per-lease secret (`secret-<unix ns>-<lease index>`) and returns it.
  - `secretReadBack(c, secret)` runs `SELECT current_setting('mg_hostile.secret', true)` and
    reports whether the secret came back.
  - `sessionRun` now returns a `sessionResult`: the dirty count as before, a count per
    `dirty-check.sql` column of the observations where that column differed from its clean
    value, and the number of secrets read back. Column names are read from the `AS <name>`
    aliases of `test/oracles/dirty-check.sql` (`dirtyColumns`), so a column added there is
    covered without a test edit. After every hostile lease the checker runs, in one
    transaction (one lease), the read-back of the newest secret planted so far, the catalog
    query and `SHOW search_path`; a secret read back is one more dirty observation. A column
    is credited only when a complete row of the right width was read and its value differs
    from the clean value; an errored, closed or wrong-width check is counted as unreadable
    (and dirty) instead.
  - `TestTierRSessionStateNeverCrossesClients`: unchanged rule (0 dirty), its failure message
    now prints the per-column counts and the secret count.
  - `TestTierRSessionAblationWithoutResetCrosses`: besides "some dirt", every column must
    have moved at least once (`dirty-check column <name> never differed from its clean value
    with the reset switched off: the hostile set leaves it blind`) and the secret must have
    been read back at least once (`the custom setting mg_hostile.secret was never read back
    with the reset switched off: the read-back cannot fail`), and no check may be unreadable (`N dirty-check
    observations were unreadable with the reset switched off: an unread column proves
    nothing`).
- `test/e2e/c07_tierr_test.go` `policyRun`: each hostile lease gets its per-lease secret; the
  observer reads the newest secret back and runs the catalog query in one transaction (the
  lease right after the hostile lease), and a read-back counts
  exactly like a catalog difference (a crossing when the observer is tenant 1, a same-tenant
  carry-over when it is tenant 2), so the existing rules of
  `TestTierRPoolSessionStateUnderEachReleasePolicy` apply unchanged for always and
  on-switch, re-seed on and off. A hostile call that returns an error, ends in a closed
  connection or returns no row is counted (`hostileErrors`) and fails the test (`<arm>: N
  hostile calls failed, so their state was never planted`); an unreadable observer lease
  fails it too (`<arm>: N observer leases were unreadable, so their state was never
  checked`).

## Decisions the brief left open

1. **Role-change form.** On Postgres 17.11 (`postgres:17`), both `PERFORM set_config('role',
   'mg_hostile_role', false)` and `EXECUTE 'SET ROLE mg_hostile_role'` work inside a plain
   (not SECURITY DEFINER) plpgsql function called by the non-superuser backend role, and both
   leave `current_user` = `mg_hostile_role`, `session_user` = the backend role after the call
   returns. Probe (scratch container `zgw-lease-port-probe`, removed): `SELECT do_role();
   SELECT current_user, session_user;` printed `mg_hostile_role | gw`. I used `set_config`.
   Proof inside the suite: the ablation test's `role_changed` column moves (18 of 24
   observations, claim C3). Postgres overruled nothing (brief 1e): no form was refused and
   every column can be dirtied by the backend role, so 1c has no exception.
2. **Facts checked in the same probe:** `pg_settings` lists no `mg_hostile%` row after
   `do_custom` (count 0), so only the read-back can see that kind; `RESET ALL` empties the
   placeholder (`current_setting` returns the empty string, not NULL) but does not reset the
   role; `DISCARD ALL` clears all three; `do_role` adds no `source = 'session'` row, so only
   `role_changed` sees it.
3. **Which secret is read back.** The newest secret planted in the run, after every lease,
   not only after the custom-setting lease. This is stricter (a secret that survives several
   leases is still caught) and costs one statement per observation.
4. **Read-back position (changed in the repair round, `d3e8d4b`).** Every observation is one
   transaction on the observer: `BEGIN`, the secret read-back, `dirty-check.sql`,
   `SHOW search_path`, `COMMIT` (`observeOneLease` in `test/e2e/tierr_test.go`). The whole
   check is the single lease right after the hostile lease, so no reset runs between the
   hostile call and any part of the check. The clean baseline is taken the same way (through
   the proxy in `sessionRun`, on a direct connection in `policyRun`), so its `ReadyForQuery`
   status (`ZT`) matches. The first build ran the read-back as a separate statement after
   the catalog query, which put a reset in between (review 1, finding 1).
5. **Observer tenant in `policyRun`.** Main chose tenant 2 for odd `i`. With eight kinds that
   would have put the custom-setting kind (index 7) in front of tenant 2 only, so it could
   never be a crossing. The observer is now tenant 2 when `round + i` is odd, so every kind
   meets both tenants over the two rounds. The four arms' counts changed accordingly
   (on-switch same-tenant carry-over 8 instead of main's 4).
6. **Hostile-call errors in `policyRun`.** Main ignored the result of the hostile call, so a
   missing function passed that test silently (seen in the tests-first run, below). Added in
   `1563c68`: an error fails the arm. In the session-state test an error was already a dirty
   observation.
7. **Canary is by hand** (orchestrator decision 1): no product plant. The permanent guard is
   the positive-control assertion (claim C3).
8. **Records links.** No checker reads Markdown under `agent_work/` (below), so the copies are
   byte for byte with no link edit. One relative link in the copies no longer resolves:
   `lab-records/NOTES-E0.md` line 4, `../LEASE-EXPERIMENT-PLAN.md` (it pointed from
   `lease-exp/` to `c05-classifier/LEASE-EXPERIMENT-PLAN.md`; from `lab-records/` it names
   `lease-exp/LEASE-EXPERIMENT-PLAN.md`). Left as is; the README says the copies are
   verbatim and their paths may not resolve.
9. **Where `conservation.sql` went.** Into `report-sql/` itself (the brief's list), not a
   `checks/` subfolder. `lease-report.sh` globs `[0-9]*.sql`, so it does not run it.

## Claims and their proof

| # | Claim | Proof |
| --- | --- | --- |
| C1 | Through the real binary with `DISCARD ALL`, none of the eight hostile kinds leaves state that the next lease sees: 0 dirty observations over 24 hostile leases, including 0 secrets read back | `test/e2e/tierr_test.go` `TestTierRSessionStateNeverCrossesClients` (G6) |
| C2 | Under always and on-switch, re-seed on and off, no session state and no secret crosses tenants; same-tenant carry-over appears only under on-switch; every hostile call succeeds | `test/e2e/c07_tierr_test.go` `TestTierRPoolSessionStateUnderEachReleasePolicy` (G6) |
| C3 | With the reset planted off, each of the seven `dirty-check.sql` columns differs from its clean value at least once and the secret is read back at least once. Observed every run: prepared 21, cursors 19, listens 20, adv_locks 22, session_gucs 24, temp_rels 23, role_changed 18, secret 17 | `test/e2e/tierr_test.go` `TestTierRSessionAblationWithoutResetCrosses` (G6) |
| C4 | The new tests fail without the new hostile kinds (tests first) | Run T below |
| C5 | A partial reset with no `CLOSE ALL` and no role reset passes main's tests at `98cee87` and fails the new ones | Canary (i) below |
| C6 | A reset that leaves out only `RESET ALL` passes main and is caught only by the secret read-back; `DISCARD PLANS; DISCARD TEMP` is caught by the old columns and also by `cursors`, `role_changed` and the read-back | Canary (ii) below |
| C7 | Removing a kind from `hostileCalls` makes the ablation test fail naming the blind column (or the read-back) | Canary (iii) below |
| C8 | The three tests are stable under load: 20 consecutive runs each, all pass, same counts every run | Loop runs below |
| C9 | The 44 copied files equal their blobs on `exp/lease-lab` (`lease-report.sh` until its own edit commit) | Copy list below; check command under "Commands" |
| C10 | `report-sql/lease-report.sh` runs from its new folder over main's data; table 01 reproduces byte for byte; the other tables need inputs main does not hold | "Records: does the report still run" below |
| C11 | `e7/isolation/e4.sql` over main's `per-run.tar.gz` reproduces `e4-oracle-check.txt` exactly | Same section |
| C12 | No test or gate stage picks up SQL, CSV or Markdown under `agent_work/` | "Records: nothing picks them up" below |

## Canary runs (deliverable 1d)

Scratch folder `/tmp/zgw-lease-port/` (deleted at the end). Trees were made with
`git archive 98cee87` (`main98/`) and `git archive 1563c68` (`head/`), then copied per run.
The mutation (`tools/mutate.py <tree> <reset>`) replaces the constant `discardAll` in
`internal/proxy/lease.go` with the given reset text and makes the reset judge accept any
`CommandComplete` tag for it:

```diff
-const discardAll = "DISCARD ALL"
+const discardAll = `RESET ALL; DEALLOCATE ALL; UNLISTEN *; DO $$BEGIN PERFORM pg_advisory_unlock_all(); END$$; DISCARD TEMP`
@@ func (j *resetJudge) expect
 	case pgwire.MsgCommandComplete:
+		if tag == discardAll {
+			j.sawTag = true
+			break
+		}
```

Every run: `GOFLAGS=-buildvcs=false MG_TIER_R=1 go test -count=1 -timeout 15m ./test/e2e/ -run
'TestTierRSessionStateNeverCrossesClients|TestTierRSessionAblationWithoutResetCrosses|TestTierRPoolSessionStateUnderEachReleasePolicy' -v`
in the tree (`-buildvcs=false` because the scratch trees have no `.git`). Outputs quoted
from the logs.

**T. Tests first** (`head/` with `test/e2e/realenv.go` from `98cee87`, so the three functions
do not exist): exit 1.
- `TestTierRSessionStateNeverCrossesClients`: FAIL, `9 dirty observations through the proxy
  (prepared 0, cursors 0, ..., secret read back 0)` (the 9 are the failing calls).
- `TestTierRSessionAblationWithoutResetCrosses`: FAIL, `dirty-check column cursors never
  differed ...`, `dirty-check column role_changed never differed ...`, `the custom setting
  mg_hostile.secret was never read back ...`.
- `TestTierRPoolSessionStateUnderEachReleasePolicy`: FAIL, `always/off: 6 hostile calls
  failed, so their state was never planted` (and the same for the other three arms). Before
  `1563c68` this test passed here; that is decision 6.

**(i) Partial reset of the evaluation's section 1**
(`RESET ALL; DEALLOCATE ALL; UNLISTEN *; DO $$BEGIN PERFORM pg_advisory_unlock_all(); END$$; DISCARD TEMP`):
- main `98cee87`: exit 0, all three PASS (`tier R: 0 dirty observations over 15 hostile
  leases`; crossings 0 in all four arms).
- this branch: exit 1. Session state FAIL `21 dirty observations through the proxy
  (prepared 0, cursors 19, listens 0, adv_locks 0, session_gucs 0, temp_rels 0,
  role_changed 18, secret read back 0)`. Per-policy FAIL in all four arms: `5 session-state
  crossings between tenants`, `same-tenant carry-over 6 (present only under on-switch)` for
  always, and `1 hostile calls failed` (the second `do_cursor` finds `hostile_cursor`
  still open). Ablation PASS (it plants no reset at all).
- Caught by: `cursors` (held cursor) and `role_changed` (role). The secret is cleared by
  `RESET ALL`.

**(ii-a) Everything `DISCARD ALL` does except `RESET ALL`**, with `RESET search_path` for the
one ordinary setting the hostile set uses. This is the separable case: a hand-written reset
that resets named settings instead of all
(`CLOSE ALL; RESET ROLE; RESET search_path; DEALLOCATE ALL; UNLISTEN *; DO $$BEGIN PERFORM pg_advisory_unlock_all(); END$$; DISCARD PLANS; DISCARD TEMP; DISCARD SEQUENCES`):
- main `98cee87`: exit 0, all three PASS.
- this branch: exit 1. Session state FAIL `17 dirty observations through the proxy
  (prepared 0, cursors 0, listens 0, adv_locks 0, session_gucs 0, temp_rels 0,
  role_changed 0, secret read back 17)`; per-policy FAIL `4 session-state crossings between
  tenants` in all four arms.
- Caught by: the secret read-back only. Every catalog column is clean.

**(ii-b) `DISCARD PLANS; DISCARD TEMP` only:**
- main `98cee87`: exit 1 already (`32 dirty observations through the proxy`; `6 session-state
  crossings between tenants` per arm).
- this branch: exit 1, `69 dirty observations through the proxy (prepared 21, cursors 19,
  listens 20, adv_locks 22, session_gucs 24, temp_rels 0, role_changed 18, secret read back
  17)`; per arm `8 session-state crossings between tenants`, `2 hostile calls failed`.
- The new checks add: `cursors` 19 (held cursor), `role_changed` 18 (role), secret read back
  17 (custom setting).

**(iii) One kind removed from `hostileCalls`** (`head/` copy, the ablation test is the one that
must fail; the others pass with fewer kinds):
- without `do_role()`: exit 1, `dirty-check column role_changed never differed from its
  clean value with the reset switched off: the hostile set leaves it blind`.
- without `do_cursor()`: exit 1, `dirty-check column cursors never differed ...`.
- without `do_custom(...)`: exit 1, `the custom setting mg_hostile.secret was never read back
  with the reset switched off: the read-back cannot fail`.

## Loop runs (flakiness)

`MG_TIER_R=1 go test -count=20 -timeout 30m ./test/e2e/ -run 'TestTierRSession|TestTierRPoolSessionState' -v`
in the worktree, load average 15–20 on 4 cores (three other builds running):
- at `b1d70df`: exit 0, 60 PASS, 0 FAIL in 129.7 s; every ablation run printed the same
  counts (C3), every session run `0 dirty observations over 24 hostile leases`.
- at `1563c68` (the final test code): exit 0, 60 PASS, 0 FAIL in 112.0 s, load average about
  15; the same counts every run, no hostile call failed, same-tenant carry-over 0 under
  always and 8 under on-switch in all 40 runs of each.

## Repair round

Repair builder: Opus, 2026-11-27, one round against `REVIEW-1.md` (FAIL: 2 major, 2 minor)
and `agent_work/adversarial/lease-lab-port/RESULTS.md` (33 held, 3 broken), under the
orchestrator decisions in `/root/briefs/zgateway-p2-lease-port-repair.md`. Still no product
code: `git diff --stat main -- internal cmd go.mod go.sum` prints nothing.

| Item | Outcome |
| --- | --- |
| Review finding 1 (MAJOR, read-back two or three leases late) | fixed, `d3e8d4b`; regression: mutation M7 now fails `TestTierRSessionStateNeverCrossesClients` and the `always/off` and `always/on` arms of `TestTierRPoolSessionStateUnderEachReleasePolicy` (below) |
| Review finding 2 (MAJOR, unreadable check credits every column) | fixed, `d3e8d4b`; regression: `test/e2e/sessionoracle_test.go` `TestSessionOracleCreditsOnlyCompleteDifferingRows`, `TestSessionOracleCountsABrokenLeaseAsDirtyAndUnreadable`; mutation M6 now fails `TestTierRSessionAblationWithoutResetCrosses` |
| Review finding 3 (MINOR, third stale pointer `REPORT.md:255`) | fixed, `4307437` ("Review files are in [lab-records/](lab-records/).") |
| Review finding 4 (MINOR, closed hostile call not counted) | fixed, `d3e8d4b`; regression: `TestHostileCallFailsUnlessItAnsweredCleanly` (`hostileFailed`: an `E:` token, a `closed:` token, no `ReadyForQuery` or no data row is a failure) |
| `TestAdv_3_ErrorIsNotMovement` (BROKEN) | fixed, `d3e8d4b` (same root cause as finding 2); passes now |
| `TestAdv_3_EmptyIsNotMovement` (BROKEN) | fixed, `d3e8d4b`; passes now |
| `TestAdv_3_TruncatedIsNotMovement` (BROKEN) | fixed, `d3e8d4b`; passes now |

No claim was narrowed and nothing is disputed. The adversary's three C3 tests assert exactly
what C3 and deliverable 1c say; their probe still compiles against the repaired code because
`sessionResult{columns, moved}` and `observe(clean, got)` kept their shapes.

### What changed in `test/e2e`

- `observeOneLease(c, check, secret)`: `BEGIN`; secret read-back (when a secret has been
  planted); `dirty-check.sql`; `SHOW search_path`; `COMMIT`. A reply with an `E:` or
  `closed:` token or without `ReadyForQuery` marks the observation `broken`.
- `sessionResult.observe` credits column `i` only when `completeRow` returned a row (clean
  reply, data row exactly as wide as `moved`) and `row[i] != clean[i]`; otherwise it adds one
  to `unreadable` and credits nothing. `sessionResult.record` counts a broken observation as
  dirty and unreadable and credits nothing.
- `TestTierRSessionAblationWithoutResetCrosses` fails on any unreadable check
  (`N dirty-check observations were unreadable with the reset switched off: an unread
  column proves nothing`). `TestTierRSessionStateNeverCrossesClients` counts one as dirty.
- `policyRun` uses `observeOneLease` for the observer and for the direct clean baseline
  (`directBaseline`), counts a broken observer lease in `unreadable`, and the policy test
  fails on it. Hostile calls in both runs use `hostileFailed`.
- Count changes from the one-lease check: `always-reseed` reseeds 36 instead of 45 (16
  hostile + 16 observer + 4 seed leases; the separate read-back lease is gone), and the
  session string gains `unreadable checks 0`. Every other count is unchanged (C3's per-column
  counts, on-switch same-tenant carry-over 8).

### Reproduced before the fix

Scratch `/tmp/zgw-lease-lab-port/` (deleted at the end). Mutated trees were `git archive` of
the named commit plus the mutation, run with `GOFLAGS=-buildvcs=false MG_TIER_R=1 go test
-count=1 -timeout 15m ./test/e2e/ -run <tests> -v`.

- **TestAdv_3_\*** at `2c70d83`: `go test -count=1 -v ./agent_work/adversarial/lease-lab-port/...
  -run '^TestAdv_3_'` exit 1, all three FAIL (`column prepared falsely counted as exercised
  (1) by invalid observation`, and so on for all seven columns, four for Truncated).
- **M7** (skip the reset only after the custom-setting lease). The mutation, in
  `internal/proxy/dbconn.go` `writeDB`: remember per connection that the last write
  contained `do_custom`; when the next write is the `DISCARD ALL` query, send
  `DO $$BEGIN END$$` instead; in `internal/proxy/lease.go` `resetJudge.expect` accept the
  tag `DO` as well. (A first try sent `SELECT 1`; its row made the reset judge fail and
  close the connection, which wipes the state, so that version proves nothing.) At
  `2c70d83`: exit 1, but `TestTierRSessionStateNeverCrossesClients` PASS (`tier R: 0 dirty
  observations over 24 hostile leases`), `always/off` and `always/on` PASS (`crossings 0,
  same-tenant carry-over 0`); only `on-switch/off` and `on-switch/on` FAIL (`2
  session-state crossings between tenants`). This is the review's result.
- **M6** (remove `do_cursor()` and `do_role()` from `hostileCalls`, and replace the first
  check reply, `round == 0 && i == 0`, with `E:ERROR:42501`, `ZI`). At `2c70d83`: exit 0,
  `TestTierRSessionAblationWithoutResetCrosses` PASS (`51 dirty observations ... cursors 1,
  ... role_changed 1, secret read back 13`). This is the review's result.

### After the fix

- **TestAdv_3_\*** at `d3e8d4b`: exit 0, all three PASS.
- **M7** on `d3e8d4b`: exit 1.
  - `TestTierRSessionStateNeverCrossesClients` FAIL: `3 dirty observations through the
    proxy (prepared 0, cursors 0, listens 0, adv_locks 0, session_gucs 0, temp_rels 0,
    role_changed 0, secret read back 3, unreadable checks 0)`.
  - `always/off` FAIL: `1 session-state crossings between tenants`, `same-tenant
    carry-over 1 (present only under on-switch)`.
  - `always/on` FAIL: `1 session-state crossings between tenants`, `same-tenant
    carry-over 1 (present only under on-switch)`.
  - `on-switch/off` and `on-switch/on` FAIL as before (`2 session-state crossings`).
- **M6** on `d3e8d4b` (the injected reply replaces the catalog reply of the first
  observation, which is then broken, as a real error reply makes it): exit 1,
  `TestTierRSessionAblationWithoutResetCrosses` FAIL: `1 dirty-check observations were
  unreadable with the reset switched off: an unread column proves nothing`, `dirty-check
  column cursors never differed ...`, `dirty-check column role_changed never differed ...`
  (`cursors 0`, `role_changed 0`, `unreadable checks 1`).
- **Stability**, on the final code: `MG_TIER_R=1 go test -count=20 -timeout 40m ./test/e2e/
  -run 'TestTierRSessionStateNeverCrossesClients|TestTierRSessionAblationWithoutResetCrosses|TestTierRPoolSessionStateUnderEachReleasePolicy' -v`:
  exit 0, 60 PASS, 0 FAIL, 298.9 s under load average about 25. Identical counts in all 20
  runs: session `0 dirty observations over 24 hostile leases`; ablation `69 dirty
  observations ... (prepared 21, cursors 19, listens 20, adv_locks 22, session_gucs 24,
  temp_rels 23, role_changed 18, secret read back 17, unreadable checks 0)`; policy
  always crossings 0 / carry-over 0, on-switch crossings 0 / carry-over 8, re-seed on and
  off.

## Records (deliverable 2)

All under `agent_work/chunks/c05-classifier/lease-exp/`: `lab-records/` (18 notes and review
files plus `NOTES-E0.md`, 370,189 bytes with the README), `report-sql/` (14 SQL files,
8 `empty/` sentinels, `lease-report.sh`; 1,065 SQL lines), `e7/isolation/e4.sql`.
`lab-records/README.md` is new: what the files are, that the code and run directories they
name were not kept, and that the branch was deleted after this port.

**Edits to copies.** Only `report-sql/lease-report.sh` (`a2f023b`, 7 lines changed, line count
kept so its `sed -n '2,19p'` usage text still prints the header): `root=` became
`here=$(cd "$(dirname "$0")" && pwd -P)`; the SQL directory default is `$here`; the
calibration default is `$(dirname "$here")/calibration.json`; four header lines say so. The
copy commit `23221ba` wrote it with mode 0644; `a2f023b` restores the branch's 0755. No link
edit was needed (below).

**Does the report still run (C10, C11).** Commands, in the scratch folder:

```
tar -xzf agent_work/chunks/c05-classifier/lease-exp/e7/isolation/per-run.tar.gz -C /tmp/zgw-lease-port/report/in
PATH=$PATH:/root/.local/bin agent_work/chunks/c05-classifier/lease-exp/report-sql/lease-report.sh /tmp/zgw-lease-port/report/in /tmp/zgw-lease-port/report/out
  -> lease-report: 12 tables in /tmp/zgw-lease-port/report/out; table 00 has 108 rows: the campaign is not readable as is   (exit 1)
tar -xzf agent_work/chunks/c05-classifier/lease-exp/e7/tier-r-per-run.tar.gz -C /tmp/zgw-lease-port/report/tr
PATH=$PATH:/root/.local/bin agent_work/chunks/c05-classifier/lease-exp/report-sql/lease-report.sh /tmp/zgw-lease-port/report/tr/1-r6 /tmp/zgw-lease-port/report/out-r6 server_extra=0
  -> lease-report: 12 tables in /tmp/zgw-lease-port/report/out-r6; table 00 has 62 rows: the campaign is not readable as is   (exit 1)
```

- The script runs from its new folder (SQL and calibration found by the new defaults; DuckDB
  v1.5.5) and writes all 12 tables.
- Over r6, compared with `e7/r6/tables/`: `01-calibration.csv` is byte-identical (45 rows);
  `02-isolation.csv` and `07b-rule-step.csv` are identical (both header only for r6). Table
  00's 8 `runner,run.valid 0` rows equal main's 8 rows once the run-directory prefix
  (`/root/runs/lease-r/1-r6/` against the scratch path) is stripped. Tables 03–08 and 10 come out
  header only, and all 18 rows of table 09 say `inconclusive` ("a run of this arm is in
  table 0" or "no run of the arm at this load").
- **Missing inputs, exactly.** Main's tarballs keep per run only `cpu.csv`, `lab.csv`,
  `run.csv`, `server.csv`, `summary.csv` (`tier-r-per-run.tar.gz`) or `lab.csv`,
  `oracle.csv`, `summary.csv` (`isolation/per-run.tar.gz`). Table 00 names what is missing:
  for r6, `histograms.csv`, `metrics.csv` and `oracle.csv` in each of 18 runs; for the
  isolation set, `histograms.csv`, `leases.csv`, `metrics.csv` and `run.csv` in each of 24
  runs (and `sim/per-run-lab.tar.gz` holds only `lab.csv`). So the report SQL is kept as the
  definition of the thresholds and the rules, and it cannot regenerate tables 03–10 from
  what main holds.
- `e4.sql`: `duckdb -bail -c "SET VARIABLE e4_root='/tmp/zgw-lease-port/report/in';" -f
  agent_work/chunks/c05-classifier/lease-exp/e7/isolation/e4.sql` exits 0 and its output is
  identical (`diff`, 58 lines) to `e7/isolation/e4-oracle-check.txt`.

**Nothing picks them up (C12).** Checked against this branch:
- The only Go tests that glob SQL glob `test/analysis/`: `internal/obs/simrun/csvassert_test.go`
  (`test/analysis/*`), `wiresql_test.go` (`test/analysis/*/*.sql`), `classifier_test.go`
  (`test/analysis/<scenario>/*.sql`). Every other `Glob` and `WalkDir` in test code
  (`grep -rn "Glob(\|WalkDir(" --include='*.go'`) reads `.go` files only, or a run
  directory it created, or a fixed list of folders outside `agent_work/chunks/`.
- `scripts/csv-assert.sh` (G7) finds scenarios under `test/analysis/` and run files under the
  run roots the gate passes; `scripts/gate.sh` excludes `agent_work/` from product Go.
- Site build (G9): `.github/scripts/build_site.py` never publishes `agent_work/`, so it checks
  no link inside the copies; its tracked-link rule applies to published pages, and the only
  published link into `lease-exp/` is `book/chapters/05-classifier.md` to `REPORT.md`, which
  stays. `scripts/site.sh /tmp/zgw-lease-port/site`: exit 0, `built 27 pages`. The repository
  has no other Markdown link check (`scripts/c06a_site_links_test.go` tests the site
  script's rule on a fixture). So no edit was needed.

## Pointer fixes (deliverable 3)

`c7b1801`, plain factual sentences (drafts for the prose seat):
- `REPORT.md` line 6: the branch "was" on the local branch, was deleted after this port, and
  its notes and report SQL are in `lab-records/` and `report-sql/` (links).
- `REPORT.md` section 4 (was line 144): the corrected formula stated in full, h0 = S + 2 round
  trips + 21 µs, r = round trip + 89 µs, ceiling = cap / (h0 + resets per lease × r +
  connects per lease × (close lag + connect + fresh-backend penalty)), ratio h0/(h0 + r)
  79.7% against 79.1–79.4% measured (plan 73.9%), within 1% at 120% load; link to
  `lab-records/NOTES-E3.md`. Checked against NOTES-E3 section 3 item 6 (the formula) and
  section 7 item 4 (79.7%, 79.1–79.4%, 73.9%, "within 1%"). The report's existing "predicted
  74%, measured 79%" agrees.
- `CALIBRATION-NOTE.md` "Where the rest is": the probe, `lease-calib.sh` and `runs/r0-*` were
  not kept; the branch was deleted after this port; `calibration.json` is their summary;
  links to `lab-records/NOTES-E0.md` (method and schema, section 4) and the two reviews.

## Knowledge note (deliverable 4)

`docs/knowledge/lease-experiment-method.md` (`8d21be2`): bridge address against the published
port, the CPU-bound connect ceiling, reset cost by backend history, `pg_sleep` overshoot,
the A/A floor, the corrected capacity formula, and where the method lives. Each figure names
its file: `CALIBRATION-NOTE.md` ("Conditions", the P2 verdict, "Limits", the calibration
table), `REPORT.md` (summary, section 4), `lab-records/NOTES-E0.md` (section 5),
`lab-records/NOTES-E3.md` (section 3 item 6, section 7 item 4), `report-sql/09-verdict.sql`.
`agent_work/INDEX.md` has a new section with rows for `report-sql/`, `e4.sql` and the note.

## Files copied (blob hashes on `exp/lease-lab`, tip `5ffc7a3cbabadd15ce875eb0ab325f90c790b7bf`)

Destination paths are under `agent_work/chunks/c05-classifier/lease-exp/`. Source blob =
`git rev-parse exp/lease-lab:<source>`; at `23221ba` every destination's `git hash-object`
equals it (44 of 44).

| Destination | Source on `exp/lease-lab` | Blob | Copy |
| --- | --- | --- | --- |
| `lab-records/NOTES-E1.md` | `agent_work/chunks/c05-classifier/lease-exp/NOTES-E1.md` | `2df2d0bd4891a83f3307e6d8704808051379832b` | byte for byte |
| `lab-records/NOTES-E2.md` | `agent_work/chunks/c05-classifier/lease-exp/NOTES-E2.md` | `e279989ffe491e453cc5768d464a6126e0ee0ff6` | byte for byte |
| `lab-records/NOTES-E3.md` | `agent_work/chunks/c05-classifier/lease-exp/NOTES-E3.md` | `2ef9a4169781ce5e637a9901930a913a9b17cb11` | byte for byte |
| `lab-records/NOTES-E4.md` | `agent_work/chunks/c05-classifier/lease-exp/NOTES-E4.md` | `abd46ac3ab9b88036722c1e4fe73564bbbe4fc9c` | byte for byte |
| `lab-records/NOTES-E5.md` | `agent_work/chunks/c05-classifier/lease-exp/NOTES-E5.md` | `935994cde4cace199b66bb75a465cd96b3f6b22d` | byte for byte |
| `lab-records/NOTES-E6.md` | `agent_work/chunks/c05-classifier/lease-exp/NOTES-E6.md` | `6a8c3ea3d6c81ca210b535d87b5523e62a68b70c` | byte for byte |
| `lab-records/REVIEW-1.md` | `agent_work/chunks/c05-classifier/lease-exp/REVIEW-1.md` | `32787f3aa68543b50953ca7cd8b63c805b229b59` | byte for byte |
| `lab-records/REVIEW-2.md` | `agent_work/chunks/c05-classifier/lease-exp/REVIEW-2.md` | `16e116c33fdc93fe4eb8e928360a8f6d204b3f55` | byte for byte |
| `lab-records/REVIEW-E1-1.md` | `agent_work/chunks/c05-classifier/lease-exp/REVIEW-E1-1.md` | `a2b00bd0350a87c6868b4f15b8c4cfdde00baaab` | byte for byte |
| `lab-records/REVIEW-E2-1.md` | `agent_work/chunks/c05-classifier/lease-exp/REVIEW-E2-1.md` | `205fdf99eee40e853e28854824800b112d9b60d9` | byte for byte |
| `lab-records/REVIEW-E2-2.md` | `agent_work/chunks/c05-classifier/lease-exp/REVIEW-E2-2.md` | `cb99b4e19f6969765daa1d5d212ec1d64aba4227` | byte for byte |
| `lab-records/REVIEW-E3-1.md` | `agent_work/chunks/c05-classifier/lease-exp/REVIEW-E3-1.md` | `084ce7d9a12c84d99c2b9fd6f917fa528948f544` | byte for byte |
| `lab-records/REVIEW-E3-2.md` | `agent_work/chunks/c05-classifier/lease-exp/REVIEW-E3-2.md` | `679eecb76d0cc95c7839712fc597f6dea633b12c` | byte for byte |
| `lab-records/REVIEW-E4-1.md` | `agent_work/chunks/c05-classifier/lease-exp/REVIEW-E4-1.md` | `3d6a64f74cb3d34dfe38a011680776abbfcf886f` | byte for byte |
| `lab-records/REVIEW-E5-1.md` | `agent_work/chunks/c05-classifier/lease-exp/REVIEW-E5-1.md` | `2db2454f167356704ade9626d96b914268549409` | byte for byte |
| `lab-records/REVIEW-E5-2.md` | `agent_work/chunks/c05-classifier/lease-exp/REVIEW-E5-2.md` | `2892947f7c664206d8dde0d8e3579490f3b4fa7f` | byte for byte |
| `lab-records/REVIEW-E6-1.md` | `agent_work/chunks/c05-classifier/lease-exp/REVIEW-E6-1.md` | `64e944bc4bd43fadbeb0fd9fd1508a5f73ac6495` | byte for byte |
| `lab-records/REVIEW-E6-2.md` | `agent_work/chunks/c05-classifier/lease-exp/REVIEW-E6-2.md` | `30764aa77a5ea3cdc3dc428efb4852260cac9962` | byte for byte |
| `lab-records/NOTES-E0.md` | `agent_work/chunks/c05-classifier/lease-exp/NOTES.md` | `e5a8ad9ba8610c828cf1b98180c9e03e753b8c32` | byte for byte (renamed) |
| `e7/isolation/e4.sql` | `test/analysis/lease/oracle/e4.sql` | `d007c7b5b05829de4101859c0eaf9e249f16b896` | byte for byte |
| `report-sql/00-validity.sql` | `test/analysis/lease/report/00-validity.sql` | `9c1963576764bb3fdcecbc4b2bf930e42257ad4b` | byte for byte |
| `report-sql/01-calibration.sql` | `test/analysis/lease/report/01-calibration.sql` | `3852ce534878c725acf3b3ea3dda5ef77f473324` | byte for byte |
| `report-sql/02-isolation.sql` | `test/analysis/lease/report/02-isolation.sql` | `694cef989b1ee0d666dcf5ce3a36de4028224dd2` | byte for byte |
| `report-sql/03-cost.sql` | `test/analysis/lease/report/03-cost.sql` | `46885408fc51869e620158840029adf6207fa787` | byte for byte |
| `report-sql/04-capacity.sql` | `test/analysis/lease/report/04-capacity.sql` | `8fe55789bb20a48133c0340ae99d6e6f2b2f76a8` | byte for byte |
| `report-sql/05-waiting.sql` | `test/analysis/lease/report/05-waiting.sql` | `c9d7fb313e82ffa73176c4d5109e0f09c45e07de` | byte for byte |
| `report-sql/06-warm-stock.sql` | `test/analysis/lease/report/06-warm-stock.sql` | `d8e79856fd41f24e971e01ce8fbb492d8fac8d69` | byte for byte |
| `report-sql/07-rule.sql` | `test/analysis/lease/report/07-rule.sql` | `cf5535eda43b4b7e85d5720adf70693148ee9fdc` | byte for byte |
| `report-sql/07b-rule-step.sql` | `test/analysis/lease/report/07b-rule-step.sql` | `3b2efad2c12ef23bbc07192b2bb5c646eefb17c5` | byte for byte |
| `report-sql/08-sim-vs-real.sql` | `test/analysis/lease/report/08-sim-vs-real.sql` | `7c06de12bbee681dab66067fe1af41f9da49bc75` | byte for byte |
| `report-sql/09-verdict.sql` | `test/analysis/lease/report/09-verdict.sql` | `88fc14c53c27e75754108b1b43133f96f7abb542` | byte for byte |
| `report-sql/10-switch-budget.sql` | `test/analysis/lease/report/10-switch-budget.sql` | `70abce00c8eac937721994e18b580076abfdb886` | byte for byte |
| `report-sql/empty/cpu.csv` | `test/analysis/lease/report/empty/cpu.csv` | `1d092dcba7c0c0908e92c452f6506621aa6a18bd` | byte for byte |
| `report-sql/empty/histograms.csv` | `test/analysis/lease/report/empty/histograms.csv` | `8f0bc43a1fa676b903060fcb204bc427bf503260` | byte for byte |
| `report-sql/empty/lab.csv` | `test/analysis/lease/report/empty/lab.csv` | `942a47139f86778b1f3e5ad923104dd038b1ce88` | byte for byte |
| `report-sql/empty/leases.csv` | `test/analysis/lease/report/empty/leases.csv` | `3645ac9c6f7f27f636b1824c5b6a5c480b9e41fe` | byte for byte |
| `report-sql/empty/metrics.csv` | `test/analysis/lease/report/empty/metrics.csv` | `73796445ad6a3a2619d51c4245db145aa724ecc5` | byte for byte |
| `report-sql/empty/oracle.csv` | `test/analysis/lease/report/empty/oracle.csv` | `276699999450cc552f89dad708afe195e19ab383` | byte for byte |
| `report-sql/empty/run.csv` | `test/analysis/lease/report/empty/run.csv` | `5f155c7ea9d6e9ac68003e608315da36c3b8f2d2` | byte for byte |
| `report-sql/empty/server.csv` | `test/analysis/lease/report/empty/server.csv` | `892b27d0991afaeda99c8ed6c6c977a81a447bd1` | byte for byte |
| `report-sql/params.sql` | `test/analysis/lease/report/params.sql` | `42f2bc2df3544c85bd22bf64da6b9196ab69c042` | byte for byte |
| `report-sql/views.sql` | `test/analysis/lease/report/views.sql` | `c6b52bb8dfe63b29118f3bae4de30dd54b4d7169` | byte for byte |
| `report-sql/conservation.sql` | `test/analysis/lease/checks/conservation.sql` | `62bc03b60202bb822e5ec61e7fe060f54124ea87` | byte for byte |
| `report-sql/lease-report.sh` | `scripts/lease-report.sh` | `6347cb95dca4112bb698ab4537ecf83dac91d999` | edited in a2f023b (7 lines: defaults and header), mode 0755 |

## Deviations

1. **Gate: only G0, G1, G2, G3 and G6**, as the brief says (`GATE_ONLY=G0,G1,G2,G3,G6`), not
   the full gate; such a result never counts for landing. G7 (CSV assertions over the tier R
   run directories, including `tierr-session-state`) was not run; the tests added no
   scenario and the counters G7 reads are not asserted on lease counts. Result below.
2. **`policyRun`'s observer tenant** changed from `i % 2` to `(round + i) % 2` (decision 5),
   and the per-policy test now fails on a hostile-call error (decision 6). Both are beyond
   the brief's letter; both make the new kinds testable.
3. **Canary (ii)** was run in both forms the brief allows (ii-a and ii-b), not one.
4. `lab-records/README.md` is not a copy (the brief asks for it).

## Limits

- The read-back finds the secret only by its exact value; it does not look for other
  placeholder settings a client might create. `pg_settings` cannot list placeholders, so a
  placeholder of another name with the reset broken would be seen only if the hostile set
  plants it.
- The check is one transaction, so it sees the state left for the next lease, not state that
  only appears outside a transaction block.
- `mg_hostile_role` has no privileges of its own; after `do_role` with the reset off, later
  hostile calls rely on PUBLIC's default `EXECUTE` and `TEMPORARY` grants, which the
  `postgres:17` image keeps.
- Session authorization (E4's `session_auth`) is not ported: it needs a superuser login
  (EVALUATION.md section 1).
- The report SQL cannot regenerate tables 03–10 from main's data (missing inputs above).

## Commands

```
export PATH=/usr/local/go/bin:$PATH
# the three tests this port changed (needs Docker and postgres:17)
MG_TIER_R=1 go test -count=1 ./test/e2e/ -run 'TestTierRSession|TestTierRPoolSessionState' -v
# the flakiness loop
MG_TIER_R=1 go test -count=20 -timeout 30m ./test/e2e/ -run 'TestTierRSession|TestTierRPoolSessionState' -v
# all of tier R, as G6 runs it
scripts/tier-r.sh
# the gate stages run for this port
GATE_ONLY=G0,G1,G2,G3,G6 scripts/gate.sh lease-lab-port
# check the copies (needs the branch); prints nothing when every copy matches
L=agent_work/chunks/c05-classifier/lease-exp
grep '^| `' agent_work/chunks/lease-lab-port/NOTES.md | awk -F'`' 'NF>=7 && $4 !~ /^#/ {print $2, $4, $6}' | while read d s b; do
  [ "$(git show 23221ba:$L/$d | git hash-object --stdin)" = "$b" ] && [ "$(git rev-parse "exp/lease-lab:$s")" = "$b" ] || echo "MISMATCH $d"; done
```

## Gate

**Repair round**, on the clean tree at `5e13280` (stage build, baseline `98cee87`,
`dirty_tree=false`), `GATE_OUT=/tmp/zgw-lease-lab-port/gate scripts/gate.sh lease-lab-port`,
all stages (no `GATE_ONLY`), load average 25 to 40 with three other gates running:
**overall fail**, exit 1. `counts_for_landing=false` only because `GATE_OUT` was set; the
orchestrator runs the landing gate.

- Pass: G0 (all 7 rows), G1 (all 4), G2 comments, G3 unit (1,793 s: this is
  `go test -count=1 -timeout 30m ./...`, including `agent_work/adversarial/lease-lab-port`),
  G3 counted, G3 race, G4 markers, G4 sweep (2,296 s, 200 seeds), G8 gen-docs, G9 site;
  G10 skip-none.
- G5 fuzz fail: `internal/pgwire FuzzClassify` had `no execution rate in the go test
  output`. Its 1,101-entry seed corpus did not finish baseline coverage within 20 s under
  load, so no rate line was printed. No crash was found. Rerun alone (`go test -run '^$'
  -fuzz '^FuzzClassify$' -fuzztime 20s ./internal/pgwire/`): PASS, 3,040 execs/s.
- G6 tier-r fail: `TestTierRSlowReader` (`dropped 0 pauses 34`). G7 csv fail is the same run
  (`tierr-slow-reader/faults.sql: 1 violating rows over 1 runs`; the other 236 SQL files
  pass). This test is not touched by this branch, and the flake already exists on main:
  `git archive 98cee87`, `MG_TIER_R=1 go test -count=10 -run '^TestTierRSlowReader$'`: 9
  PASS, 1 FAIL (`dropped 0 pauses 27`). On this branch, `-count=5`: 4 PASS, 1 FAIL
  (`dropped 0 pauses 28`). Under load the slow client is sometimes not dropped within the
  test's window. Left for its owner; no product or unrelated test code changes in this
  repair.
- Rerun of the two red stages on the same clean tree,
  `GATE_ONLY=G5,G6 GATE_OUT=/tmp/zgw-lease-lab-port/gate2 scripts/gate.sh lease-lab-port`:
  **overall pass**, exit 0. G5 fuzz pass (406 s, 18 targets, 0 without a rate). G6 tier-r
  pass (430 s, Postgres 17.11).
- Running G3 rewrites tracked evidence logs under `agent_work/adversarial/lease-lab-port/
  evidence/` (the adversary's harness appends to them). I restored them with `git checkout`
  after each run. They are not part of this repair.
