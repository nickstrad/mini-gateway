# Chunk 8 — adversarial record (Astra, gpt-6-astra)

Astra's first run was ended by its provider's content filter after the harness was written
and before any report; the one re-dispatch finished from the files it left and reported.
First pass, against the build tip `78510fa`: **HELD 46, BROKEN 6** (52 cases, 15 claims).

After the one repair round (tip `40cc0a6`), re-run by the orchestrator:
**51 pass, 1 settled by the orchestrator**.

Fixed product defects:

- The folded `other` statistics row (more than 4,096 tenants seen) was checked against one
  tenant's capacity, so valid queues failed the queue's own check
  (`TestAdv_1_ChurnBeyondCounterCap`, `TestAdv_8_FoldedStatsConservation`; also review 1
  finding 1). There is one `other` row now, bounded by the total capacity.
- The pending-shed buffer grew without bound when a consumer drained only part of it
  (`TestAdv_1_PartialShedDrainMemory`). It is allocated once at 2 × capacity + 2.
- A request handed back by `Unpop` kept its enqueue time but lost the rule state, so its
  deadline moved (`TestAdv_6_UnpopPreservesDeadline`; also review 1 finding 6).
- The head-to-head table script broke on a path with an apostrophe
  (`TestAdv_13_QuotedOutputDirectory`).

## Narrowed claims (accepted by the orchestrator)

- **Claim 4** (`TestAdv_4_BurstyTenfoldLoad`, skipped with a pointer here). As written: at
  tenfold load the queue rules keep at least 95% of capacity served. As narrowed: for
  arrivals spread at a constant rate, closer together than the target (the benchmark's
  pattern, about one every 0.55 ms). For bursts separated by gaps longer than the allowed
  wait, expired work cannot fill the following service slots (the attack measured 10%).

## Disputed by the repairer, left to review 2

- Review 1 finding 4, the `FollyRule` half: the comparison arm copies folly's code, which
  has the same zero-length first window; the load gauge half was fixed.
