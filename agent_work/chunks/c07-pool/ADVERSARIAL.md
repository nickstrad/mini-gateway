# Chunk 7 — adversarial record (Astra, gpt-6-astra)

Astra's first run was ended by its provider's content filter after the harness was written
and before any report; the one re-dispatch finished from the files it left and reported.
First pass, against the build tip `70d8668`: **HELD 33, BROKEN 14** (47 cases, 15 claims).

After the one repair round (tip `a6e5058`), re-run by the orchestrator:
**42 pass, 5 settled by the orchestrator** (four tests under three narrowed claims, one
input that no longer exists).

Fixed product defects:

- The pool core applied an operation before expiring what was due at the same instant, so
  a reset, a check or a cleanup finishing exactly at its deadline survived, and a connection
  at its lifetime could be lent (`TestAdv_4_ResetDeadlineEquality`,
  `TestAdv_4_LifetimeEqualityCheckout`, `TestAdv_5_LateCheckSuccess`,
  `TestAdv_9_CleanupDeadlineDrain`; review 1 finding 4). One rule now, the same as chunk
  6's: time passes before the event.
- The creation-rate spacing rounded down for a rate that does not divide a second
  (`TestAdv_2_NonDivisorRate`).
- A failed check of an idle connection opened the breaker, also for an SQL error in the
  check (`TestAdv_6_OldIdleCheckRefusal`, `TestAdv_7_SQLFailureViaCheck`; review 1 finding
  8). Ruled by the orchestrator: such a failure closes that connection and does not count
  toward the breaker; the replacement dial decides. A half-open breaker now lets requests
  wait for its trial.
- A connection whose cancel never reported completion could be lent again
  (`TestAdv_10_LateCancelAfterExpiry`). It is closed when the cancel's wait expires.
- Jitter is drawn per nanosecond (`TestAdv_3_JitterCollisions` passes).

## Narrowed claims (accepted by the orchestrator)

- **Claim 1** (`TestAdv_1_CloseOutstanding`): a connection counts toward the cap until its
  close is issued, not until the driver confirms the close.
- **Claim 3**: jitter spreads expiries; it does not make them unique. The measured bounds
  are in NOTES.md.
- **Claim 6** (`TestAdv_6_RecentIdleBlackhole`, `TestAdv_6_RecentIdlePeerForgot`): a
  connection idle for less than `pool.check_after_idle` is lent without a check, so after
  a silent death the first statement on each such connection can fail once (`08006`). Later
  work is served.
- **Claim 8** (`TestAdv_8_ReseedSameTenant`): the re-seed accompanies a reset; under
  `on-switch` a same-tenant reuse runs neither, by the policy's definition.

## Settled

- `TestAdv_3_SubMicrosecondJitter`: a lifetime that jitter would spread by less than 1 ms
  is refused at validation, so the case cannot be built.

Three of the five skips are on tests written as one-line functions; the orchestrator
opened each into a block to insert the skip line, and `gofmt` realigned their neighbours.
No assertion was touched.

## For review 2

Two behaviour changes the repair made beyond the list: a half-open breaker queues requests
for its single trial; the `pool-slots-held` scenario's check no longer uses claim 6's bound.
