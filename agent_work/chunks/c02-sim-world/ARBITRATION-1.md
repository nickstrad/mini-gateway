VERDICT: PASS

# Chunk 2 (simulated world) — arbitration 1

Arbiter: Muse Wonder Full (`muse-wonder-full-1.0`, Muse), the top seat outside the
build/review pair. Builder: Claude Opus. Reviewer: Gemini Neon (`REVIEW-1.md`,
`REVIEW-2.md`, both FAIL, one repair round between them). Branch `work/c02-sim-world`.
Reviewed product tip `c80f08d9d6bcac6934dcb487d465a41537778bd4`; branch tip at the start of
this arbitration `3fcedd7ebd4bf351748734d71c2781c1feb14c74`. This verdict is on the chunk
as it stands after the two arbiter fix commits below, as the arbitration brief directs.
Both REVIEW-2 findings are real; both are repaired here by small local changes with
regression tests, so no second repair round is needed. The two narrowed claims (7 and 11)
were not re-litigated.

Scratch reproductions were run in a `git archive` copy under `/tmp/zgw-c02-arb/src`
(deleted after use); the regression tests committed below were then run in this worktree
before each fix (failing) and after it (passing).

## Ruling on REVIEW-2 finding 1 (BLOCKER): real; falsifies claim 3; fixed

**The finding is correct in every particular I checked.** `commit()` in
`internal/sim/fakepg/exec.go` compared only the *final* row count (`rowsAfter`) with
`MaxRows`, marked the transaction committed, then applied the pending writes one at a
time; `apply()` returns silently when `srv.put` reports a full table. A transaction whose
writes pass `MaxRows` midway and come back under it with a later `DELETE` therefore
commits on the wire, loses the intermediate write without a trace in the table or the
write log, and still applies and logs the rest.

My reproductions (committed as regression tests; traces and state on the unfixed tree):

- `TestCommitThatPassesMaxRowsMidwayIsRolledBack` — `MaxRows: 2`, initial `{k1}`; session
  A: `BEGIN; INSERT k2; DELETE k1`; session B: `INSERT k3`; A: `COMMIT`. Unfixed:
  `C:COMMIT ZI`, rows `[{k3 v3}]` (`k2` silently lost), write log `[insert k3, delete k1]`,
  transaction log `TxCommitted` with 2 writes, `WritesApplied=2`,
  `world.CheckWrites` = `statement 2 wrote a row and committed but is missing from the write log`.
- `TestCommitWhoseRecreatedRowPassesMaxRowsIsRolledBack` — the reviewer's re-creating
  `UPDATE` variant. Unfixed: `C:COMMIT ZI`, rows `[{k3 v3}]` (the `k1` update silently
  lost), same `CheckWrites` error.
- `TestCommitInsertThenDeleteOfOneKeyAtAFullTableIsRolledBack` — `INSERT k2; DELETE k2`
  racing a filler. Unfixed: `C:COMMIT ZI`, final rows accidentally correct
  (`[{k1 v1} {k3 v3}]`), but the write log holds `DELETE k2` with no `INSERT k2` and
  `CheckWrites` fails the same way. A table-only replay would not have caught this one;
  the statement/write bijection does.
- `TestImplicitCommitThatPassesMaxRowsMidwayIsRolledBack` (arbiter addition) — the same
  defect through a multi-statement `Query` (implicit transaction, 50 ms service so the
  filler lands mid-message). Unfixed: `C:INSERT 0 1 C:DELETE 1 ZI`, rows `[{k3 v3}]`,
  `CheckWrites` fails. Explicit and implicit commits share `commit()`, so one fix covers
  both; the test pins the wire form (`... E:53100 ZI` after the statement tags, before
  `ReadyForQuery`, whole message rolled back).

This falsifies claim 3 ("the write log is exact: every applied write once; none missing;
none duplicated") in its worst direction for an oracle — a *missing* write under a
*committed* transaction — and breaks commit atomicity ("writes inside a transaction apply
at `COMMIT`"). The reviewer is upheld; the repair-round commit check (`6fc0e83`) fixed the
final-count half of the original blocker and missed the peak.

**Fix (arbiter commit `0ad082c0214f4dc7c2b279721082e691357dc610`,
`fakepg: roll back a commit whose writes pass MaxRows midway`).** `rowsAfter` becomes
`peakRowsAfter`: it walks the pending writes in order exactly as `apply` will (same
per-key presence tracking, seeded from the live table, so a re-creating `UPDATE` counts as
an insert) and returns the highest row count reached; `commit()` rolls back with
`ERROR 53100` when that peak exceeds `MaxRows`, before anything is marked committed or
applied. Peak (sequential) semantics is the right rule here, not final-state atomic
collapse: statement execution already enforces the limit sequentially against pending net
rows, `apply`/`put` are sequential, and reordering or coalescing writes at commit would
change same-key outcomes (`INSERT k; DELETE k` is not `DELETE k; INSERT k`) and break the
per-statement write-log bijection later chunks assert. With the peak at or under
`MaxRows`, `srv.put` inside the commit loop provably cannot fail (before each
new-key insert the simulated count is at most `MaxRows-1`; deletes never fail), so the
silent return in `apply` is unreachable on the commit path. The autocommit path needs no
change: its `MaxRows` check and `apply` happen in the same simulator event with nothing
interleaved. Wire-visible consequence, intended: a commit that no longer fits *at any
point* of its apply order now answers `E:53100 ZI` and rolls back — the same outcome the
chunk already defines for a commit whose final count does not fit
(`TestCommitThatNoLongerFitsIsRolledBack`), instead of a false `COMMIT`.

Regression tests committed with the fix (all in `internal/sim/fakepg/repair_test.go`; the
four rollback tests fail without the fix, the two controls pass before and after):
`TestCommitThatPassesMaxRowsMidwayIsRolledBack`,
`TestCommitWhoseRecreatedRowPassesMaxRowsIsRolledBack`,
`TestCommitInsertThenDeleteOfOneKeyAtAFullTableIsRolledBack`,
`TestImplicitCommitThatPassesMaxRowsMidwayIsRolledBack`,
`TestCommitThatDeletesFirstStillCommitsAtAFullTable` (delete-first order at a full table
still commits — the fix must not over-reject), `TestInsertThenDeleteOfOneKeyCommits`
(net-zero transaction still commits with both writes logged). Each asserts the wire trace,
the exact final rows, and `world.CheckWrites` over the real logs.

## Ruling on REVIEW-2 finding 2 (MINOR): real, and wider than stated; fixed

**The mechanism the reviewer describes is real; its reach is understated.** Arrival
admission in `internal/sim/workload/client.go` compared only `len(c.backlog)` with
`Tenant.Backlog`. A request in retry backoff sits in `c.waiting`, outside the slice, and
the backoff timer prepends it back unchecked. There are two observable forms:

- The reviewer's form (already-sent request retried, e.g. `Retry.OnCodes`): the
  `NotYetSent` gauge indeed stays within the cap and conservation holds, as the review
  says — but the backlog slice itself does not stay capped. In a white-box scratch probe
  (package-internal test, `Backlog: 1`, `SELECT 1/0` with `OnCodes: [22012]`, 7 ms service,
  103 ms backoff, sampled every 100 µs for 600 ms) the unfixed slice peaked at **16** with
  `NotYetSent` peak 1 and `InFlight` peak 32. The probe was scratch-only (the slice length
  has no black-box observable; it appears only in `describe()`/the state digest), so it is
  evidence here, not a committed test.
- The never-sent form (arbiter addition; request retried after a refused connect,
  `Retry.OnConnect`): such a request keeps counting in `NotYetSent` while it waits, so the
  documented gauge itself breaks. Committed probe
  `TestBacklogCapCountsRetryingRequests` (`Backlog: 2`, every connect refused with
  `53300`, 200 ms backoff, 50 arrivals 1 ms apart): unfixed, after 100 ms,
  `NotYetSent {value 50, peak 50}`, `Dropped 0`, `Retries 50`. Each refused connect cycled
  one queued request into backoff and admission let a fresh one take its slice slot.

The bounded-backlog rule, as the chunk documents and tests it — NOTES.md steady-state row
("client backlog of not-yet-sent requests … `Tenant.Backlog` … gauge `NotYetSent`") and
`TestBacklogCapCountsCouldNotSend` asserting `NotYetSent.Peak == Backlog` — is therefore
falsified in the never-sent form, and the queue the cap names is exceeded in both forms.
Total memory stayed bounded by configuration (cap plus in-flight retries) and conservation
held throughout, which is why MINOR is the right severity; but "document that retries are
exempt" would have left the *gauge* lying, so I fixed the admission instead of the wording.

**Fix (arbiter commit `185dc151d0b393a83bf0525e99c181399b8289d3`,
`workload: count retrying requests against the backlog cap`).** Admission now counts
`len(backlog) + waiting`, plus one for the current request while it may still retry
(`attempts < Retry.Max`) — the current request is the one other way back into the slice
without passing admission (it can fail after fresh arrivals have refilled the slice), and
counting it makes `backlog + waiting (+ returnable current) ≤ Backlog` an invariant, so
the slice, the gauge, and their peaks all stay within the cap. With retries off
(`Retry.Max = 0`, the default and every baseline scenario) the extra terms are always zero
and behaviour is bit-for-bit the old one; the full scenario sweep in the gate confirms no
baseline moved. Intended consequence with retries on: fresh arrivals yield (drop, counted)
while earlier work is still queued in any form — finishing admitted work takes precedence
over admitting new work past the cap. Regression test committed with the fix:
`TestBacklogCapCountsRetryingRequests` in `internal/sim/workload/repair_test.go` (fails
without it with `not yet sent {50 50}`; also proves recovery — after the fault clears the
admitted requests complete and conservation holds). The white-box slice probe above drops
to peak slice 1 / peak queued 1 with the fix (scratch re-run).

## Independent sweep: nothing else falsifies a claim

Beyond the two findings I re-derived the write path and the queue accounting from the code
and probed the neighbouring cases in scratch (all on the unfixed tree unless noted):

- Same root cause, already covered by the fix: implicit transactions (above) and a
  middle-peak ordering (`INSERT k2; INSERT k3; DELETE k1` at `MaxRows: 3` racing a filler:
  the middle insert, statement 3, was the one dropped). No other ordering I tried drops,
  duplicates, or mislogs a write once the peak rule is in place.
- Held as designed: a second insert racing table growth is refused at statement time
  (`E:53100 ZE`, then `ROLLBACK`); a single re-creating `UPDATE` at a full table already
  rolled back (final count exceeds); concurrent `DELETE` of one key logs both committed
  deletes and `CheckWrites` passes (replay is delete-idempotent; the log records committed
  row-changing statements, the semantics `CheckWrites` is built on); concurrent `INSERT`
  of one key commits last-writer-wins with both writes logged once — the documented
  no-row-locks limit in NOTES.md, outside claims 1–3 as scoped, not a silent corruption.
- Call-graph audit: `apply` has exactly two callers (autocommit `write`, the `commit`
  loop) and `put` exactly two (`New`, checked; `apply`, checked); `backlog` grows in
  exactly two places (`arrive`, the retry timer). The only discarded errors in the three
  packages are `Close`/`Reset`/registration/histogram-merge idioms. No other silent-drop
  path of the same shape exists.
- Claims 1, 2, 4, 5, 6, 8, 9, 10: I read the governing code paths (startup/refusal order
  and `limit()`/`sessionBound`, cancel, snapshot-at-service-start, fault tables) and rely
  for breadth on the chunk's own suite, the adversarial package (32 held, 2 narrowed), the
  recorded-transcript fidelity test, and the gate's sweep/fuzz stages below (green twice
  on the fixed tree; live tier R is sandbox-blocked in this session — see Commands). I
  found no further falsification and no reason to reopen the reviewer's revert-verified
  repair checks. This sweep was concentrated on the oracle paths the dispute touched
  (writes, logs, caps, queues); it is not a line-by-line re-review of all ~10k lines.

One documentation nit for the orchestrator/prose seat (not a blocker, left untouched by
this arbitration): NOTES.md decision 17 (retries) should gain a clause that retrying
requests count against `Tenant.Backlog`; the steady-state row already reads correctly
under the fixed code.

## What changed

| Commit | Files | Change |
| --- | --- | --- |
| `0ad082c` | `internal/sim/fakepg/exec.go`, `internal/sim/fakepg/repair_test.go` | Peak-row commit check (`peakRowsAfter`); 6 regression/control tests |
| `185dc15` | `internal/sim/workload/client.go`, `internal/sim/workload/repair_test.go` | Backlog admission counts waiting + returnable current; 1 regression test |

No comments in Go, standard library only (no new imports in product code), `gofmt` clean.
No kernel/simulator API change, no `PLAN.md`/`STATE.md`/`NOTES.md`/adversarial edits, no
push or merge. Nothing else needed changing: in particular `apply()` itself is unchanged
because the peak check makes its full-table return unreachable at commit, and the
statement-time `MaxRows` check was already sequential and correct.

## Commands run and results

All with `PATH=/usr/local/go/bin:$PATH` (Go 1.26.8) in this worktree unless noted;
`GOCACHE` pointed at the scratch folder because the sandbox home cache is read-only.

- Scratch (`git archive` copy of `3fcedd7`): the three reviewer cases, implicit and
  middle-peak variants, statement-race/concurrent-delete/concurrent-insert probes, and
  both backlog probes — reproduced as quoted above; candidate fixes validated there first
  (`go test ./internal/sim/fakepg ./internal/sim/workload ./internal/sim/world
  ./internal/testclient` PASS in scratch).
- Worktree, before each fix: the four new fakepg rollback tests FAIL (`C:COMMIT ZI, want
  E:53100 ZI`; implicit: missing `E:53100`), two controls PASS;
  `TestBacklogCapCountsRetryingRequests` FAILS (`not yet sent {50 50}`).
- Worktree, after each fix: new tests PASS; `go test ./internal/sim/workload` PASS.
- `gofmt -l .` — clean. `go build ./... && go vet ./... && go test -count=1 ./...` on the
  clean committed tree at `185dc15` — **PASS**, all 14 packages including
  `agent_work/adversarial/c02-sim-world` (narrowed claims 7/11 skipped as recorded).
- `scripts/gate.sh c02-sim-world` (build stage) on the clean committed tree at `185dc15`,
  run twice (the two launches overlapped in time; both computed `dirty_tree=false` at the
  same SHA and agree check by check; the later finisher's `result.json` is the one quoted,
  started `2026-11-25T02:21:09Z`, 1095 s): **`gate: overall=fail
  counts_for_landing=false`**. Every check passes except G6 tier-r (`fail`, setup exit 2:
  `tier-r.sh: the Docker daemon is not reachable`) and the expected `G7 csv` / `G8
  gen-docs` `skip-not-built`. Passing in both runs: G0 (tools, gofmt, build, vet,
  tools-vet, tools-test, tidy), G1 (detlint over 9 package patterns, importlint,
  boundlint, paniccheck), G2 comments vs baseline `42018a939343b116d5cf92aad35e8a108072f87a`,
  G3 unit + race, G4 markers + sweep (200 seeds per scenario), G5 fuzz (7 targets × 20 s),
  G9 site, G10 extra (10 cross-process scenario replays, identical digests). G6 ran no
  test at all: this arbitration session's shell sandbox refuses connections to the Docker
  Unix socket (`docker ps` as root: `permission denied while trying to connect to the
  docker API at unix:///var/run/docker.sock`; an unsandboxed retry requires a human
  approval this session's policy disables, and the only local server binaries are Postgres
  16, not the required 17). Tier R is therefore *unrun in this session*, not failed on its
  merits. Substitutes on record: REVIEW-2 ran live tier R against Postgres 17.11 on this
  chunk's code before the two arbiter fixes (4 passed, 0 failed); neither fix can move that
  result (the tier-R tests drive real sockets with the test client, not `workload`, and
  the fake side of fidelity runs at default `MaxRows`, where peak and final row counts are
  both far below the cap and every commit takes the identical path); and the recorded
  PG17 transcript comparison (`TestFidelityAgainstRecordedPostgres17`, inside G3) passes
  on the fixed tree in both runs. The orchestrator's landing gate outside this sandbox
  still has to produce the counting result; the only expected difference is G6 green where
  Docker is reachable. The verdict above does not depend on G6: no claim in dispute (3, or
  the backlog rule) is exercised only by tier R, and claim 1's live half is covered as
  just described. The generated `agent_work/chunks/c02-sim-world/gate/result.json` was
  deleted after the run as instructed (untracked); per-check gate logs are gitignored.
