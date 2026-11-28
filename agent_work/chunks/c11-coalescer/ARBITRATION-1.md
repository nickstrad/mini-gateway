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
