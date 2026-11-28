# Chunk 9 — adversarial record (Astra, gpt-6-astra)

First pass, against the build tip `27970a5`: **HELD 37, BROKEN 12** (49 named checks, at
least three for each of the 15 claims; `agent_work/adversarial/c09-fairness/RESULTS.md`).
Astra judged all twelve as claims worded too broadly. The orchestrator judged one of them a
product defect.

After the one repair round (tip `7e01a1e`), re-run by the orchestrator with `ADV_LONG=1`:
**38 pass, 11 settled by the orchestrator**.

Fixed product defect:

- With the shares rounded down so that they sum to less than the limit, the last
  connection stayed idle for good while tenants waited (limit 3, two equal tenants:
  `TestAdv_3_RoundedRemainderIdles`; also review 1 finding 1). The last free connection is
  now held back only while some active tenant is below its share; otherwise it is lent by
  round robin. Claim 3 states exactly when a connection may idle.

## Narrowed claims (accepted by the orchestrator)

Each is written in full in NOTES.md under "Narrowed claims" and held by a test there; the
attack tests are skipped with a pointer here.

- **Claim 4** (`TestAdv_4_ContinuousBetterGroup`). As written: every waiting tenant is
  chosen within a bounded number of rounds. As narrowed: the bound is within a group; a
  tenant at or above its share may wait for as long as tenants below their share keep
  arriving, until one of its own leases ends or the queue rule sheds the request.
- **Claims 5 and 6** (`TestAdv_5_ShortQuietDuringLongRead`, `TestAdv_5_QuietAtLeaseBoundary`,
  `TestAdv_5_QuietEarlyInFlood`, `TestAdv_6_ShortQuietLongFlood`, `TestAdv_6_OddPoolRounding`,
  `TestAdv_6_VeryLongFloodLeases`). As written: the quiet tenant's p99 stays within twice its
  standalone value and over 99% of its requests are served. As narrowed: the quiet tenant's
  added wait is at most one flooder lease; the factor of two and the 99% hold for the
  scenario shapes written out in NOTES.md. Quiet requests begin to be shed once a flooder
  lease reaches the allowed queue wait (none at 90 ms, 2 of 8 at 150 ms, against 100 ms).
- **Claim 7** (`TestAdv_7_TotalQueuePressure`). As written: every quiet tenant keeps its
  standalone outcomes beside a flooder. As narrowed: while the quiet tenants' own combined
  demand stays within capacity (in the scenario: at most 18 quiet requests in the proxy at
  once). 120 quiet tenants arriving together exceed it.
- **Claim 8** (`TestAdv_8_NewTenantShrinksShare`, `TestAdv_8_LimitReductionWithHeldLeases`,
  `TestAdv_8_HeldPeerChangesShare`). As written: a tenant holding transactions open never
  exceeds its share. As narrowed: there is no preemption; the share bounds new lends, and
  leases already held when a tenant joins or the limit falls run until they end.
