VERDICT: PASS

Arbitration of REVIEW-2 against starting tip `b7cab8c`, with the local fixes below.
All five findings are real. None requires a redesign. The final verdict applies to the
fixed code, not the code that received the second FAIL. The accepted narrowings of
claims 1, 3, 6 and 8 and the ruling on failed non-trial idle checks stand unchanged.

## Rulings and reproductions

1. **BLOCKER, corePump: real; fixed in `d487288`.**
   `TestCoreArbitrationOneRequestBoundsPreparation` builds 11 idle tenant-1 connections
   under on-switch at cap 12, and 42 stale idle connections at cap 45. On the old code,
   the tenant-0 query fails `Engine.Invariants` because connection 10 is idle in the
   engine but resetting in the core; the stale case fails because connection 37 is idle
   in the engine but checking in the core. Both reproduce the review's stranded actions.
   The eight pump turns and the second checkout in enqueue are unjustified retries:
   starting asynchronous preparation does not make another connection lendable.
   The fix executes the buffered checkout actions plus one Demand result without
   redispatching, and avoids retrying a failed direct checkout in enqueue. Existing
   completion and timer paths still dispatch. The regression requires exactly one reset
   or four checks, checks invariants after every engine event through the existing rig,
   and verifies that a successful preparation forwards the waiting query. Both cases
   were rerun against the restored pre-fix product files and failed as described, then
   passed with the fix. This settles the invariant and bounded-preparation violations.

2. **MAJOR, unplanted fuzz/model: real model defect; fixed in `fc60f00`.**
   `TestArbitrationNoReplyCloseDoesNotSuspectNewConnections` replays the exact input
   `[]byte("700900700000Y01C20900100")`. Before the fix it deterministically reports
   `connection 3 lost its suspect flag without a check (lent suspect next)`.
   The pool is right: the zero-reply deadline marks the connections present then;
   subsequently abandoning that connection's cleanup is not a new observation of a
   failed replacement connection. `model.apply` already marks the original suspects.
   Removing only `CloseNoReply` from the close-action suspect switch eliminates the
   erroneous second marking. Actual later loss events still mark suspects in the
   model's loss handling. The immediate suspect assertions and planted-bug checks stay.

3. **MAJOR, half-open trial failure: real; fixed in `4c416e7`.**
   `TestCoreArbitrationTrialFailureRefusesWithIdleSibling` opens the breaker, admits a
   query behind its half-open trial, and fails the trial dial. With one idle sibling,
   the old code leaves `queued=1` and emits no reply; without a sibling it already
   answers correctly. Checkout cannot lend the idle sibling while open, so suppressing
   refusal because `Usable()` sees it is wrong. Both cases now receive `53M03` in the
   trial-failure event and leave no queued request. Opening/re-opening flushes queued
   requests regardless of remaining connections. The old repair test's expectation of
   no refusal while an idle sibling remains was changed to require refusal; that was
   the same mistaken contract, not independent behavior to preserve. Requests already
   holding a lease are not part of this queue flush.

4. **MINOR, Allow snapshot: real; fixed in `c0fa331`.**
   `TestArbitrationAllowRefreshesBreakerSnapshot` fails before the fix with actual
   breaker half-open, snapshot breaker 1 (open), and transitions `[1 0 0 0]`.
   Allow now copies the breaker state and transition counters after ticking it.
   This is constant work and avoids scanning all connections with `gauges()` on the
   admission path. Allow still has no action output; action-returning operations expire
   connection deadlines, and the engine fires due timers before admission. This API
   distinction is now explicit in the knowledge note.

5. **MINOR, post-reap success assertion: real; fixed in `c1d2a81`.**
   `TestSlotsHeldCheckRejectsFailedLateClient` first runs the real slots-held scenario
   successfully, then substitutes `53M03` for every late-client reply and places another
   client's successful result after the check's 3.3-second threshold but before reap.
   The old checker accepts these results; the fixed checker rejects them. This is a
   mutation of the checker input, not a claim that the stock scenario currently fails
   to recover. It establishes that the purported recovery assertion was insufficient.
   Independently, the review's suggested `allSteps` alone is also insufficient: that
   helper accepts an ErrorResponse followed by ReadyForQuery. The fix requires all
   expected late-client steps, no error/closed tokens, and a successful SELECT tag.

## Other inspection and scope

Inspected the briefs, both reviews, repair rulings, notes and adversarial settlement,
plus the pool/model, engine integration, config, shell, observability and scenario code.
No additional claim-breaking product defect was established. The previous narrowing
exceptions were not reopened, and non-trial idle-check failures still do not open the
breaker. The model repair does not weaken its suspect assertions.

`9385020` updates `docs/knowledge/pool-core.md` with the dispatch, breaker, Allow and
no-reply distinctions for subsequent builders. No reusable script was needed. No Go
comments or dependencies were added. PLAN.md, STATE.md, agent_work/INDEX.md and the
adversarial harness were not edited; the specific arbitration brief overrides their
usual maintenance requirements. No push, merge or rebase was performed.

## Verification

All product changes were committed before the required verification. Build, vet and the
full requested test set started on clean `c1d2a81`; the later `9385020` changes knowledge
prose only. The gate started on clean `9385020`.

- Reproduction command: `go test -count=1 ./internal/proxy ./internal/pool ./test/e2e
  -run 'Arbitration|TestSlotsHeldCheckRejectsFailedLateClient'`. Failed on the old code
  for all five findings. The stale preparation case was rerun after correcting its
  setup (the helper's single failed spare dial must not open its breaker), with the old
  corepool.go and lease.go restored; both preparation cases failed the engine invariant.
- `go test -count=1 ./internal/proxy ./internal/pool ./test/e2e
  -run 'Arbitration|TestSlotsHeldCheckRejectsFailedLateClient|TestCore'`: PASS after fixes.
- `go build ./... && go vet ./... && go test -count=1 ./internal/... ./test/...
  ./agent_work/adversarial/c07-pool/...`: PASS, exit 0. In particular, simrun 299.697 s,
  e2e 234.127 s, c07 adversarial 12.976 s. No wall-clock growth test failed in this run.
- `go test ./internal/pool -run '^$' -fuzz '^FuzzPoolOps$' -fuzztime 30s`: PASS,
  188,693 executions, 31.127 s including startup/shutdown; no planted bug enabled.
- `TMPDIR=/tmp/zgw-c07-arb scripts/fuzz-plant.sh`: PASS, both plants found inside
  their 20-second budgets: engine queue-slot 18 s, pool reset-clears-suspect 5 s.
- `GATE_ONLY=G0,G1,G2,G3 scripts/gate.sh c07-pool`: PASS, exit 0. G0 build/vet/format/tools/tidy,
  G1 all lints, G2 comments, G3 unit (274 s) and race (26 s, four package patterns)
  all passed. No wall-clock growth failures. The result correctly says
  `counts_for_landing=false` for this requested partial gate.
- `git diff --check`: PASS. Final worktree clean; all arbitration artifacts committed.

This is the partial gate requested by the brief, not a full landing gate; the
orchestrator still owns full gate execution and landing.

All reproduction and test logs were written to `/tmp/zgw-c07-arb/`; their outcomes are
recorded here and the scratch directory was removed at completion. Knowledge-store
searches (ordinary, --global, and explicit --root) could not run because this environment
has no configured kb location. They returned no search IDs, so feedback could not be
recorded. No knowledge-store corpus was read or changed.
