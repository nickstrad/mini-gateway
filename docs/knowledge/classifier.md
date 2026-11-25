# Statement classifier, error table and reply builders (chunk 5, 2026-11-25)

Facts a later builder needs before using `internal/work` and the chunk 5 half of
`internal/pgwire`. The full rules and limits are in
`agent_work/chunks/c05-classifier/NOTES.md`; the generated tables are
`docs/error-contract.md` and `docs/reject-list.md`.

## Calling it

- `var c pgwire.Classifier` (zero value usable; `c.Config.KeyScope` = `work.ScopeGlobal` or
  `work.ScopeTenant`). `u := c.Classify(tenant, queryText, inTx)` returns a `work.Unit`;
  `inTx` is true when the client's last `ReadyForQuery` status was `T` or `E`.
  `pgwire.ClassifyQuery(text, inTx)` is the same without counters, tenant or key.
- Pass the `Query` string without its terminating NUL. The key (`u.Key.Text`) is a copy, so
  the framer may reuse its buffer.
- `u.Class`: `ClassReject` (answer with `ErrRowUnsupportedFeature`, send nothing),
  `ClassStartLease` / `ClassContinueLease` (forward). `ClassAnswerLocally` is never
  produced in v1. `u.MayLeaveOpen()` informs admission only; the status byte ends a lease.
- Replies: `var b pgwire.ReplyBuilder; out, act, err := b.Build(dst, id, status)`. Pass the
  status the client believes (`I` outside a transaction, else the backend's current status).
  `act.End` says whether a `ReadyForQuery` was appended (`ReplyEndReady`), none
  (`ReplyEndNone`, FATAL: close after writing) or one is owed after `Sync`
  (`ReplyEndSync`); `act.Close` asks to close. Rows with no message (TLS handshake, partial
  reply) build nothing and set `Close`. `ErrRowFor(work.Refusal)` maps a refusal to its row.
- Extended protocol: one `pgwire.SyncDiscard` per client connection; feed it every client
  message type with `Step` before anything else and act on the step (refuse with
  `ErrRowUnsupportedExtended`, drop, or append `ReplyReady(dst, status)` on `Sync`).

## Things that are easy to get wrong

- A `Query` with a rejected statement anywhere is rejected whole; nothing of it may be
  forwarded.
- The classifier rejects some spellings people do not expect: `SET local.x = 1` (a session
  custom setting, checked on Postgres 17), `UPDATE pg_settings SET …`, any `pg_temp`
  reference, `set_config(…, 'true')` (only the bare word `true` is transaction-local),
  `set_config` with a setting name that is not a plain `'…'` literal, and `SET LOCAL
  standard_conforming_strings` / `client_encoding` / `NAMES`.
- **The lexer is only right while `standard_conforming_strings` is on and the client
  encoding is the pool's.** With it off (even transaction-locally) Postgres reads
  `SELECT '\' ; SELECT ' ; SET search_path = hidden ; --'` as a string followed by a real
  `SET` (verified on Postgres 17). The wire layer must refuse a database connection whose
  `ParameterStatus` for `standard_conforming_strings` is not `on` (or whose
  `client_encoding` is not the pool's) and treat either arriving during a client's turn as
  a dirty connection.
- Qualified column references (`t.a` outside `FROM`/`JOIN`) are never read-only-provable
  (attribute notation can call a function). Joins written with qualified columns are
  therefore not coalescable. `IS DISTINCT FROM t.f` and the `FROM` inside `trim`,
  `substring`, `extract` and `overlay` are expression positions, not table positions:
  Postgres 17 calls a writing `f(t)` through all of them.
- Postgres calls any keyword as a function after a schema qualifier (`public.select()`,
  `public.in(1)`); only unqualified reserved words are not calls.
- `SET LOCAL search_path = 'pg_temp'` (or `set_config('search_path', 'pg_temp', true)`)
  followed by an unqualified `CREATE TABLE` leaves a session temp table after `COMMIT`.
  `SET` quotes each string value (`'PG_TEMP, public'` is one schema name), while
  `set_config` parses its value as a list and lowercases unquoted elements.
- `EXPLAIN` option names may be quoted: `EXPLAIN ("analyze") …` executes the statement.
- Postgres's relative-time input words (`now`, `today`, `tomorrow`, `yesterday`) make a
  literal per-call, also through an untyped literal coerced to a column type.
- Postgres's scanner drops a trailing `+`/`-` from a multi-character operator unless it
  contains one of `~ ! @ # ^ & | \` ? %`: `71+-3` is `71 + -3`.
- Error-table row ids are stable names (`shed`, `unsupported-feature`, …) and appear in the
  reply's detail line `mini-gateway error row: <id>` and in the counter key `reply_<id>`.
  Adding a row: append to `ErrRowID` and `ErrorRows`, run `scripts/gen-docs.sh`, and
  `MG_UPDATE_GOLDEN=1 go test -run TestReplyGoldenBytes ./internal/pgwire`.
- `scripts/gen-docs.sh` is a driver over `scripts/gen-docs.d/*.sh`; each part must be
  executable and accept `--check`.
- Tier R: `TestTierRClassifierDifferential` rebuilds its schema in the database
  `mg_c05_classifier` before every statement (about 60 s). Postgres does not list custom
  placeholder settings in `pg_settings`, so a session-state check over `pg_settings` misses
  `SET a.b = …`; use `current_setting('a.b', true)`.
- `SET TRANSACTION …` outside a transaction block marks `transaction_isolation` /
  `transaction_read_only` with source `session` in `pg_settings` without changing the
  value: compare against `reset_val`, not the source alone.
