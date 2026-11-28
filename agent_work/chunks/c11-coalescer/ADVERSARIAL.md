# Chunk 11 — adversarial record (harness by Astra, gpt-6-astra; report assembled by the orchestrator)

Astra wrote the harness: 57 named attacks over the builder's claims, plus long campaigns
behind `ADV_LONG=1`. Both of its runs (the first, and the one re-dispatch) were ended by its
provider's content filter after the harness was complete and had been run, and before a
report was written. The orchestrator ran the harness unchanged against the build tip
`dcf0c48` and wrote `agent_work/adversarial/c11-coalescer/RESULTS.md` from the tests' own
output, marked as such. First pass: **HELD 51, BROKEN 6**. The judgment of each failure
below is the orchestrator's, not Astra's; review 2 is asked to check it.

After the one repair round (tip `d237847`), re-run by the orchestrator with `ADV_LONG=1`:
**53 pass, 4 settled by the orchestrator**.

Fixed product defects:

- Two different (tenant, text) pairs could be treated as one flight: the table joined
  tenant and text with a single zero byte (`TestAdv_3_TenantDelimiterCollision`). The key
  now begins with the tenant's length; a property test and the fuzz target
  `FuzzShareKeyIsUnambiguous` hold it.
- A flight deadline computed near the largest instant wrapped into the past
  (`TestAdv_8_DeadlineOverflow`). Deadlines and no-share expiries saturate at the largest
  instant.

## Narrowed claims (accepted by the orchestrator)

Each is written in full in NOTES.md under "Narrowed claims" and held by a test there; the
attack tests are skipped with a pointer here.

- **Claim 1** (`TestAdv_1_CancelAcknowledgementGap`, `TestAdv_1_NoShareBypassOverlap`,
  `TestAdv_1_FullTableDuplicateBypass`). As written: at most one in-flight read per key.
  As narrowed: at most one read per key is in flight among the flights the coalescer owns.
  Three cases are outside it and each is counted: a read whose last waiter left and whose
  cancel has not yet finished on the database (`flight_ends{cancelled}`); a key on the
  no-share list (`passed{no-share}`); a request arriving while the flight table is full
  (`passed{full}`).
- **Claim 6** (`TestAdv_6_ResultBeforeCancelTie`). As written: a caller that has left gets
  its own error even if a result arrives at that moment. As narrowed: at one instant the
  order of events decides — a result applied before the waiter's leave is delivered to that
  waiter; a leave applied first gets nothing of the result.
