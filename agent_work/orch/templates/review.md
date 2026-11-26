# mini-gateway — @CHUNK@: code review (Neon seat) — review round @ROUND@

You are a fresh, independent reviewer. You review a build made by a different model (Opus).
Work in the current directory, the git worktree `@WT@`, checked out at the build's tip
(`@TIP@`). Change NO file in the repository: you may run anything, and write scratch files
only under `/tmp/zgw-@CHUNK@-review@ROUND@/`. Your one output is the review file
`@OUT@` (outside the repository; write it with your file tool).

## What to read

1. The builder's brief: `@BRIEF@` and the common rules `/root/briefs/zgateway-p2-builder-rules.md`.
2. `PLAN.md`: the chunk's section, section 6.2 (testing rules), 7.6 (observability landing
   gate), 10.3 (the *Release It!* checklist), 12.3 (the landing gate).
3. The builder's notes: `agent_work/chunks/@CHUNK@/NOTES.md`.
4. The diff: `git diff @BASE@..@TIP@` (and `git log @BASE@..@TIP@`). Read ALL product code
   in the diff line by line, and the tests.
@EXTRA@

## What to do

- Build and test it yourself (PATH needs `/usr/local/go/bin`): `go build ./... && go vet ./...`,
  `go test -count=1 ./...`, and the chunk's own commands from NOTES.md. Run a few seeds beyond
  the pinned ones (`DST_SEED=<n>` / `scripts/sweep.sh -seeds 50` where it exists).
- Check every deliverable of the brief exists and does what the brief says. Check every
  test the brief lists exists and actually asserts the property (a test that cannot fail is
  a finding: try to break the code in a scratch copy under /tmp and see whether a test
  notices — do this for at least five important properties and report which).
- Hunt for real defects: wrong logic, missed edge cases, nondeterminism (map order, time,
  randomness), unbounded growth, exit paths that skip a counter or leak a resource, claims
  in NOTES.md that the code does not keep, tests that assert less than they say.
- Answer the *Release It!* checklist of PLAN.md section 10.3, one line per anti-pattern,
  for the code of this chunk ("not applicable here because …" is a valid answer).
- Check the observability gate (section 7.6) for this chunk: `Snapshot()` present, counters
  move in a test, conservation asserted.
- Fuzz targets (PLAN.md section 6.6): a target that runs the simulator must take its seed from a
  fixed value or a dedicated prefix of its input, never from a hash of the input. A target
  that breaks this is a MAJOR finding.
- Do not review style, naming taste, or the absence of comments (comments are forbidden at
  this stage by project rule and are written later by another seat).

## Output format — `@OUT@`

Line 1 is exactly one of: `VERDICT: PASS`, `VERDICT: NITS`, `VERDICT: FAIL`.
- FAIL: at least one defect that makes a claim of the brief false, a missing deliverable or
  listed test, a test that cannot fail for an important property, or a gate rule broken.
- NITS: only small issues that do not falsify a claim.
- PASS: none.
Then:
- `## Findings` — numbered; each with severity (BLOCKER / MAJOR / MINOR), file:line, what
  is wrong, how you know (the command or the reasoning), and what a fix must achieve.
- `## Mutation checks` — the properties you tried to break and whether a test caught it.
- `## Release It! checklist` — one line per anti-pattern.
- `## Observability` — the 7.6 items.
- `## What I ran` — commands and results (counts, times).
Be concrete and verifiable; no praise, no summary of what the code does.

When the file is written, print its first line and then a final line containing exactly
AGENT-DONE.
