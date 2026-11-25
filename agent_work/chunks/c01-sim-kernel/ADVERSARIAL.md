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
