# mini-gateway — @CHUNK@: repair round (Opus seat)

You are a fresh builder doing the ONE repair round for this chunk. Work ONLY in the
worktree `@WT@` (branch `@BRANCH@`). The rules in
`/root/briefs/zgateway-p2-builder-rules.md` bind you (no comments in Go, commits, no push).

## Read

1. The original brief: `@BRIEF@`.
2. The builder's notes: `agent_work/chunks/@CHUNK@/NOTES.md`.
3. The review: `agent_work/chunks/@CHUNK@/REVIEW-@ROUND@.md` — every finding.
4. The adversarial results: `agent_work/adversarial/@CHUNK@/RESULTS.md` and the failing
   tests beside it — every BROKEN attack.
@EXTRA@

## Do

- For every review finding of severity BLOCKER or MAJOR, and every BROKEN attack: first
  reproduce it, then either FIX the code (with a regression test in the product test suite
  that fails before the fix) or, where the finding is wrong, show why with evidence. Where
  an attack shows the CLAIM was too broad and the behaviour is right, do not bend the code:
  write the narrowed claim precisely in NOTES.md under "Narrowed claims" (the orchestrator
  decides whether to accept it).
- MINOR findings: fix those that are cheap and safe; list the rest as declined with a reason.
- Do not edit files under `agent_work/adversarial/` (the adversary's tests are evidence);
  if an adversarial test itself is wrong (it asserts something no claim says), say so in
  NOTES.md with the reasoning.
- Add a section "Repair round" to NOTES.md: one line per finding and per broken attack —
  fixed (commit, regression test) / narrowed / disputed (why) / declined (why).
- Then the usual finish: `go test -count=1 ./...` including the adversarial package,
  `scripts/gate.sh @CHUNK@` green at the build stage on a clean tree, summary, and a final
  line containing exactly AGENT-DONE.
