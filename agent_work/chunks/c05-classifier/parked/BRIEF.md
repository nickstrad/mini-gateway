# mini-gateway — chunk 5: Statement classifier and the contracts — builder brief

Seat: Opus (product Go and tests, logic only). Chunk directory name: `c05-classifier`.
Worktree: `/root/Software/mini-gateway-work/c05` — branch `work/c05-classifier`.
This chunk is built concurrently with chunks 3 (`internal/obs` collector and adapters) and
4 (wire sessions: in `internal/pgwire` the files `frame*.go`, `prestartup*.go`,
`client*.go`, `backend*.go`, `scram*.go`, `cycle*.go`, `params*.go`, `snapshot_wire*.go`,
`fatal*.go`). Stay inside your files so the three merge cleanly: you own `internal/work`
and, in `internal/pgwire`, the files `lex*.go`, `classify*.go`, `errors*.go` (the error
table), `reply*.go` (builders for proxy-made replies), `rejectlist*.go`,
`snapshot_classifier*.go`, and their tests; plus `scripts/gen-docs.sh` wiring for YOUR
generated documents only (chunk 3 also extends that script for `docs/csv-schema.md`: keep
your part in a separate, clearly delimited block or a file under `scripts/gen-docs.d/` that
the script runs — create the `gen-docs.d` mechanism if the script is still the skeleton).
Do not create `internal/proxy` or `internal/shell`. Because chunk 4's framer is not on your
branch, build replies and parse test messages directly with `pgproto3` message types
(`Encode`), and write your wire-level e2e against the smallest private test harness you
need (a few lines that frame `Query` messages); the orchestrator joins the two at chunk 6.

Read, in this order, before writing anything:

1. `/root/briefs/zgateway-p2-builder-rules.md` — the rules for every builder. They bind you.
2. `PLAN.md`: "Chunk 5 — Statement classifier and the contracts" (your scope); decisions D4,
   D8, D10, D11; section 4.2 (the work vocabulary seam — what `internal/work` is and why);
   section 9.1 (the error contract — the table you turn into code), 9.4 (the reject list);
   section 4.1 rows 1, 4, 7, 8 (why classification matters); chunks 6–12 (your consumers:
   read what each will ask of the classifier and the vocabulary).
3. Ground truth: `agent_work/phase1/probes/pgwire/` (main.go and output),
   `docs/knowledge/phase1-findings.md`.
4. What exists: the simulator and its fake Postgres (`internal/sim/fakepg` — its statement
   matcher and write log), `scripts/tier-r.sh`, notes of chunks 1 and 2, `docs/knowledge/`.
   The simulator API is frozen (tag `freeze-sim`).
5. `docs/knowledge/foundation-gates.md`.

## Purpose

Deciding, from the text of a statement, what the proxy may do with it — and fixing in
generated, tested tables what the proxy tells a client when it refuses or fails. The
classifier is a **lexer, not a SQL parser**, and is deliberately conservative: when it
cannot tell, a statement is still forwarded but is never shared and never assumed read-only.

## Deliverables

### `internal/work` — the vocabulary (plain types; no Postgres types; no imports from pgwire)

Small plain types the cores (pool, admission, coalescer, retry) will be written against:
a tenant id; a unit of work with its class — **reject** (with a reason), **start a lease**,
**continue a lease**, **answer locally**; flags: transaction control (begins / ends / none
/ unknown), read-only-provable, coalescable; an optional **share key**; and an **outcome**:
connection released clean / still held / dirty; reached the database or not; error class.
Plus the small enumerations the error contract needs (refusal classes). Keep it minimal,
documented in NOTES.md field by field, with a test that the package imports nothing but the
standard library. `internal/work` passes the determinism lint.

### In `internal/pgwire` — lexer and classifier

- **The SQL lexer**: single-quoted strings (with `''`), escape strings (`E'...'` with
  backslash escapes), dollar-quoted strings (`$$...$$`, `$tag$...$tag$`), quoted identifiers
  (`"..."` with `""`), line comments (`--`), nested block comments (`/* /* */ */`),
  identifiers and keywords (case-insensitive, ASCII folding only — state the limit for
  non-ASCII identifiers), numbers, operators, parameters, and **statement splitting** on
  top-level `;`. Unicode-escape strings (`U&'...'`), bit/hex strings are tokenised as
  strings. It never panics and never allocates more than a bounded multiple of the input;
  an unterminated string/comment/dollar-quote yields a "cannot fully tokenise" result.
  `standard_conforming_strings` is assumed on (the default; state the limit).
- **The classifier**: one `Query` message's text → a work unit:
  - **reject** with a reason — the reject list of PLAN.md 9.4: session `SET` and `RESET`
    (but `SET LOCAL` and `SET TRANSACTION ...` / `SET CONSTRAINTS` — which are
    transaction-scoped — are allowed; `SET SESSION CHARACTERISTICS` is rejected; say exactly
    which `SET` forms fall where); `PREPARE`, `EXECUTE`, `DEALLOCATE`; `LISTEN`, `UNLISTEN`,
    `NOTIFY` (state your decision on `NOTIFY` — it is transaction-scoped delivery but useless
    without `LISTEN`; default: allowed, not coalescable); `DECLARE ... WITH HOLD`; temporary
    tables (`CREATE TEMP/TEMPORARY TABLE`, `... INTO TEMP`, `CREATE TEMP VIEW/SEQUENCE`);
    session advisory-lock functions (`pg_advisory_lock`, `pg_try_advisory_lock`,
    `pg_advisory_unlock*`, shared variants — but the `_xact_` variants are allowed);
    `DISCARD`; `LOAD`; `COPY` (any direction); `set_config(..., false)` (when the third
    argument is not the literal `true`, reject).
  - **transaction control**: `BEGIN`, `START TRANSACTION`, `COMMIT`, `END`, `ROLLBACK`,
    `ABORT`, `SAVEPOINT`, `RELEASE`, `ROLLBACK TO`, `PREPARE TRANSACTION` / `COMMIT
    PREPARED` (reject two-phase commit: it leaves state outside the session — state it),
    and what a multi-statement message does (`BEGIN; SELECT 1` leaves a transaction open —
    probe B1): the classifier reports "may leave a transaction open: yes / no / unknown"
    for the whole message; the lease's real end is always decided by the status byte, this
    flag only informs admission.
  - **read-only-provable**: every statement in the message is a `SELECT`/`VALUES`/`SHOW`/
    `TABLE` (or `EXPLAIN` without `ANALYZE`) with no data-changing keyword anywhere at any
    depth of parentheses or CTE (`INSERT`, `UPDATE`, `DELETE`, `MERGE`, `TRUNCATE`, DDL
    keywords, `CALL`, `DO`, `COPY`, `LOCK`, `NEXTVAL`-style function names…), no `INTO`
    clause, no locking clause (`FOR UPDATE/SHARE/NO KEY UPDATE/KEY SHARE`), and every
    function it calls (an identifier directly followed by `(`) is on the allow-list.
  - **coalescable** (decision D10): read-only-provable AND exactly one statement AND
    begins with `SELECT` AND contains nothing whose result differs per call or per session
    (`now()`, `current_timestamp` and friends as bare keywords, `random()`, `txid_current`,
    `pg_backend_pid`, `current_user`/`session_user`, sequence functions, `pg_sleep`…) —
    i.e. every function is on the **coalesce allow-list** (a second, smaller list of
    functions known to have no side effects and no per-call results: aggregates, string and
    math functions, `coalesce`-style expressions — choose a defensible initial list and
    put both lists in one table in code that the generated document shows). The share key
    is the exact statement text (PLAN.md D10: "the key is the exact statement text") —
    provide the key as bytes plus the tenant/scope hook the coalescer chunk will configure.
    Support the `hinted` mode marker: an opt-in leading comment (`/* mg:share */`) is
    reported as a flag (a statement is never coalescable by hint alone if it fails the
    proof; the hint only matters in `hinted` mode).
  - **Conservative by construction**: any message the lexer cannot fully tokenise, or that
    contains a construct the classifier does not model, is classified "forward, start or
    continue a lease, not read-only-provable, not coalescable, transaction effect unknown".
    Nothing is ever rejected because it is unknown.
  - An empty query string, comments only, and `;` only are classified (the proxy forwards
    them; Postgres answers `EmptyQueryResponse`).
- **The error table as code** (decision D11, PLAN.md 9.1): one row per situation — a stable
  row id, severity, SQLSTATE (fix the private codes: `53M01`..`53M05`, and the standard
  ones in the table), message text, and the three contract facts: reached the database?
  may a write have been applied? safe to retry? — and whether the client connection stays
  usable. Rows for situations later chunks implement are present now (the table is the
  contract); each row has a counter key.
- **Builders for every proxy-made reply**: `ErrorResponse` (severity, code, message, and a
  detail line stating the row id) followed by the correct end marker — `ReadyForQuery` with
  the status byte the client believes (`I` outside a transaction; inside one the builder is
  given the current status), or no marker for FATAL (connection then closed). The
  **discard-until-Sync rule** for extended-protocol messages: after refusing an extended
  message with `0A000`, messages are discarded until `Sync`, then `ReadyForQuery` — encode
  that as a tiny state helper with tests.
- **Generated documents, held by tests**: `docs/error-contract.md` (the table), and
  `docs/reject-list.md` (every rejected feature with its reason and an example, the
  transaction-scoped forms that are allowed, and both function allow-lists). Generated by
  `scripts/gen-docs.sh` (see the note on sharing that script above); a test fails when the
  committed files differ from the generator's output.

## Tests (write these first)

- **A classifier corpus** in testdata: several hundred statements with expected classes
  (one statement per record: text, expected class, flags, reason), including hostile ones:
  keywords inside strings, comments, dollar-quoted bodies and quoted identifiers;
  data-changing CTEs (`WITH x AS (DELETE ... RETURNING *) SELECT ...`); locking clauses;
  `SELECT ... INTO`; multi-statement messages; `BEGIN; ...` shapes; `SET LOCAL` vs `SET`;
  `set_config`; advisory locks both kinds; mixed case; odd whitespace; nested comments;
  a function call with whitespace/comment between name and `(`; schema-qualified function
  names (`pg_catalog.set_config`); quoted function names; `SELECT` calling an unknown
  function (forwarded, not read-only-provable); extremely long statements; non-UTF-8 bytes.
- **Property**: classification is identical for every way of padding with whitespace and
  comments between tokens (generate paddings from a seed), and for keyword case changes.
- **Conservative-by-construction test**: any statement the lexer cannot fully tokenise is
  never coalescable and never read-only-provable (fuzz-style property over mutated corpus
  entries).
- **Fuzz** (`FuzzLex`, `FuzzClassify`): no panic, bounded output, lexer round-trip (the
  token spans cover the input without overlap).
- **Differential test against real Postgres (tier R)**: every corpus statement the
  classifier marks "leaves no transaction open" leaves status `I` when executed alone on
  real Postgres 17 (with the needed tables created by the test); every statement marked
  read-only-provable leaves a watched table unchanged (checksum before/after) and succeeds
  inside a `BEGIN READ ONLY` transaction (Postgres itself refuses writes there — a stronger
  oracle; state which corpus entries are excluded and why, e.g. functions Postgres marks
  volatile but harmless). Also run the read-only-provable set against the fake's write log.
- Error table: every row has a unique id and a valid 5-character SQLSTATE; private codes
  sit in class 53 with an `M` subclass; golden bytes for every built reply; the end marker
  carries the status byte given; FATAL has no marker.
- Generated-document tests (`--check` clean).
- **End-to-end for this chunk, in the simulator**: each reject-list feature sent by a
  simulated client as a `Query` through a minimal wire harness (frame → classify → reply
  builder) gets `0A000` with the right row, the connection stays usable (a following
  allowed statement is classified forwardable), and **nothing reaches the fake Postgres**
  (its statement counter stays zero for the rejected ones). Replay, quiesce, non-zero
  counters. Ablation / planted-bug canary: with the test-only switch "treat `SET` as
  forwardable" the scenario must FAIL (the fake's statement counter moves and its session
  state changes).
- **CSV-flush assertion** (chunk 3 builds the flush concurrently — write the SQL files
  under `test/analysis/<scenario>/` to the schema of PLAN.md 7.7 and the same assertions as
  exact Go checks on snapshots; the orchestrator wires the flush at integration):
  classifier rejects = statements sent; the fake Postgres statement count is 0.

## Observability obligations (landing gate)

Classifier `Snapshot()`: statements by class; rejects by reason; unclassifiable (lexer
could not tokenise); coalescable; read-only-provable; hinted. Error-contract counters keyed
by row id (a small counter set the reply builders increment — every proxy-made reply is
counted on every path). Counters-move test; conservation: classified = sum over classes.

## Claims this chunk makes (an adversary will try to break each)

1. No statement containing a session-level feature of the reject list (PLAN.md 9.4) is
   classified forwardable, however it is spelled, cased, commented or padded — within the
   stated limits of a lexer (a function or view that hides the feature is out of scope and
   is stated as a limit).
2. No statement that can write, lock, or call a function outside the allow-list is ever
   classified read-only-provable; no statement outside the D10 definition is ever
   classified coalescable. (False negatives are fine; one false positive breaks the claim.)
3. Anything the lexer cannot fully tokenise is forwarded, never shared, never read-only.
4. Classification does not depend on whitespace, comments between tokens, or keyword case.
5. The lexer and classifier never panic and use bounded memory on any byte string.
6. Transaction-effect flag: a message marked "leaves no transaction open" never leaves
   status `T` or `E` on real Postgres when executed outside a transaction.
7. Every proxy-made reply is a well-formed `ErrorResponse` with the row's SQLSTATE,
   followed by the correct end marker (or none for FATAL), and is counted.
8. `docs/error-contract.md` and `docs/reject-list.md` cannot drift from the code.
9. `internal/work` imports only the standard library and no Postgres type appears in it;
   the classifier's output is expressed entirely in `internal/work` types.
10. Reject-list statements never reach the database (statement counter zero in the fake).

## Steady-state row

Nothing here grows at run time: the lexer works on the message it is given, counters are
fixed-size (bounded reasons and classes). Gauge: none. State the maximum memory per
classification (a bounded multiple of the statement length) and test it.

## Size

About 1,300 lines of product code.

## Finish

As the builder rules say: gate green at the build stage (`scripts/gate.sh c05-classifier`,
including tier R), NOTES.md complete at `agent_work/chunks/c05-classifier/NOTES.md` — it
MUST contain a section "Stated limits of the classifier" written for the prose seat — clean
tree, summary, and a final line containing exactly AGENT-DONE.


## Notes from the orchestrator on your starting point (read before you start)

- Your branch starts from chunk 2 as repaired and attacked (`work/c02-sim-world`), which is
  in its second review and has not landed yet; chunk 2 gets a comments-only prose pass on
  another branch and your branch will be rebased onto it. Prefer NEW files; where you must
  edit an existing file keep the edit small and local, and list it in NOTES.md. The gate at
  the build stage compares comments with the merge base with `main`; chunk 2 files are
  comment-free in your tree and that passes.
- What chunk 2 actually built (names differ slightly from the wording above): the fake is
  `internal/sim/fakepg`; simulated clients are `internal/sim/workload`; the sans-I/O test
  client (framer, codec, session) is `internal/testclient`; scenarios, the driver interface
  and the fidelity runner are `internal/sim/world`; float-using generators are
  `internal/randdist`. Read `agent_work/chunks/c02-sim-world/NOTES.md` and
  `docs/knowledge/sim-world.md` first.
- Do NOT edit `agent_work/INDEX.md` (three chunks are building at once). Put the index rows
  you would add in a section "Index rows" at the end of your NOTES.md; the orchestrator adds
  them. The same goes for shared one-line lists: add your lines to
  `tools/lint-rules/det-packages.txt`, `race-packages.txt`, `imports.txt`,
  `prose-paths.txt` and `.gitignore` only by APPENDING lines at the end of the file.
- `scripts/gen-docs.sh` is shared by chunks 3 and 5: if it is still the skeleton, replace it
  with a small driver that runs every executable `scripts/gen-docs.d/*.sh` (passing
  `--check` through) and delete its marker; put YOUR generator in its own file there
  (`scripts/gen-docs.d/<your-chunk>-<name>.sh`). Both chunks will write the same driver;
  keep it to this exact shape so the merge is trivial:
  `for f in scripts/gen-docs.d/*.sh; do [ -x "$f" ] && "$f" "$@" || exit 1; done` after the
  usual header and `set -eu`.
- `internal/pgwire` is shared by chunks 4 and 5 (one Go package, two builders). To avoid
  name collisions: chunk 4 names its snapshot type `WireSnapshot` and chunk 5
  `ClassifierSnapshot`; chunk 5 exports only identifiers beginning with `Lex`, `Token`,
  `Classif`, `Reject`, `ErrorRow`, `ErrRow`, `Reply`, `SyncDiscard`, `Allow`; every
  unexported package-level identifier of chunk 5 begins with `lex`, `cls`, `rej`, `erow` or
  `rpl`; chunk 4 uses none of those prefixes. Test helper names follow the same rule.
  Neither chunk adds a `TestMain` or a `doc.go` to `internal/pgwire`.
