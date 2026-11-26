package pgwire

import "github.com/nickstrad/mini-gateway/internal/work"

// RejectRule documents one courtesy reject-list entry and supplies the example
// classified by TestRejectRulesExamplesClassifyAsDocumented.
type RejectRule struct {
	Reason  work.RejectReason
	Feature string
	Why     string
	Example string
}

// RejectRules is the table from which docs/reject-list.md is generated.
// Session safety rests on the post-lease reset (PLAN.md D8), not on this list.
var RejectRules = []RejectRule{
	{work.RejectSessionSet, "Session `SET` (every form except `SET LOCAL`, `SET TRANSACTION`, `SET CONSTRAINTS`; includes `SET SESSION ...`, `SET SESSION CHARACTERISTICS AS TRANSACTION ...`, `SET ROLE`, `SET SESSION AUTHORIZATION`, `SET TIME ZONE`, `SET NAMES`, `SET SCHEMA`, a custom setting whose first part is `local`, `transaction` or `constraints` such as `SET local.x = 1`, and `SET LOCAL NAMES`); `UPDATE pg_settings ...` in any statement that names `pg_settings` and contains `UPDATE`; and any statement containing `SET` or `RESET` that names `standard_conforming_strings` or `client_encoding`, `SET LOCAL` included, because both change how Postgres lexes the statements that follow", "The setting outlives the transaction and would follow the pooled connection to the next client.", "SET search_path = app"},
	{work.RejectReset, "`RESET name`, `RESET ALL`", "Changes session settings that outlive the transaction.", "RESET statement_timeout"},
	{work.RejectPrepare, "`PREPARE name AS ...`", "A prepared statement lives on the connection, not in the transaction.", "PREPARE q AS SELECT 1"},
	{work.RejectExecute, "`EXECUTE name` (also after `EXPLAIN` and in `CREATE TABLE ... AS EXECUTE`)", "Needs a prepared statement from an earlier exchange, which may be on another connection.", "EXECUTE q"},
	{work.RejectDeallocate, "`DEALLOCATE`", "Removes connection-level prepared statements.", "DEALLOCATE ALL"},
	{work.RejectListen, "`LISTEN`", "A subscription lives on the connection; notifications would reach whichever client holds it next.", "LISTEN jobs"},
	{work.RejectUnlisten, "`UNLISTEN`", "Same as `LISTEN`.", "UNLISTEN *"},
	{work.RejectCursorWithHold, "`DECLARE ... WITH HOLD`", "A holdable cursor survives the commit and stays on the connection.", "DECLARE c CURSOR WITH HOLD FOR SELECT 1"},
	{work.RejectTempObject, "Temporary objects: `CREATE [GLOBAL|LOCAL] TEMP|TEMPORARY TABLE/VIEW/SEQUENCE`, `SELECT ... INTO TEMP`, `CREATE OR REPLACE TEMP VIEW`, any reference to the `pg_temp` schema, and a transaction-local `search_path` that may name it: `SET LOCAL search_path`/`SET LOCAL SCHEMA` with a string value that contains `pg_temp` (any case) or that the classifier cannot read (an `E'...'` string with a backslash, a `U&'...'` string with an escape), and `set_config('search_path', value, true)` whose value is not one plain string literal free of `pg_temp`", "Temporary objects belong to the session and would be visible to, or missing for, the next client.", "CREATE TEMP TABLE t (a int)"},
	{work.RejectAdvisoryLock, "Session advisory-lock functions: `pg_advisory_lock`, `pg_advisory_lock_shared`, `pg_try_advisory_lock`, `pg_try_advisory_lock_shared`, `pg_advisory_unlock`, `pg_advisory_unlock_shared`, `pg_advisory_unlock_all` (any schema, quoted or not)", "A session lock is held until released or the session ends, not until the transaction ends.", "SELECT pg_advisory_lock(42)"},
	{work.RejectSetConfig, "`set_config(name, value, is_local)` whose third argument is not the bare literal `true`, or whose first argument is not a plain string literal, or names `standard_conforming_strings` or `client_encoding`", "With `false` (or anything that might be `false`) the change is session-level; a setting name the classifier cannot read might be one that changes how later statements are lexed.", "SELECT set_config('search_path', 'app', false)"},
	{work.RejectDiscard, "`DISCARD ALL|PLANS|SEQUENCES|TEMP`", "Resets session state that other clients' work may rely on; the proxy owns connection hygiene.", "DISCARD ALL"},
	{work.RejectLoad, "`LOAD 'library'`", "Loads a shared library into the backend process for the rest of the session.", "LOAD 'auto_explain'"},
	{work.RejectCopy, "`COPY` in any direction", "The copy sub-protocol is not relayed by the proxy.", "COPY kv TO STDOUT"},
	{work.RejectTwoPhase, "`PREPARE TRANSACTION`, `COMMIT PREPARED`, `ROLLBACK PREPARED`", "Two-phase commit leaves a prepared transaction outside the session that the proxy cannot track.", "PREPARE TRANSACTION 'tx1'"},
	{work.RejectExtendedProtocol, "Every extended-protocol message (`Parse`, `Bind`, `Describe`, `Execute`, `Close`, `Flush`, `Sync`) and `FunctionCall`", "v1 speaks the simple-query subset only; messages are discarded until `Sync`.", "(a Parse message)"},
}

// RejectAllowedForm documents a transaction-scoped SQL form that the courtesy
// reject list deliberately forwards.
type RejectAllowedForm struct {
	Form    string
	Why     string
	Example string
}

// RejectAllowedForms lists the documented transaction-scoped forms rendered in
// docs/reject-list.md and tested against ClassifyQuery.
var RejectAllowedForms = []RejectAllowedForm{
	{"`SET LOCAL ...`", "Lasts until the end of the current transaction.", "SET LOCAL statement_timeout = '5s'"},
	{"`SET TRANSACTION ...`", "Applies to the current transaction only.", "SET TRANSACTION ISOLATION LEVEL SERIALIZABLE"},
	{"`SET CONSTRAINTS ...`", "Applies to the current transaction only.", "SET CONSTRAINTS ALL DEFERRED"},
	{"`set_config('name', value, true)`", "The bare literal `true` makes the change transaction-local; the name must be a plain string literal.", "SELECT set_config('search_path', 'app', true)"},
	{"Transaction-level advisory locks: `pg_advisory_xact_lock`, `pg_advisory_xact_lock_shared`, `pg_try_advisory_xact_lock`, `pg_try_advisory_xact_lock_shared`", "Released at the end of the transaction.", "SELECT pg_advisory_xact_lock(42)"},
	{"`DECLARE ... CURSOR` without `WITH HOLD`", "Closed at the end of the transaction.", "DECLARE c CURSOR FOR SELECT 1"},
	{"`NOTIFY` and `pg_notify(...)`", "Delivered at commit; harmless without `LISTEN`, which is rejected. Never fits the safe shape.", "NOTIFY jobs, 'x'"},
	{"`LOCK TABLE`", "Released at the end of the transaction.", "LOCK TABLE kv IN SHARE MODE"},
}

// Session-scoped advisory-lock functions checked by tokenReject.
var rejSessionAdvisory = []string{
	"pg_advisory_lock", "pg_advisory_lock_shared", "pg_try_advisory_lock", "pg_try_advisory_lock_shared",
	"pg_advisory_unlock", "pg_advisory_unlock_shared", "pg_advisory_unlock_all",
}

// Settings that alter PostgreSQL's own lexing of subsequent statements even
// when changed with SET LOCAL inside a transaction.
var rejLexingSettings = []string{"standard_conforming_strings", "client_encoding"}

const (
	rejSearchPath = "search_path"
	rejTempSchema = "pg_temp"
)
