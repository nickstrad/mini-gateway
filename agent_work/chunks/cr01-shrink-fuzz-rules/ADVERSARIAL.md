# CR-1 — adversarial record (Astra, gpt-6-astra; report assembled by the orchestrator)

Astra wrote the harness (24 attacks, all seven claims) but its run was stopped twice by its
provider's content filter before it reported; the second run was the one re-dispatch the
protocol allows. The orchestrator ran the tests unchanged and wrote
`agent_work/adversarial/cr01-shrink-fuzz-rules/RESULTS.md`, marked as such.
First pass, against the build tip `1179f92`: **HELD 22, BROKEN 2**.

After the one repair round: **23 attacks pass, 1 settled by the orchestrator**.

Fixed product defect: `scripts/fuzz.sh` took a rate from an earlier progress line when the
last one was unreadable (`b8e9aca`).

Explained and fixed: the builder's open flake. One `FuzzWorldSchedule` input passed but
took 13 seconds, so a timed run cut it and Go reported a hung worker. The target now caps
the decoded bloat row size (`5fbf999`); the input is a committed seed.

## Narrowed claims (accepted by the orchestrator)

- **Claim 5** (`TestAdv_5_UnboundedScheduleClaim`, skipped with a pointer here). As
  written: the text form round-trips every schedule. As narrowed: every schedule of at most
  `MaxScheduleLen` (1,024) faults; a longer one is refused by `FormatSchedule` and by
  `ParseSchedule`. Bounded input is the house rule.

## Open

- Whether an adversarial report assembled by the orchestrator from an unfinished Astra run
  is enough for CR-1 is recorded in STATE.md as a question for Nick.
- G5 can fail on a loaded host with Go's own `context deadline exceeded` at the end of a
  target's time, with no failing input. `fuzz.sh` fails closed on it; the gate is re-run.
