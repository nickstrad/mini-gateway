# Chunk 12 — adversarial record (Astra, gpt-6-astra)

Astra's first run was ended by its provider's content filter; the one re-dispatch finished
from the files it left and reported. First pass, against the build tip `401f883`:
**HELD 43, BROKEN 5** (48 named attacks, at least three for each of the 15 claims;
`agent_work/adversarial/c12-retry/RESULTS.md`).

After the one repair round (tip `edd18a1`), re-run by the orchestrator with `ADV_LONG=1`:
**45 pass, 3 settled by the orchestrator**.

Fixed:

- A retry granted while the breaker was closed was sent after the breaker opened
  (`TestAdv_6_BreakerReopensBeforeQueuedRetryRuns`, shown with a stub breaker). A granted
  retry is now checked again whenever a connection could be lent to it; with the breaker
  open the client gets `53M03` and the retry is counted under its own end, `breaker`. The
  token is not returned: the grant stays counted as a retry. In the shipped wiring the
  schedule could not occur — the pool core flushes the queue the moment its breaker opens,
  which a test through the real pool core holds.
- Two rows of the generated fault matrix said "yes, once" for faults that are never retried
  (`TestAdv_10_MatrixExplicitNeverPaths`; also review 1 finding 2). Each row now names why
  it is not retried, the merged deadline row is split, and a test holds that exactly one
  row is retried.

## Narrowed claims (accepted by the orchestrator)

Each is written in full in NOTES.md under "Narrowed claims"; the attack tests are skipped
with a pointer here.

- **Claim 5** (`TestAdv_5_CarriedCreditExceedsWindowFloor`,
  `TestAdv_5_QuietHourRetainsBurstCredit`). As written: in any window, retries are at most
  10% of first attempts plus the floor. As narrowed: over any window, ten times the retries
  is at most the first attempts in the window plus the credit carried into it, which is at
  most the cap. Stored credit does not expire with time, so the largest burst of retries
  after a quiet period is the cap (the attack: 15 attempts and 15 retries after a quiet
  hour). The capacity table's retry row states the cap as the largest retry burst. This
  narrowing is listed in STATE.md for Nick's veto: the alternative is credit that expires.
- **Claim 7** (`TestAdv_7_WaitingRetryConsumesQueueSlot`). As written: a waiting retry
  occupies neither a queue slot nor a connection. As narrowed: it holds no database
  connection; it does hold one place in its tenant's admission queue (the brief's
  one-waiting-place decision).
