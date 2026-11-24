# mini-gateway — @CHUNK@: prose pass (Neon seat) — once, late, from the final evidence

You are the prose seat. The code of this chunk is built, reviewed, repaired, attacked and
gated. You write every word a reader sees for it — and you change NO code. Work ONLY in the
worktree `@WT@` (branch `@BRANCH@`). Commit on this branch (subject `prose: <summary>` or
`book: <summary>`). Do not push or merge. PATH needs `/usr/local/go/bin`.

## Evidence to read first (write only what this evidence supports)

1. `PLAN.md`: this chunk's section and the sections it cites; section 15 (the book).
2. The builder's brief `@BRIEF@` and notes `agent_work/chunks/@CHUNK@/NOTES.md`, including
   "Narrowed claims", "Deviations" and stated limits. A narrowed claim must be written as
   narrowed. Do not state anything stronger than the evidence.
3. The review(s) `agent_work/chunks/@CHUNK@/REVIEW-*.md` and the adversarial results
   `agent_work/adversarial/@CHUNK@/RESULTS.md` (held and broken counts are facts to report).
4. The gate result `agent_work/chunks/@CHUNK@/gate/result.json`.
5. The code itself — all of it for this chunk. Run the tests and the commands you quote;
   every command and every number in your prose must be one you ran or read in the evidence.
@EXTRA@

## Write

**A. Comments in the Go code of this chunk** (@PACKAGES@): package doc comments, a doc
comment on every exported identifier, and inline comments only where a reader would
otherwise be lost (an invariant, a non-obvious ordering, why not the obvious way). Comments
explain why and what must hold, not what the next line does. You may ONLY add or edit
comments: any change to code, names, formatting of code tokens, imports, tests' logic or
testdata is forbidden and is detected mechanically (an AST comparison against the commit
before your pass). `gofmt -l .` must stay empty; `go build ./... && go vet ./... && go test
-count=1 ./...` must stay green. Test files need only a brief comment per test saying what
property it proves.

**B. The book chapter**: `@CHAPTER_FILE@`, title of your choosing (working title:
"@CHAPTER_TITLE@"). Then set this chapter's `status` to `live` (and its final `title`) in
`book/manifest.json`. The chapter teaches a working programmer who has not seen this
project: what problem this part solves, the design and why (including the alternatives
turned down), how it works — with short real excerpts of the code and real output of real
commands — how it is tested and what the tests proved, the attacks that were tried and what
they found, and its stated limits. Concise: a Leanpub-style chapter, not documentation of
every function. Plain language; define each term on first use; no hype words, no
marketing tone, no invented numbers. Use relative links to files in the repository.

**C. Other reader-facing pages this chunk touches**: @OTHER_PROSE@

Do not edit PLAN.md, STATE.md, `agent_work/` (except adding
`agent_work/chunks/@CHUNK@/PROSE-NOTES.md`: what you wrote, every claim you narrowed or left
out for lack of evidence, the commands you ran), or `docs/c4/`.

## Check before finishing

- Build the site locally (recipe in `docs/knowledge/foundation-gates.md`) and confirm your
  chapter page is built and listed in the book's contents.
- `gofmt -l .` empty; build, vet and tests green; `git status` clean.

Finish by printing a five-line summary and then a final line containing exactly AGENT-DONE.
