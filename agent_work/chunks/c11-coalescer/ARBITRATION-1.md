VERDICT: FAIL

Interim ruling, 2026-11-28: REVIEW-4 finding 1 is real. The new
TestRepairHeldHandoffsPreserveTheOriginalArrival reproduces it in all four
combinations of disconnect/cancel and one/two held-owner handoffs. With the
leader arriving at 10 ms, an earlier follower at 20 ms, newest followers at
180/200 ms, promotion at 250 ms and zero-byte refusal at 450 ms, all four
incorrectly grant one retry (statement timeout 400 ms, flight deadline 800 ms).
The original leader's deadline is 410 ms and the earlier follower's is 420 ms.
This violates the join's original-arrival retry bound. A local repair and the
remaining verification are pending; this interim FAIL must not authorize landing.

Finding 2 is also real on inspection: opsRig.dbWrite has no open-breaker read
oracle, and gate/extra.sh omits TestRepair. Existing deterministic tests cover
both promotion paths, but the randomized test's historical seed-63 failure
cannot establish the claimed breaker property.

Repair checkpoint: `9ce09dd` keeps the four failing reproductions; `90c0785`
copies the held owner's original arrival to its successor and adds repeated
queued/active retry handoff coverage (disconnect and cancel). Both new tests
pass. `3ea7c20` adds an event-level oracle: while the custom breaker is open,
every promotion must increment the refused-promotion count in that event. The
M-R3 mutation now fails at seed 2 with the explicit promotion assertion, with
the arrival fix present. All TestRepair tests passed before the additional
repeated-handoff test; that additional test also passed independently.
The gate filter now includes TestRepair and NOTES corrects the seed-63 claim.
Full required verification is pending; the interim verdict remains FAIL until it
finishes.
