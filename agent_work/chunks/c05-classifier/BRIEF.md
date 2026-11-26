# mini-gateway — chunk 5, re-opened: Statement classifier and the contracts — builder brief

Seat: Opus (product Go and tests, logic only). Chunk directory name: `c05-classifier`.
Worktree: `/root/Software/mini-gateway-work/c05r` — branch `work/c05r-classifier`.
You build ALONE: no other chunk is in flight, so you may edit shared files
(`agent_work/INDEX.md`, the lint rule files, `scripts/gen-docs.d/`) directly.

This is a REWORK, not a fresh build. Chunk 5 was built once (branch `work/c05-classifier`,
tip `d39ecfb`), failed its second review, and was parked while its scope was reconsidered.
Nick ruled on 2026-11-26 (`/root/briefs/zgateway-c05-ruling-reset-always.md`): the proxy
resets every database connection with `DISCARD ALL` after every lease, so **session safety
no longer depends on reading SQL**. That reset is built in chunks 6 and 7 — NOT here. What
it changes here is what the classifier is for.

Your branch already contains the first build, merged onto today's `main` (commit `5ca7559`),
with the first build's records moved to `agent_work/chunks/c05-classifier/parked/`. The tree
does not build its tests yet (see "The join").

Read, in this order, before writing anything:

1. `agent_work/orch/BUILDER-RULES.md` — the rules for every builder. They bind you; where
   this brief differs, this brief wins. One addition: landed code (chunks 0–4) has comments
   written by the prose seat — never delete or edit an existing comment line. The first
   build's own files have no comments and you add none.
2. `PLAN.md`: "Chunk 5 — Statement classifier and the contracts" (your scope, rewritten);
   decisions D4, D8, D10, D11; section 4.2; sections 9.1 and 9.4; section 18 row 9; and
   chunks 6, 7, 11 (your consumers).
3. Why the scope changed: `agent_work/chunks/c05-classifier/SCOPE-ASSESSMENT.md` (sections
   1–4), `OPTIONS.md` (options A and D, "Outside this memo's bar"), and the two reviews and
   the attack record of the first build under `parked/` (`REVIEW-1.md`, `REVIEW-2.md`,
   `ADVERSARIAL.md`, `NOTES.md`, and `arbprobe_test.go.txt` — the unfinished probe of the
   arbitration that was stopped).
4. What landed since the first build: `agent_work/chunks/c03-obs-spine/NOTES.md`,
   `agent_work/chunks/c04-wire-sessions/NOTES.md` (including "Integration with chunk 3"),
   `docs/knowledge/obs-spine.md`, the wire-scenario flush note in `docs/knowledge/`, and
   `/root/briefs/zgateway-p2-c04-integrate.md` (the same join, done for chunk 4).
5. `docs/knowledge/foundation-gates.md`, `docs/knowledge/classifier.md` (the first build's
   facts — you will rewrite it).

## The design you build to

A reading mistake may cost speed or a courtesy. It may never cost correctness.

| Part of the first build | What happens to it |
| --- | --- |
| `internal/work` (the vocabulary) | Stays. Changed only as listed under "Vocabulary". |
| Error table, reply builders, discard-until-Sync helper, `docs/error-contract.md` | Stay as they are. Passed two reviews with no finding. |
| The SQL lexer (`lex.go`) and the reject classifier (`classify.go`, `rejectlist.go`) | Stay, as **the courtesy check** (PLAN.md 9.4). No safety rests on them any more. |
| The lexer-based "read-only-provable" and "coalescable" proofs in `classify.go`, and the wide function allow-list behind them | **Removed.** Replaced by the safe-shape recogniser below. |
| The transaction-effect flag (`TxEffect`, `MayLeaveOpen`, "may leave a transaction open") | **Removed** from the vocabulary and the classifier's output. The status byte decides when a lease ends (decision D4); nothing used the flag. Keep whatever internal recognition of transaction-control statements the reject rules need (two-phase commit, `SET LOCAL` inside a transaction). |
| Classifier snapshot and counters | Stay, adjusted to the new flags. |

### The safe-shape recogniser (new; about 200 lines; its own file `share.go`)

One pure function over the raw bytes of a `Query` string. **It does not call the lexer or
the classifier and shares no helper with them** — a test holds that structurally (parse the
file with `go/ast` and assert it references no identifier defined in `lex.go`,
`classify.go` or `rejectlist.go` other than plain tables you place in `share.go` itself).
A text **fits the safe shape** if and only if ALL of these hold:

1. Its length is at most a named constant (choose and state it; a few kilobytes).
2. Every byte is an ASCII letter, an ASCII digit, the space character (0x20 — no tab, no
   line break), or one of `_ , * = < > ( ) '`. Nothing else: no dot, backslash, dollar,
   double quote, semicolon, minus, slash, colon, plus, percent, non-ASCII byte.
3. Scanning left to right with a single in-string toggle on `'` (so `''` inside a string
   toggles twice), the text ends outside a string.
4. The byte before every opening quote is not a letter, digit or underscore (this excludes
   `E'…'`, `B'…'`, `X'…'`, `N'…'` prefixes).
5. No string's content, lower-cased, contains `now`, `today`, `tomorrow` or `yesterday`
   (Postgres's relative-time input words; a substring match on purpose — over-matching
   costs a share, never a wrong result).
6. Outside strings, split into words (maximal runs of letters, digits and underscore):
   - the first word of the text is `select` (ASCII case-insensitive);
   - no word is on the **stop list**: `into`, `for`, `with`, `tablesample`, every keyword
     that begins a statement that is not a `SELECT` (`insert`, `update`, `delete`, `merge`,
     `truncate`, `create`, `alter`, `drop`, `grant`, `revoke`, `call`, `do`, `copy`, `lock`,
     `set`, `reset`, `show`, `prepare`, `execute`, `deallocate`, `listen`, `notify`,
     `unlisten`, `declare`, `fetch`, `move`, `close`, `begin`, `start`, `commit`,
     `rollback`, `abort`, `savepoint`, `release`, `discard`, `load`, `vacuum`, `analyze`,
     `explain`, `refresh`, `reindex`, `cluster`, `checkpoint`, `comment`, `import`,
     `security`, `values`, `table`), and every keyword whose value differs per call or per
     session — port the list from PostgreSQL's grammar (`gram.y`, the
     `func_expr_common_subexpr` production: `current_date`, `current_time`,
     `current_timestamp`, `localtime`, `localtimestamp`, `current_role`, `current_user`,
     `session_user`, `system_user`, `user`, `current_catalog`, `current_schema`) and name
     that source in NOTES.md;
   - every word followed (after optional spaces) by `(` is either on a short **paren-keyword
     list** (words that legitimately precede a parenthesis without being a call: `select`,
     `from`, `where`, `and`, `or`, `not`, `in`, `on`, `exists`, `any`, `all`, `some`, `by`,
     `when`, `then`, `else`, `having`, `union`, `intersect`, `except`, `join`, `as`,
     `limit`, `offset`, `distinct`, `between`, `like`, `is`, `case` — choose and justify the
     final list) or on the **share allow-list**: functions known to have no side effects
     and no per-call or per-session result. Start from the first build's *coalesce*
     allow-list (the smaller of its two lists) and remove anything doubtful. Aggregates,
     plain string and math functions, `coalesce`, `nullif`, `greatest`, `least`.
7. Nothing else. In particular the recogniser never tries to understand a construct it
   does not list: unknown means "does not fit".

The stop list, the paren-keyword list and the allow-list are tables in code, shown in a
generated document (`docs/safe-shape.md`, held by a test like the other generated
documents). **Do not widen the alphabet or drop a rule.** If a rule turns out to be wrong
or unimplementable as written, keep the stricter reading and record it under "Deviations".

The stated limits, which you write down for the prose seat (NOTES.md, "Stated limits"):
the shape trusts the objects it names — a view, a user-defined operator behind `=`, `<`,
`>`, or a table that is really a foreign table or has rules, can hide a side effect or a
per-call result; the benchmark's hot-key read `SELECT v FROM kv WHERE k = '…'` fits.

### What the classifier reports (the work unit)

For one `Query` text, the tenant, and `inTx` (a protocol fact: the client's last status
byte was `T` or `E`):

- **Class**: reject (with reason) when the courtesy check recognises a reject-list feature
  anywhere in the message — the whole message is refused, nothing of it is forwarded;
  otherwise start a lease (`inTx` false) or continue a lease (`inTx` true). Nothing is
  ever rejected because it is unknown or cannot be tokenised.
- **SafeRead**: the text fits the safe shape. This one flag is what the error contract
  means by "a statement that fits the safe shape" (PLAN.md 9.1: `08006` against `08007`).
- **Shareable**: SafeRead AND not `inTx` AND not rejected. This is what `coalesce: auto`
  shares (decision D10).
- **Hinted**: the text begins with the exact bytes `/* mg:share */`. Reported always; it
  never makes a statement Shareable or SafeRead. (A hinted text contains `/` and `*`, so
  it never fits the safe shape; in `hinted` mode chunk 11 shares on the mark alone.)
- **Share key**: the exact text (a copy) with the tenant per the configured scope, present
  when not `inTx` and not rejected and (Shareable or Hinted).
- `Opaque` (the lexer could not tokenise the message) and `Statements` stay as counters'
  inputs. An opaque message is forwarded, and is Shareable only if it fits the safe shape
  — which a text inside the safe alphabet with balanced quotes always tokenises; assert
  that as a property (safe shape ⇒ the lexer tokenises it as exactly one statement) and
  treat a counter-example as a bug in one of the two.

### Vocabulary (`internal/work`)

Remove `TxEffect`, `Tri`-based `MayLeaveOpen` and the `Tx` field (keep `Tri` if `Outcome`
uses it). Rename or replace `ReadOnly` / `Coalescable` with `SafeRead` / `Shareable` as
above. Everything else stays. Document every field in NOTES.md and in
`docs/knowledge/classifier.md` (rewrite that note for the new design: how to call it, what
each flag means, what was removed and why, the easy-to-get-wrong list cut down to what is
still true). Chunk 6 is written against this note.

### The courtesy check (lexer and reject rules)

Keep the first build's behaviour. Two things to do:

- Fix the open finding of `parked/REVIEW-2.md` (the lexer does not join string literals
  continued across a newline as Postgres's scanner does), with a regression test. It is a
  known fidelity bug and cheap. The second open finding (temporal range and array types in
  the coalescable proof) disappears with the proof it was about; say so in NOTES.md.
- State in NOTES.md, "Stated limits", that the reject list is best effort, with the known
  gaps (a function or view hiding the feature; any spelling not in the corpus).

Do NOT spend effort hunting further reject spellings. That open-ended hunt is what the
ruling ended.

## The tests: keep, hoist, retire (Nick's direction of 2026-11-26 — binding)

The first build carries about 2,800 lines of tests in `internal/pgwire` and
`internal/work`, a 562-statement corpus, and the adversary's tests under
`agent_work/adversarial/c05-classifier/`. The rule:

- **Kept**: a test whose behaviour still exists stays in the shipped suite, adapted as
  little as possible (renamed flags, removed fields). This is the default. When a test
  asserts several things and only some survive, keep the test and cut the dead assertions.
- **Hoisted**: a still-valid test that lives outside the shipped suite moves into it.
  That means the adversary's tests (`attacks_test.go`, `database_test.go` and the tier R
  script) and anything worth keeping from `parked/arbprobe_test.go.txt`: each attack that
  still tests existing behaviour becomes a normal test in `internal/pgwire` (external test
  package where it needs the simulator) or under `test/`, with a name that says what it
  proves. After hoisting, delete `agent_work/adversarial/c05-classifier/` except that its
  `RESULTS.md` moves to `parked/ADVERSARIAL-RESULTS.md` (a fresh adversary will write a new
  harness there after you).
- **Retired**: only a test of behaviour that no longer exists — the transaction-effect
  flag and the lexer-based read-only / coalescable proofs. Before retiring a proof test,
  check whether it restates as a safety property of the new design and keep it in that
  form: every statement the first build's tests call *not* coalescable or *not* read-only
  must still be not Shareable and not SafeRead (those "never" tests are the most valuable
  ones — `TestClassifierSpellingsNeverProveUnlistedCalls`,
  `TestClassifierSpellingsNeverCoalesceNonCoalescableCalls`,
  `TestClassifierTimeDependentLiteralsAreNotCoalescable`, the mutant invariants, the
  conservative-when-untokenisable test all survive this way).
- **The corpus** (`testdata/classifier-corpus.txt`) is kept whole. Reject expectations
  stay exact. Sharing expectations are restated as "never unsafe": an entry the first
  build expected coalescable now expects Shareable only if it fits the safe shape (record
  how many still do, and add entries that do); an entry it expected not coalescable must
  be not Shareable. Drop the transaction-effect column.
- **The tier R differential** (`classify_tierr_test.go`) is kept: the session-state oracle
  over forwarded statements stays as a measure of the courtesy's coverage; the read-only
  half is re-aimed at the safe shape (below).

Write the ledger in NOTES.md, section "Test ledger": one row per test function of the
first build and per adversary test (name, old file, kept / hoisted / retired, new location,
one-line reason for anything not simply kept), then the three counts, and the test line
counts before and after. Nick reads this table.

## New tests (write these first)

- **Safe shape, unit**: a table of fitting and non-fitting texts, one per rule, each rule
  violated alone.
- **Safe shape, property** (seeded): (a) insert any single byte outside the alphabet at any
  position of a fitting text → does not fit; (b) any text assembled from a small grammar
  over the allow-list fits; (c) for random byte strings, "fits" implies every rule holds
  when re-checked by an independent, slower reference checker written in the test.
- **Canary**: a test-only switch that skips the alphabet check must make (a) FAIL.
- **Structural**: the recogniser's file references nothing from the lexer or classifier.
- **Fuzz** `FuzzSafeShape` beside the kept `FuzzLex` and `FuzzClassify`: no panic; fits ⇒
  the reference checker agrees ⇒ the lexer tokenises it as one statement. No seed derived
  from a hash of the input.
- **Tier R differential for the safe shape** (real Postgres 17, a schema of plain tables
  only): every corpus entry and every generated text that fits the safe shape (a) gives the
  same outcome inside `BEGIN READ ONLY` as outside it and never SQLSTATE `25006`, (b)
  leaves the watched tables' checksum unchanged, (c) leaves the session clean by the
  catalog query `agent_work/chunks/c05-classifier/canvas/experiments/postgres-server/dirty-check.sql`
  (copy it into `test/` as a shipped oracle; chunk 6 reuses it), and (d) returns identical
  rows when run twice with nothing else running. A statement that errors (an unknown
  table) is fine if it errors the same way each time.
- Vocabulary, error-table, reply-builder and generated-document tests: kept as they are.

## The join with what landed since (owed by this chunk)

1. `classify_e2e_test.go` and `classify_obs_test.go` are in package `pgwire` and import
   `internal/sim/fakepg`, which now imports `internal/pgwire`: an import cycle. Make them
   external (`package pgwire_test`, or move the scenario world beside chunk 4's
   `internal/pgwire/pgwiretest/wireworld`, whichever is smaller), exporting through an
   `export_test.go` only what they need (the planted-bug switch stays unreachable from
   non-test code).
2. Replace the first build's private framing harness with chunk 4's framer and session
   machines where that is a small change; otherwise keep the harness and say so.
3. Register the classifier and reply snapshots as a collector source: declare the metrics
   in the catalog (component `classifier`), map them (extend `internal/obs/wiresrc` or add a
   sibling package — then add it to the import rules the same way), regenerate
   `docs/csv-schema.md`. The "declared but never emitted" and "emitted but undeclared"
   tests of chunk 3 must pass.
4. Make the reject-list scenarios flush through the collector and the CSV adapter the way
   the wire scenarios do (round-trip check on every run; byte-identical files for one seed;
   run directory under `$RUN_DIR`), and make the two SQL files under
   `test/analysis/c05-reject-list/` real assertions over the flushed files, run by
   `scripts/csv-assert.sh` in the gate's G7: classifier rejects = statements sent; the
   fake Postgres statement count is 0. Each declares its `-- requires:` metrics. Show each
   can fail (a planted wrong number makes the runner exit non-zero — as a test).
5. Update `agent_work/chunks/c05-classifier/gate/extra.sh` for the new test names; keep its
   cross-process replay check and the planted-`SET` canary.

## Observability obligations (landing gate)

Classifier `Snapshot()`: statements by class; rejects by reason; opaque (untokenisable);
safe-read; shareable; hinted. Reply counters keyed by error-table row. Counters-move test;
conservation: classified = sum over classes. Exported through the collector (join item 3).

## Claims this chunk makes (a fresh adversary attacks each)

1. **Safe shape is exact.** For any byte string, SafeRead is true only if every rule of
   "The safe-shape recogniser" holds; Shareable is true only if SafeRead, not in a
   transaction and not rejected. One counter-example breaks the claim.
2. **Safe shape is safe on a real server.** On Postgres 17 with a schema of plain tables,
   no text that fits the safe shape writes, changes session state, or returns different
   rows on an immediate re-run. (Views, user-defined functions and operators, rules and
   foreign tables are outside the claim and are the stated limit.)
3. **The recogniser does not depend on the lexer** (structural test), and a lexer bug
   cannot make a statement Shareable or SafeRead.
4. **The hint never widens.** Hinted is true exactly when the text begins with
   `/* mg:share */`; it never sets SafeRead or Shareable.
5. **No panic, bounded work.** Lexer, classifier and recogniser never panic and use memory
   and time bounded by a stated multiple of the input length, on any bytes.
6. **Nothing is refused for being unknown.** A message the lexer cannot tokenise, or a
   construct the classifier does not model, is forwarded, never rejected.
7. **The courtesy list refuses what it documents.** Every example in
   `docs/reject-list.md` and every reject entry of the corpus is refused whole, under any
   padding with whitespace and comments and any keyword case. **This claim is deliberately
   narrow: a spelling of a session feature that is forwarded is NOT a break** — session
   safety is the reset's job (PLAN.md D8). Such finds are listed as "courtesy gaps".
8. **Every proxy-made reply is well formed**: an `ErrorResponse` with the row's SQLSTATE,
   then the correct end marker (or none for FATAL), counted on every path.
9. **Generated documents cannot drift**: `docs/error-contract.md`, `docs/reject-list.md`,
   `docs/safe-shape.md`.
10. **`internal/work` imports only the standard library**, no Postgres type appears in it,
    and the classifier's output is expressed entirely in its types.
11. **Refused statements never reach the database** in the scenario (statement counter
    zero in the fake), and the flushed files say the same: rejects = statements sent.

## Steady-state row

Nothing here grows at run time: the classifier works on the message it is given, counters
are fixed-size. State the maximum memory per classification and test it.

## Not in this chunk

The reset itself (`DISCARD ALL`, its deadline, its counters, `lease.reset`): chunk 6 and
7. `internal/proxy`, `internal/shell`. Any new reject rule beyond the string-continuation
fix. A wider safe shape.

## Size

The first build is 2,152 product lines. Expect to end near 1,900: about 300–400 removed
(the proofs, the flag, most of the wide allow-list), about 200 added (`share.go`), plus the
source mapping. If you find yourself rewriting the lexer or the reject classifier, stop:
that is out of scope.

## Finish

As the builder rules say: `gofmt -l .` empty; `go build ./... && go vet ./... && go test
-count=1 ./...` green; `scripts/gate.sh c05-classifier` green at the build stage on a
clean committed tree (it includes tier R and G7; it takes about 15 minutes — run it
detached and wait for it; delete the generated `gate/result.json` if it shows as
untracked). NOTES.md at `agent_work/chunks/c05-classifier/NOTES.md` is NEW (the old one is
under `parked/`): what you built, the test ledger, each claim above with the test that
proves it, deviations, "Stated limits" (the courtesy list; the safe shape), "Courtesy gaps
known", the commands. Index rows in `agent_work/INDEX.md`. Clean tree, nothing pushed,
summary, and a final line containing exactly AGENT-DONE.
