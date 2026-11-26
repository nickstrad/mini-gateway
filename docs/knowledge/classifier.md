# Statement classifier, safe shape, error table and reply builders (chunk 5, reworked 2026-11-26)

Facts a later builder needs before using `internal/work` and the chunk 5 half of
`internal/pgwire`. Ruling 9 (2026-11-26): the proxy resets every database connection with
`DISCARD ALL` after every lease (chunks 6 and 7), so **no safety rests on reading SQL**. The
classifier now does two small things: a best-effort courtesy (the reject list) and the safe
shape (what may be shared, and what counts as a read for the error contract). Full rules,
limits and the test ledger: `agent_work/chunks/c05-classifier/NOTES.md`. Generated tables:
`docs/error-contract.md`, `docs/reject-list.md`, `docs/safe-shape.md`.

## Calling it

- `var c pgwire.Classifier` (zero value usable; `c.Config.KeyScope` = `work.ScopeGlobal` or
  `work.ScopeTenant`). `u := c.Classify(tenant, queryText, inTx)` returns a `work.Unit` and
  counts it; `inTx` is true when the client's last `ReadyForQuery` status was `T` or `E`.
  `pgwire.ClassifyQuery(text, inTx)` is the same without counters, tenant or key.
- Pass the `Query` string without its terminating NUL. The key (`u.Key.Text`) is a copy, so
  the framer may reuse its buffer.
- `pgwire.ShareFits(text)` is the safe-shape recogniser alone (no allocation, no lexer).

## What each field of `work.Unit` means

| Field | Meaning |
| --- | --- |
| `Class` | `ClassReject` (answer `ErrRowUnsupportedFeature`, forward nothing of the message), `ClassStartLease` (`inTx` false) or `ClassContinueLease` (`inTx` true). `ClassAnswerLocally` is never produced in v1. |
| `Reason` | The reject reason when `Class` is `ClassReject`, else `RejectNone`. |
| `SafeRead` | The text fits the safe shape (`docs/safe-shape.md`). The error contract's "statement that fits the safe shape" (`08006` against `08007`) is exactly this flag. Reported also when the message is rejected or in a transaction. |
| `Shareable` | `SafeRead` and not `inTx` and not rejected. What `coalesce: auto` shares (D10). |
| `Hinted` | The text begins with the exact bytes `/* mg:share */`. Never sets `SafeRead` or `Shareable` (a hinted text contains `/` and `*`, so it never fits). In `hinted` mode chunk 11 shares on the mark alone. |
| `Key`, `HasKey` | Present when not `inTx`, not rejected, and `Shareable` or `Hinted`: the exact text (a copy), `Key.Tenant` set only in `ScopeTenant`. |
| `Opaque` | The lexer could not tokenise the message. It is forwarded (unless a reject-list feature was recognised in the readable part) and never `SafeRead`. |
| `Statements` | Non-empty statements the lexer split on `;`. Counter input only. |

Removed in the rework: `TxEffect`, `MayLeaveOpen`, the `Tx` field (the status byte ends a
lease, D4; nothing used the flag); `ReadOnly` and `Coalescable` with the lexer-based proofs
and the wide function allow-list behind them; `AllowList`/`AllowLookup`;
`ClassifierMetrics`/`ReplyMetrics` (replaced by `internal/obs/clssrc`).

## Replies and the extended protocol

- `var b pgwire.ReplyBuilder; out, act, err := b.Build(dst, id, status)`. Pass the status the
  client believes. `act.End`: `ReplyEndReady` (a `ReadyForQuery` was appended),
  `ReplyEndNone` (FATAL: close after writing), `ReplyEndSync` (one is owed after `Sync`);
  `act.Close` asks to close. `ErrRowFor(work.Refusal)` maps a refusal to its row.
- One `pgwire.SyncDiscard` per client connection; feed it every client message type with
  `Step` before anything else (refuse with `ErrRowUnsupportedExtended`, drop, or append
  `ReplyReady(dst, status)` on `Sync`).
- Replies are encoded with chunk 4's encoder (`AppendReadyForQuery`, `beginMessage`), not
  `pgproto3`: `internal/pgwire` is in the determinism list.

## Observability

- `Classifier.Snapshot()`: `Classified`, `ByClass`, `ByReason`, `Opaque`, `SafeRead`,
  `Shareable`, `Hinted`, `Keys`, `Statements`; `Check()` holds classified = sum over
  classes, rejects = sum over reasons, shareable ≤ safe-read, keys ≤ shareable + hinted.
- Export: `clssrc.Source(func() clssrc.Snapshot{Classifier, Replies, Sync})`, component
  `classifier`: `statements`, `statements_by_class{outcome}`, `rejects{reason}`, `opaque`,
  `safe_read`, `shareable`, `hinted`, `share_keys`, `sql_statements`, `replies{reason=row
  id}`, `replies_built`, `reply_failures`, `extended_refused`, `extended_discarded`,
  `extended_synced`. Chunk 6 registers it beside `wiresrc`.
- Scenario world: `internal/pgwire/pgwiretest/clsworld` (`Build`, `Run`, `Result.Verify`);
  flushed by `simrun.Classifier`; SQL in `test/analysis/classifier-reject-only/` and
  `classifier-reject-list/`.

## Things that are easy to get wrong

- A `Query` with a rejected statement anywhere is rejected whole.
- **The reject list is a courtesy, not a guarantee.** A spelling it misses, or a function or
  view that hides a session feature, is forwarded and cleaned by the reset. Do not add
  safety logic on top of `Class`.
- **Never derive "shareable" or "a read" from anything but `SafeRead`/`Shareable`.** Do not
  reuse the lexer for it: the recogniser deliberately shares no code with the lexer.
- The hint is exact: `/*mg:share*/`, a leading space, or a different case is not hinted.
- The safe shape trusts the objects it names: a view (system views such as
  `pg_stat_activity` included), a user-defined operator behind `=`/`<`/`>`, a user function
  that shadows an allow-listed name by argument type, a foreign table or a table with rules
  can hide a side effect or a per-call result. `coalesce: off` and `hinted` exist for that.
- So can a **row-level security policy** on an ordinary table: the policy expression runs
  for every read and may call `set_config`. No reader of query text can see it. Databases
  with policies need `coalesce: off` or `hinted`.
- What repeats for a safe-shape text is its rows **as a multiset**. On a table past
  `min_parallel_table_scan_size` (8 MB by default) Postgres 17 plans `Gather` over a
  parallel scan: row order without `ORDER BY`, `string_agg`/`array_agg` without `ORDER BY`
  and `sum`/`avg` over `double precision` can differ between two valid runs. Oracles
  compare re-runs as sorted rows (`shrRowMultiset`), on exact types only.
- **Linear work, always.** The classifier runs on every `Query` before any length cap:
  never search a whole statement, or rescan the rest of a token run, once per token. The
  lexer scans an operator run once (`lexOpRun`) and cuts every operator from that scan;
  the classifier reads a statement's `update`/`set`/`reset` words once (`spanWords`).
  `TestLexOperatorRunsGrowLinearly` and `TestClassifierSpanKeywordChecksGrowLinearly` hold
  this.
- `ReplyBuilder` counts `ByRow`/`Total` only for replies it built; a refused build
  (pass-through row, bad status, bad row) counts in `Failed` alone.
- Postgres joins string literals continued across a newline (whitespace and `--` comments
  with at least one newline between the closing and the next opening `'`; block comments
  do not count), and an `E'…'` string keeps its escape mode across the join. The lexer now
  does the same (`lexContinuation`); a `search_path` or `set_config` name given as a
  continued string is treated as unreadable.
- `temp`/`temporary` refuse only where a temporary object is made: after `CREATE`,
  `CREATE OR REPLACE` or `INTO` (not `INSERT`/`MERGE INTO`), with an optional
  `GLOBAL`/`LOCAL` between. The `OR REPLACE` branch requires the preceding `CREATE`.
  `SELECT a FROM local temp` and `SELECT true OR replace temp FROM t` are reads;
  `temporary` is also a valid output alias in the latter.
- The classifier still rejects some spellings people do not expect: `SET local.x = 1`,
  `UPDATE pg_settings SET …`, any `pg_temp` reference, `set_config(…, 'true')`,
  `set_config` with a setting name that is not one plain `'…'` literal, `SET LOCAL
  standard_conforming_strings` / `client_encoding` / `NAMES`, and an unreadable
  transaction-local `search_path` value.
- Postgres's scanner drops a trailing `+`/`-` from a multi-character operator unless it
  contains one of `` ~ ! @ # ^ & | ` ? % ``: `71+-3` is `71 + -3`.
- Error-table row ids are stable names and appear in the reply's detail line
  `mini-gateway error row: <id>` and as the `reason` label of `classifier.replies`. Adding a
  row: append to `ErrRowID` and `ErrorRows`, run `scripts/gen-docs.sh`, and
  `MG_UPDATE_GOLDEN=1 go test -run TestReplyGoldenBytes ./internal/pgwire`.
- `test/oracles/dirty-check.sql` is the shipped session-cleanliness oracle (prepared
  statements, cursors, listens, own advisory locks, session-sourced settings, temp
  relations, role change); compare its row before and after, do not expect zeros (startup
  options are `client`-sourced, not `session`).
- Tier R: `TestTierRClassifierDifferential` (database `mg_c05_classifier`, about 40 s) and
  `TestTierRSafeShapeDifferential` (database `mg_c05_safe_shape`, plain tables only, about
  10 s). Postgres does not list custom placeholder settings in `pg_settings`; use
  `current_setting('a.b', true)`.
