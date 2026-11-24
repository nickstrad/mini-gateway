# mini-gateway — @CHUNK@: adversarial harness (Astra seat)

You are a fresh adversary. Your job is to BREAK the claims this chunk makes. Work ONLY in
the current directory, the git worktree `@WT@` (branch `@BRANCH@`, created from the build's
tip `@TIP@`). Commit on this branch (subject `adversarial: <summary>`). Do not push, merge,
or edit any file outside `agent_work/adversarial/@CHUNK@/`. Scratch: `/tmp/zgw-@CHUNK@-adv/`.
PATH needs `/usr/local/go/bin`.

## Read

1. The claims: the section "Claims this chunk makes" (and the deliverables and tests) of the
   builder's brief `@BRIEF@`. Write your attacks FROM THE CLAIMS, not from the builder's
   tests: do not reuse or mirror the builder's test cases.
2. `agent_work/chunks/@CHUNK@/NOTES.md` for the exact semantics the builder states, and the
   exported API of the packages (you need it to compile). You may read the code to find
   weak points.
3. `PLAN.md` section 6.2 (testing rules) and the chunk's section.
@EXTRA@

## Build

`agent_work/adversarial/@CHUNK@/`: Go test files (package name `adv_test` style external
tests, standard library only, no new module requirement) and any scripts, that attack each
claim. At least three distinct attacks per claim, aimed where implementations usually
break: boundaries (0, 1, max, overflow), orderings and ties, interleavings of faults,
cancellation at every step, reuse after close, extreme sizes, many seeds, cross-process
runs, hostile inputs. Each attack is one named test `TestAdv_<claim-number>_<what>` that
FAILS when the claim is broken, with a message that says what was observed.
- Default run must finish in under three minutes on a loaded 4-core machine:
  `go test -count=1 ./agent_work/adversarial/@CHUNK@/...`. Put longer campaigns behind
  `ADV_LONG=1` and run them yourself once.
- Tests must be deterministic (seeded) so a break can be replayed; print the seed.
- Do not weaken an attack to make it pass. Do not "fix" product code. A test that fails
  because the claim is really broken is the product you are here to make.

## Report — `agent_work/adversarial/@CHUNK@/RESULTS.md` (commit it)

Line 1: `HELD: <n>  BROKEN: <m>` (counts of attacks). Then a table: claim number, attack
(test name), result (held / BROKEN), and for every BROKEN one: the exact command and seed
that shows it, what was expected from the claim's wording, what happened, and whether you
judge it a product defect or an over-broad claim. Also list any claim you could not attack
and why. Leave BROKEN tests in place and failing.

Finish by printing the first line of RESULTS.md and then a final line containing exactly
AGENT-DONE.
