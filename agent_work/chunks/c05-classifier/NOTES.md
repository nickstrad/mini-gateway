# Chunk 5 — Statement classifier and the contracts: builder notes

Built by Opus on 2026-11-25 on branch `work/c05-classifier` (worktree
`/root/Software/mini-gateway-work/c05`) from `/root/briefs/zgateway-p2-c05-classifier.md`.
Durable facts for later builders: `docs/knowledge/classifier.md`.

## What was built

| Deliverable | Where |
| --- | --- |
| Work vocabulary (standard library only, in the determinism lint) | `internal/work/work.go`; test `internal/work/work_test.go` |
| SQL lexer | `internal/pgwire/lex.go` |
| Classifier (one `Query` text → `work.Unit`), counters | `internal/pgwire/classify.go`, `internal/pgwire/snapshot_classifier.go` |
| Reject list, allowed transaction-scoped forms, function allow-lists, keyword lists | `internal/pgwire/rejectlist.go` |
| Error table as code | `internal/pgwire/errors.go` |
| Reply builders, the discard-until-Sync helper | `internal/pgwire/reply.go` |
| Generated documents | `docs/error-contract.md` (from `errors_doc.go`), `docs/reject-list.md` (from `rejectlist_doc.go`) |
| Generator | `scripts/gen-docs.sh` (driver), `scripts/gen-docs.d/c05-contracts.sh` (this chunk's part) |
| Corpus | `internal/pgwire/testdata/classifier-corpus.txt` (562 records: 186 rejected, 376 forwarded, 165 read-only-provable, 116 coalescable) |
| Golden reply bytes | `internal/pgwire/testdata/reply-golden.txt` |
| CSV-flush assertions (SQL, run once chunk 3's runner and chunk 6's flush exist) | `test/analysis/c05-reject-list/rejects-equal-sent.sql`, `fakepg-statements-zero.sql` |
| Gate extra (G10) | `agent_work/chunks/c05-classifier/gate/extra.sh` |

### Existing files changed (all small)

| File | Change |
| --- | --- |
| `scripts/gen-docs.sh` | The chunk 0 skeleton replaced by the driver shape the orchestrator gave: header, `set -eu`, `cd` to the root, `for f in scripts/gen-docs.d/*.sh; do [ -x "$f" ] && "$f" "$@" \|\| exit 1; done` |
| `scripts/.not-built/gen-docs` | Deleted (the stage is built) |
| `tools/lint-rules/det-packages.txt` | One line appended: `internal/work` |

Nothing else outside the owned files changed. `internal/proxy` and `internal/shell` were not
created; no `TestMain` or `doc.go` in `internal/pgwire`. Every exported identifier of this
chunk in `internal/pgwire` begins with `Lex`, `Token`, `Classif`, `Reject`, `ErrorRow`,
`ErrRow`, `Reply`, `SyncDiscard` or `Allow`; every unexported package-level identifier
(test helpers included) with `lex`, `cls`, `rej`, `erow` or `rpl`. Test functions are
`TestLex…`, `TestClassif…`, `TestReject…`, `TestErrorRow…`, `TestErrRow…`, `TestReply…`,
`TestSyncDiscard…`, `TestAllowList…`, `TestScenarioClassifier…`, `TestTierRClassifier…`,
`FuzzLex`, `FuzzClassify`. The type with the methods `Snapshot()` is `ClassifierSnapshot`
(the classifier), `ReplySnapshot` (reply counters) and `SyncDiscardSnapshot`.

## The work vocabulary (`internal/work`), field by field

| Type | Values / fields | Meaning |
| --- | --- | --- |
| `TenantID` | string | The tenant (decision D6: the startup `user`). |
| `Class` | `ClassNone` (zero, never produced), `ClassReject`, `ClassStartLease`, `ClassContinueLease`, `ClassAnswerLocally`, `NumClasses` | What the engine does with the unit. `Forwards()` is true for the two lease classes. The classifier never produces `ClassAnswerLocally` in v1 (empty queries are forwarded, as the brief says); it is reserved. |
| `TxEffect` | `TxNone`, `TxBegins`, `TxEnds`, `TxWithin`, `TxUnknown` | Transaction-control effect of the whole message (rules below). `MayLeaveOpen()` maps it to `Tri`: begins → yes, unknown → unknown, the rest → no. |
| `Tri` | `No`, `Yes`, `Unknown` | Three-valued fact (reached the database, write applied, may leave a transaction open). |
| `RejectReason` | `RejectNone` and 16 reasons (`session-set`, `reset`, `prepare`, `execute`, `deallocate`, `listen`, `unlisten`, `cursor-with-hold`, `temp-object`, `advisory-lock`, `set-config`, `discard`, `load`, `copy`, `two-phase`, `extended-protocol`), `NumRejectReasons` | Why a unit is rejected; `String()` gives the label used in counters and documents. `extended-protocol` is never produced by the classifier (extended messages are not `Query` texts); it is there for the wire layer's counters. |
| `KeyScope` | `ScopeGlobal`, `ScopeTenant` | The scope hook for the share key; chunk 11 configures it. |
| `ShareKey` | `Tenant TenantID`, `Text []byte`; `Equal` | `Text` is the exact statement bytes (a copy; D10); `Tenant` is empty in the global scope and the tenant in the tenant scope. |
| `Unit` | `Tenant`, `Class`, `Reason`, `Tx`, `ReadOnly`, `Coalescable`, `Hinted`, `Opaque`, `Statements`, `Key`, `HasKey` | One classified `Query` message. `ReadOnly` = read-only-provable; `Coalescable` implies `ReadOnly`, one statement and outside a transaction; `Hinted` = the leading `/* mg:share */` comment; `Opaque` = the classifier could not model it (forwarded, never shared, never read-only, `Tx` unknown); `Statements` = non-empty statements in the message; `HasKey`/`Key` only when coalescable. |
| `Release` | `ReleaseClean`, `ReleaseHeld`, `ReleaseDirty` | Outcome for the database connection. |
| `ErrorClass` | `ErrNone`, `ErrRefused`, `ErrDatabase`, `ErrConnection`, `ErrTimeout`, `ErrCancelled`, `ErrProtocol` | Outcome error class. |
| `Outcome` | `Release`, `Reached Tri`, `WriteApplied Tri`, `Error ErrorClass` | What happened to a unit. |
| `Refusal` | `RefusalNone`, `RefusalShed`, `RefusalRate`, `RefusalBreaker`, `RefusalCoalesceCap`, `RefusalDraining`, `RefusalUnsupported`, `RefusalCancelledQueued` | The refusal classes of the error contract; `pgwire.ErrRowFor(r)` gives each one's error row. |

`internal/work` imports nothing (`TestWorkImportsOnlyTheStandardLibrary`, plus importlint's
deny rule and detlint).

## The lexer

`LexerFor(src).Next()` yields `Token{Kind, Start, End}` spans; `LexAll(src)` returns all
tokens and `clean` (false when any `TokenBad` appeared). Kinds: space (`' ' \t \n \r \f \v`),
line comment (`--` to `\n` or `\r`), block comment (nested), word (identifier or keyword:
`[A-Za-z_\x80-\xff][A-Za-z0-9_$\x80-\xff]*`), quoted identifier (`"…"` with `""`), Unicode
identifier (`U&"…"`, either case), string (`'…'` with `''`; `E'…'` with backslash escapes;
`N'…'`, `B'…'`, `X'…'`, `U&'…'` with `''`), dollar string (`$$…$$`, `$tag$…$tag$`), number
(digits, `_` between digits, fraction, exponent, `0x`/`0o`/`0b`), parameter (`$1`), operator
(maximal run of `+-*/<>=~!@#%^&|\`?`, stopping before `--` or `/*`; as in Postgres's scanner, a
multi-character operator ending in `+` or `-` loses its trailing signs unless it contains one
of `` ~ ! @ # ^ & | ` ? % ``, so `71+-3` is `71`, `+`, `-`, `3` and `@-` stays one operator), punctuation
(`( ) [ ] , ; : .`), bad. Unterminated strings, quoted identifiers, dollar strings and
comments make one bad token to the end of the input; any other byte the lexer does not know
(`\`, `{`, `}`, a lone `$`, control bytes, NUL) is a one-byte bad token and lexing continues.
Tokens never overlap and cover the input exactly (`FuzzLex`, `TestLexSpansCoverTheCorpus`).
The lexer allocates nothing; `LexAll` allocates the token slice.

## The classifier

`ClassifyQuery(text, inTx) work.Unit` is the pure function; `Classifier.Classify(tenant,
text, inTx)` adds the tenant, the share key (copied, scoped by `Classifier.Config.KeyScope`)
and the counters. `inTx` is the wire layer's knowledge of the client's status (`T` or `E`):
it selects `ClassContinueLease` over `ClassStartLease` and turns coalescing off (D10:
"outside a transaction").

Order of decisions for one message:

1. Lex. Significant tokens (not space, not comments, not bad) are kept. A block comment
   before the first significant token whose text, trimmed, is `mg:share` sets `Hinted`.
2. Split on every `;` token (empty pieces dropped).
3. **Reject** if any statement matches a reject rule (below). This runs over every token
   the lexer produced, including those before or after a bad token, so a reject-list
   feature is refused even in a message the lexer could not fully tokenise.
4. **Opaque** if the lexer saw a bad token, or a Unicode identifier does not decode:
   forwarded, `Tx` unknown, not read-only, not coalescable.
5. **Transaction effect** (below); an unmodelled statement makes the message opaque.
6. **Read-only-provable**, then **coalescable** (below).

### Which statements are rejected

Leading keyword (after an `EXPLAIN [ ( … ) ] [ANALYZE|ANALYSE|VERBOSE]…` prefix):

| Leading form | Result |
| --- | --- |
| `SET LOCAL …`, `SET TRANSACTION …`, `SET CONSTRAINTS …` | **allowed** (transaction-scoped) |
| `SET local.x …`, `SET transaction.x …`, `SET constraints.x …`, `SET local = …`, `SET local TO …`, a bare `SET LOCAL`, `SET LOCAL NAMES …`, `SET TRANSACTION NAMES …` | **rejected, session-set** (tier R shows `SET local.x = 1` sets a session-level custom setting) |
| every other `SET …`: `SET name`, `SET SESSION …`, `SET SESSION CHARACTERISTICS AS TRANSACTION …`, `SET ROLE`, `SET SESSION AUTHORIZATION`, `SET TIME ZONE`, `SET NAMES`, `SET SCHEMA`, `SET XML OPTION`, `SET "local" …` | **rejected, session-set** |
| `RESET …` | rejected, reset |
| `PREPARE TRANSACTION` | rejected, two-phase |
| `PREPARE` (other) | rejected, prepare |
| `EXECUTE` | rejected, execute |
| `DEALLOCATE` | rejected, deallocate |
| `LISTEN` / `UNLISTEN` | rejected |
| `DECLARE …` with `WITH HOLD` before the first `FOR` | rejected, cursor-with-hold (a CTE named `hold` after `FOR` is not) |
| `DISCARD`, `LOAD`, `COPY` (any direction) | rejected |
| `COMMIT PREPARED`, `ROLLBACK PREPARED` | rejected, two-phase |

Anywhere in a statement:

| Form | Result |
| --- | --- |
| word `TEMP`/`TEMPORARY` right after `CREATE`, `GLOBAL`, `LOCAL`, `INTO` or `REPLACE` (except `INSERT INTO temp` / `MERGE INTO temp`, a table named `temp`) | temp-object |
| any name (unquoted folded, quoted, or decoded `U&"…"` with `UESCAPE`) equal to `pg_temp` or starting `pg_temp_` | temp-object |
| `SET LOCAL search_path` (the name in any spelling) or `SET LOCAL SCHEMA` whose value holds a string (`'…'`, `$$…$$`, `E'…'`, `U&'…'`, `N'…'`) that contains `pg_temp` in any ASCII case, or that the classifier cannot read (an `E'…'` with a backslash, a `U&'…'` with a backslash or a `UESCAPE`) | temp-object (review 1 finding 3: `BEGIN; SET LOCAL search_path = 'pg_temp'; CREATE TABLE x …; COMMIT` leaves a session temp table on Postgres 17) |
| `set_config('search_path', value, true)` (setting name in any ASCII case) whose value is not exactly one string token, or is a string that may name `pg_temp` as above | temp-object |
| `EXECUTE` right after `AS` (`CREATE TABLE … AS EXECUTE`) | execute |
| a name followed by `(` (whitespace and comments between allowed; any schema; quoted or `U&`) equal to `pg_advisory_lock`, `pg_advisory_lock_shared`, `pg_try_advisory_lock`, `pg_try_advisory_lock_shared`, `pg_advisory_unlock`, `pg_advisory_unlock_shared`, `pg_advisory_unlock_all` | advisory-lock (the four `_xact_` functions are allowed) |
| a name `set_config` followed by `(` whose third top-level argument is not exactly the bare word `TRUE` (any case), including a missing third argument or an unclosed call; or whose first argument is not exactly one plain `'…'` string literal; or whose first argument names `standard_conforming_strings` or `client_encoding` (case-insensitively) | set-config (a setting name the classifier cannot read might be a lexing setting) |
| a name `pg_settings` in a statement that contains the word `UPDATE` | session-set (`UPDATE pg_settings SET setting = …` is `SET` in disguise; tier R canary) |
| a name equal (ASCII case-insensitively) to `standard_conforming_strings` or `client_encoding` in a statement that contains the word `SET` or `RESET` — `SET LOCAL` and `ALTER ROLE/DATABASE/SYSTEM … SET` included | session-set: both settings change how Postgres lexes the statements that follow, so a transaction-local change would let a later statement hide a `SET` inside what this lexer reads as a string (tier R canary: after `SET LOCAL standard_conforming_strings = off`, `SELECT '\' ; SELECT ' ; SET search_path = hidden ; --'` sets `search_path` on Postgres 17 while the lexer sees only strings) |

A message is rejected as a whole when any of its statements is rejected; the first reason
found is reported.

**Decision on `NOTIFY`:** allowed (forwarded), never read-only-provable, never coalescable;
the same for `pg_notify(...)`. It is delivered at commit and harmless without `LISTEN`.
**Decision on two-phase commit:** rejected (`PREPARE TRANSACTION`, `COMMIT PREPARED`,
`ROLLBACK PREPARED`): it leaves a prepared transaction outside the session.

### Transaction effect for the whole message

Per statement: `BEGIN …` and `START TRANSACTION …` begin; `COMMIT|END|ROLLBACK|ABORT
[WORK|TRANSACTION] [AND NO CHAIN]` end; `… AND CHAIN` begins; `SAVEPOINT`, `RELEASE`,
`ROLLBACK [WORK|TRANSACTION] TO` are within; any other modelled leading keyword (`SELECT`,
`VALUES`, `TABLE`, `WITH`, `SHOW`, `EXPLAIN`, `(`, `INSERT`, `UPDATE`, `DELETE`, `MERGE`,
`TRUNCATE`, `CREATE`, `ALTER`, `DROP`, `GRANT`, `REVOKE`, `COMMENT`, `ANALYZE`, `VACUUM`,
`REINDEX`, `CLUSTER`, `REFRESH`, `LOCK`, `NOTIFY`, allowed `SET`, `DECLARE`, `FETCH`, `MOVE`,
`CLOSE`, `CHECKPOINT`) is none; anything else (`CALL`, `DO`, `IMPORT`, `SECURITY LABEL`, a
statement containing `BEGIN ATOMIC`, unknown words, a `COMMIT` with trailing words) is
unknown. For the message: any unknown → `TxUnknown` (opaque); else any begin → `TxBegins`;
else any end → `TxEnds`; else any within → `TxWithin`; else `TxNone`. "Any begin" and not
"the last one" because an error after `BEGIN` aborts the rest of the message
(`BEGIN; SELECT 1/0; COMMIT` leaves `E`): the flag is conservative. The lease's real end
is always the status byte.

### Read-only-provable

Every statement of the message (at least one) must:

- lead with `SELECT`, `VALUES`, `TABLE`, `WITH`, `SHOW`, or `(` followed by one of these, or
  `EXPLAIN` (options allowed) followed by one of these;
- contain none of these unquoted words anywhere: `INSERT UPDATE DELETE MERGE TRUNCATE CREATE
  ALTER DROP GRANT REVOKE COMMENT CALL DO COPY LOCK VACUUM ANALYZE ANALYSE REINDEX CLUSTER
  REFRESH IMPORT SECURITY REASSIGN NOTIFY LISTEN UNLISTEN SET RESET DISCARD PREPARE EXECUTE
  DEALLOCATE DECLARE BEGIN START COMMIT ROLLBACK ABORT SAVEPOINT RELEASE CHECKPOINT LOAD INTO
  OPERATOR NEXTVAL SETVAL`, and no `FOR` followed by `UPDATE`, `SHARE`, `NO` or `KEY`;
- contain no parameter (`$1`) and only operators from the standard list in
  `docs/reject-list.md`;
- call only allow-listed functions: every name followed by `(` is a call except reserved
  keywords and type names that cannot be function names (`rejNonCallWords`), a word right
  after `)` (`OVER (`, `FILTER (`, an alias column list), a word right after `AS`
  (`AS sub(x, y)`), and a CTE name right after `WITH` / `WITH RECURSIVE`; a call is allowed
  only if the name is on `AllowList` and it is unqualified or qualified by exactly
  `pg_catalog`. A name right after `.` is always a call when `(` follows, even a reserved
  word (`public.select()`, `public.in(1)`: Postgres calls any keyword after a schema
  qualifier);
- for `EXPLAIN ( … )`, contain no option name, in any spelling (unquoted, `"…"`, `U&"…"`),
  equal to `analyze` or `analyse` (ASCII case-insensitively);
- use a dotted name that is not a call (`a.b`, `a.b.c`) only directly after `FROM`, `JOIN`,
  `TABLE` or `ONLY` in table position: that keyword must sit at the statement's top level or
  directly inside a parenthesis whose first token is `SELECT`, `WITH` or `TABLE` (a
  subquery), and a `FROM` must not follow `DISTINCT` (`IS [NOT] DISTINCT FROM`). The `FROM`
  of `trim(… FROM x)`, `substring(… FROM x)`, `extract(… FROM x)` and `overlay(… FROM x)` is
  therefore an expression position. `t.a` in a select list or a condition is refused,
  because the attribute notation `t.f` can call a function `f(t)` without parentheses. This
  is a deliberate false negative: queries with qualified column references are not proved.

### Coalescable (decision D10)

Read-only-provable, exactly one statement, outside a transaction, first token `SELECT`
(not `WITH`, `(`, `VALUES`, `TABLE`, `SHOW`, `EXPLAIN`), no per-call keyword
(`CURRENT_DATE CURRENT_TIME CURRENT_TIMESTAMP LOCALTIME LOCALTIMESTAMP CURRENT_USER
SESSION_USER USER CURRENT_ROLE CURRENT_SCHEMA CURRENT_CATALOG SYSTEM_USER TABLESAMPLE`), no
name (unquoted, quoted or `U&`, any schema) equal to a time type `date time timetz timestamp
timestamptz` (text cast to them can name the current time: `('n' || 'ow')::timestamptz`),
no string literal containing `now`, `today`, `tomorrow` or `yesterday` in any ASCII case
(Postgres's relative-time input words, also reached by an untyped literal coerced to a
column type), no `E'…'` string with a backslash and no `U&'…'` string with an escape (not
decoded), and every call has "yes" in the coalescable column of `AllowList`. Read-only but not
coalescable on the list: `age`, `clock_timestamp`, `current_schema`, `current_setting`,
`gen_random_uuid`, `now`, `pg_backend_pid`, `pg_sleep`, `random`, `statement_timestamp`,
`timeofday`, `transaction_timestamp`. The key is the exact text (comments and the hint
included), as bytes, copied; the hint is a flag only and never makes a statement
coalescable.

### Empty messages

An empty string, whitespace, comments only, `;` only: `ClassStartLease` (or continue),
0 statements, `TxNone`, not read-only, not coalescable (`TestClassifierEmptyMessages`).
Postgres answers `EmptyQueryResponse`; the proxy forwards them.

## The error table and the replies

`ErrorRows` (indexed by `ErrRowID`) holds 20 rows: the 15 situations of PLAN.md 9.1 (shed
`53M01`, rate gate `53M02`, breaker `53M03`, coalesce cap `53M04`, draining `53M05`,
reject list `0A000`, extended protocol `0A000`, cancelled while queued `57014`, lost after a
read `08006`, lost after a possible write `08007`, lost inside a transaction `FATAL 08006`,
idle in transaction `FATAL 25P03`, plaintext under TLS required `FATAL 28000`, TLS
handshake failure (close, no message), reply partly sent (close, no message), and the
pass-through row for Postgres's own errors), plus four rows the brief left to this chunk to
fix for chunks 4, 6 and 7: `startup-parameter` (`FATAL 0A000`, PLAN.md 9.4's startup
parameters), `auth-failed` (`FATAL 28P01`, the text Postgres uses), `too-many-clients`
(`FATAL 53300`, the text Postgres uses) and `protocol-violation` (`FATAL 08P01`). Each row:
id, situation, reply kind, SQLSTATE, message, reached the database, may a write have been
applied, safe to retry, what happens to the client connection, refusal class, the chunk that
implements it, and the counter key `reply_<id>`.

`ReplyBuilder.Build(dst, id, status)` appends the row's reply and returns a `ReplyAction`
(`End`: ready / none / sync; `Close`). `ERROR` rows: `ErrorResponse` (`S` and `V` severity,
`C` code, `M` message, `D` = `mini-gateway error row: <id>`) then `ReadyForQuery` with the
status given (`I`, `T` or `E`; anything else is refused with `ReplyErrBadStatus`). The
draining row asks to close when the status is `I`. `FATAL` rows: no end marker, `Close`.
The extended row: `ErrorResponse` only, `End = ReplyEndSync`. Close-only rows: nothing
written, `Close`. The pass-through row cannot be built (`ReplyErrNoMessage`). Every call is
counted in `ByRow[id]` and `Total`, failures also in `Failed`, so a reply is counted on
every path. `ReplyReady(dst, status)` appends a bare `ReadyForQuery`.

`SyncDiscard.Step(msgType)` encodes the discard-until-Sync rule: not discarding, an
extended message (`P B D E C H`) → refuse (send the extended row) and start discarding; a
`Sync` or `FunctionCall` (`F`) → refuse and send `ReadyForQuery` at once; discarding,
`Sync` → send `ReadyForQuery`, stop; `Terminate` → pass through, stop (Postgres also leaves
the discard state on `Terminate`); anything else (a `Query` too) → drop.

**Detail line decision:** `D` carries `mini-gateway error row: <id>`; the row id is the
stable name, the SQLSTATE alone does not distinguish `0A000` rows.

## Generated documents

`scripts/gen-docs.sh` runs every executable `scripts/gen-docs.d/*.sh` with its arguments.
`scripts/gen-docs.d/c05-contracts.sh` runs `go test -run '^TestClassifierWriteContractDocs$'
./internal/pgwire` with `MG_GEN_DOCS_DIR=<scratch>` and copies (or, with `--check`,
compares) `error-contract.md` and `reject-list.md`. `TestClassifierContractDocsMatchTheCode`
fails a plain `go test` too when a committed document differs. The `deny` lines for both
documents were already in `tools/lint-rules/prose-paths.txt` (chunk 0).

## Observability

- `Classifier.Snapshot()` → `ClassifierSnapshot`: `Classified`; `ByClass[work.NumClasses]`;
  `ByReason[work.NumRejectReasons]`; `Unclassifiable` (the lexer saw a bad token, counted
  whether or not the message was also rejected); `Opaque`; `ReadOnly`; `Coalescable`;
  `Hinted`; `MayLeaveOpen[no, yes, unknown]`; `Statements` (SQL statements, summed).
  `Check()`: classified = sum over classes = sum over may-leave-open; rejects = sum over
  reasons; coalescable ≤ read-only.
- `ReplyBuilder.Snapshot()` → `ReplySnapshot`: `ByRow[ErrRowCount]`, `Total`, `Failed`;
  conservation `Total` = sum of `ByRow`.
- `SyncDiscard.Snapshot()`: `Refused`, `Discarded`, `Synced` (one helper per client
  connection; the wire layer sums them).
- `ClassifierMetrics(snap)` and `ReplyMetrics(snap)` give flush-ready rows (component,
  metric, outcome label, reason label, value) for chunk 3's CSV adapter: component
  `classifier` with metrics `statements`, `statements_by_class` (outcome = class),
  `rejects` (reason = reject reason), `unclassifiable`, `opaque`, `read_only`,
  `coalescable`, `hinted`, `sql_statements`, `may_leave_open` (outcome = no/yes/unknown);
  component `wire` with `proxy_replies` (reason = row id), `proxy_replies_total`,
  `proxy_reply_failures`. These names are proposals for chunk 3's registry
  (`docs/csv-schema.md`); chunk 6 wires the flush. All counters; no gauges.
- Tests: `TestClassifierSnapshotCountersMoveAndConserve` (every class but none/answer-locally,
  every reason the classifier produces, every may-leave-open value, and every other counter
  moves over the corpus; `Check` passes and catches two planted inconsistencies),
  `TestReplyEveryRowIsWellFormedWithTheRightMarker` (counted on every path, conservation),
  `TestReplyRefusesBadInput` (failures counted), `TestSyncDiscardRule`.

## Steady-state row and memory per classification

Nothing grows at run time: the counters are fixed-size arrays (bounded classes, reasons,
rows); the classifier and the builders keep no per-message state. Gauge: none.

Memory per classification: at most `ClassifMemoryFactor` (80) bytes per input byte plus
`ClassifMemoryConstant` (64 KiB). The lexer counts significant tokens first and allocates
the token slice once (24 bytes per token); the paren table is another 16 bytes per token;
the paren stack and the statement spans are sized exactly by a counting pass; unquoted
names are folded into a fixed 63-byte scratch buffer (no allocation); quoted and Unicode
names allocate their decoded bytes; the share key is one copy of the text. The paren table
holds four `int32` per token (match, two commas, enclosing parenthesis). Measured worst
case at 1 MiB over eleven adversarial shapes (all parentheses, all `;`, dotted names,
nested `set_config`, Unicode identifiers, …): 56 bytes per input byte
(`TestClassifierMemoryIsBounded`). Time is linear (every scan is a single pass; the
`set_config` argument check uses the precomputed comma positions).

## Claims and the tests that prove them

| # | Claim | Tests (file: function) |
| --- | --- | --- |
| 1 | No statement containing a reject-list feature is forwardable, however spelled, cased, commented or padded (lexer limits stated) | `classify_test.go: TestClassifierCorpus` (186 rejected records: case, comments, whitespace, multi-statement, quoted, schema-qualified, `U&` with `UESCAPE`, comment between name and `(`, invalid UTF-8 around them, `SET local.x`, `UPDATE pg_settings`, `SET LOCAL standard_conforming_strings`, `SET LOCAL NAMES`, `set_config` with an unreadable name, string `search_path` values naming `pg_temp`), `classify_spellings_test.go: TestClassifierSpellingsAlwaysRejectTheRejectList` (seeded: every reject reason, its spellings — schema qualification, quoting, `U&` with and without `UESCAPE`, case, comments, whitespace, parenthesised `EXPLAIN` options, expression positions including `IS DISTINCT FROM`, `trim(FROM …)`, `substring(… FROM …)`, CTEs, `LATERAL`, `ROWS FROM` — inside multi-statement wrappers, padded by `clsPad`), `classify_repair_test.go: TestClassifierSearchPathStringNamingTempSchema`, `TestClassifierGeneratedStatements` (a reject at the end of a 450 KB message, after a NUL byte, after invalid UTF-8), `TestClassifierPaddingAndCaseProperty`; `rejectlist_test.go: TestRejectRulesExamplesClassifyAsDocumented`; tier R `classify_tierr_test.go: TestTierRClassifierDifferential` (every forwarded corpus statement leaves the session state — session-sourced settings changed from their reset value, own advisory locks, prepared statements, cursors, LISTEN channels, temp relations, current and session user — unchanged on Postgres 17; oracle canaries show the oracle sees 11 rejected forms, that `SET local.x`, `SET transaction.y`, `SET constraints.z` are session settings, and that a transaction-local `standard_conforming_strings = off` lets a later statement hide a `SET` from this lexer — the reason that setting is rejected; repair canaries: seven `search_path`-to-`pg_temp` forms leave a session temp table on Postgres and are rejected) |
| 2 | Nothing that can write, lock or call a non-allow-listed function is read-only-provable; nothing outside D10 is coalescable | `TestClassifierCorpus` (data-changing CTEs at any depth, locking clauses, `SELECT … INTO`, `EXPLAIN ANALYZE`, sequences, unknown, schema-qualified, quoted and attribute-notation calls, parameters, odd operators, keyword-named schema-qualified calls, attribute calls after expression-position `FROM`, quoted `EXPLAIN` options, time literals), `classify_spellings_test.go: TestClassifierSpellingsNeverProveUnlistedCalls` (seeded, about 8,000 distinct statements: every reserved/type word, write keyword, known writing function, 40 random names and a near-miss of every allow-listed name, each as a call under eight qualifiers, three quotings and seven argument shapes in 33 expression positions and 4 `FROM` positions, plus attribute-notation forms; and every allow-listed name under a qualifier other than `pg_catalog`; never read-only, never coalescable), `TestClassifierSpellingsNeverCoalesceNonCoalescableCalls`, `classify_repair_test.go: TestClassifierSchemaQualifiedKeywordNamesAreCalls`, `TestClassifierAttributeCallAfterExpressionFrom`, `TestClassifierExplainQuotedAnalyzeOption`, `TestClassifierTimeDependentLiteralsAreNotCoalescable`, `TestClassifierInvariantsOverMutants`, `FuzzClassify` (coalescable ⇒ read-only, one statement, outside a transaction); tier R `TestTierRClassifierDifferential` (each of the read-only-provable records that Postgres accepts leaves the tables, sequence, large objects and prepared transactions unchanged and runs inside `BEGIN READ ONLY`; oracle canaries: 5 writing statements are caught; repair canaries: 14 statements the classifier refuses to prove — keyword-named schema-qualified calls, attribute calls after `IS DISTINCT FROM` / `trim(FROM` / `substring(FROM`, quoted `EXPLAIN ("analyze")` — really write on Postgres, and four time-literal shapes give a different value on each call); `classify_obs_test.go: TestClassifierReadOnlyCorpusLeavesTheFakeUnchanged` (every read-only record against the fake: write log empty, table unchanged) |
| 3 | Anything the lexer cannot fully tokenise is forwarded, never shared, never read-only (see Deviations for the precise form) | corpus records flagged `bad`; `TestClassifierConservativeWhenUntokenisable` (≥ 1,000 mutated corpus entries the lexer cannot tokenise: never read-only, never coalescable, no key; forwarded as opaque with `Tx` unknown unless a reject-list feature was found); `FuzzClassify` |
| 4 | Classification does not depend on whitespace, comments between tokens, or keyword case | `TestClassifierPaddingAndCaseProperty` (6 seeds × every corpus record: random whitespace incl. `\f \v \r\n`, block, nested and line comments — some containing `;` and `SET` — between every pair of tokens, and random case of every word); `classify_repair_test.go: TestLexOperatorTrailingSignFollowsPostgres` (`71+-3` classifies as `71+ -3`; tier R shows Postgres gives the same value for both) |
| 5 | No panic, bounded memory on any byte string | `FuzzLex`, `FuzzClassify` (gate: 20 s each), `TestClassifierMemoryIsBounded`, `TestClassifierGeneratedStatements` (20,000 nested parentheses, 5,000 nested `set_config`, 20,000 statements) |
| 6 | A message marked "leaves no transaction open" never leaves `T` or `E` on Postgres | tier R `TestTierRClassifierDifferential` (every such corpus record, alone, on a fresh connection: final status `I`; canaries `BEGIN`, `BEGIN; SELECT 1/0; COMMIT`, `SELECT 1; BEGIN` are caught by the oracle) |
| 7 | Every proxy-made reply is a well-formed `ErrorResponse` with the row's SQLSTATE and the correct end marker (none for FATAL), and is counted | `errors_test.go: TestReplyEveryRowIsWellFormedWithTheRightMarker`, `TestReplyGoldenBytes`, `TestReplyBytesMatchAHandEncoding`, `TestErrorRowsAreWellFormed`, `TestSyncDiscardRepliesEndWithReadyAfterSync` |
| 8 | `docs/error-contract.md` and `docs/reject-list.md` cannot drift from the code | `rejectlist_test.go: TestClassifierContractDocsMatchTheCode`; gate G8 `scripts/gen-docs.sh --check` |
| 9 | `internal/work` imports only the standard library; the classifier's output is entirely `internal/work` types | `work_test.go: TestWorkImportsOnlyTheStandardLibrary`; gate G1 importlint (`deny … internal/work/… : internal/pgwire/… pgproto3 …`) and detlint (`internal/work` in `det-packages.txt`); `ClassifyQuery` and `Classify` return `work.Unit` |
| 10 | Reject-list statements never reach the database | `classify_e2e_test.go: TestScenarioClassifierRejectList`, `TestScenarioClassifierRejectOnly` (fake statement count exactly the allowed ones / zero, statement log holds only allowed texts), `TestClassifierRejectOnlyFlushNumbers`; gate extra |

## End-to-end scenario (tier S)

`classify_e2e_test.go` holds a minimal wire harness (about 200 lines, test code only): a
simnet listener `gw` that, per client connection, dials the fake Postgres, relays the
startup packet and every backend byte, frames client messages (length-prefixed), feeds
every message to a `SyncDiscard`, classifies every `Query` with the client's current status
(tracked from the backend's `ReadyForQuery`), and answers a rejected one with
`ReplyBuilder.Build(…, ErrRowUnsupportedFeature, status)` without forwarding it. Chunk 6
replaces the harness with chunk 4's framer and the engine.

- `TestScenarioClassifierRejectList`: 1–3 simulated clients (`workload.Probe`), seeded;
  each sends `SHOW search_path`, then every reject-rule example twice (as written and with
  seeded padding and case) plus three extra `SET` spellings, in a seeded order, each
  followed by `SELECT 1`; then a `Parse` + `Sync`; then `SHOW search_path`; then
  `Terminate`. Checks: every client sees exactly `E:0A000 ZI` per reject and `T D C:SELECT 1
  ZI` per follow-up (the connection stays usable), `E:0A000 ZI` for the extended pair, and
  the same `search_path` before and after; classifier rejects = reject statements sent;
  reply counters = rejects and parse messages; the fake started exactly the allowed
  statements and its statement log holds only `SELECT 1` and `SHOW search_path`; no
  writes, no parameter reports; classifier conservation; counters non-zero; quiesce
  (`CheckQuiesce`) after the clients leave; replay (run twice per seed, identical digests;
  `REPLAY:` line on failure).
- `TestScenarioClassifierRejectOnly`: the same with no allowed statement at all: the fake's
  statement count is exactly 0.
- **Planted-bug canary / ablation** `TestClassifierPlantedSetForwardableIsCaught`: the
  test-only switch `ClassifierConfig.plantSetForwardable` (unexported, set only by tests in
  the package) treats every `SET` as forwardable; the scenario must pass without it and fail
  with it, with both "a rejected statement reached the database" and "session state
  changed" in the failure (seeds 1–3).
- Gate extra: both scenarios, pinned seeds plus 20, in two processes; identical digests;
  then the canary.

## Tier R

`TestTierRClassifierDifferential` (in `scripts/tier-r.sh`'s run; about 60 s) creates the
database `mg_c05_classifier`, and before each statement rebuilds the schema (`kv`, `t`,
`u`, sequence `s`, materialized view `mv`) so that earlier statements cannot change later
ones; the schema also holds a table `w` (in the checksum) and writing SQL functions
`public."select"()`, `public."in"(int)`, `public."exists"()`, `public."current_timestamp"(int)`,
`evil_write(t)` and `evil_write_text(t)` that insert into `w`, so the repair shapes really
write. It then checks, per corpus record that is not rejected and not `noexec`: the
session-state oracle; for "leaves no transaction open" records, the final status `I`; for
read-only-provable records, success, unchanged checksum (tables, sequence, relation count,
large objects, prepared transactions) and success inside `BEGIN READ ONLY`. Last run
(2026-11-25, Postgres 17, repair round): 374 forwarded statements left the session state
unchanged; 327 "no transaction left open" statements ended with `I`; 163 read-only-provable
statements passed both oracles; 188 records not run (rejected, or `noexec`); plus the oracle
canaries and the repair canaries (`clsTierRRepairCanaries`: seven `search_path` forms that
leave a session temp table, fourteen writing shapes the read-only oracle catches, four
time literals whose value moves between calls, three operator pairs Postgres evaluates
alike). About 100 s.

Excluded (`noexec`): the two records with invalid UTF-8 (both read-only and coalescable): Postgres refuses invalid UTF-8 before parsing, so they cannot succeed
inside `BEGIN READ ONLY`; they are classified by the tier S tests. Rejected records are not
run (they never reach a database). No read-only record had to be excluded for volatility:
`BEGIN READ ONLY` accepts `random()`, `now()`, `pg_sleep(0)` and the other read-only
functions on the list.

Oracle limit: Postgres does not list custom placeholder settings (`local.x`) in
`pg_settings`, so the session-state oracle cannot see them; a separate check
(`clsTierRCustomSettingIsSession`) shows that `SET local.x`, `SET transaction.y` and
`SET constraints.z` are session settings and that the classifier rejects them.

## CSV-flush assertions

`test/analysis/c05-reject-list/*.sql`, written to the schema of PLAN.md 7.7 against the
views of `test/analysis/views.sql` (`runs`, `final`; chunk 3 builds them and
`scripts/csv-assert.sh`): rejects = statements sent and classified = rejects, and
`fakepg/statements_started` = 0, for the scenario `classifier-reject-only`; both check the
schema version first and fail when a metric has no rows. The same assertions as exact Go
checks: `TestClassifierRejectOnlyFlushNumbers` (on `ClassifierMetrics` rows) and the
scenario check. Not run by the gate yet: there is no flush on this branch (chunk 6 wires it
into every scenario) and the runner is chunk 3's. The metric `scenario/statements_sent`
must be exported by the harness when chunk 6 wires the flush.

## Stated limits of the classifier

For the prose seat (Neon) and the book. The classifier is a lexer with a small amount of
pattern matching, not a SQL parser.

1. **Hidden behaviour is out of scope.** A function, procedure, `DO` block, view, rule,
   trigger, column default, type input or type-modifier function, or a user-defined
   operator can hide a session change, a write or a lock. `DO $$ … EXECUTE 'SET …' … $$`
   is forwarded (as opaque); a view over a writing function looks read-only. The second
   line of defence (D8: a `ParameterStatus` during a client's turn marks the connection
   dirty) catches reported settings only.
2. **Function resolution is by name.** A call is allowed if its name is on the list and it
   is unqualified or qualified by `pg_catalog`. Postgres resolves overloads across the
   whole search path by argument types, so a user function `public.lower(integer)` can be
   chosen for `lower(1)` over `pg_catalog.lower(text)`. The list is trusted only in a
   database where untrusted users cannot create functions in the search path.
3. **Attribute notation** (`t.f` calling `f(t)`) cannot be told from a column reference, so
   a dotted name is accepted only in table position (`FROM`, `JOIN`, `TABLE`, `ONLY` at a
   query level, not `IS DISTINCT FROM` and not the `FROM` inside `trim`, `substring`,
   `extract` or `overlay`). This makes the proof refuse every qualified column reference and
   every table list after a comma (`FROM a, s.b`) — false negatives, never a false
   positive.
4. **Identifier case folding is ASCII only**, as Postgres does for multibyte encodings:
   `A–Z` fold, every byte ≥ 0x80 is an identifier byte and is compared exactly. Names are
   compared after truncation to 63 bytes (Postgres's `NAMEDATALEN - 1`).
5. **`standard_conforming_strings` is assumed on** (the default since 9.1) and the client
   encoding is assumed to be the pool's fixed one (UTF-8 or another encoding in which no
   multibyte character contains the bytes `'` or `\`). With `standard_conforming_strings`
   off, or with an encoding such as SJIS, Postgres lexes `'…\'…'` differently from this
   lexer and a `SET` can hide inside what the classifier reads as a string (shown on
   Postgres 17 in tier R). The classifier rejects every visible way to change either
   setting (`SET`, `SET LOCAL`, `SET [LOCAL] NAMES`, `set_config`, `ALTER … SET`, `UPDATE
   pg_settings`). What it cannot see — a server or database default, a function that calls
   `set_config` — is for the wire layer: both settings are reported in `ParameterStatus`,
   so chunks 4 and 6 must refuse a database connection whose `standard_conforming_strings`
   is not `on` or whose `client_encoding` is not the pool's, and treat a `ParameterStatus`
   for either during a client's turn as a dirty connection (D8).
6. **Numbers follow Postgres 16+** (`0x`, `0o`, `0b`, `_` separators). A number directly
   followed by a letter is split into a number and a word (Postgres 17 rejects such
   "trailing junk"; the split only makes the classifier see more names).
7. **Statement splitting is on every `;`**, including those inside parentheses and inside
   SQL-standard function bodies (`BEGIN ATOMIC … END`). A statement containing `BEGIN
   ATOMIC` makes the message opaque; a reject-list pattern inside such a body is still
   rejected (an over-rejection, never an under-rejection).
8. **Over-rejection is possible in rare spellings**: a table named `pg_advisory_lock`
   followed by a column list, a reference to `pg_temp` in `SET LOCAL search_path`, `SELECT 1
   AS execute` after `AS`, and any `set_config` whose third argument is not the bare word
   `true` (`'true'`, `(true)`, `true::bool`) or whose setting name is not a plain string
   literal (`$$search_path$$`, `E'…'`, an expression), and any statement with `SET` or
   `RESET` that mentions `client_encoding` or `standard_conforming_strings`, are rejected.
   Apart from that last rule (unreadable `set_config` names) and the `search_path` rule
   below, nothing is rejected because it is unknown. The `search_path` rule over-rejects
   on purpose: `SET LOCAL search_path = 'PG_TEMP'` names a (missing) schema `"PG_TEMP"`
   because `SET` quotes each string value (checked on Postgres 17), yet it is rejected (the
   match is ASCII case-insensitive because `set_config` lowercases unquoted list elements);
   any string containing `pg_temp` (`'my_pg_temp_x'`) and any `E'…'`/`U&'…'` value with an
   escape is rejected; `set_config('search_path', <anything but one string>, true)` is
   rejected.
9. **Session state that is not on the reject list is not rejected**: `setseed()`, the
   sequence cache behind `currval`/`lastval`, large-object descriptors (transaction-scoped
   anyway), `ALTER ROLE/DATABASE/SYSTEM … SET` (persistent, not session-level). These are
   forwarded; none is read-only-provable.
10. **The transaction-effect flag is coarse and conservative**: any `BEGIN` anywhere in the
    message reports "may leave a transaction open: yes" even when a later `COMMIT` would
    close it, because an error in between aborts the rest of the message. `CALL` and `DO`
    are "unknown". The status byte decides the lease's real end.
11. **Untokenisable input**: the lexer marks an unterminated string, quoted identifier,
    dollar quote or comment, and bytes Postgres's lexer would also refuse, as "cannot fully
    tokenise". Such a message is forwarded as opaque (Postgres then reports the syntax
    error and executes nothing, because it parses the whole message before running any
    statement) — unless the tokens it could read already contain a reject-list feature, in
    which case it is rejected.
12. **The read-only proof is about the statement text, not about time**: a read-only
    statement can still take locks implicitly (`AccessShareLock`), wait on other locks, or
    run for long (`pg_sleep`, `generate_series`).
13. **Coalescing and time**: a statement that names a time type or holds a string with a
    relative-time word is never coalescable, which also refuses harmless ones
    (`SELECT DATE '2020-01-02'`, `SELECT 'snow'`). Values that depend on session settings
    (`TimeZone`, `DateStyle`) are assumed equal across the pool's connections, because the
    proxy rejects session `SET` and coalesces only outside a transaction. A domain over a
    time type, or a user type whose input function reads the clock, is hidden behaviour
    (limit 1).

## Decisions the brief left open

1. **`inTx` is an argument of `Classify`.** The wire layer knows the client's status byte;
   the classifier uses it for start/continue and to keep coalescing off inside a
   transaction. Read-only-provable does not depend on it.
2. **Rejection wins over "cannot tokenise"** (see Deviations).
3. **Extra rows in the error table** for situations PLAN.md names but 9.1 does not tabulate
   (startup parameter, authentication failure, client cap, protocol violation), so chunks 4,
   6 and 7 have codes to use. Rows with no message (TLS handshake, partial reply,
   pass-through) are in the table with an empty SQLSTATE; the SQLSTATE test applies to rows
   that send a message.
4. **A lone `Sync` or a `FunctionCall`** is refused with the extended row followed at once
   by `ReadyForQuery`.
5. **`ClassAnswerLocally` is never produced** in v1.
6. **`internal/pgwire` is not added to the determinism lint**: chunk 4 builds TLS and SCRAM
   code in the same package, which detlint's import list does not allow; the orchestrator
   can add it at integration. Everything in this chunk's `internal/pgwire` files is
   deterministic (no time, no randomness, no maps reaching output, no goroutines) and imports
   only `bytes`, `errors`, `strconv`, `strings`, `internal/work` and `pgproto3`.
7. **No race-list entry**: nothing in this chunk uses concurrency.
8. **The share-key scope** is a field of `ClassifierConfig` (`work.ScopeGlobal` default,
   `work.ScopeTenant`); chunk 11 sets it.

## Deviations

- **Claim 3, precise form.** "Anything the lexer cannot fully tokenise is forwarded" holds
  except when the tokens the lexer could read contain a reject-list feature: that message
  is rejected (claim 1 wins). Such a message cannot run on Postgres anyway (a lexer error
  aborts the whole message), so rejecting it changes nothing for the database and keeps
  claim 1 free of a "however spelled" escape through a trailing unterminated quote. Never
  shared and never read-only hold without exception.
- **The e2e harness is test code**, not chunk 4's framer (not on this branch, as the brief
  says).
- **CSV SQL assertions are written but not executed** here (no flush or runner on this
  branch).
- **Size**: product code 2,152 lines after the repair round (1,969 before it; estimate
  ~1,300; the tables of `rejectlist.go` and `errors.go` are about 400 of them); tests 2,827
  lines plus the 562-record corpus.
- **Two rules reject for an unreadable input**: `set_config(<expression>, …, true)` is
  rejected because the setting name cannot be read and might be a lexing setting (see
  limit 5); and a transaction-local `search_path` whose value cannot be read (an escaped
  `E'…'`/`U&'…'` string, or a `set_config` value that is not one string literal) is
  rejected as temp-object because it might name `pg_temp` (repair round, review 1
  finding 3). The brief says nothing is rejected because it is unknown; these are the two
  exceptions, made on purpose.

## Narrowed claims

- **Claim 3** (adversarial `TestAdv_3_RejectBeforeLexError`, BROKEN). Precise form:
  "A message the lexer cannot fully tokenise is never read-only-provable, never
  coalescable and never given a share key. It is forwarded as opaque (`Tx` unknown)
  unless the tokens the lexer could read already contain a reject-list feature; then it
  is rejected with that feature's reason." The behaviour is the deviation the orchestrator
  accepted (brief of the repair round, item 7): `DISCARD ALL; SELECT 'unfinished` is
  rejected, not forwarded. Postgres parses the whole message before running any of it, so
  such a message cannot execute anything; rejecting it keeps claim 1 free of an escape
  through a trailing unterminated quote. The adversarial test asserts the original wording
  ("forwarded"), which this narrowing replaces; it is left failing as evidence and is the
  only failing test in `go test ./...`. The code was not changed.

## Repair round

Repair of review 1 (`REVIEW-1.md`, FAIL) and the adversary's three BROKEN attacks
(`agent_work/adversarial/c05-classifier/RESULTS.md`), 2026-11-25. Every finding was first
reproduced by a regression test that failed on `b8f45cf` (`classify_repair_test.go`);
then the class of each hole was closed, not the example. Commits: `1be0b3e` (fixes,
regression tests, corpus, regenerated `docs/reject-list.md`), `ce4da7e` (seeded spelling
generator), `e8b2f47` (tier R repair shapes), `0ff1443` (scenario name, SQL guards).

- **Review F1 [BLOCKER] schema-qualified keyword-named calls** — fixed in `1be0b3e`:
  `notACall` never applies to a name right after `.`, so `<schema>.<any word>(` is checked
  against the allow-list and the `pg_catalog` qualifier. Regression
  `TestClassifierSchemaQualifiedKeywordNamesAreCalls` (all 106 words of `rejNonCallWords`
  under four spellings); corpus records; tier R repair canary (`public.select()`,
  `public.in(1)`, `public.exists()`, `public.current_timestamp(3)` write on Postgres 17).
- **Review F2 [BLOCKER] attribute-notation calls after an expression-position `FROM`** —
  fixed in `1be0b3e`: a table keyword counts only at a query level (statement top or a
  parenthesis opening `SELECT`/`WITH`/`TABLE`, tracked by a new enclosing-parenthesis
  index) and never after `DISTINCT`; `a.b.c` inherits it because every dot is checked.
  Regression `TestClassifierAttributeCallAfterExpressionFrom` (`IS [NOT] DISTINCT FROM`,
  `trim`, `substring … FROM/FOR`, `extract`, `overlay`, chained, nested, in `WHERE`);
  corpus; tier R canary (Postgres writes through five of these shapes).
- **Review F3 [MAJOR] string-literal `search_path` naming `pg_temp`** — fixed in
  `1be0b3e`: `SET LOCAL search_path` (any spelling of the name) / `SET LOCAL SCHEMA` with a
  string that contains `pg_temp` or cannot be read, and `set_config('search_path', v,
  true)` unless `v` is one string free of `pg_temp`, are rejected as temp-object.
  Regression `TestClassifierSearchPathStringNamingTempSchema` (26 forms incl. `$$…$$`,
  `E'…'`, `U&'…'` with `UESCAPE`, quoted and `U&` setting names, concatenation,
  `pg_catalog.set_config`, the three `BEGIN … COMMIT` sequences; 7 allowed controls);
  tier R canary (seven forms leave a session temp table on Postgres 17 and are rejected).
- **Review F4 [MAJOR] quoted `EXPLAIN` option `"analyze"`** — fixed in `1be0b3e`: every
  name in an `EXPLAIN ( … )` option list is decoded (quoted, `U&`) and compared
  case-insensitively with `analyze`/`analyse`. Regression
  `TestClassifierExplainQuotedAnalyzeOption`; corpus; tier R canary
  (`EXPLAIN ("analyze") SELECT public.select()` writes on Postgres).
- **Review F5 [MINOR] scenario name and empty-match guard** — fixed in `0ff1443`: the
  reject-only scenario is named `classifier-reject-only`; both SQL files add a violation
  row when no run of that scenario exists (checked with DuckDB: an empty `runs` table gives
  one violation row, a passing run gives none).
- **Review F6 [MINOR] stale counts in NOTES.md** — fixed in this notes commit: the corpus
  counts, the rejected-record count in the claims table and the `noexec` sentence now
  match the corpus (562 records, 186 rejected, 165 read-only, 116 coalescable, 2 `noexec`).
- **Adversary claim 2 `TestAdv_2_TimeDependentLiterals` (BROKEN)** — fixed in `1be0b3e`:
  a product defect against D10. Coalescable now also requires no time-type name and no
  string holding a relative-time word or an undecoded escape (the class: a literal or
  computed text read by a time type's input). Regression
  `TestClassifierTimeDependentLiteralsAreNotCoalescable` (19 shapes incl. typed literals,
  `('n' || 'ow')::timestamptz`, quoted and qualified type names, implicit coercion
  `date_trunc('day', 'now')`); tier R canary (values move between calls). The adversarial
  test now passes.
- **Adversary claim 3 `TestAdv_3_RejectBeforeLexError` (BROKEN)** — narrowed, see
  "Narrowed claims"; code unchanged; the adversarial test is correct against the original
  wording and stays failing.
- **Adversary claim 4 `TestAdv_4_PostgresUnaryOperatorBoundary` (BROKEN)** — fixed in
  `1be0b3e`: a product defect. The lexer now applies Postgres's rule for a trailing `+`/`-`
  in a multi-character operator. Regression `TestLexOperatorTrailingSignFollowsPostgres`;
  tier R canary (Postgres gives equal values for three pairs, the classifier equal
  classes). Corpus record `SELECT 1 +++ 2` changed from not-read-only to read-only and
  coalescable (Postgres reads it as `1 + + + 2`; tier R runs it inside `BEGIN READ ONLY`).
  The adversarial test now passes.
- **Class coverage (repair brief item 6)** — `ce4da7e`: `classify_spellings_test.go`, a
  seeded generator (`clsSpellSeed`) composing schema qualification, quoting, `U&` with and
  without `UESCAPE`, case, comments, whitespace, parenthesised `EXPLAIN` options and
  expression-position keywords over every reject reason (must be rejected with that
  reason) and over every reserved/type word, write keyword, known writing function, random
  name and near-miss of an allow-listed name (never read-only, never coalescable), plus
  every non-coalescable allow-listed function, per-call keyword, time type and relative-time
  literal (never coalescable). Against `b8f45cf` it reports about 1,800 failures (statement × transaction state);
  against the repair it reports none. Tier R extended in `e8b2f47` (see "Tier R").
- **Side effects of the fixes (false negatives, by design)**: seven corpus records lost
  coalescable — two typed time literals (`DATE '2020-01-02'`, `TIMESTAMP '…'`) and five
  strings with `E'…'`/`U&'…'` escapes; schema-qualified type names with a modifier
  (`pg_catalog.numeric(10, 2)`) are now calls off the list.

## Commands

```
export PATH=/usr/local/go/bin:$PATH
go test -count=1 ./internal/work ./internal/pgwire                 # everything in tier S
go test -count=1 -run 'Scenario|Planted' -v ./internal/pgwire      # e2e and canary
go test -count=1 -run 'Spellings' -v ./internal/pgwire             # seeded spelling generator
go test -count=1 ./agent_work/adversarial/c05-classifier/...        # the adversary's attacks
go test -run '^$' -fuzz '^FuzzClassify$' -fuzztime 60s ./internal/pgwire
go test -run '^$' -fuzz '^FuzzLex$' -fuzztime 60s ./internal/pgwire
scripts/gen-docs.sh            # regenerate docs/error-contract.md and docs/reject-list.md
scripts/gen-docs.sh --check    # what the gate runs (G8)
MG_UPDATE_GOLDEN=1 go test -run TestReplyGoldenBytes ./internal/pgwire   # rewrite golden bytes
scripts/tier-r.sh              # includes TestTierRClassifierDifferential (Docker, Postgres 17)
agent_work/chunks/c05-classifier/gate/extra.sh
scripts/gate.sh c05-classifier
```

## Gate

See the section "Gate result" at the end (written after the run).

## Index rows

Rows the orchestrator may add to `agent_work/INDEX.md`:

| Path | What |
| --- | --- |
| `scripts/gen-docs.sh` | Driver: runs every `scripts/gen-docs.d/*.sh` (`--check` passed through); gate G8 |
| `scripts/gen-docs.d/c05-contracts.sh` | Generates `docs/error-contract.md` and `docs/reject-list.md` from `internal/pgwire` |
| `docs/error-contract.md` | Generated error contract (rows, SQLSTATEs, contract facts, counter keys) |
| `docs/reject-list.md` | Generated reject list, allowed forms, function allow-lists |
| `internal/pgwire/testdata/classifier-corpus.txt` | The classifier corpus (562 records) |
| `test/analysis/c05-reject-list/` | CSV-flush SQL assertions for the reject-list scenario |
| `agent_work/chunks/c05-classifier/gate/extra.sh` | Chunk 5 gate extra: reject-list scenarios across processes, SET canary |
| `docs/knowledge/classifier.md` | Facts about the classifier, the error table and the reply builders for later chunks |

## Gate result

`scripts/gate.sh c05-classifier` at the build stage on commit `de4cecf` (clean tree),
2026-11-25: **overall pass, counts_for_landing true**. G0–G3 pass (unit 33 s); G4 markers
pass, sweep pass (200 seeds per scenario, 385 s); G5 fuzz pass (9 targets, 20 s each);
G6 tier R pass (Postgres 17.11, 78 s); G7 csv `skip-not-built` (chunk 3's runner); G8
gen-docs pass; G9 site pass; G10 extra pass (40 reject-list runs replayed across
processes, SET canary caught). `result.json` was not committed; the orchestrator reruns
the gate. Only this notes section changed after the gated commit.
