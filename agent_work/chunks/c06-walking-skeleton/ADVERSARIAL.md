# Chunk 6 — adversarial record

## Edits to earlier harnesses by the orchestrator

- `agent_work/adversarial/cr01-shrink-fuzz-rules/adv_test.go` pins the number of fuzz
  targets so that a new one is noticed. Chunk 6 adds `FuzzEngineOps` (`test/e2e`). The
  orchestrator reviewed it against PLAN.md section 6.6 on 2026-11-26: its simulator seed
  is the constant `OpSeed`, not derived from the input; decoding is total, bounded
  (`MaxOps`) and local (`TestOpDecodingIsTotalBoundedAndLocal`); one execution's cost is
  bounded (`TestFuzzEngineOpsCostIsBounded`). The pinned number is now 15 simulator-level
  targets, and `test/e2e` is added to the directories whose fuzz entry points are checked
  for hash calls.

## Astra's pass (gpt-6-astra)

First pass, against the build tip: **HELD 43, BROKEN 3** —
`agent_work/adversarial/c06-walking-skeleton/RESULTS.md`. (The first run was cut by its
provider's content filter; the one re-dispatch finished the harness and wrote the report.)

After the one repair round: **44 attacks pass, 2 settled by the orchestrator**.

Fixed product defects: bytes left in the same read after the reset's `ReadyForQuery` were
accepted and the connection lent (`TestAdv_3_ResetWithTrailingReply`; the review's second
blocker was the same fault at the end of a lease) — any leftover byte now closes the
connection; a reset reply arriving at the instant of its deadline was accepted
(`TestAdv_3_ResetDeadlineTie`) — every engine deadline now follows one rule: timers due
at or before an event's time fire before the event.

## Narrowed claims (accepted by the orchestrator)

- **Claim 13** (`TestAdv_13_ExplicitZeroCapacity`, skipped with a pointer here). As
  written: caps are positive and validation fails closed. As narrowed: a cap that is zero
  or omitted takes its default; a negative cap is refused naming its key. The generated
  capacity document says so for each cap.

## An attack the repair made wrong (settled by the orchestrator)

- `TestAdv_5_ZeroAcceptedBytes` (held at first) treated a database close with no write
  report as "zero bytes accepted" and expected `reached: No`. Review 1 finding 3 showed
  that in the shell a close can overtake the writer's report, so silence proves nothing;
  the ruling is that a request handed to the driver may have reached unless the driver
  reports a zero-byte refusal. The corrected form is the product test
  `TestWriteRefusedWithZeroBytesIsNotReached`.

## A gate script change by the orchestrator

The first final-stage gate run failed with "no space left on device". Every real-server
run removed its Postgres containers with `docker rm -f`, which leaves each container's
anonymous data volume behind: 279 such volumes (16 GB, all Postgres 17 data directories
created 2026-11-24 to 26, none attached to a container) had built up on vm-1. The
orchestrator removed exactly those, and `scripts/tier-r.sh`, `scripts/chaos.sh` and
`test/e2e/realenv.go` now remove containers with their volumes (`docker rm -f -v`). The
adversaries' own scripts under `agent_work/adversarial/` still use the old form.
