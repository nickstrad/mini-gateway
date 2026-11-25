# Chunk 2 — adversarial record (Astra, gpt-6-astra)

First pass, against the build tip `66e52dc`: **HELD 29, BROKEN 5** —
`agent_work/adversarial/c02-sim-world/RESULTS.md`.

| Broken attack | What it showed | Outcome |
| --- | --- | --- |
| `TestAdv_2_StatementLimitErrorAbortsTransaction` | An error from the row limit inside a transaction did not put the transaction in the failed state | Fixed in the repair round |
| `TestAdv_3_FullTableCommitMustNotLogPhantom` | A commit refused by a full table still logged writes | Fixed (the review's blocker 1 is the same defect) |
| `TestAdv_5_StallHitsAlreadyServing` | The stall window missed statements already in service | Fixed (the review's blocker 2) |
| `TestAdv_7_SendWindowIndependentOfService` | With a slow server, requests are not put on the wire at their intended times | **Claim narrowed** (below) |
| `TestAdv_11_AllGoImportsIncludingTests` | Go files outside the wire packages import `pgproto3` | Fixed outside `tools/`; **claim narrowed** for `tools/` (below) |

After the one repair round: **HELD 32, BROKEN 0, NARROWED 2** (orchestrator's run,
2026-11-25: `go test -count=1 ./agent_work/adversarial/c02-sim-world/...`).

## Narrowed claims (accepted by the orchestrator)

**Claim 7.** As written: "the number of requests sent in a window equals the intended number
regardless of server speed". As narrowed: arrivals follow the schedule whatever the server
speed; every arrival is eventually sent; each request's latency is measured exactly from
its intended send time (so a stalled server shows up in latency — no coordinated omission);
the wire send of a request waits for the previous reply on that client connection. Reason:
a simulated client is one Postgres session, and a request can be a multi-statement
transaction; a later request written to the wire during it would run inside that
transaction. This is also what decision D5 gives the proxy (one request per client
connection). Proof: `TestOpenLoopArrivalsIgnoreABusyClientAndLatencyIsExact`.

**Claim 11.** As written: `pgproto3` is imported only in `internal/sim/fakepg/...` and the
test-client package. As narrowed: that holds for every Go file outside `tools/`. The import
lint's own test fixtures under `tools/detlint` must import `pgproto3` to test the rule.
Side effect: the Phase 1 probe `agent_work/phase1/probes/pgwire/main.go` was renamed
`main.go.txt` (it is a nested module, never compiled with the product).

The two attack tests carry a `t.Skip` naming this file; the skip lines are the only edit the
orchestrator made to the adversary's code.
