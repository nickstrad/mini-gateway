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
