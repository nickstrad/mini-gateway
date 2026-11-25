# Apache ShardingSphere-Proxy: a full ANTLR PostgreSQL grammar

## Sources

| Project | URL | Commit SHA | Commit date | Local / web |
| --- | --- | --- | --- | --- |
| Apache ShardingSphere (master) | https://github.com/apache/shardingsphere.git | `03aa12d519fc826773241b96a7d40ae4135d3ba1` | 2026-11-24T22:25:21+08:00 | LOCAL SOURCE (sparse, blobless, depth-1 clone at `/root/scratch/c05-canvas/repos/shardingsphere`, 24 MB) plus `gh api` for single files outside the sparse set, and for commit and issue counts |
| Newest release | `gh release view -R apache/shardingsphere` | tag `5.5.3` | published 2026-02-28 | WEB (GitHub API) |
| PostgreSQL (used only for comparison with `gram.y`) | existing clone `/root/scratch/c05-canvas/repos/postgres` | `4483697be0e0` | 2026-11-25 | LOCAL SOURCE |

All paths below are relative to the ShardingSphere clone root unless stated. "VERIFIED IN CODE" means
I read the cited lines at the commit above. I did not run anything. The project was not built.

## Summary row

| Project | Reads SQL with | Session-state mechanism | Read/write and share decisions | List type | Cost of a miss | Fails open or closed |
| --- | --- | --- | --- | --- | --- | --- |
| ShardingSphere-Proxy (PostgreSQL frontend) | A hand-maintained ANTLR4 grammar (8,042 `.g4` lines) plus a 5,343-line Java visitor. The rule names follow PostgreSQL's `gram.y`, but no file says the grammar is derived from it | The proxy answers `SET`/`RESET`/`SHOW` itself and never forwards them. For PostgreSQL it then discards the value, because no PostgreSQL "replay" provider exists. Every other statement goes to a backend connection taken from a HikariCP pool shared by all client sessions. Outside a transaction that connection goes back to the pool after each command, with no reset. Session state changed through `SELECT`/DDL/`PREPARE`/`LISTEN` is not detected | Read/write splitting only (no result cache or coalescing found). A statement goes to a replica if it is a `SelectStatement` with no locking clause, no data-modifying CTE and no write hint, and the session is outside a transaction. Function calls are not inspected | Allow-list by statement **class** (`SelectStatement` → may read from a replica; everything else → primary), with two deny checks inside `SELECT` (lock clause, DML CTE) | Read/write: a volatile or writing function in a `SELECT` (e.g. `nextval`) goes to a replica and errors there (or reads stale data). Session state: a change made through a function, temp table, `PREPARE`, advisory lock or `WITH HOLD` cursor stays on the pooled backend connection and reaches the next proxy client that gets that connection: a cross-client leak | **Closed** on parse failure (`SQLParsingException` goes to the client) and on multi-statement queries. **Open** for everything that parses: session effects that are not `SET` syntax are forwarded without a check |

## Apache ShardingSphere-Proxy

### 1. Does it read SQL? With what, how big, how kept in step

**Every simple Query is fully parsed before anything else happens. VERIFIED IN CODE.**
`PostgreSQLComQueryExecutor` constructor
(`proxy/frontend/dialect/postgresql/src/main/java/.../command/query/simple/PostgreSQLComQueryExecutor.java:77-82`):

```java
SQLStatement sqlStatement = ProxySQLComQueryParser.parse(packet.getSQL(), databaseType, connectionSession);
proxyBackendHandler = ProxyBackendHandlerFactory.newInstance(databaseType, packet.getSQL(), sqlStatement, connectionSession, packet.getHintValueContext());
```

`ProxySQLComQueryParser.parse` (`proxy/backend/core/.../handler/ProxySQLComQueryParser.java:47-55`) returns
`EmptyStatement` for comment-only text and otherwise calls the dialect `SQLParserEngine`.

**Grammar files and sizes** (`parser/sql/engine/dialect/postgresql/src/main/antlr4/`; `wc -l`):

| File | Lines |
| --- | --- |
| `imports/postgresql/BaseRule.g4` | 2,249 |
| `imports/postgresql/DDLStatement.g4` | 2,121 |
| `imports/postgresql/PostgreSQLKeyword.g4` | 1,531 |
| `imports/postgresql/Keyword.g4` | 641 |
| `imports/postgresql/DMLStatement.g4` | 559 |
| `org/.../autogen/PostgreSQLStatementParser.g4` (top rule `execute`) | 213 |
| `DALStatement.g4` 147, `DCLStatement.g4` 106, `Symbol.g4` 98, `TCLStatement.g4` 93, `Literals.g4` 62, `Alphabet.g4` 48, `ModeLexer.g4` 42, `StoreProcedure.g4` 42, `LCLStatement.g4` 39, `PostgreSQLStatementLexer.g4` 28, `Comments.g4` 23 | 728 |
| **Total** | **8,042** |

**Visitor code** (tree → statement objects), `parser/sql/engine/dialect/postgresql/src/main/java`: 5,343
lines, mainly `PostgreSQLStatementVisitor.java` (2,520), `PostgreSQLDDLStatementVisitor.java` (1,684),
`PostgreSQLDCLStatementVisitor.java` (325), `PostgreSQLDALStatementVisitor.java` (215). The
PostgreSQL-specific statement classes (`parser/sql/statement/dialect/postgresql`) add 79 files and 2,615
lines. All of this sits on a dialect-neutral statement model and a binder (`infra/binder`) that I did not
size. For comparison, PostgreSQL's own `src/backend/parser/gram.y` has 20,166 lines at the commit above.

**Statement coverage.** The top rule `execute` (`PostgreSQLStatementParser.g4:27-211`) is an alternation
of about 185 statement rules, ending with `) SEMI_? EOF` (one statement, an optional trailing semicolon,
then end of input). `gram.y`'s `stmt:` has 125 alternatives, but these do not correspond one-to-one
(ShardingSphere splits some of them). The grammar does contain rules for every session-relevant
statement: `set`, `resetParameter`, `show`, `discard`, `prepare`, `deallocate`, `listen`, `unlisten`,
`declare`, `lock`, `load`, `explain`, `copy` (VERIFIED IN CODE, `grep` over the `execute` alternatives
and the rule definitions in `DALStatement.g4:24-59`, `DDLStatement.g4:1714,1874,1910,1939`).

**Lexical layer. VERIFIED IN CODE.** It ports the hard scanner rules. The rules do not say whether they
were checked against a real server.
- `Literals.g4:23` (quoted from the file):
  `STRING_: E ESCAPE_STRING_LITERAL_ (STRING_CONTINUATION_ ESCAPE_STRING_LITERAL_)* | STRING_LITERAL_ (STRING_CONTINUATION_ STRING_LITERAL_)*;`
  So `E'…'` strings use backslash escapes, plain strings use `''` only (that is, it assumes
  `standard_conforming_strings = on`), and string continuation across a newline (with `--` comments
  allowed in between) is modelled in `STRING_CONTINUATION_` (`Literals.g4:37`).
- Dollar quotes: `ModeLexer.g4:22-41` pushes into a `DOLLAR_QUOTED_STRING_MODE` and closes only on the
  same tag (`isTag()` in `parser/.../PostgreSQLLexerBase.java:49-51` compares with a tag stack).
- Nested block comments: `Comments.g4:22`: `BLOCK_COMMENT: '/*' (BLOCK_COMMENT|.)*? '*/' -> channel(HIDDEN);`
- The plain-string rule was wrong until 2026: issue #36416 "PostgreSQL Lexer Fails to Parse Backslash
  ('\\')" (closed 2026-02-05) reports that `'\'` was treated as an escape. That is the same class of
  mistake as mini-gateway's review-2 blocker. The lexer is still a hand port that gets fixed one
  report at a time.

**Is it derived from `gram.y`? COULD NOT FIND a header or comment saying so.** `grep -i "gram.y|derived|ported|src/backend"`
over the dialect directory finds nothing. The `.g4` files carry only the Apache licence header. But the rule names
mirror `gram.y` nonterminals one for one: `aExpr`/`bExpr`/`cExpr` (`BaseRule.g4:635,693,713`),
`funcApplication` (1029), `aexprConst` (1044), `selectWithParens`, `selectClauseN`, `simpleSelect`
(`DMLStatement.g4:145-167`), `preparableStmt` (219). My **inference** is that it was transcribed by hand
from `gram.y` at some point and has been patched issue by issue since. Nothing in the tree records
which PostgreSQL version it tracks.

**Maintenance cost. Evidence via the GitHub API:**
- Commits touching the grammar directory (`parser/sql/engine/dialect/postgresql/src/main/antlr4`) since
  2024-11-25: **25**, by month: 2025-08: 2, 2026-02: 3, 2026-08: 1, 2026-09: 1, 2026-10: 8,
  2026-11: 10. The directory was renamed around mid-2025. The old path
  `parser/sql/dialect/postgresql/src/main/antlr4` adds **7** more (2025-06-06 to 2025-08-24). So there were
  at least 32 grammar commits in two years, bunched into bursts. The newest are "Preserve PostgreSQL
  SQL parsing and binding metadata (#40533)" (2026-11-23), "Fix PostgreSQL keyword identifier
  compatibility (#40538)" (2026-11-21) and "Support PostgreSQL aggregate FILTER …" (2026-11-18).
  The whole dialect directory (grammar plus visitor) had 69 commits since 2024-11-25.
- Issues labelled both `in: SQL parse` and `db: PostgreSQL`: **69 in total, 1 open** (#29673 "postgreSql
  support regexp_split_to_table function", opened 2024-01-08), **6 opened since 2024-11-25**. Issues
  matching `postgresql "no viable alternative"` (the ANTLR parse-failure message): 34, 2 open. Examples:
  #24862 "exec postgresql range type construcor function faild, error: no viable alternative at input …
  tsrange"; #33452 "SQL syntax exception occurs when execute SELECT EXTRACT(WEEK FROM TIMESTAMP …)";
  #33833 "… when select keyword an error is reported"; #34777 "PostgreSQL StackOverflowError
  PostgreSQLStatementVisitor.visitExprList"; #36704 "Support to parse more SQL statements for PostgreSQL".
  The exact query the brief suggested (`"postgresql syntax"`) returns only 6 issues, mostly unrelated.
- The parser test suite keeps an explicit list of SQL it cannot parse:
  `test/it/parser/src/main/resources/sql/unsupported/unsupported.xml` has 34 cases tagged PostgreSQL
  (for example `SELECT $1_0`, `SELECT $1.2`, `SELECT $0x10`).

**What happens when the grammar cannot parse a statement: an error goes to the client (fails closed).
VERIFIED IN CODE.** `parser/sql/engine/core/.../database/parser/SQLParserExecutor.java:48-73`:

```java
ParseASTNode result = twoPhaseParse(sql);
if (result.getRootNode() instanceof ErrorNode) {
    throw new SQLParsingException(sql);
}
...
((Parser) sqlParser).getInterpreter().setPredictionMode(PredictionMode.SLL);
return (ParseASTNode) sqlParser.parse();
} catch (final ParseCancellationException ex) {
    ... setPredictionMode(PredictionMode.LL); ...
    } catch (final ParseCancellationException exception) {
        throw new SQLParsingException(sql + ", " + exception.getMessage());
```

COULD NOT FIND any pass-through-on-parse-failure path for the PostgreSQL proxy in the directories read.

### 2. Session-state safety

**`SET`, `RESET` and `SHOW` never reach PostgreSQL. The proxy handles them itself. VERIFIED IN CODE.**
`ProxyBackendHandlerFactory.newInstance` (`proxy/backend/core/.../handler/ProxyBackendHandlerFactory.java:134-138`)
asks the dialect's admin-executor creator first. If it returns an executor, the statement never goes to a
backend. `PostgreSQLAdminExecutorCreator.create`
(`proxy/backend/dialect/postgresql/.../handler/admin/PostgreSQLAdminExecutorCreator.java`):

```java
if (sqlStatement instanceof SetStatement) {
    return Optional.of(new PostgreSQLSetVariableAdminExecutor((SetStatement) sqlStatement));
}
if (sqlStatement instanceof ShowStatement) {
    return Optional.of(new PostgreSQLShowVariableExecutor((ShowStatement) sqlStatement));
}
if (sqlStatement instanceof PostgreSQLResetParameterStatement) {
    return Optional.of(new PostgreSQLResetVariableAdminExecutor((PostgreSQLResetParameterStatement) sqlStatement));
}
```

`DatabaseAdminUpdateProxyBackendHandler.execute` (`.../handler/admin/DatabaseAdminUpdateProxyBackendHandler.java:46-49`)
only runs the executor and returns an update header.

**For PostgreSQL the SET value is then discarded. VERIFIED IN CODE.** `PostgreSQLSetVariableAdminExecutor.execute`
(lines 42-48) calls `CharsetSetExecutor.set` (which acts only on `client_encoding`) and
`SessionVariableRecordExecutor.recordVariable`
(`proxy/backend/core/.../variable/session/SessionVariableRecordExecutor.java:47-54`):

```java
if (DatabaseTypedSPILoader.findService(ReplayedSessionVariableProvider.class, databaseType).map(optional -> optional.isNeedToReplay(variableName)).orElse(false)) {
    connectionSession.getRequiredSessionVariableRecorder().setVariable(variableName, assignValue);
    connectionSession.getDatabaseConnectionManager().markSessionVariablesDirty();
} else {
    log.debug("Set statement {} = {} was discarded.", variableName, assignValue);
}
```

The only implementation of `ReplayedSessionVariableProvider` in the whole tree is
`proxy/backend/dialect/mysql/.../MySQLReplayedSessionVariableProvider.java` (from `git ls-tree -r HEAD | grep ReplayedSessionVariable`,
confirmed by `gh search code "implements ReplayedSessionVariableProvider"`). So for PostgreSQL every
`SET x = v` goes only to a debug log. The client still gets `CommandComplete "SET"` and a
`ParameterStatus(x, v)` (`PostgreSQLComQueryExecutor.java:117-141`). **The client is told the setting
changed when it did not.** The documentation admits one instance of this:
`docs/document/content/features/sharding/limitation.en.md:11` says "…do not support 'SEARCH_PATH' to
modify the schema search path."

`SHOW` is answered from a fixed table of 7 names (`PostgreSQLShowVariableExecutor.java:49-63`:
`application_name`, `client_encoding`, `integer_datetimes`, `timezone` fixed to `Etc/UTC`,
`transaction_isolation`, `transaction_read_only`, `server_version`). Any other name returns an empty
string (`getOrDefault(name, unused -> new String[]{"", "", ""})`, line 81).

**The replay mechanism that does exist (MySQL only).** The brief asked for these names; this is what they do. VERIFIED IN CODE:
`RequiredSessionVariableRecorder` (`proxy/backend/core/.../session/RequiredSessionVariableRecorder.java`)
keeps a map of variables. `toSetSQLs` turns it into `SET k=v` per variable for PostgreSQL (lines 87-92) and
`toResetSQLs` into `RESET ALL` (line 110). `ProxyDatabaseConnectionManager`
(`proxy/backend/core/.../connector/ProxyDatabaseConnectionManager.java`) replays the map on every newly
obtained or "pending replay" connection (`setSessionVariablesIfNecessary`, ~line 174;
`replaySessionVariablesIfNecessary`, ~line 125). Before closing connections it runs `RESET ALL`
(`resetSessionVariablesIfNecessary`, ~line 420). This is "track and replay", and it really exists, but
the PostgreSQL code has no provider, so the map is always empty and **neither replay nor reset ever
runs for PostgreSQL**. The PostgreSQL branches of `toSetSQLs`/`toResetSQLs` are unreachable through
the SET path at this commit.

**`SET` forms that do not produce an assignment fail.** `visitSet` (`PostgreSQLDALStatementVisitor.java:88-102`)
adds a `VariableAssignSegment` only for `configurationParameterClause` (`SET x = v`/`SET x TO v`) and
`SET NAMES`. `SET TIME ZONE …`, `SET ROLE …`, `SET SESSION AUTHORIZATION …`, `SET x FROM CURRENT`,
`SET SCHEMA '…'` parse (`DALStatement.g4:28-41`) into a `SetStatement` with an empty list, and
`PostgreSQLSetVariableAdminExecutor.execute` line 43 calls `getVariableAssigns().iterator().next()`.
**Inference from code, not run:** that throws `NoSuchElementException`, so the client gets an error
(fails closed, by accident).

**Backend connection lifetime: per command outside a transaction, per transaction inside one, taken
from a pool shared by all client sessions. VERIFIED IN CODE.**
- Connections come from `ProxyContext.getInstance().getBackendDataSource().getConnections(...)`
  (`ProxyDatabaseConnectionManager.createNewConnections`), which is a process-wide data source. The default pool is
  HikariCP (`docs/.../shardingsphere-proxy/yaml-config/data-source.en.md:11`: "When not specified, the
  default data source connection pool is HikariCP." DOCS ONLY for the default).
- After every command, `CommandExecutorTask.finish` (`proxy/frontend/core/.../command/CommandExecutorTask.java:158-162`)
  calls `closeExecutionResources()`. That method (`ProxyDatabaseConnectionManager`, ~line 300) closes the
  connections when the session is not in a connection-held transaction:

```java
if (!connectionSession.getTransactionStatus().isInConnectionHeldTransaction(TransactionUtils.getTransactionType(...))) {
    result.addAll(closeHandlers(true));
    result.addAll(closeConnections(false));
}
```

  `closeConnections` calls `resetSessionVariablesIfNecessary` (a no-op for PostgreSQL, as shown above)
  and then `each.close()`. With a pool, `close()` hands the connection back. **No `DISCARD ALL` and no
  `RESET ALL` is sent for PostgreSQL** (`grep DISCARD` over `proxy/backend/core/src/main` finds nothing).
  Whether HikariCP itself resets anything beyond JDBC properties (autocommit, read-only, isolation,
  catalog/schema): COULD NOT FIND in this clone. From memory, HikariCP resets only those JDBC-level
  properties and never PostgreSQL session state. Not verified here.
- Connection mode: `MEMORY_STRICTLY` / `CONNECTION_STRICTLY` are about how many connections one query
  may open per data source (`max-connections-size-per-query`, default 1, `props.en.md:18`) and whether
  result sets are streamed or loaded into memory (`docs/.../reference/sharding/execute.en.md:29-49`,
  DOCS ONLY). They do not tie a client to a backend connection, and they do not touch session state.

**Protocol-level prepared statements** (extended-protocol Parse/Bind) are owned by the proxy, per client
session (`ServerPreparedStatementRegistry`, `PreparedStatementCacheContext` under
`proxy/backend/core/.../session/`; `PostgreSQLComParseExecutor` in the frontend). They are re-prepared
on whatever backend connection runs them, and `closeConnections` closes the cache (`connectionSession.getPreparedStatementCacheContext().closeAll()`).
VERIFIED that the classes exist and that `closeAll` is called. The mechanism inside: not read in detail.
**SQL-level `PREPARE`/`LISTEN`/`DECLARE`/temp tables get no special handling:** `grep -i "Listen|Prepare|Declare|temporary|advisory|Discard"`
over `proxy/backend` and `proxy/frontend` main code finds only cursor handling in
`ProxySQLExecutor.isDDLWithoutMetaDataChanged` (lines 171-177) and type metadata. No PostgreSQL
`DialectProxyStateSupportedSQLProvider` exists (only MySQL's), so the only statement classes rejected
outright are `DCLStatement` and `RenameTableStatement` (`ProxyBackendHandlerFactory.java:79`). One
more check (`ProxySQLExecutor.java:144-157`) refuses metadata-changing DDL **inside** a transaction for
PostgreSQL. The docs give its SQLSTATE: `features/transaction/limitations.en.md:15-16`, "rejected
before execution with SQLSTATE `0A000`". So `CREATE TEMP TABLE` is refused inside `BEGIN` and
forwarded outside one.

**Summary of the mechanism.** (1) Intercept and swallow the visible `SET` syntax. (2) Forward everything
else to a backend connection, lent per command and returned to a shared pool. (3) Do nothing on
release. The default is the only behaviour; there is no setting that adds `DISCARD ALL`. In mini-gateway's terms
it is a deny-list on one statement class (and it lies to the client about the result), with no
mechanism behind it.

### 3. Read-vs-write and shareability decisions

**Read/write splitting (only when a readwrite-splitting rule is configured). VERIFIED IN CODE.**
`ReadwriteSplittingDataSourceRouter.route` (`features/readwrite-splitting/core/.../route/ReadwriteSplittingDataSourceRouter.java:43-59`)
tries the primary router, then the transactional router, then falls back to the standard (replica
load-balancing) router. The primary rule,
`QualifiedReadwriteSplittingPrimaryDataSourceRouter.java:40-64`:

```java
return isWriteRouteStatement(sqlStatementContext) || isHintWriteRouteOnly(hintValueContext);
...
return containsLockSegment(sqlStatement) || containsLastInsertIdProjection(sqlStatementContext) || !isSelectStatement(sqlStatement)
        || !SQLStatementContextExtractor.getDataModificationBaseContexts(sqlStatementContext).isEmpty();
```

The transactional rule (`QualifiedReadwriteSplittingTransactionalDataSourceRouter.java:39-56`): in a
transaction, route by `transactionalReadQueryStrategy`. The default is `PRIMARY`
(`features/readwrite-splitting/api/.../ReadwriteSplittingDataSourceGroupRuleConfiguration.java:51`);
`FIXED` and `DYNAMIC` send reads to replicas.

So the brief's guess is right: **"a `SelectStatement`, with no lock clause, no data-modifying CTE,
no write hint, outside a transaction" goes to a replica.** **Function calls are not considered.**
`containsLastInsertIdProjection` is a MySQL `LAST_INSERT_ID()` special case. The data-modifying-CTE
check works for PostgreSQL because `visitCommonTableExpr` stores a non-SELECT `preparableStmt` as the
CTE's `dataModification` (`PostgreSQLStatementVisitor.java:1668-1676`). The lock clause becomes a
`LockSegment` (`PostgreSQLStatementVisitor.java:1707`).

- **List type:** an allow-list by statement class (only `SelectStatement` may go to a replica), with two
  deny checks inside it. Anything that is not a `SelectStatement` (EXPLAIN, SHOW, CALL, DO, …) goes to
  the primary. That direction is safe.
- **Cost of a miss:** a `SELECT` that writes (`nextval`, `set_config`, a writing user function,
  `pg_advisory_lock`) goes to a replica. On a hot standby a write fails with an error; session effects
  (`set_config`, advisory lock) succeed and stay on the replica's pooled connection. The docs accept
  stale reads: `features/readwrite-splitting/limitations.en.md`: "Data inconsistency resulting from
  data synchronization delays between primary and secondary databases is not supported."
- **Escape hatch: a client-side hint in a SQL comment.** `PostgreSQLComQueryPacket` (in
  `database/protocol/dialect/postgresql/.../simple/PostgreSQLComQueryPacket.java`, read via `gh api` at
  the same commit) runs `SQLHintUtils.extractHint(originSQL)` and then `removeHint`. `hintValueContext.isWriteRouteOnly()`
  forces the primary. This is the same idea as mini-gateway's `hinted` mode, used the other way round
  (the client marks *writes*, not shareable reads).

**Shareability / caching / coalescing: n/a.** COULD NOT FIND any result cache or request coalescing in
`proxy/backend`, `proxy/frontend` or `features/readwrite-splitting`.

**Fail open or closed:** closed on parse failure (error), closed on multiple statements (see P6). For
text that parses, the read/write decision fails **open** on functions: an unknown function inside
`SELECT` is treated as a read.

### 4. Documented limitations (the project's own words)

- `docs/document/content/features/sharding/limitation.en.md:11`: "When no schema is specified in SQL,
  default access to 'public' schema. Other schemas need to declare before the table name, and do not
  support 'SEARCH_PATH' to modify the schema search path."
- same file, lines 178-180: "### Semicolons separate multiple statements … Not support simultaneous
  execution of multiple SQL statements separated by `;`."
- `docs/document/content/reference/db-compatibility/_index.en.md:12`: "Due to the complexity of SQL
  syntax, a few SQL are not supported for now." Line 22: "native SQL will not deliver the features
  provided uniformly to the database, and a message will be displayed indicating that the operation is
  not supported."
- `docs/document/content/features/transaction/limitations.en.md:15-16`: "When ShardingSphere-Proxy uses
  PostgreSQL or openGauss, metadata-changing DDL statements such as `CREATE`, `ALTER`, and `DROP` are not
  supported in transactions. Such statements are rejected before execution with SQLSTATE `0A000` …"
- `docs/document/content/user-manual/shardingsphere-proxy/session/_index.en.md`: session management "is
  only available when the storage node is MySQL".
- `docs/document/content/user-manual/shardingsphere-jdbc/unsupported/_index.en.md` (JDBC driver, not the
  proxy): "Do not support operations of stored procedure, function and cursor", "Do not support
  savepoint related operations", "Do not support Schema/Catalog operation".
- **No document says that `SET` of other parameters is discarded, or that session state persists on
  pooled backend connections.** COULD NOT FIND either.

### 5. Language, licence, maturity, dependency weight

Java (JDK 8+ build), Apache-2.0, an Apache top-level project. Newest release 5.5.3 (2026-02-28, GitHub
API). Latest commit 2026-11-24, so it is very active. From memory: started as Sharding-JDBC around 2016;
not verified here. Reuse from a Go proxy would mean either (i) running the ANTLR grammar through
ANTLR's Go target, which needs the 8,042-line grammar *and* a rewrite of the 5,343-line Java visitor plus
the statement model (the `PostgreSQLLexerBase` superclass and the grammar's inline Java predicates, e.g.
`{$NUMBER_.text.matches("[0-9]+") }?` at `BaseRule.g4:26`, would also need porting), or (ii) running a
JVM sidecar. Neither is a library dependency in the usual sense.

### Probe table

"Proxy" means ShardingSphere-Proxy with a PostgreSQL frontend and one PostgreSQL data source. "R/W" means
the same with a readwrite-splitting rule. All rows were worked out by reading code; nothing was run.

| Probe | What ShardingSphere does | Code path |
| --- | --- | --- |
| P1 `SET search_path = x` | Parsed → `SetStatement` → admin executor. **Never sent to PostgreSQL.** Value discarded (no PG replay provider). Client gets `SET` + `ParameterStatus(search_path,x)`. No leak, but the client is silently misinformed. Docs: `search_path` unsupported | `PostgreSQLAdminExecutorCreator`; `PostgreSQLSetVariableAdminExecutor.java:42-48`; `SessionVariableRecordExecutor.java:47-54`; `PostgreSQLComQueryExecutor.java:128-141` |
| P2 `SELECT set_config('search_path','h',false)` | Parsed → `SelectStatement`, not a system-table query → forwarded. Runs on a pooled connection that goes back to the shared pool after the command with no reset → **the next proxy session that gets that connection runs with `search_path = h`**. R/W: routed to a replica (it is a SELECT with no lock) and leaks there | `PostgreSQLSelectAdminExecutorFactory.newInstance` (only system/statistics tables intercepted); `CommandExecutorTask.finish` → `closeExecutionResources` → `closeConnections`; `QualifiedReadwriteSplittingPrimaryDataSourceRouter.java:44-48` |
| P3 `SELECT f()` (f runs SET / creates temp table) | Same as P2: forwarded, leaks across clients through the pool. The tree cannot know what `f` does | as P2 |
| P4 lexical tricks | The lexer models `E''` escapes, continuation (`Literals.g4:23,37`), dollar quotes with tag matching (`ModeLexer.g4`), nested comments (`Comments.g4:22`), so the tree probably matches PostgreSQL. But the tree is not what protects session state: P2 is not caught however it is spelled. One residual divergence: plain strings assume `standard_conforming_strings = on`, and a client `SET standard_conforming_strings = off` is swallowed (P1 path), so the backend stays `on` and the two agree by accident | as cited |
| P5 `PREPARE p AS …` | Parsed (`DDLStatement.g4:1939`), forwarded, the prepared statement stays on the pooled connection. A later `EXECUTE p` may land on another connection (error); another client may hit "already exists" | no special handling found (grep, §2) |
| P5 `LISTEN c` | Parsed (`DDLStatement.g4:1910`), forwarded. The registration stays on the pooled connection; COULD NOT FIND any path that relays `NotificationResponse` to the client | as above |
| P5 `CREATE TEMP TABLE t(…)` | Outside a transaction: forwarded, and the temp table stays on the pooled connection, visible to the next session. Inside a transaction: refused with `0A000` (metadata-changing DDL) | `ProxySQLExecutor.java:144-157`; `transaction/limitations.en.md:15-16` |
| P5 `SELECT pg_advisory_lock(1)` | SELECT → forwarded (R/W: to a replica). The lock stays held by the pooled connection after the client is done | as P2 |
| P5 `DECLARE c CURSOR WITH HOLD …` | Parsed (`declare`), treated as a cursor DDL that is allowed in transactions (`isDDLWithoutMetaDataChanged`). The holdable cursor stays on the connection after commit | `ProxySQLExecutor.java:171-177` |
| P6 `SELECT 1; SET search_path = x` | The top rule `execute` accepts one statement then `SEMI_? EOF` → parse error → `SQLParsingException` to the client (**fails closed**). The packet is not split (`PostgreSQLComQueryPacket` reads one string). Documented as unsupported | `PostgreSQLStatementParser.g4:211`; `SQLParserExecutor.java:48-73`; `sharding/limitation.en.md:180` |
| P7 `SELECT nextval('s')` | R/W: SELECT, no lock → **replica** → fails on a hot standby. Plain proxy: n/a | `QualifiedReadwriteSplittingPrimaryDataSourceRouter.java:44-48` |
| P7 `WITH d AS (DELETE …) SELECT …` | R/W: the CTE holds a `dataModification` → **primary** (correct) | `PostgreSQLStatementVisitor.java:1668-1676`; `SQLStatementContextExtractor.getDataModificationBaseContexts` (infra/binder, via `gh api`, lines 86-99) |
| P7 `SELECT * FROM t FOR UPDATE` | `LockSegment` → **primary** (correct) | `PostgreSQLStatementVisitor.java:1707`; router line 50-52 |
| P7 `SELECT now()` | R/W: replica. Harmless for routing. No cache exists, so "shareable" is n/a | router |
| P7 `SELECT * FROM v` (view over a volatile function) | R/W: replica. If the function writes, it fails on the standby; otherwise a read | router |
| P7 `EXPLAIN ANALYZE DELETE FROM t` | `ExplainStatement`, not a `SelectStatement` → **primary** (correct, by the allow-list's direction) | `PostgreSQLDALStatementVisitor.java:158-170`; router `!isSelectStatement` |

## Claims of completeness

- COULD NOT FIND any claim that ShardingSphere decides session safety from SQL text. The docs claim the
  opposite in general terms ("a few SQL are not supported", `db-compatibility/_index.en.md:12`) and say
  nothing about session state on pooled connections.
- The design does imply one claim: once a statement parses, its class decides whether it may go to a
  replica. The code shows this holds only at the statement-class level. Function calls inside a
  `SELECT` are invisible to it (P2, P3, P7 `nextval`), exactly the grammar-layer hole mini-gateway hit.
- The full grammar buys **completeness of the lexical layer and of statement boundaries** (P4, P6: it
  knows where strings end, and it refuses more than one statement). It does **not** buy completeness for
  D3: session changes through functions, temp tables, `PREPARE`, `LISTEN`, advisory locks and holdable
  cursors are parsed correctly and then forwarded with no check, onto connections shared across
  clients. For the one form it does intercept (`SET`), it gets the semantics wrong (it discards the
  value and tells the client it worked). For D4, it uses the same class-level allow-list as option (b)
  and the same blind spot for functions. D5 is n/a. **By its own evidence, an 8,000-line grammar, a
  5,000-line visitor and continuous maintenance (at least 32 grammar commits in two years, 69 parse
  issues) do not give completeness for D3/D4/D5. The project does not even attempt D3 for PostgreSQL.**

## Mapping to options (a) (b) (c1) (c2)

- Closest to **(c1)** in kind: a real, full parser. But it is a derived grammar, not PostgreSQL's own
  parser, so it carries (c1)'s cost without (c1)'s fidelity. It shows the brief's point about (c1): the
  tree fixes the lexical layer but not the grammar layer, because "does this function change session
  state" is not in the text.
- For D3 it is **none of the options**. It is closest to a degenerate (a): a deny-list of one statement class
  (`SET`/`RESET`), with no mechanism behind it, applied to connections shared across clients. It has
  none of (b)'s reset on release for PostgreSQL. The MySQL replay path is a *track-and-replay*
  mechanism, which the assessment does not list (see below).
- For D4 (read/write routing) it matches **(b)'s allow-list by first keyword / statement class**, with
  the same blind spot for functions. It does not use **(c2)**: it never wraps reads in `BEGIN READ ONLY`.
  A replica gives (c2)'s server-side enforcement by accident, because a hot standby refuses writes.

## Mechanisms not in the assessment

1. **Track-and-replay of session variables (the MySQL path).** VERIFIED IN CODE in
   `RequiredSessionVariableRecorder` and `ProxyDatabaseConnectionManager`. The proxy keeps an allow-list of
   variables that matter (`ReplayedSessionVariableProvider.isNeedToReplay`). It stores the client's values
   in its own session object and replays them as `SET k=v` on every backend connection it borrows
   (`setSessionVariablesIfNecessary`; `markSessionVariablesDirty` queues already-held connections for a
   replay). Before releasing the connections it sends a blanket reset (`RESET ALL` for PostgreSQL,
   `SET k=DEFAULT` for MySQL). For a Go proxy in front of vanilla Postgres this would mean: allow
   `SET` of a small set of parameters, record them per client, prefix each lease with the `SET`s (or one
   `SET … ; SET …` in the same simple Query), and send `RESET ALL` or `DISCARD ALL` on release. It turns
   "reject `SET`" into "support `SET` safely" for a named list. It still needs the reset for anything
   not on the list, so it complements (b) rather than replacing it. Caveat: it is only as safe as the
   detection of `SET` (it does nothing about `set_config`), which is why it still needs the reset.
2. **Answer session-introspection statements locally.** ShardingSphere answers `SHOW` itself from a
   table (`PostgreSQLShowVariableExecutor`) and sends the matching `ParameterStatus` messages to the
   client after `SET` (`PostgreSQLComQueryExecutor.createParameterStatusResponse`). A Go proxy that
   tracks a few parameters could answer `SHOW <tracked>` without a backend round trip, and must send
   `ParameterStatus` itself if it ever swallows a `SET`. ShardingSphere is an example of getting that
   wrong (it reports values it never applied).
3. **Client hint inside a comment, stripped before forwarding** (`SQLHintUtils.extractHint`/`removeHint`
   in `PostgreSQLComQueryPacket`). The assessment already has `hinted` mode. What ShardingSphere adds is
   only that it strips the hint from the forwarded text, which keeps the backend's statement statistics
   and logs clean. Minor.
4. **Per-dialect "statements refused in this proxy state" SPI** (`DialectProxyStateSupportedSQLProvider`,
   only MySQL implements it). This is an allow/deny-by-class table keyed on proxy state (for example,
   inside a failed transaction only `COMMIT`/`ROLLBACK` are allowed,
   `ProxyBackendHandlerFactory.java:152-158`). Nothing new for mini-gateway, because Postgres already
   enforces the failed-transaction rule.

Nothing else new: ShardingSphere does not use protocol signals (e.g. `ParameterStatus` from the backend,
which PostgreSQL sends when a `GUC_REPORT` setting changes) to detect session changes, and it does not tag
connections as dirty based on what the server reports.

## Could not verify

- A header or comment saying the grammar is derived from `gram.y`, or which PostgreSQL version it
  tracks: COULD NOT FIND. The derivation is an inference from rule names.
- Whether HikariCP resets any PostgreSQL session state when a connection goes back to the pool: COULD
  NOT FIND in this clone (HikariCP is not in the tree). From memory it does not. Not verified.
- Whether the proxy ever relays `NotificationResponse` (LISTEN/NOTIFY) to clients: COULD NOT FIND a path.
- The internals of how protocol-level prepared statements are re-prepared on borrowed connections: classes
  located, logic not read.
- Whether `CREATE TEMP TABLE` outside a transaction triggers a metadata refresh that registers the temp
  table as a logical table: not read.
- Commit counts before mid-2025 under even older grammar paths: only the two paths named above were
  counted, so the two-year total is a lower bound.
- First release date: from memory only (Sharding-JDBC around 2016). Not verified.
- A better candidate project than ShardingSphere for this question: none met in this cluster.
