# Change request CR-1 — value lowering in the shrinker, and the fuzz rules in the tooling

Filed by the Fable orchestrator on 2026-11-26 against the frozen simulator API (tag
`freeze-sim`, chunk 2). Authority: Nick's DST fuzz ruling of 2026-11-24
(`/root/briefs/zgateway-dst-fuzz-ruling.md`), items 2 and 3. The ruling said to file the
request at once and execute it only after the freeze; it was not filed on 2026-11-24
(wave A was in flight and the chain then stopped at chunk 5), so it is filed and executed
together now. PLAN.md sections 6.6 and 19.1 carry the rules.

## What is requested

1. **`internal/sim`: value lowering.** `sim.Shrink` deletes whole faults only. Add a pass
   that lowers the values of the faults that remain — `At`, `Arg1`, `Arg2` — toward their
   smallest still-failing values, and a general helper for lowering other integer inputs
   (workload sizes, operation lists) that chunk 6's operation fuzz target will use.
2. **Shrunk scenarios as files.** A found failure is kept as a readable scenario — the
   fault list (and later the operation list) as text that parses back — and runs as a
   plain test, instead of being kept as a pinned seed alone.
3. **`scripts/fuzz.sh` and the gate: the three rules of PLAN.md 6.6.** Executions per
   second reported per target; a rate floor of 100 per second for targets that run the
   simulator, below which a target is labelled a seed sweep; the existing simulator-level
   targets audited against "no seed from a hash of the input".

## What does not change (the freeze)

`sim.Shrink`'s signature and its results for every input; every existing exported
identifier of `internal/sim`; trace and state digests of every existing scenario (the
golden digests must not move). The change adds identifiers only.

## Status

| Step | State |
| --- | --- |
| Filed | 2026-11-26, this file |
| Brief | `BRIEF.md` beside this file |
| Build, review, attack, landing | see STATE.md |
