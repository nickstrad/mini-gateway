# Chunk 1 — adversarial record (Astra, gpt-6-astra)

First pass, against the build tip `19edca0`: **HELD 30, BROKEN 4** —
`agent_work/adversarial/c01-sim-kernel/RESULTS.md`. The four breaks were real product
defects in the simulated network, all found independently by the Neon review as well:
a writer blocked on a full window was not woken when the reader closed and discarded
(`TestAdv_4_ClosedReaderFreesBlockedWriter`); latency sums wrapped instead of saturating
(`TestAdv_4_LatencySumSaturates`, `TestAdv_3_RefusalRoundTripOverflow`); the maximum jitter
panicked (`TestAdv_4_MaximumJitter`).

After the one repair round (commits `571e020`..`e044034`): **HELD 34, BROKEN 0**
(`ADV_LONG=1 go test -count=1 ./agent_work/adversarial/c01-sim-kernel/...`, run by the
orchestrator on 2026-11-25). No claim was narrowed. The attack tests stay in the tree and
run with `go test ./...`.

## Scope note added at chunk 3 (orchestrator, 2026-11-25)

Chunk 1's claim 10 covered `internal/sim/...` and `internal/obs` as they were then: pure
measurement code. Chunk 3 adds the CSV flush adapter, `internal/obs/csvflush`, which by
design writes files and, in a real process, runs one goroutine with a bounded queue
(PLAN.md section 7.7). The two attack tests `TestAdv_10_AliasedEntropyAndClock` and
`TestAdv_10_ConcurrencyAndFloatingSyntax` walk every folder under `internal/obs`, so the
orchestrator added `csvflush` to the folders their walker skips (beside `simtest` and
`obstest`). The claim is unchanged for every package in `tools/lint-rules/det-packages.txt`,
which is what the determinism lint enforces.
