# 5. What the proxy says and what it will not guess: the error contract, a courtesy list, and a safe shape for sharing

Chapters 1 through 4 built a deterministic simulator, a fake Postgres 17 server, an observability spine, and sans-I/O wire session state machines. None of them looks inside a client's SQL `Query` message or defines what the proxy answers when it refuses or loses a request.

Chunk 5 adds that boundary across four places:

- [`internal/work`](../../internal/work/work.go): a small, standard-library-only vocabulary (`Unit`, `Class`, `RejectReason`, `ShareKey`, `Outcome`, `Refusal`) so the future pool, admission queues, coalescer, and retry cores never import Postgres wire types.
- [`internal/pgwire`](../../internal/pgwire): the 20-row error contract ([`errors.go`](../../internal/pgwire/errors.go)), wire reply builders and the extended-protocol discard-until-`Sync` machine ([`reply.go`](../../internal/pgwire/reply.go)), the SQL lexer and courtesy reject classifier ([`lex.go`](../../internal/pgwire/lex.go), [`classify.go`](../../internal/pgwire/classify.go), [`rejectlist.go`](../../internal/pgwire/rejectlist.go)), and an independent safe-shape recogniser ([`share.go`](../../internal/pgwire/share.go)). Three code-generated reference documents come with them: [`docs/error-contract.md`](../../docs/error-contract.md), [`docs/reject-list.md`](../../docs/reject-list.md), and [`docs/safe-shape.md`](../../docs/safe-shape.md).
- [`internal/pgwire/pgwiretest/clsworld`](../../internal/pgwire/pgwiretest/clsworld/clsworld.go): a simulated reject-list gateway and two scenarios (`classifier-reject-list` and `classifier-reject-only`) built on Chunk 4's framer.
- [`internal/obs/clssrc`](../../internal/obs/clssrc/clssrc.go) and [`internal/obs/simrun/classifier.go`](../../internal/obs/simrun/classifier.go): 15 counters under component `classifier`, flushed to CSV and checked by three DuckDB SQL assertions under `test/analysis/classifier-*/`.

This chunk has an unusual history: it was built once, failed its second review, was parked while its premise was tested on real Postgres 17, and was then reworked under a new rule. Telling that story honestly explains why the code is shaped the way it is.

---

## Why the first build was parked, and what replaced its premise

Under **transaction pooling**, the proxy lends a database connection to a client for a single transaction and returns it to the pool when Postgres reports idle status `'I'`. Two things go wrong if the proxy is careless:

1. **Session state crosses tenants.** A client runs `SET search_path = app`, `PREPARE q AS ...`, `LISTEN jobs`, `CREATE TEMP TABLE`, or `SELECT pg_advisory_lock(1)`, and the next tenant to borrow that backend connection inherits the state.
2. **Unsafe reads are shared or retried.** The proxy may let identical in-flight reads share one database round trip (**coalescing**, planned for Chunk 11). It may also tell a client after a lost connection that retrying is safe (`08006` rather than `08007`). If it does either for a statement that writes or depends on the current clock, that statement would corrupt data or return stale answers.

The first build of Chunk 5 (`work/c05-classifier`, parked under `agent_work/chunks/c05-classifier/parked/`) tried to solve both problems by reading SQL text with a hand-written lexer: reject every SQL form that changes session state, and prove from tokens whether a statement was read-only and coalescable.

Three rounds of building, reviewing, and attacking exposed **11 holes** in that approach ([`SCOPE-ASSESSMENT.md`](../../agent_work/chunks/c05-classifier/SCOPE-ASSESSMENT.md)): 3 during the build (`SET local.x`, `UPDATE pg_settings`, `SET LOCAL standard_conforming_strings`), 6 in Review 1 and Attack 1 (schema-qualified keyword calls like `public.select()`, attribute-notation calls like `t.evil_write`, `search_path` strings naming `pg_temp`, quoted `EXPLAIN ("analyze")`, `'now'::timestamp`, and Postgres's trailing-sign operator splitting), and 2 more in Review 2 (single-quoted strings continued across a newline—which let an escape string hide a `SET` from the lexer—and temporal range/array casts). The hole count per round was not falling to zero. A survey of 19 existing proxies and databases ([`CANVAS.md`](../../agent_work/chunks/c05-classifier/CANVAS.md)) found the same pattern: even projects embedding Postgres's real C parser (`libpg_query`) still had list holes, because a function or view can change session state without naming the change in query text.

Chunk 5 was parked. An options memo ([`OPTIONS.md`](../../agent_work/chunks/c05-classifier/OPTIONS.md)) evaluated mechanisms that take SQL reading out of the trust base, and a three-stage lease experiment on real Postgres 17 ([`lease-exp/REPORT.md`](../../agent_work/chunks/c05-classifier/lease-exp/REPORT.md)) measured them:

| Release policy on real Postgres 17 | Session state crossing tenants (column checks) | Secrets read back across tenants | Idle cost per action | Goodput at 80% load (5 ms leases) | Goodput at 80% load (0.5 ms leases, sim) | Wait p99 vs baseline (real / sim) |
| --- | --- | --- | --- | --- | --- | --- |
| None (baseline) | 15,657 / 34,006 | 3,531 / 4,856 | — | 1.00 | 1.00 | 1.00× |
| `DISCARD ALL` after every lease (`reset-always`) | 0 / 34,006 | 0 / 4,856 | 0.09–0.18 ms | 1.000 | 0.79 (−21%) | inconclusive (1.25×, inside A/A noise) / 1.08–1.33× |
| `DISCARD ALL` on tenant switch (`reset-on-switch`) | 0 / 34,006 (same-tenant carry-over allowed) | 0 / 4,856 | 0.09–0.18 ms | 1.000 | 0.79–0.81 | inconclusive (2.38×, inside A/A noise) / 1.07–1.32× |
| Close and reconnect on tenant switch (`remake-on-switch`) | 0 / 34,006 (zero residue) | 0 / 4,854 | 16 ms (30–33 ms under load) | 1.000 (6 tenants) | 0.01–0.52 (most cells) | 5.4–9.7× (fail) / 2.55–111× |

On that evidence, Nick issued [Ruling 9](../../PLAN.md#18-nicks-rulings-the-decision-record) ([decision D8](../../PLAN.md#3-design-decisions-this-plan-makes)):

- **Session safety never depends on reading SQL.** The proxy will send `DISCARD ALL` (Postgres's own session-reset command) after every lease, with `lease.reset: on-switch` and `lease.reseed: on` as config options on the same code point.
- **Alternatives turned down:** the conditional "known clean" reset (it puts SQL reading back into the trust base), close-on-tenant-switch (5.4–9.7× p99 wait on real Postgres), privilege-revocation deny-lists (they miss unrevoked functions and `nextval`/`lastval`), and embedding `libpg_query` (adds cgo/WASM and still cannot see catalog side effects).
- **Important boundary:** **the `DISCARD ALL` reset itself is not built in Chunk 5.** It lands in Chunk 6 (the walking skeleton) and Chunk 7 (the pool).

What Ruling 9 changed here is the classifier's design rule: **a reading mistake may cost speed or a courtesy error, never correctness.**

- The work vocabulary, the error table, and the reply builders stayed (they had passed two reviews with no finding).
- The `TxEffect` / `MayLeaveOpen` transaction-effect flag was removed: Postgres's `ReadyForQuery` status byte (`'I'`, `'T'`, `'E'`) decides when a lease ends ([decision D4](../../PLAN.md#3-design-decisions-this-plan-makes)).
- The lexer and reject list stayed only as a **best-effort courtesy** ([section 9.4 of the plan](../../PLAN.md#94-reject-list-a-best-effort-courtesy-ruling-9-derived-from-rds-proxys-pinning-triggers-i27-and-pgbouncers-and-hyperdrives-transaction-mode-tables)): clients get an immediate `ERROR 0A000` for visible session features instead of watching them vanish silently at transaction end.
- The lexer-based read-only and coalescable proofs—and their 149-function wide allow-list—were deleted and replaced by a tiny **safe-shape recogniser** ([`share.go`](../../internal/pgwire/share.go)) that shares no code with the lexer.

---

## Inside the reworked Chunk 5

```text
Query text (raw bytes), tenant, inTx
   │
   ├──► ShareFits(text)  [share.go — 196 lines, zero imports, zero allocs, no lexer]
   │        └──► SafeRead ──► Shareable = SafeRead && !inTx && !rejected
   │
   ├──► bytes.HasPrefix(text, "/* mg:share */")
   │        └──► Hinted  (never sets SafeRead or Shareable)
   │
   └──► Lexer + reject rules [lex.go, classify.go, rejectlist.go — courtesy check]
            ├──► match any reject rule ──► ClassReject + RejectReason ──► ReplyBuilder (0A000)
            └──► otherwise             ──► ClassStartLease / ClassContinueLease + Opaque + Statements
```

### 1. The protocol-free work vocabulary (`internal/work/work.go`)

[`internal/work/work.go`](../../internal/work/work.go) (182 lines before comments) imports nothing outside the standard library and names no wire protocol types. Every `Classifier.Classify` call returns a single `work.Unit`:

- `Class` (`ClassReject`, `ClassStartLease`, `ClassContinueLease`; `ClassAnswerLocally` is reserved and never produced in v1) and `Reason` (16 `RejectReason` labels from `session-set` to `extended-protocol`).
- `SafeRead` (`ShareFits(text)`), reported on every message regardless of transaction state or rejection. This single boolean is what the error contract means by "a statement that fits the safe shape" (`08006` vs `08007`).
- `Shareable` (`SafeRead && !inTx && Class != ClassReject`), which Chunk 11's `coalesce: auto` mode will share.
- `Hinted` (exact leading bytes `/* mg:share */`). Because `/` and `*` are outside the safe-shape alphabet, a hinted text is never `SafeRead` or `Shareable`. In `coalesce: hinted` mode, Chunk 11 shares on the client's explicit mark.
- `Key` and `HasKey`: present when `!inTx && Class != ClassReject && (Shareable || Hinted)`. `Key.Text` is a fresh `bytes.Clone` of the raw query bytes (so the framer can reuse its buffer), and `Key.Tenant` is filled only under `work.ScopeTenant`.
- `Opaque` (the lexer saw a `TokenBad`) and `Statements` (non-empty statements split on `;`).

### 2. The error contract, reply builders, and discard-until-`Sync` (`errors.go`, `reply.go`)

Every refusal or failure the proxy produces itself is an ordinary Postgres `ErrorResponse` (`'E'`) carrying a documented **SQLSTATE** (Postgres's five-character error code), a detail line `mini-gateway error row: <row id>`, and the exact end-of-reply marker clients expect. All 20 rows live in one table, `ErrorRows` in [`internal/pgwire/errors.go`](../../internal/pgwire/errors.go), from which [`docs/error-contract.md`](../../docs/error-contract.md) is generated:

- Five private pre-forward refusal codes sit inside standard class `53` (insufficient resources): `53M01` (`shed`), `53M02` (`rate-gate`), `53M03` (`breaker-open`), `53M04` (`coalesce-cap`), and `53M05` (`draining`). Each declares `Reached: No`, `WriteApplied: No`, and leaves the connection usable (or closes when idle for `draining`).
- Unsupported SQL features (`0A000`, `unsupported-feature`) and extended-protocol messages (`0A000`, `unsupported-extended`) are also refused before forwarding.
- Post-forward connection loss splits cleanly on `SafeRead`: `08006` (`lost-after-read`, safe to retry) when `SafeRead` was true outside a transaction, and `08007` (`lost-after-write`, transaction resolution unknown) for everything else.
- `FATAL` rows (`lost-in-transaction`, `idle-in-transaction`, `tls-required`, `startup-parameter`, `auth-failed`, `too-many-clients`, `protocol-violation`) emit no `ReadyForQuery` and close the connection. `tls-handshake` and `partial-reply` close without writing any message, and `pass-through` accounts for Postgres's own error relayed unchanged.

[`ReplyBuilder.Build`](../../internal/pgwire/reply.go) encodes messages with Chunk 4's local framing helpers (`beginMessage`, `appendCString`, `finishMessage`, `AppendReadyForQuery`) rather than `pgproto3` because `internal/pgwire` is determinism-checked. Refused calls—an out-of-range row ID, a status byte other than `'I'`, `'T'`, or `'E'`, or `ErrRowPassThrough`—leave `dst` untouched and increment only `Failed`:

```go
func (b *ReplyBuilder) Build(dst []byte, id ErrRowID, status byte) ([]byte, ReplyAction, error) {
	out, act, err := rplBuild(dst, id, status)
	if err != nil {
		b.snap.Failed++
		return dst, ReplyAction{}, err
	}
	b.snap.ByRow[id]++
	b.snap.Total++
	return out, act, nil
}
```

For clients that speak Postgres's **extended query protocol** (`Parse`, `Bind`, `Describe`, `Execute`, `Close`, `Flush`, `Sync`, plus legacy `FunctionCall`), [`SyncDiscard`](../../internal/pgwire/reply.go) follows Postgres's per-connection error-recovery contract. It refuses the first extended message with `ErrRowUnsupportedExtended` (with no `ReadyForQuery` yet), then silently drops every following message except `Terminate` (`'X'`) until `Sync` (`'S'`) arrives. Then it sends one `ReadyForQuery` carrying the client's current transaction status. A standalone `Sync` or `FunctionCall` gets `ErrorResponse` and `ReadyForQuery` immediately.

### 3. The safe-shape recogniser (`internal/pgwire/share.go`)

[`ShareFits(text []byte) bool`](../../internal/pgwire/share.go) (196 lines before comments, zero imports, zero heap allocations) decides `SafeRead` directly on raw bytes without calling the lexer or classifier:

```go
func ShareFits(text []byte) bool { return shareFits(text, false) }

func shareFits(text []byte, skipAlphabet bool) bool {
	if len(text) == 0 || len(text) > ShareMaxLen {
		return false
	}
	if !skipAlphabet && !shareAlphabetOnly(text) {
		return false
	}
	return shareStringsOK(text) && shareLeadsWithSelect(text) && shareWordsOK(text)
}
```

A query fits if and only if all eight rules in [`docs/safe-shape.md`](../../docs/safe-shape.md) hold:

1. Length is 1 to `ShareMaxLen` (**4,096** bytes).
2. Every byte is an ASCII letter, ASCII digit, space (`0x20`), or one of `_ , * = < > ( ) '`. No dot (blocking `t.f` attribute calls and schema qualifiers), semicolon, backslash, dollar sign, double quote, minus, slash, colon, plus, percent, tab, newline, or non-ASCII byte.
3. Toggling in/out of a string on every `'` ends outside a string (`''` toggles twice).
4. The byte before every opening `'` is not a letter, digit, or `_` (blocking `E'...'`, `B'...'`, `X'...'`, `N'...'`).
5. No string's content, lower-cased, contains `now`, `today`, `tomorrow`, or `yesterday` as a substring (blocking Postgres's relative-time input words such as `date 'today'`).
6. Only spaces precede the first word, and that first word is `select` (ASCII case-insensitive), so `(SELECT 1)` and `'a' SELECT` do not fit.
7. Outside strings, no word is on `ShareStopWords`. That list holds `into`, `for`, `with`, `tablesample`, 46 non-`SELECT` statement leads, and the 12 paren-free SQL-value functions ported from PostgreSQL's `gram.y` `func_expr_common_subexpr` production (such as `current_timestamp`, `current_user`, and `user`).
8. Outside strings, every word followed (after optional spaces) by `(` must appear on one of two lists. The first is `ShareParenKeywords`: 25 words, all Postgres 17 reserved `R` or column-name `C` keywords that cannot be called as unqualified functions (`by`, `join`, `like`, and `is` were intentionally dropped because Postgres 17 marks them `U`/`T`). The second is `ShareAllowList`: 66 sorted pure aggregates, string, math, and `coalesce`/`nullif`/`greatest`/`least` functions, trimmed from the first build's 135-entry coalescable list by removing window, JSON, array, date/time, formatting, and set-returning functions.

The benchmark's hot-key read `SELECT v FROM kv WHERE k = 'a'` fits cleanly; anything the recogniser does not list simply does not fit.

### 4. The lexer and courtesy reject list (`lex.go`, `classify.go`, `rejectlist.go`)

The lexer ([`lex.go`](../../internal/pgwire/lex.go), 469 lines before comments) and reject classifier ([`classify.go`](../../internal/pgwire/classify.go), 595 lines before comments) keep the first build's 15 SQL reject reasons (plus `extended-protocol` at the wire layer), generated into [`docs/reject-list.md`](../../docs/reject-list.md). Transaction-scoped forms (`SET LOCAL`, `SET TRANSACTION`, `SET CONSTRAINTS`, `set_config('name', value, true)`, `pg_advisory_xact_lock`, non-holdable cursors, `NOTIFY`, `LOCK TABLE`) are forwarded.

Three notable invariants live in the lexer and classifier:

- **String continuation across newlines (`lexContinuation`).** Fixing Review 2's blocker from the parked build, the lexer joins adjacent single-quoted literals separated by whitespace and `--` comments containing at least one `\n` or `\r`. It preserves `E'...'` escape mode across the join and never joins across block comments, spaces alone, or dollar strings—verified over the wire against Postgres 17.11. A continued string used as a `search_path` or `set_config` setting name is treated as unreadable (`clsContinued`).
- **Linear time on hostile inputs (`lexOpRun`, `spanWords`).** `ClassifyQuery` runs on every `Query` before any length cap. For that reason, the lexer scans each maximal operator run once into `lexOpRun` and cuts trailing `+`/`-` operators in O(1), and the classifier scans each statement span once in `spanWords` for `update`, `set`, and `reset` before checking individual tokens.
- **Strict temporary-object context (`makesTemp`).** `temp` and `temporary` refuse as `temp-object` only after `CREATE`, `CREATE OR REPLACE`, or `INTO` (excluding `INSERT INTO` and `MERGE INTO`), with an optional `GLOBAL`/`LOCAL` between. Ordinary reads such as `SELECT a FROM local temp` and `SELECT true OR replace temp FROM t` are forwarded.

---

## Observability and flushed CSV assertions

[`internal/obs/clssrc`](../../internal/obs/clssrc/clssrc.go) maps `ClassifierSnapshot`, `ReplySnapshot`, and `SyncDiscardSnapshot` into **15 counters** under component `classifier`: `statements` (headline), `statements_by_class{outcome}`, `rejects{reason}`, `opaque`, `safe_read`, `shareable`, `hinted`, `share_keys`, `sql_statements`, `replies{reason}`, `replies_built`, `reply_failures`, `extended_refused`, `extended_discarded`, and `extended_synced`. They are registered in [`internal/obs/catalog`](../../internal/obs/catalog/catalog.go) and regenerated into [`docs/csv-schema.md`](../../docs/csv-schema.md). Every enum slot is emitted even when zero, and `replies_built` avoids colliding with the Prometheus name of `replies`.

[`simrun.Classifier`](../../internal/obs/simrun/classifier.go) drives the two `clsworld` scenarios (`classifier-reject-list` and `classifier-reject-only`) through all four adapters, flushing at `t = 0`, every 100 ms virtual, and at run end. Three DuckDB SQL files (`test/analysis/classifier-reject-only/rejects-equal-sent.sql`, `fakepg-statements-zero.sql`, and `test/analysis/classifier-reject-list/forwarded-equal-started.sql`) check the flushed CSV files: every sent query was classified, every reject-only query was refused locally, and `fakepg.statements_started` stays 0 on reject-only runs. Flushing all 26 baseline, wire, and classifier scenarios on seed 1 validates all 65 SQL assertion files in DuckDB `1.5.5`:

```sh
$ RUN_DIR=/tmp/c05-all-runs go test -count=1 -run '^(TestScenarioFlush|TestScenarioWireFlush|TestScenarioClassifierFlush)$' ./internal/obs/simrun -seed=1
ok  	github.com/nickstrad/mini-gateway/internal/obs/simrun	14.248s

$ scripts/csv-assert.sh /tmp/c05-all-runs | head -n 5
NOTE: v1.5.5 (Variegata) d8cdaa33fd; 26 run directories; 25 scenarios with SQL files
ok: test/analysis/classifier-reject-list/forwarded-equal-started.sql over 1 runs
ok: test/analysis/classifier-reject-only/fakepg-statements-zero.sql over 1 runs
ok: test/analysis/classifier-reject-only/rejects-equal-sent.sql over 1 runs
ok: test/analysis/direct-1000-identical-reads/database-reads.sql over 1 runs

$ scripts/csv-assert.sh /tmp/c05-all-runs | tail -n 1
csv-assert: 65 SQL files run, 0 failures; v1.5.5 (Variegata) d8cdaa33fd
```

---

## Testing, test retention, and real-server oracles

Nick's rework rule forbade quietly dropping tests from the first build: every surviving behaviour stays tested, every still-valid adversary test moves into the shipped suite, and only tests of removed features are retired. The ledger in [`NOTES.md`](../../agent_work/chunks/c05-classifier/NOTES.md) accounts for all 79 prior tests: **46 kept**, **30 hoisted** (29 first-build adversary attacks plus the stopped arbitration probe), and **3 retired** (the three transaction-effect flag oracles, plus the superseded standalone tier R script). The statement corpus grew from 562 to **599 records** with all 190 original reject entries preserved and every previously non-coalescable entry asserted still non-shareable.

Running the core classifier, safe-shape, canary, memory, and linearity tests produces:

```sh
$ go test -count=1 -v -run '^(TestSafeShapeTable|TestSafeShapePropertyFitsAgreesWithTheReferenceChecker|TestSafeShapeCanaryWithoutTheAlphabetCheckThePropertyFails|TestSafeShapeRecogniserReferencesNothingFromTheLexerOrClassifier|TestSafeShapeAllocatesNothing|TestClassifierCorpus|TestClassifierTempWordsRejectOnlyWhereATemporaryObjectIsMade|TestClassifierPlantedSetForwardableIsCaught|TestClassifierMemoryIsBounded|TestLexOperatorRunsGrowLinearly|TestClassifierSpanKeywordChecksGrowLinearly|TestClassifierWorkGrowsLinearly)$' ./internal/pgwire
=== RUN   TestClassifierTempWordsRejectOnlyWhereATemporaryObjectIsMade
--- PASS: TestClassifierTempWordsRejectOnlyWhereATemporaryObjectIsMade (0.00s)
=== RUN   TestClassifierCorpus
    classify_test.go:151: corpus: 599 records, 60 fit the safe shape; 40 of the first build's 116 coalescable records still fit
--- PASS: TestClassifierCorpus (0.00s)
=== RUN   TestClassifierMemoryIsBounded
    classify_test.go:494: worst measured allocation per input byte at 1 MiB: 52 (bound 80 per byte + 65536)
--- PASS: TestClassifierMemoryIsBounded (1.91s)
=== RUN   TestLexOperatorRunsGrowLinearly
    classify_test.go:547: "+": 4 KiB {opRunBytes:4096 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:16401 tokenVisits:53275 literalBytes:6}}, 64 KiB {opRunBytes:65536 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:262161 tokenVisits:851995 literalBytes:6}}
    classify_test.go:547: "-": 4 KiB {opRunBytes:0 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:17 tokenVisits:26 literalBytes:6}}, 64 KiB {opRunBytes:0 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:17 tokenVisits:26 literalBytes:6}}
    classify_test.go:547: "-+": 4 KiB {opRunBytes:4096 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:16401 tokenVisits:53275 literalBytes:6}}, 64 KiB {opRunBytes:65536 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:262161 tokenVisits:851995 literalBytes:6}}
    classify_test.go:547: "+-": 4 KiB {opRunBytes:4096 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:16401 tokenVisits:53275 literalBytes:6}}, 64 KiB {opRunBytes:65536 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:262161 tokenVisits:851995 literalBytes:6}}
    classify_test.go:547: "*+": 4 KiB {opRunBytes:4096 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:25 tokenVisits:53 literalBytes:6}}, 64 KiB {opRunBytes:65536 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:25 tokenVisits:53 literalBytes:6}}
    classify_test.go:547: "=-": 4 KiB {opRunBytes:4096 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:25 tokenVisits:53 literalBytes:6}}, 64 KiB {opRunBytes:65536 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:25 tokenVisits:53 literalBytes:6}}
    classify_test.go:547: "<+-": 4 KiB {opRunBytes:4095 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:29 tokenVisits:66 literalBytes:6}}, 64 KiB {opRunBytes:65535 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:29 tokenVisits:66 literalBytes:6}}
    classify_test.go:547: "+ ": 4 KiB {opRunBytes:2048 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:8209 tokenVisits:26651 literalBytes:6}}, 64 KiB {opRunBytes:32768 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:131089 tokenVisits:426011 literalBytes:6}}
    classify_test.go:547: "1+": 4 KiB {opRunBytes:2048 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:16401 tokenVisits:53275 literalBytes:6}}, 64 KiB {opRunBytes:32768 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:262161 tokenVisits:851995 literalBytes:6}}
--- PASS: TestLexOperatorRunsGrowLinearly (0.07s)
=== RUN   TestClassifierSpanKeywordChecksGrowLinearly
    classify_test.go:554: "pg_settings ": 4 KiB {opRunBytes:0 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:2063 tokenVisits:5482 literalBytes:3757}}, 64 KiB {opRunBytes:0 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:32783 tokenVisits:87402 literalBytes:60077}}
    classify_test.go:554: "standard_conforming_strings ": 4 KiB {opRunBytes:0 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:893 tokenVisits:2362 literalBytes:3948}}, 64 KiB {opRunBytes:0 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:14057 tokenVisits:37466 literalBytes:63186}}
    classify_test.go:554: "client_encoding ": 4 KiB {opRunBytes:0 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:1553 tokenVisits:4122 literalBytes:3846}}, 64 KiB {opRunBytes:0 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:24593 tokenVisits:65562 literalBytes:61446}}
    classify_test.go:554: "\"pg_settings\" ": 4 KiB {opRunBytes:0 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:1185 tokenVisits:4114 literalBytes:3802}}, 64 KiB {opRunBytes:0 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:18741 tokenVisits:65560 literalBytes:60859}}
    classify_test.go:554: "pg_settings, ": 4 KiB {opRunBytes:0 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:3167 tokenVisits:8847 literalBytes:3471}}, 64 KiB {opRunBytes:0 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:50427 tokenVisits:141175 literalBytes:55457}}
    classify_test.go:554: "SELECT pg_settings ": 4 KiB {opRunBytes:0 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:2597 tokenVisits:6906 literalBytes:3661}}, 64 KiB {opRunBytes:0 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:41405 tokenVisits:110394 literalBytes:58639}}
--- PASS: TestClassifierSpanKeywordChecksGrowLinearly (0.01s)
=== RUN   TestClassifierWorkGrowsLinearly
    classify_test.go:601: "(": 4 KiB {opRunBytes:0 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:16395 tokenVisits:40971 literalBytes:0}}, 64 KiB {opRunBytes:0 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:262155 tokenVisits:655371 literalBytes:0}}
    classify_test.go:601: "a ": 4 KiB {opRunBytes:0 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:12299 tokenVisits:32778 literalBytes:2048}}, 64 KiB {opRunBytes:0 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:196619 tokenVisits:524298 literalBytes:32768}}
    classify_test.go:601: ";": 4 KiB {opRunBytes:0 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:0 tokenVisits:28672 literalBytes:0}}, 64 KiB {opRunBytes:0 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:0 tokenVisits:458752 literalBytes:0}}
    classify_test.go:601: "'x'": 4 KiB {opRunBytes:0 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:15 tokenVisits:24 literalBytes:0}}, 64 KiB {opRunBytes:0 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:15 tokenVisits:24 literalBytes:0}}
    classify_test.go:601: "set_config(1,2,": 4 KiB {opRunBytes:0 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:4928 tokenVisits:15032 literalBytes:10}}, 64 KiB {opRunBytes:0 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:78656 tokenVisits:240312 literalBytes:10}}
    classify_test.go:601: "U&\"a\" ": 4 KiB {opRunBytes:0 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:3419 tokenVisits:10238 literalBytes:3410}}, 64 KiB {opRunBytes:0 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:54619 tokenVisits:163838 literalBytes:54610}}
    classify_test.go:601: "SELECT abs(t.a) FROM t; ": 4 KiB {opRunBytes:0 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:10030 tokenVisits:24990 literalBytes:2720}}, 64 KiB {opRunBytes:0 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:161070 tokenVisits:401310 literalBytes:43680}}
    classify_test.go:601: "'a'\n": 4 KiB {opRunBytes:0 lookahead:2047 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:15 tokenVisits:24 literalBytes:0}}, 64 KiB {opRunBytes:0 lookahead:32767 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:15 tokenVisits:24 literalBytes:0}}
    classify_test.go:601: "-- c\n": 4 KiB {opRunBytes:0 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:0 tokenVisits:0 literalBytes:0}}, 64 KiB {opRunBytes:0 lookahead:0 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:0 tokenVisits:0 literalBytes:0}}
    classify_test.go:601: "$a$x$a$ ": 4 KiB {opRunBytes:0 lookahead:1024 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:2059 tokenVisits:6667 literalBytes:0}}, 64 KiB {opRunBytes:0 lookahead:16384 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:32779 tokenVisits:106507 literalBytes:0}}
    classify_test.go:601: "$a ": 4 KiB {opRunBytes:0 lookahead:2730 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:8201 tokenVisits:21850 literalBytes:1365}}, 64 KiB {opRunBytes:0 lookahead:43690 clsCounts:{lex:{reads:{} steps:{} opRuns:{} ahead:{}} keywordChecks:131081 tokenVisits:349530 literalBytes:21845}}
--- PASS: TestClassifierWorkGrowsLinearly (0.05s)
=== RUN   TestSafeShapeTable
--- PASS: TestSafeShapeTable (0.00s)
=== RUN   TestSafeShapeCanaryWithoutTheAlphabetCheckThePropertyFails
    share_test.go:216: with the alphabet check skipped, 113896 texts with a byte outside the alphabet fit
--- PASS: TestSafeShapeCanaryWithoutTheAlphabetCheckThePropertyFails (0.23s)
=== RUN   TestSafeShapePropertyFitsAgreesWithTheReferenceChecker
    share_test.go:434: 15817 of 200000 random texts fit; ShareFits and the reference agreed on all
--- PASS: TestSafeShapePropertyFitsAgreesWithTheReferenceChecker (0.36s)
=== RUN   TestSafeShapeRecogniserReferencesNothingFromTheLexerOrClassifier
--- PASS: TestSafeShapeRecogniserReferencesNothingFromTheLexerOrClassifier (0.02s)
=== RUN   TestSafeShapeAllocatesNothing
--- PASS: TestSafeShapeAllocatesNothing (0.00s)
=== RUN   TestClassifierPlantedSetForwardableIsCaught
--- PASS: TestClassifierPlantedSetForwardableIsCaught (0.04s)
PASS
ok  	github.com/nickstrad/mini-gateway/internal/pgwire	2.716s
```

Key properties proved across the suite:

- **Structural independence and exactness:** `TestSafeShapeRecogniserReferencesNothingFromTheLexerOrClassifier` parses `share.go` with `go/ast` and fails if it imports any package or references any identifier defined outside `share.go`. `TestSafeShapePropertyFitsAgreesWithTheReferenceChecker` checks 200,000 seeded texts (15,817 fit) against an independent reference checker and verifies that every fitting text lexes cleanly as exactly one statement. `TestSafeShapeAllocatesNothing` proves 0 heap allocations.
- **Canaries with teeth:** Bypassing the alphabet check lets 113,896 non-alphabet texts fit. Planting `SET` as forwardable fails `classifier-reject-list` with both `"a rejected statement reached the database"` and `"session state changed"`. Mutating any of 8 CSV metrics fails `scripts/csv-assert.sh` with `"violating rows"`, and mutating any contract table breaks `scripts/gen-docs.sh --check`.
- **Fuzzing and bounded work:** Three new fuzz targets (`FuzzLex`, `FuzzClassify`, `FuzzSafeShape`, bringing the repository total to 14) run with no panics and invariant checks. Worst measured allocation at 1 MiB is **52 bytes/input byte** (bound: 80 B/byte + 64 KiB). Across all 26 linear-work shapes, the growth tests count operations rather than elapsed time—measuring lexer reads, classifier token steps, operator-run bytes, lookahead bytes, keyword checks, token visits, and literal bytes—and require 16× input growth (4 KiB to 64 KiB) to take at most **17×** the work, so verdicts do not depend on machine load.
- **Live Postgres 17 differential (Tier R):** `TestTierRClassifierDifferential` verified that 407 forwarded corpus statements left session state unchanged. `TestTierRSafeShapeDifferential` ran 81 corpus/table texts, 400 generated texts, and 6 queries on a 200,000-row table `big` (5 of 6 confirmed to use a `Parallel Seq Scan` plan) against Postgres 17.11. Every run produced identical outcomes inside `BEGIN READ ONLY` (never `25006`), unchanged table checksums, clean sessions under the shipped [`test/oracles/dirty-check.sql`](../../test/oracles/dirty-check.sql) oracle, and matching row multisets on re-run (37 ending in the same Postgres error each time).

The full per-step count compiles in only under the build tag `clscount` (via [`internal/pgwire/clscount`](../../internal/pgwire/clscount/on.go)), so the default build drops every counting block at compile time. The gate runs the tagged suite through [`scripts/counted-growth.sh`](../../scripts/counted-growth.sh): `TestCountedWorkGrowsLinearly` measures `ClassifyQuery`'s own lexer passes across all 26 shapes, requiring lexer reads to grow 15×–17× and all seven counters to stay within 17×, while AST guards (`TestLexLoopsCountTheirSteps`, `TestClassifierLexLoopsCountTheirSteps`) require every lexer and `(*clsMsg).lex` loop to count its step. In the default build, string bodies (`'x'`) and line comments (`-- c\n`, `-`) are not counted; the whole-call linear-work claim holds in the tagged run:

```sh
$ scripts/counted-growth.sh | grep -E '^(NOTE:|=== RUN|--- PASS:|PASS|ok)'
NOTE: -tags clscount -run Linearly: 5 tests passed
=== RUN   TestCountedWorkGrowsLinearly
--- PASS: TestCountedWorkGrowsLinearly (0.10s)
=== RUN   TestCountedCheckRejectsWorkThatDoesNotGrowLinearly
--- PASS: TestCountedCheckRejectsWorkThatDoesNotGrowLinearly (0.00s)
=== RUN   TestLexOperatorRunsGrowLinearly
--- PASS: TestLexOperatorRunsGrowLinearly (0.07s)
=== RUN   TestClassifierSpanKeywordChecksGrowLinearly
--- PASS: TestClassifierSpanKeywordChecksGrowLinearly (0.01s)
=== RUN   TestClassifierWorkGrowsLinearly
--- PASS: TestClassifierWorkGrowsLinearly (0.05s)
PASS
ok  	github.com/nickstrad/mini-gateway/internal/pgwire	0.240s
```

---

## Narrowed claims and stated limits

Three claims from the builder brief were broader than what any reader of query text—or Postgres 17 itself—can promise, and were formally narrowed:

1. **Safe shape on real Postgres 17 excludes tables with row-level security policies (Claim 2).** On Postgres 17, for a schema of plain tables *without row-level security policies* (no `CREATE POLICY` active on a read table, or bypassed by the reading role), no text that fits the safe shape writes, changes session state, or returns a different multiset of rows on re-run. A policy expression executes on every read and may call `set_config` or alter visible rows without leaving any trace in the SQL text. Databases using row-level security policies should run `coalesce: off` or `hinted`.
2. **Re-runs repeat as a multiset of rows, for SQL-defined values only (Claim 2).** Once a plain table exceeds `min_parallel_table_scan_size` (8 MB by default), Postgres 17 chooses a parallel `Gather` plan whose workers emit blocks in completion order. Two valid runs of the same safe-shape query can therefore differ in row order when `ORDER BY` is absent, in the output of order-sensitive aggregates (`string_agg`, `array_agg` without an inner `ORDER BY`), and in IEEE-754 floating-point aggregates (`sum`, `avg` over `real`/`double precision`, because floating-point addition is non-associative). Sharing hands every identical in-flight read one valid evaluation of the query.
3. **A recognised reject-list feature with an unreadable argument is refused (Claim 6).** A message is never refused merely because it is untokenisable or uses unmodelled SQL. Three deliberate exceptions apply where a reject-list feature *is* recognised: (a) a reject-list feature in the readable prefix of an untokenisable message (`DISCARD ALL; SELECT 'unfinished`), (b) `set_config(name, value, is_local)` whose setting name is not one plain `'...'` string literal, and (c) a transaction-local `search_path`/`SCHEMA` value the classifier cannot read (backslash `E'...'`, escaped `U&'...'`, continued string, or non-literal expression in `set_config`).

### Further stated limits

- **Safe shape trusts named objects.** Beyond row-level security policies, the recogniser cannot see the database catalog. Side effects or per-call results can still hide behind a view (including volatile system views like `pg_stat_activity`), a user-defined operator behind `=`, `<`, `>`, or `*`, a user function shadowing an allow-listed name in `search_path`, a foreign table, or a table with rules. Values that depend on session settings (`TimeZone`, `DateStyle`, `lc_*`) rely on every connection being reset to the pool's defaults after each lease (Chunk 6). Qualified names (`kv.v`), casts (`::`), comments, multi-line queries, `$1` parameters, window functions, and `JOIN (...)` do not fit and require `coalesce: hinted`.
- **Courtesy list is best effort.** Functions, procedures, `DO` blocks, views, rules, triggers, defaults, and unmodelled spellings are forwarded and rely on Chunk 6's `DISCARD ALL` reset. One known courtesy gap is a `UESCAPE` literal continued across a newline in a Unicode-escaped advisory-lock call (`SELECT U&"pg_advisory_!006cock" UESCAPE '!'\n'' (1)`). The lexer assumes `standard_conforming_strings = on`, folds ASCII case, and truncates identifiers at 63 bytes.
- **No reset or proxy binary yet.** Both session reset (`DISCARD ALL`) and the runnable proxy binary land in Chunk 6. That reset leaves four known Postgres residue items: the random seed unless `lease.reseed` is on, the existence of custom placeholder names, loaded extension/PL memory state, and privileged replication origins.
