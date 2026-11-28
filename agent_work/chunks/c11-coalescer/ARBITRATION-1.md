VERDICT: PASS

Arbitration of REVIEW-4, 2026-11-28. Decision (c) stands: one zero-byte-loss
retry for the whole flight. Both findings are repaired locally and all required verification passed.
This is the final ruling on the join after those repairs.

## Finding 1 — real, repaired

The held owner has phase cpCoalesced, just like a follower. coalLeave returned
without preserving its request arrival after Table.Leave selected the newest
follower. Queued and active owners take coalHandoff, which transfers the entire
request; held owners never reached that transfer.

I reproduced the defect before changing product code in
TestRepairHeldHandoffsPreserveTheOriginalArrival. The running read starts at
relative time 0; held leader 11 arrives at 10 ms, earlier follower 12 at 20 ms,
and newer followers 13/14 at 180/200 ms. At 210 ms the owner disconnects or
cancels; the repeated-handoff variants also remove successor 14. Promotion is
at 250 ms and zero-byte refusal at 450 ms. Statement timeout is 400 ms, flight
deadline 800 ms. All four variants failed with RetryAsked=1, RetryPending=1:
the original leader's 410 ms deadline and earlier follower's 420 ms deadline
had passed. Granting that retry violates the original-arrival rule and permits
the late statement described by the reviewer. The test now requires no retry,
no new read and an 08006 answer to the earlier follower. All four pass.

- `9ce09dd`: failing regression and early interim ruling committed.
- `90c0785`: five-line local change in coalLeave copies arrived to a held
  successor before processing notices. The successor keeps its own request
  identity and tenant, since this flight has not entered any tenant queue yet.
  Repeated handoffs carry the same original instant forward.
- That fix commit also adds TestRepairRepeatedRetryHandoffsKeepTheAttemptAndTenant:
  a first refusal grants one retry, then two cross-tenant owner departures occur
  while it is queued or active, by disconnect or cancel. It checks the queued
  tenant, the lease tenant and that a second refusal cannot grant another retry.
  All four variants pass.

## Finding 2 — real, repaired

The original randomized harness had deadline and conservation assertions but no
per-event assertion that an open-breaker promotion was refused. Its historical
M-R3 failure at seed 63 exposed finding 1, not the claimed breaker property.
The deterministic custom-breaker and pool-core-flush tests remain valid direct
regressions. The gate extra's filter did omit TestRepair.

`3ea7c20` adds an opsRig.ev oracle: whenever the custom breaker is open, every
promotion in that event must also increment CoalescePromotedRefused. Thus a
promotion cannot silently queue or forward. I removed the coalStart breaker
branch temporarily with the arrival repair present: TestRepairRandom failed at
seed 2 with "a held flight promoted at an open breaker was not refused".
The branch was restored. The full 400-sequence test passes with the fix.
The same commit includes TestRepair in extra.sh and corrects NOTES.md.

I rejected an overbroad candidate assertion against every read write: an
ordinary first attempt admitted before the custom breaker opens can still
forward (seed 1). The final assertion specifically checks promotions, the
contract at issue, and does not redefine ordinary first-attempt scheduling.

## Other ownership paths

| Path | Arrival, charge and attempts | Evidence |
| --- | --- | --- |
| Held owner disconnect/cancel; repeated held handoffs | Now carries the original arrived. No queue or lease exists yet, and attempt is zero. Promotion uses the surviving owner's tenant for its first queue entry; enq moves to promotion time for queue-wait measurement. | New held-handoff regression, both departure kinds and repeated handoffs; coalJoin, coalStart and coalLeave inspection. |
| Queued owner disconnect/cancel | coalHandoff transfers old.req, retaining arrived, tenant and attempt. waiting[req.id] changes client; queue timer retains its deadline. No new queue place. | Existing TestJoinAQueuedOwnerHandoffAcrossTenantsUnderABindingShareKeepsTheChargeOnTheQueuedTenant; new repeated queued-retry handoff variants. |
| Active owner disconnect/cancel | Same request transfer; db.client changes but db.lastTenant does not. The lease ends on the tenant it was lent to. Retry re-enters req.tenant, and retains arrived and attempt. | Existing running-handoff tests for pool core and fixed set, existing TestRepairAfterAHandoffTheLeadersArrivalBoundsTheRetrySoNoFollowerGetsAStatementSentPastItsDeadline, new repeated active-retry variants. |
| Ordinary follower departure | LeftFollower removes only that member. Does not modify owner request or attempt. | Table.Leave inspection, existing coalescer leave tests and randomized engine sequences. |
| Last member leaves | LeftLast ends the flight. No successor state to copy; normal queued/active cleanup ends any pending retry and lease. | Existing coalescer leave/cancel tests, retry exit tests and randomized drain/Quiesce. |
| Flight/request deadline | Table.expire removes owner and followers together; it does not choose a successor. A running expired flight is blocked from retry by flightGone. Queued retries use retryClamp from arrived. A client closure caused by a client timer goes through closeClient and the same handoff paths above. | Existing running/promoted deadline tests at before/equal/after expiry, retry-expiry test and original-arrival handoff test; closeClient inspection. |
| Shutdown | stopping is set before client departures; those departures use the same transfers. coalStart refuses to promote while stopping, then remaining flight state is shut down. No reset of attempts or tenant charges. | TestShutdownSendsNothingNewToTheDatabase, TestJoinShutdownWithARetryPendingAndFlightsHeldSendsNothingNewToTheDatabase, orphan-flight shutdown test, shutdown and closeClient inspection. |

No additional ownership defect found. Held-flight tenant behavior differs
intentionally from queued/running behavior: there is no earlier queued-under
tenant to preserve. All previously charged flights retain that charge, even
across a second handoff. RetryCore ignores Failure.Tenant; its budget is global,
while admission and lease accounting explicitly use req.tenant/lastTenant.
No changes to Table.Leave, requestDeadline, retry policy or fairness are needed.

## Verification

Required commands ran on clean committed code at `9a62373` (repair commits
above plus the evidence checkpoint). Every command below exited 0.
Go was /usr/local/go/bin/go (go1.26.8); output was redirected to files.


| Command | Result |
| --- | --- |
| `go build ./... && go vet ./...` | PASS |
| `go test -count=1 -timeout 20m ./internal/proxy/... ./internal/coalesce/... ./internal/admission/... ./internal/retry/...` | PASS; proxy 15.034 s, admission 16.321 s; includes all 400 randomized sequences |
| `go test -count=1 -timeout 20m ./agent_work/adversarial/c09-fairness/... ./agent_work/adversarial/c11-coalescer/... ./agent_work/adversarial/c12-retry/...` | PASS; 3.452 / 2.714 / 0.362 s |
| `go test -count=1 -timeout 30m -run 'Coalesce' ./test/e2e/` | PASS; 182.064 s |
| `GATE_ONLY=G0,G1,G2,G3 scripts/gate.sh c11-coalescer` (once) | PASS, clean committed tree (`dirty_tree=false`), 1,048 s total: G0 tools/gofmt/build/vet/tools-vet/tools-test/tidy; G1 detlint/importlint/boundlint/paniccheck; G2 comments; G3 full unit 942 s, counted-growth 1 s, race 82 s. Partial by request, `counts_for_landing=false`; full landing gate remains the orchestrator's responsibility. |
| `go test -count=1 -run TestRepairHeldHandoffs ./internal/proxy` | FAIL in all four variants before fix, PASS after |
| `go test -count=1 -run TestRepairRepeated ./internal/proxy` | PASS in all four variants |
| `go test -count=1 -run TestRepair ./internal/proxy` | PASS after arrival fix and final promotion oracle |
| `go test -count=1 -run TestRepairRandom ./internal/proxy` with M-R3 temporarily applied | Expected FAIL at seed 2, direct promotion oracle; mutation restored before committed-tree validation |
| `git diff --check` | PASS |

The gate's repository-wide unit run also passed simrun (582.899 s), e2e
(729.368 s), all earlier adversarial packages and the remaining packages.
The required race run covered eight package patterns. No new Go comments or
non-standard-library dependencies were added.

Tracked evidence logs rewritten under agent_work/adversarial were restored with
git checkout. Generated gate logs/result and /tmp/zgw-c11-arb were removed after
recording the results. No edits to PLAN.md, STATE.md, the adversarial source,
INDEX.md, book/ or docs/c4/; no push, merge or rebase. Durable ownership guidance
was added separately to docs/knowledge/coalescer.md; no reusable script was
needed beyond the existing gate extra's filter correction.

Environment note: the initial required kb lookup and --global retry both failed
before creating a search ID because no kb location was configured. No search
result was used and no feedback ID existed; the task proceeded from its supplied
paths and repository evidence without accessing the knowledge corpus.
