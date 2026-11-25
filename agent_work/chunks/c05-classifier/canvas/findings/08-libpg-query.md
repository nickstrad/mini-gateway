# libpg_query / pg_query_go / wasilibs go-pgquery — the real PostgreSQL parser as a library

Canvas 08 for mini-gateway chunk 5. Research only; nothing under `/root/Software/mini-gateway*`
was touched. Measurements were taken on this VM (4 cores, 7 GB, shared with three other agents,
load average 1.9–3.5 during the runs), so absolute times are noisy; ratios are more reliable.
Scratch programs, raw benchmark output and the live-server probe script are in
`/root/scratch/c05-canvas/work/08-libpg-query/` (`bench_cgo.txt`, `bench_wasm.txt`,
`bench_wasm_warm.txt`, `bench_cgo_par.txt`, `probe_out.txt`, `probe2_out.txt`,
`live_pg_probe.sh`).

Evidence tags: **[CODE]** = VERIFIED IN CODE (path:line at the commit below), **[RUN]** = measured
or observed by running it here, **[DOCS]** = DOCS ONLY, **[NOT FOUND]** = COULD NOT FIND.

## Sources

| Project | URL | Commit SHA | Commit date | Newest release | Local/web |
| --- | --- | --- | --- | --- | --- |
| libpg_query | https://github.com/pganalyze/libpg_query | 86b593ba91e3a04e71127f8c8629da2c0708ba91 | 2026-11-25 | `18.1.0` (2026-09-30), PG 18.6 | LOCAL SOURCE |
| pg_query_go | https://github.com/pganalyze/pg_query_go | 7ac09d4bd830e591a69c6ebc756bb8f71de0281e | 2026-09-30 | `v6.2.5` (2026-09-30), embeds libpg_query `17-6.2.5`, PG 17.7 | LOCAL SOURCE + built/run |
| wasilibs/go-pgquery | https://github.com/wasilibs/go-pgquery | 3939604ca45d03428f8503a2f589398895745004 | 2026-11-24 | no tags/releases; pseudo-version `v0.0.0-20261124022233-3939604ca45d`; embeds PG 17.7 | LOCAL SOURCE + built/run |
| PostgreSQL (for server-side behaviour) | https://github.com/postgres/postgres | 4483697be0e0d22b9b4fd847e8bf2d998c56a741 (master, `20devel`) | 2026-11-25 | — | LOCAL SOURCE (existing clone) |
| PostgreSQL 16 server (Ubuntu package) | — | — | — | 16.x | RUN (throwaway cluster in /tmp, removed) |
| sqlc-dev/oliphant (better candidate, short) | https://github.com/sqlc-dev/oliphant | 9fbe0c6a2789e321b75116d0022b425cc5af43e4 | 2026-08-26 | tag `v0.2.0`; sqlc pins v0.3.1 | LOCAL SOURCE (clone deleted after reading) |
| pgdogdev/pgdog, pgdogdev/pg_raw_parse, sqlc-dev/sqlc, jackc/pgx | GitHub | pgdog 1d1d861f (2026-11-25), sqlc 3ad8b20f (2026-10-23) | — | — | WEB READS (`gh api`) |

## Summary rows

None of the three is a proxy: they make no session-state, read/write or sharing decisions. The
rows describe what a proxy would get from them.

| Project | Reads SQL with | Session-state mechanism | Read/write and share decisions | List type | Cost of a miss | Fails open or closed |
| --- | --- | --- | --- | --- | --- | --- |
| libpg_query | The real PostgreSQL scanner (`scan.l`) and grammar (`gram.y`), extracted from PG 18.6 with 15 patches | none (library) | none; exposes `pg_query_is_utility_stmt` (node-type test) and `pg_query_summary` (statement types, function names, tables) | n/a — the consumer writes the list, but over parse-tree node types instead of text | n/a | Returns an error on text it cannot scan/parse — **except** `pg_query_split_with_scanner`, which can return zero statements and no error (see below) |
| pg_query_go | libpg_query 17-6.2.5 (PG 17.7) via cgo | none | none; Go wrappers `IsUtilityStmt`, `Summary` | n/a | n/a | as libpg_query; cannot select `standard_conforming_strings=off` (option not exposed) |
| wasilibs/go-pgquery | the same C (PG 17.7) compiled to WASM, run under wazero | none | none; **no** Split, IsUtilityStmt or Summary exported | n/a | n/a | as libpg_query |

---

## libpg_query

### 1. Does it read SQL, with what, how kept in step

**It is the PostgreSQL raw parser, nothing more.** [CODE] `pg_query.h:165-201` is the whole API:

```c
PgQueryNormalizeResult pg_query_normalize(const char* input);
PgQueryScanResult pg_query_scan(const char* input);              // tokens as ScanResult protobuf
PgQueryScanTokensResult pg_query_scan_tokens(const char* input); // tokens as a C array
PgQueryParseResult pg_query_parse_opts(const char* input, int parser_options);         // JSON tree
PgQueryProtobufParseResult pg_query_parse_protobuf_opts(const char* input, int parser_options);
PgQueryPlpgsqlParseResult pg_query_parse_plpgsql(const char* input);
PgQueryFingerprintResult pg_query_fingerprint_opts(const char* input, int parser_options, int fingerprint_options);
PgQuerySplitResult pg_query_split_with_scanner(const char *input);
PgQuerySplitResult pg_query_split_with_parser(const char *input);
PgQueryDeparseResult pg_query_deparse_protobuf(PgQueryProtobuf parse_tree);
PgQueryIsUtilityResult pg_query_is_utility_stmt(const char *query);
PgQuerySummaryParseResult pg_query_summary(const char* input, int parser_options, int truncate_limit);
```

- **Scanner.** `src/pg_query_scan.c:40` runs the extracted `scan.l` (`scanner_init(input, &yyextra,
  &ScanKeywords, ScanKeywordTokens)`) and returns every token with start/end byte offsets and
  keyword kind. Patch `04_lexer_comments_as_tokens.patch` makes the scanner also return
  `SQL_COMMENT`/`C_COMMENT` tokens for `pg_query_scan` only; the patch header says "By default
  (e.g. when called from the parser) comments continue to be ignored like whitespace, so parsing
  behaves the same as upstream Postgres." [CODE]
- **Raw parser.** `src/pg_query_parse.c` calls `raw_parser(input, rawParseMode)` — the same
  function PostgreSQL's `pg_parse_query` calls — and serialises the `RawStmt` list to JSON or
  protobuf. [CODE]
- **Split.** `src/pg_query_split.c:7-120` (`_with_scanner`): runs the scanner twice and cuts at
  `;` when a keyword was seen since the last cut and the parenthesis depth is zero.
  `_with_parser` (`:125`) uses the `RawStmt` locations of a full parse. [CODE]
- **Utility test.** `src/pg_query_is_utility_stmt.c:12-24`: per statement, `false` for
  `T_SelectStmt/T_InsertStmt/T_UpdateStmt/T_DeleteStmt/T_MergeStmt`, `true` for every other node
  type. [CODE]
- **Summary.** `pg_query_summary` (`src/pg_query_summary*.c`) walks the raw tree and returns
  statement types (including those nested in CTEs: `src/pg_query_summary_statement_type.c:279`
  adds `DeleteStmt` wherever met), function names, tables with a Select/DML/DDL/Call context, CTE
  names and filter columns (`protobuf/pg_query.proto:4202-4240`). [CODE]

**What it does not do** [CODE, by absence]: there is no parse analysis (`parse_analyze*`,
`transformStmt`) and no catalog. The tree for `SELECT (t).f FROM t` is an `A_Indirection` over a
`ColumnRef` (`probe2_out.txt`) — the library cannot tell that `f` is a function (attribute
notation). It cannot resolve `search_path`, tell a view from a table (`SELECT * FROM v` gives
`RangeVar v`), know a function's volatility or body, or know that `'now'::timestamptz` calls a
volatile input function — the cast is just a `TypeCast` node with a string constant. The patch
`12_remove_acl_check.patch` says it outright: "This is not needed for pg_query (we never check
permissions)". [CODE]

**Size and extraction.** The PostgreSQL code is copied, not reimplemented:
`src/postgres/src_backend_parser_gram.c` is 54,555 lines and `src_backend_parser_scan.c` 11,491
(flex output). `Makefile:10-12` pins `PG_VERSION = 18.6`; `Makefile:124-140` downloads the
release tarball from ftp.postgresql.org and applies the 15 patches in `patches/`;
`scripts/extract_source.rb` then uses libclang (`require 'ffi/clang'`) to follow symbol
references from the entry points and copy only the functions needed into `src/postgres/*.c`
(`@symbols_to_output`, `analyze_file`). [CODE]

**Patches that change accepted language** (all make the library accept *more* than the server):
`01_parser_additional_param_ref_support.patch` (accepts `$n` in `SET ROLE $4`, `CREATE USER test
PASSWORD $1`, `SELECT DATE $1`…), `09_allow_param_junk.patch` (removes the `param_junk` error, so
`$1abc` scans as `PARAM IDENT`), `16_notify_payload_param.patch` (`NOTIFY c, $1`). [CODE] Direction
matters: a superset only produces "library parses, server rejects", which is harmless for a proxy
that forwards to the server. [inference]

**Release cadence vs PostgreSQL majors.** [DOCS — GitHub releases API] `16-5.0.0` 2023-12-22,
`17-6.0.0` 2024-10-31, `18.0.0` 2026-05-21, `18.1.0` 2026-09-30; `17-6.2.x` point releases continue
in parallel (latest `17-6.2.5`, 2026-09-30). PostgreSQL GA dates are from memory (16: Sept 2023,
17: Sept 2024, 18: Sept 2025), so the lag was about 3 months for 16, about 5 weeks for 17 and
about 8 months for 18. Minor PG versions are picked up by bumping `PG_VERSION` (now 18.6).

### 2–4. Session state, read/write, documented limits

n/a — a library. It documents no session-feature limitations because it makes no such decision.
The only "limitations" it states are about its own parser options (below) and the split helpers
(`pg_query.h:184-191`: "we try to support special cases like comments, strings containing ";" …").

### The residual lexical risk: GUCs and encoding (the key finding of this canvas)

**libpg_query lexes with fixed GUC values.** The scanner copies three globals at
`scanner_init` (`src/postgres/src_backend_parser_scan.c:11272-11274`), whose defaults are
`:8691-8695`:

```c
__thread int   backslash_quote = BACKSLASH_QUOTE_SAFE_ENCODING;
__thread bool  escape_string_warning = true;
__thread bool  standard_conforming_strings = true;
```

Only the parse entry points can change them, through option bits (`pg_query.h:132-134`:
`PG_QUERY_DISABLE_BACKSLASH_QUOTE 16`, `PG_QUERY_DISABLE_STANDARD_CONFORMING_STRINGS 32`,
`PG_QUERY_DISABLE_ESCAPE_STRING_WARNING 64`), set and then restored around `raw_parser` in
`src/pg_query_parse.c:48-60`:

```c
standard_conforming_strings = !((parser_options & PG_QUERY_DISABLE_STANDARD_CONFORMING_STRINGS) == ...);
escape_string_warning = !((parser_options & PG_QUERY_DISABLE_ESCAPE_STRING_WARNING) == ...);
result.tree = raw_parser(input, rawParseMode);
backslash_quote = BACKSLASH_QUOTE_SAFE_ENCODING;
standard_conforming_strings = true;
escape_string_warning = true;
```

`pg_query_scan` and both `pg_query_split_*` take no options, so **the scanner and the splitter
always assume `standard_conforming_strings = on`**. [CODE] What each GUC does to the scanner:

- `standard_conforming_strings` decides whether `'…'` is a plain string (backslash is an ordinary
  character) or an escape string (`\'` and `\\` are escapes): `scan.c:9434-9437`
  `if (yyextra->standard_conforming_strings) BEGIN(xq); else BEGIN(xe);`. **This changes where
  strings end, so it changes statement boundaries.** [CODE]
- `backslash_quote` only decides whether `\'` inside an escape string is an *error*
  (`scan.c:9659-9661`), and only with `SAFE_ENCODING` when the client encoding is client-only. It
  can make the server reject, never make it read a different boundary. [CODE]
- `escape_string_warning` only emits warnings (`scan.c:11436-11461`). [CODE]
- `quote_all_identifiers` affects output (deparse/ruleutils), not input scanning. I found no other
  GUC read by the extracted scanner. [CODE — grep of `scan.c` for `yyextra->` settings finds only
  the three above]

**Demonstrated disagreement** [RUN, PG 16 server, `live_pg_probe.sh`; library output in
`probe_out.txt`]. Text: `SELECT 'a\' ; --' ; SET search_path = x`.

| Reader | What it sees |
| --- | --- |
| libpg_query (SCS on) | `SELECT[0,6) SCONST[7,11) ';'[12,13) SQL_COMMENT[14,39)` — one `SelectStmt`; split returns `["SELECT 'a\\'"]` |
| server, SCS on | one SELECT returning `a\`; `search_path` unchanged |
| server, SCS off (set by `PGOPTIONS='-c standard_conforming_strings=off'`, i.e. the startup packet) | the string is `a' ; --`, then **`SET` runs: `search_path` = `x`** |

So a session in which `standard_conforming_strings` is off can hide any statement from the library.
SCS can be turned off by `SET`, `set_config()`, the startup packet `options`, `ALTER ROLE/DATABASE
… SET`, or `postgresql.conf`. Two facts bound the risk:

1. **Within one simple Query message the lexing mode is fixed at message start.** PostgreSQL
   raw-parses the whole message before executing any of it: `exec_simple_query` calls
   `parsetree_list = pg_parse_query(query_string)` (`src/backend/tcop/postgres.c:1083`) and only
   then loops over the list (`:1119`). [CODE] Confirmed [RUN]: `SET standard_conforming_strings =
   off; SELECT 'a\' ; --' ; SHOW search_path` in one message lexes the SELECT with SCS on (returns
   `a\`), and so does the `set_config('standard_conforming_strings','off',false)` variant. A
   syntax error anywhere aborts the whole message before the first statement runs ([RUN]:
   `SET search_path = x; SELECT 1);` → `ERROR: syntax error at or near ")"`).
2. **The server announces every change of SCS.** `standard_conforming_strings` is `GUC_REPORT`
   (`src/backend/utils/misc/guc_parameters.dat:2911`), so a ParameterStatus message is sent at
   startup and whenever it changes, including on rollback (`doc/src/sgml/protocol.sgml:1519-1550`).
   [CODE/DOCS] A proxy that watches ParameterStatus knows the exact mode for the *next* message.

**Encoding.** The server converts the Query text from `client_encoding` to the server encoding
before parsing (`pq_getmsgstring` → `pg_client_to_server`, `src/backend/libpq/pqformat.c:557`;
called at `postgres.c:4934`). [CODE] A proxy sees the client's bytes. libpg_query's own encodings
default to `SQL_ASCII` (`src/postgres/src_backend_utils_mb_mbutils.c:113,115`) and the scanner is
byte-oriented. For UTF-8 and every server-safe encoding, all bytes of multibyte characters are
≥ 0x80, so they cannot be mistaken for `'`, `\`, `;`, `$` or `-`. For the client-only encodings
(`PG_ENCODING_IS_CLIENT_ONLY`, `src/include/mb/pg_wchar.h:137`: SJIS, BIG5, GBK, UHC, GB18030)
a trailing byte can be 0x5C (`\`); the server converts first and sees a character, a byte-level
scanner sees a backslash. Under SCS on this matters only inside `E'…'` strings, but it can still
move a string's end. [CODE for the mechanism; the specific byte values are from memory of
CVE-2006-2314, not re-checked]. `client_encoding` is also `GUC_REPORT` (`guc_parameters.dat:454`).

**Conclusion on the lexical layer.** With the real scanner, the residual lexical risk reduces to
exactly two session values — `standard_conforming_strings` and `client_encoding` — both reported by
the server in ParameterStatus. A proxy that (a) refuses or closes any connection whose reported SCS
is not `on` or whose `client_encoding` is client-only, or (b) scans with the matching option, closes
it. Option (b) is not reachable through either Go binding (see below) for the scanner or splitter.

### Split-with-scanner can fail open

`src/pg_query_split.c:19`: `size_t open_parens = 0;` and `:43` `open_parens--;` with no check. A
stray `)` wraps it to `SIZE_MAX`, after which no `;` ever counts. [CODE] Observed [RUN]:
`SplitWithScanner("SELECT 1); SET search_path = x")` returns `[]` with **no error**, while
`SplitWithParser` returns `syntax error at or near ")"`. The server rejects that text too (whole
message parsed first), so nothing executes — but a classifier that loops over the split result would
classify *zero statements* and might call the message clean. Also `"SELECT 1 $1abc"` splits fine
with the scanner (patch 09) and fails in the parser. **Use `_with_parser`, or treat "zero
statements" and any scanner/parser disagreement as unknown.**

### 5. Language, licence, maturity, weight

C (plus the generated protobuf-c), BSD-3-Clause (`LICENSE`) with the PostgreSQL licence for the
copied server code (`README.md:414`). First tag `9.4-1.0.0`; active (last commit the day of this
reading). Weight for a Go proxy: see the two bindings.

### Probe table (behaviour of the library; applies to both Go bindings unless they say otherwise)

All rows [RUN] from `probe_out.txt` / `probe2_out.txt` (pg_query_go v6.2.5, PG 17.7 grammar).
"Decision" is n/a everywhere: the library classifies nothing; the column says what a consumer
would have to work with.

| Probe | What the library returns | What stays for the consumer |
| --- | --- | --- |
| P1 `SET search_path = x` | `VariableSetStmt{name:"search_path", kind:VAR_SET_VALUE}`; `SET LOCAL` gives `is_local:true`; IsUtility `true` | Exact. Node type is an allow-list key that cannot be spelled differently. |
| P2 `SELECT set_config('search_path','h',false)` | `SelectStmt` with `FuncCall{funcname:["set_config"]}`; `pg_catalog.set_config` gives `["pg_catalog","set_config"]`; Summary `functions=[set_config]`; IsUtility `false` | The library finds every *visible* call by name, regardless of comments/quoting. It does not know what `set_config` does: a name list (grammar layer) is still needed. Attribute notation `(x).f` is not a `FuncCall`. |
| P3 `SELECT f()` | `FuncCall f`; Summary `functions=[f]` | Nothing about `f`'s body — catalog layer, unsolved. |
| P4 lexical tricks | Brief's string: `SCONST[7,32)` spans `E'ok'`, newline, `'\' \|\| set_config(` — the continuation rule is applied as the server applies it; the text is a syntax error in both library and server ([RUN] PG 16: `syntax error at or near "','"`, same position). Dollar quotes and nested comments: `SELECT $$ ; SET x = 1 $$ /* ; SET y=2 /* nested */ ; */ ; LISTEN c` → split `[SELECT …, LISTEN c]`, matching the server. `SCS=off` trick: **library and server disagree** (section above). | Exact for SCS on and a server-safe encoding; wrong otherwise. |
| P5 | `PrepareStmt`; `ListenStmt`; `CreateStmt` with `relation.relpersistence:"t"`; `SelectStmt`+`FuncCall pg_advisory_lock`; `DeclareCursorStmt{options:288}` = `CURSOR_OPT_HOLD (0x20) \| CURSOR_OPT_FAST_PLAN (0x100)` (`parsenodes.h:3288,3290`) | Statement forms exact. `pg_advisory_lock` again a function name to list. |
| P6 `SELECT 1; SET search_path = x` | Split → 2 statements; IsUtility `[false true]`; Summary types `[SelectStmt VariableSetStmt]` | Exact (use the parser split, see fail-open note). |
| P7 `nextval('s')` | `FuncCall nextval` | name list (volatility is catalog). |
| P7 `WITH d AS (DELETE …) SELECT …` | top node `SelectStmt`, IsUtility `false`; Summary types `[SelectStmt DeleteStmt]` | Visible: a walker or Summary sees the DELETE. |
| P7 `SELECT … FOR UPDATE` | `SelectStmt.lockingClause=[LCS_FORUPDATE]` | Visible. |
| P7 `SELECT now()` | `FuncCall now` | name list; `'now'::timestamptz` is a `TypeCast` of a string — catalog layer. |
| P7 `SELECT * FROM v` | `RangeVar v` | view-ness and its functions: catalog, unsolved. |
| P7 `EXPLAIN ANALYZE DELETE FROM t` | `ExplainStmt` (IsUtility `true`), Summary `[ExplainStmt DeleteStmt]` | Visible; the `ANALYZE` option is a `DefElem` in `options`. |

---

## pg_query_go

### 1. Reads SQL with

libpg_query **17-6.2.5 (PG 17.7)** compiled in via cgo — one PostgreSQL major behind libpg_query
master. `parser/include/pg_query.h:165-166` (`PG_MAJORVERSION "17"`, `PG_VERSION "17.7"`); commit
`7ac09d4b` "Release 6.2.5 and update to libpg_query 17-6.2.5". [CODE] The parse tree JSON says
`"version":170007` [RUN]. The C sources (19 MB, 89 files in `parser/`) are vendored into the Go
module and compiled by cgo on first build (`parser/parser.go:4`: `#cgo CFLAGS: -Iinclude
-Iinclude/postgres -g -fstack-protector -std=gnu99 …`). [CODE]

Go API (`pg_query.go:12-100`) [CODE]: `Scan`, `ParseToJSON`, `Parse` (protobuf decoded into Go
structs), `Deparse`, `ParsePlPgSqlToJSON`, `Normalize`, `NormalizeUtility`, `Fingerprint`,
`FingerprintToUInt64`, `HashXXH3_64`, `SplitWithScanner`, `SplitWithParser`, `IsUtilityStmt`,
`Summary`; raw-bytes variants in `parser/parser.go` (`ScanToProtobuf :86`, `ParseToProtobuf
:104`). **No parser-options entry point**: `grep parse_opts|DISABLE_STANDARD` over the Go files
finds nothing, so a Go caller cannot scan, split or parse with `standard_conforming_strings=off`.
[CODE] `pg_query.go:1` is `//go:build cgo`: with `CGO_ENABLED=0` the functions disappear and the
build fails (`undefined: pg.SplitWithScanner`) [RUN].

### 2–4.
n/a (library). README: "This Go library and its cgo extension use the actual PostgreSQL server
source to parse SQL queries" (`README.md:5`). It documents build time — "Expect up to 3 minutes"
(`README.md:16-18`) — and that "allocation counts exclude the cgo portion" (`README.md:185`). [DOCS]

### 5. Language, licence, maturity, weight — and measured integration cost

Go + C, BSD-3-Clause. Tags v4.2.1 … v6.2.5; v6 line tracks PG 17. Go deps: `google.golang.org/protobuf`
(+ test-only `go-cmp`, `hujson`) (`go.mod`). [CODE]

**Build and binary** [RUN, gcc 13.3, Go 1.26.8, `GOFLAGS=-p=2`]:

| Item | Value |
| --- | --- |
| cold build of a one-call program (C compile) | **64 s**, max RSS 216 MB (`cgo_build.log`) |
| warm rebuild | 2.1 s |
| binary size | 11.8 MB vs 2.4 MB hello-world → **+9.4 MB** (with debug info; not stripped) |
| dynamic deps | `libc.so.6` only (`ldd`) |
| static | `-ldflags '-linkmode external -extldflags "-static"'` builds and runs (12.8 MB) |
| `CGO_ENABLED=0` | does not compile |
| cross-compile `GOARCH=arm64 CGO_ENABLED=1` without a cross C toolchain | fails (`gcc_arm64.S: no such instruction`) |

So CI needs a C compiler per target, and cross-builds need a cross toolchain (or zig cc / docker).

**Hot-path cost** [RUN, `-cpu 1 -benchtime 300ms`, ns/op; Go-side B/op and allocs/op only — C
allocations are not counted]. Queries: *short* `SELECT v FROM kv WHERE k = 'a'` (31 B), *medium*
ORM-style query with 14 quoted identifiers (≈330 B), *2KB* `IN` list of 2,000+ bytes.

| Operation | short | medium | 2KB |
| --- | --- | --- | --- |
| `SplitWithScanner` | **3.4 µs**, 40 B, 2 allocs | 11–16 µs | 54–62 µs |
| `SplitWithParser` | 4.0 µs, 40 B, 2 | 20 µs | 72 µs |
| `ScanToProtobuf` (raw bytes) | 5.6–6.2 µs, 184 B, 3 | 29–30 µs | 196 µs |
| `Scan` (decoded to Go structs) | 10.6–12.5 µs, 872 B, 15 | 57–61 µs, 6.1 KB, 69 | 360–406 µs, 40 KB, 369 |
| `ParseToJSON` | 10.3 µs, 592 B, 2 | 40 µs | 247 µs |
| `ParseToProtobuf` (raw bytes) | 27 µs, 136 B, 2 | 244 µs | 721 µs |
| `Parse` (protobuf decoded) | 47 µs, 2.1 KB, 42 | 293 µs, 9 KB, 215 | 1.23 ms, 49 KB, 1,100 |
| `Fingerprint` | 10.7 µs, 32 B, 2 | 52 µs | 200 µs |

Parallel (`ScanToProtobuf`, short): 5.8 µs/op at `-cpu 1`, **1.7 µs/op at `-cpu 4`** — scales
about 3.4× on 4 cores (thread-local `__thread` globals, no global lock). These numbers are about
3–10× slower than pg_query_go's own published figures on darwin/arm64 (`README.md:153-183`:
`RawParseSelect1-4` 3.2 µs, `FingerprintSelect1-4` 1.9 µs), which I attribute to the loaded VM
and slower cores; the *relative* ordering is the useful part: **split < scan < JSON parse <
protobuf parse**, and protobuf serialisation is the dominant cost of a full tree. That matches
PgDog's experience (below).

### Probe table
Same as the libpg_query table (that table was produced with this binding). Difference: no way to
pass `PG_QUERY_DISABLE_STANDARD_CONFORMING_STRINGS`.

---

## wasilibs/go-pgquery

### 1. Reads SQL with

The libpg_query C from pg_query_go (PG 17.7, `internal/cparser/include/pg_query.h:166`) compiled
with wasi-sdk 34 to `wasm32-wasip1-threads` (`buildtools/wasm/Dockerfile:1,17`), embedded as
`internal/wasm/libpg_query.so` (2.5 MB, `//go:embed`, `internal/wasm/wasm.go`) and run in wazero
v1.12.0. One patch replaces `sigsetjmp` with `setjmp` for WASM (`buildtools/wasm/patch.txt`).
[CODE] It returns pg_query_go's Go types (`pg_query.go:3-4` imports
`github.com/pganalyze/pg_query_go/v6`), so pg_query_go is a dependency for its types even
without cgo (the cgo files are excluded by `//go:build cgo`). [CODE]

**Narrower API.** The WASM module exports only (`Dockerfile` `-Wl,--export=` list): `pg_query_parse`,
`_parse_protobuf`, `_parse_plpgsql`, `_scan`, `_normalize`, `_fingerprint`, `_deparse_protobuf`,
`XXH3_64bits_withSeed` (+ malloc/free/TLS symbols). **No `split_with_scanner`, no
`split_with_parser`, no `is_utility_stmt`, no `summary`, no parse options.** `pg_query.go` has no
`Split*` functions (grep). [CODE] Statement splitting would have to be done from `Parse`
(`RawStmt.stmt_location/len`) or from `Scan` tokens.

**Instances and concurrency** (`parser/parser_wazero.go`) [CODE]:
- `initWASM` (`:35-62`, once per process): new wazero runtime with threads + exception handling,
  `CompileModule` of the 2.5 MB module, instantiate a shared `memory.wasm` (`flags 0x03` = shared
  with max; min 34 pages, max 65,536 pages = 4 GiB, per the 28-byte module and
  `--max-memory=4294967296` in the Dockerfile LDFLAGS), then a root instance running `_initialize`.
  No compilation cache is configured (`wazero.NewRuntimeConfig()` without
  `WithCompilationCache`).
- Every call does `getABI()` (`:236-250`): pop an instance from a global `container/list` under a
  mutex, or `newABI()` (`:172-233`), which `malloc`s TLS + a stack (`-z stack-size=1048576`, i.e.
  1 MiB) **inside the shared linear memory** and instantiates another module instance.
  `Close()` (`:278-282`) pushes it back. **The pool is unbounded and never shrinks**: it grows to
  the peak number of concurrent callers, and the malloc'd stacks are never freed.

**Measured** [RUN, `wasmcold`, `bench_wasm*.txt`]:

| Item | Value |
| --- | --- |
| build, `CGO_ENABLED=0`, cold module cache | 23.9 s (pure Go; most of it compiling wazero) |
| binary | 14.2 MB vs 2.4 MB hello → **+11.8 MB** (10.3 MB if only the `parser` package is imported) |
| **first call** (compile + root + first instance) | **3.2 s**, RSS 4 MB → **285 MB** peak (second run: 3.18 s, 275 MB) |
| first `Scan` inside `go test` | 4.2 s, 327 MB allocated |
| second call | 22 µs |
| 64 goroutines × 20 calls (64 instances) | 21 ms; RSS did not grow measurably (253–285 MB) — stacks live in lazily touched linear memory |

Hot path after warm-up (`-cpu 1`):

| Operation | short | medium | 2KB | vs cgo |
| --- | --- | --- | --- | --- |
| `ScanToProtobuf` | 20–23 µs, 11 KB, 10 allocs | 78–83 µs | 408–452 µs | 2.3–3.7× slower |
| `Scan` (decoded) | 31 µs | 109 µs | 563–618 µs | 1.5–2.9× |
| `ParseToJSON` | 64 µs | 159 µs | 587 µs | 2.4–6× |
| `Parse` (decoded) | 219 µs | 880 µs | 3.5 ms | 2.8–4.6× |
| `Fingerprint` | 221 µs, **451 KB/op** | 572 µs, 950 KB/op | 1.0 ms, 950 KB/op | 5–20× (Go-side allocation cause not investigated) |

Parallel `ScanToProtobuf` short: 21.5 µs at `-cpu 1`, 11.6 µs at `-cpu 4` (1.85× on 4 cores; the
cgo build scaled 3.4×). The README's own CI numbers agree in direction: "the WebAssembly version
performs about 4-5x slower than cgo … If your application requires online processing of high
volume of queries, it may be better to continue with cgo" (`README.md`, Performance). [DOCS + RUN]

### 2–4. n/a (library). README, Stability: "it is possible future issues that only affect
WebAssembly compilation will be found" and "this library cannot be used with TinyGo because it
relies on WebAssembly threads support". [DOCS]

### 5. Language, licence, maturity, weight
Go + embedded WASM, MIT (`LICENSE`); upstream C BSD-3/PostgreSQL. No tags or releases (pseudo-version
only); active (last commit 2026-11-24). Requires Go 1.26 (`go.mod`). Deps: wazero, wazero-helpers,
pg_query_go (types), protobuf, x/sys. No C toolchain; cross-compiles like any pure Go module.
Cost to a proxy: a 3 s / ~280 MB start-up step (do it at boot, not on the first client), 20–30 µs
per short-statement scan, and no splitter.

### Probe table
Same results as libpg_query's table for Scan/Parse (identical C, same PG 17.7). P6 differs: no
`Split*`/`IsUtilityStmt`/`Summary`; the consumer must count `ParseResult.Stmts`.

---

## Users in a proxy hot path

- **PgDog** no longer uses pg_query.rs. [WEB READ] `Cargo.toml` (workspace) pins
  `pg_raw_parse = { git = "https://github.com/pgdogdev/pg_raw_parse.git", rev = "a4784fc6…" }`;
  `pgdog/Cargo.toml:85` `pg_raw_parse.workspace = true`; CHANGELOG line 457: "Add
  `query_parser_engine = "pg_query_protobuf"` deprecation warning and remove the setting from usage".
  pg_raw_parse's README: "provides direct access to the PostgreSQL parser. It's 20-60x faster than
  `pg_query.rs` … has no dependencies except libpg_query". So the one proxy that parses every
  statement with the real parser kept libpg_query and **dropped the protobuf round trip** — the
  same cost my benchmark shows dominating. (pg_raw_parse internals not read: [NOT FOUND] in this
  canvas.)
- **sqlc** no longer uses pg_query_go. [WEB READ] sqlc `go.mod` has no `pganalyze` entry;
  `internal/engine/postgresql/parse.go:9-10` imports `github.com/sqlc-dev/oliphant`. sqlc is an
  offline code generator, not a hot path.
- **pganalyze** uses libpg_query offline (fingerprint/normalize for query statistics) — from
  memory; not re-checked here.
- I did not find a Go proxy that runs pg_query_go per statement. [NOT FOUND — not searched beyond
  the above]

## Pure-Go alternatives (one line each on fidelity)

- **sqlc-dev/oliphant** — see "Better candidate" below; hand-written Go port verified byte-for-byte
  against libpg_query outputs; hard-codes SCS on.
- **jackc/pgx `internal/sanitize`** — a small state-machine lexer (raw, single quote, double quote,
  `E'…'`, dollar quote, comments; `sanitize.go:223-467`) used only to substitute `$n` in client-side
  interpolation; its own comment says it "is only safe when standard_conforming_strings is on"
  (`sanitize.go:515`). Not a statement splitter; no string-continuation rule found in the state
  list. [WEB READ]
- **auxten/postgresql-parser** — CockroachDB's parser extracted; "Pure Golang PostgreSQL … Parser"
  (repo description); CockroachDB dialect, not PostgreSQL's grammar — fidelity to real PG not
  guaranteed. [WEB READ, metadata only]
- **cockroachdb/cockroachdb-parser** — archived (2026-04-30). [WEB READ, metadata only]

## Better candidate: sqlc-dev/oliphant (short treatment)

[LOCAL SOURCE at 9fbe0c6a, then deleted to save disk]
1. **Reads SQL with** a hand-written pure-Go port of `scan.l`/`gram.y` with the same API and
   protobuf types as pg_query_go. README: "A hand-written, pure Go (no cgo, no wasm) drop-in
   replacement for pganalyze/pg_query_go … Pinned to libpg_query `18.0.0` (PostgreSQL 18.4,
   patched) … Every golden in `parser/testdata` derives from that oracle, byte for byte"; status:
   "the corpus — 2,067 golden files / 308,561 cases across eleven suites — passes"; lexer: "token
   streams byte-identical across the corpus". It is kept in step by regenerating goldens from a
   cgo oracle (`oracle/`, `cmd/regenerate`). About 42,600 non-test, non-`ast/` Go lines; lexer
   2,423 lines (`internal/lexer/*.go`). [CODE for sizes; DOCS for corpus claims] Note: its README
   says "the exact build pg_query_go v6.2.2 ships" is 18.0.0, but pg_query_go's own source at
   `7ac09d4b` (v6.2.5) embeds 17.7 — the README's statement contradicts the code I read.
2. **SCS**: `internal/lexer/lexer.go:239-240`: "`scan.l: {xqstart}` — standard_conforming_strings is
   on, so this is always a standard (xq) string." Same residual risk as libpg_query, with no
   option to change it. [CODE]
3–4. n/a (library).
5. Go 1.24, licence file `LICENSE` + `LICENSE.LIBPG_QUERY` + `LICENSE.POSTGRESQL` (GitHub reports
   NOASSERTION); created 2026-08-15, tags v0.1.0/v0.2.0, used by sqlc. Fidelity rests on a
   differential corpus, which is exactly the method the assessment proposes for a hand lexer — but
   done for the whole grammar. Very young; correctness outside the corpus is unproven.

---

## Claims of completeness

- libpg_query `README.md:5`: "uses the actual PostgreSQL server source to parse SQL queries and
  return the internal PostgreSQL parse tree." The claim is about *parsing*, and it survives the
  code: the grammar and scanner are copied (`src/postgres/src_backend_parser_{gram,scan}.c`), with
  15 patches that only widen the language. Two qualifications the README does not state: the
  scanner and splitter are fixed to `standard_conforming_strings=on`, and `split_with_scanner` is
  a heuristic that can return zero statements without an error.
- None of the three claims to decide safety, read-only-ness or cacheability. `IsUtilityStmt`
  (pg_query_go `pg_query.go:88-91`: "true = utility statement / DDL, false = SELECT / INSERT / UPDATE /
  DELETE / MERGE") is a node-type test; `SELECT set_config(...)` and `SELECT pg_advisory_lock(1)`
  are "not utility" [RUN].
- go-pgquery README: "There are no known behavior differences with upstream libraries" — true for
  the functions it exports; it exports fewer (no split/is-utility/summary). [CODE]

## Mapping to options (a) (b) (c1) (c2)

All three are the engine of option **(c1)**. What (c1) actually buys, split into its two halves:

**Scanner only** (`Scan`/`SplitWithParser` → tokens and statement boundaries; 3.4–6 µs for a short
statement with cgo, 20–30 µs with WASM):
- Closes the lexical layer exactly — strings, `E''`, `U&''`, dollar quotes, nested comments,
  continuation across a newline, operator splitting — **provided** the session has
  `standard_conforming_strings = on` and a server-safe `client_encoding`, both of which the proxy
  can verify from ParameterStatus. The review-2 blocker class (lexer loses its place) disappears.
- Gives exact first keywords per statement and an exact statement count, which is all option (b)'s
  "known clean" test and its shareable-shape recogniser need. With real tokens the recogniser
  need not restrict the alphabet: "one statement, tokens ⊆ {SELECT keyword, identifiers, constants,
  `= , . * ( )`}, every IDENT before `(` on the allow-list" is decidable on the token stream.
- The (b) substring taint test could become a token test (an `IDENT`/keyword equal to
  `set_config`, `pg_advisory_lock`, `temp`…), which removes false positives from string contents.
  It stays a list.

**Full parse tree** (`Parse`, 10–47 µs short with cgo, 64–219 µs with WASM) adds over the scanner:
- Exact statement kinds (`VariableSetStmt`, `PrepareStmt`, `ListenStmt`, `DeclareCursorStmt` with
  the HOLD bit, `CreateStmt.relpersistence='t'`, `LockingClause`, data-modifying CTEs): every
  *syntactic* session-state form and write form, without the "dozen spellings of SET" problem. An
  allow-list over node types is finite and complete for statement forms.
- Every visible function call as a qualified name, whatever the spelling (schema-qualified,
  quoted, commented) — except attribute notation `(x).f`, which is not a FuncCall.

**What stays unsolved either way** (catalog layer): what `f()` does, whether `v` is a view and over
what, function volatility, casts that call volatile input functions (`'now'::timestamptz`,
`'now'::tstzrange`), `search_path` resolution, attribute-notation calls. The **grammar-layer
lists also stay**: the parser tells you a call is `set_config`; *you* must list that `set_config`,
`pg_advisory_lock`, `nextval`, `now`, `random`, `txid_current`, … matter. The list moves from
"spellings" to "function names", which is shorter and closed under quoting, but not complete.

So (c1) fixes the lexical layer and turns grammar-layer *spelling* holes into a function-name
allow-list; it does not remove the need for (b)'s reset-on-release for decision 3, nor make an
allow-list for decision 5 catalog-complete. It **combines** with (b) rather than replacing it.
**(c2)** (`BEGIN READ ONLY`) is orthogonal and still useful for decision 4.

## Mechanisms not in the assessment

1. **ParameterStatus as a session-state signal.** The server sends ParameterStatus for every
   change of a `GUC_REPORT` parameter, also on rollback (`protocol.sgml:1513-1550`); the backend
   flushes them with `ReportChangedGUCOptions()` just before ReadyForQuery (`postgres.c:4826`). On PostgreSQL ≥ 18 the set includes **`search_path`**, plus
   `standard_conforming_strings`, `client_encoding`, `DateStyle`, `IntervalStyle`, `TimeZone`,
   `default_transaction_read_only`, `application_name`, `session_authorization`, `is_superuser`
   (`guc_parameters.dat` lines 63, 454, 620, 807, 1374, 1456, 2622, 2712, 2911, 3106; docs:
   "`search_path` was not reported by releases before 18"). [CODE/DOCS] In a Go proxy: parse
   `'S'` messages on the backend stream (it already parses the stream to find ReadyForQuery);
   any ParameterStatus for one of these names during a lease marks the connection dirty → reset or
   close on release. This catches `SET`, `set_config()`, and a function that changes
   `search_path` out of sight — the catalog-layer case of P3 — **without reading SQL**. It does not
   cover non-reported GUCs (e.g. `work_mem`, custom `x.y`), prepared statements, temp tables,
   LISTEN, advisory locks or cursors. Mini-gateway's backend is "one ordinary Postgres"; this needs
   PG 18 for `search_path`. It also gives the exact lexing mode for the next message (section on
   SCS above).
2. **Fail-closed on lexical mode.** Because the simple-query path lexes the whole message under the
   SCS value current at message start (`postgres.c:1083`), a proxy can make the real scanner exact
   by refusing to forward when the last reported `standard_conforming_strings` ≠ `on` (or by
   closing such a connection). This is a two-line check, not a lexer rule.
3. **Ask the server for the catalog facts, once, and cache them.** Not a mechanism I found in these
   projects, but the parse tree makes it practical: for every `FuncCall` name in a statement the
   proxy could look up `pg_proc.provolatile` (and `prosecdef`, `proconfig`) on first sight and cache
   by name + `search_path`; a function that is not `i`/`s` is not shareable. It still cannot see
   inside `SET`-ing function bodies (volatility says nothing about session effects) and the cache
   needs invalidation on DDL, so it helps decision 5, not decision 3. [inference]
4. **Statement-level reset is cheap to detect from the tree.** `DISCARD ALL` itself, and
   `RESET ALL`, are `DiscardStmt` / `VariableSetStmt{kind:VAR_RESET_ALL}` — a proxy can let a
   client's own reset mark the connection clean. Minor.

## Could not verify

- PostgreSQL GA dates for 16/17/18 used to compute libpg_query's lag: **from memory**.
- The exact client-only-encoding trail-byte ranges (0x5C as a second byte in SJIS/BIG5/GBK/GB18030):
  mechanism verified in code (`pg_client_to_server` before parsing; `PG_ENCODING_IS_CLIENT_ONLY`),
  byte values **from memory**, not tested against a server.
- pg_raw_parse internals (whether it skips protobuf, how it calls libpg_query): **COULD NOT FIND**
  — only its README and PgDog's Cargo/CHANGELOG were read.
- pganalyze's own use of libpg_query: **from memory**, not re-checked.
- Why go-pgquery's `Fingerprint` allocates 450–950 KB per call on the Go side: measured, not
  investigated.
- How many wazero instances the pool held after the 64-goroutine run (the pool is unexported); the
  per-instance cost was inferred from code (1 MiB stack + TLS in shared memory), not measured.
- Whether pg_query_go will move to libpg_query 18: no PG 18 tag exists at `7ac09d4b`
  (latest v6.2.5, PG 17.7); oliphant's README claims otherwise — **contradiction unresolved**.
- auxten/postgresql-parser and cockroachdb-parser fidelity: repository metadata only.
- Benchmarks ran on a shared, loaded VM; cgo figures are 3–10× slower than pg_query_go's published
  darwin/arm64 numbers. Treat absolute numbers as upper bounds.
