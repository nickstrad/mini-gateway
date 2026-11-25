package pgwire

import "github.com/nickstrad/mini-gateway/internal/work"

type RejectRule struct {
	Reason  work.RejectReason
	Feature string
	Why     string
	Example string
}

var RejectRules = []RejectRule{
	{work.RejectSessionSet, "Session `SET` (every form except `SET LOCAL`, `SET TRANSACTION`, `SET CONSTRAINTS`; includes `SET SESSION ...`, `SET SESSION CHARACTERISTICS AS TRANSACTION ...`, `SET ROLE`, `SET SESSION AUTHORIZATION`, `SET TIME ZONE`, `SET NAMES`, `SET SCHEMA`)", "The setting outlives the transaction and would follow the pooled connection to the next client.", "SET search_path = app"},
	{work.RejectReset, "`RESET name`, `RESET ALL`", "Changes session settings that outlive the transaction.", "RESET statement_timeout"},
	{work.RejectPrepare, "`PREPARE name AS ...`", "A prepared statement lives on the connection, not in the transaction.", "PREPARE q AS SELECT 1"},
	{work.RejectExecute, "`EXECUTE name` (also after `EXPLAIN` and in `CREATE TABLE ... AS EXECUTE`)", "Needs a prepared statement from an earlier exchange, which may be on another connection.", "EXECUTE q"},
	{work.RejectDeallocate, "`DEALLOCATE`", "Removes connection-level prepared statements.", "DEALLOCATE ALL"},
	{work.RejectListen, "`LISTEN`", "A subscription lives on the connection; notifications would reach whichever client holds it next.", "LISTEN jobs"},
	{work.RejectUnlisten, "`UNLISTEN`", "Same as `LISTEN`.", "UNLISTEN *"},
	{work.RejectCursorWithHold, "`DECLARE ... WITH HOLD`", "A holdable cursor survives the commit and stays on the connection.", "DECLARE c CURSOR WITH HOLD FOR SELECT 1"},
	{work.RejectTempObject, "Temporary objects: `CREATE [GLOBAL|LOCAL] TEMP|TEMPORARY TABLE/VIEW/SEQUENCE`, `SELECT ... INTO TEMP`, `CREATE OR REPLACE TEMP VIEW`, and any reference to the `pg_temp` schema", "Temporary objects belong to the session and would be visible to, or missing for, the next client.", "CREATE TEMP TABLE t (a int)"},
	{work.RejectAdvisoryLock, "Session advisory-lock functions: `pg_advisory_lock`, `pg_advisory_lock_shared`, `pg_try_advisory_lock`, `pg_try_advisory_lock_shared`, `pg_advisory_unlock`, `pg_advisory_unlock_shared`, `pg_advisory_unlock_all` (any schema, quoted or not)", "A session lock is held until released or the session ends, not until the transaction ends.", "SELECT pg_advisory_lock(42)"},
	{work.RejectSetConfig, "`set_config(name, value, is_local)` whose third argument is not the bare literal `true`", "With `false` (or anything that might be `false`) the change is session-level.", "SELECT set_config('search_path', 'app', false)"},
	{work.RejectDiscard, "`DISCARD ALL|PLANS|SEQUENCES|TEMP`", "Resets session state that other clients' work may rely on; the proxy owns connection hygiene.", "DISCARD ALL"},
	{work.RejectLoad, "`LOAD 'library'`", "Loads a shared library into the backend process for the rest of the session.", "LOAD 'auto_explain'"},
	{work.RejectCopy, "`COPY` in any direction", "The copy sub-protocol is not relayed by the proxy.", "COPY kv TO STDOUT"},
	{work.RejectTwoPhase, "`PREPARE TRANSACTION`, `COMMIT PREPARED`, `ROLLBACK PREPARED`", "Two-phase commit leaves a prepared transaction outside the session that the proxy cannot track.", "PREPARE TRANSACTION 'tx1'"},
	{work.RejectExtendedProtocol, "Every extended-protocol message (`Parse`, `Bind`, `Describe`, `Execute`, `Close`, `Flush`, `Sync`) and `FunctionCall`", "v1 speaks the simple-query subset only; messages are discarded until `Sync`.", "(a Parse message)"},
}

type RejectAllowedForm struct {
	Form    string
	Why     string
	Example string
}

var RejectAllowedForms = []RejectAllowedForm{
	{"`SET LOCAL ...`", "Lasts until the end of the current transaction.", "SET LOCAL statement_timeout = '5s'"},
	{"`SET TRANSACTION ...`", "Applies to the current transaction only.", "SET TRANSACTION ISOLATION LEVEL SERIALIZABLE"},
	{"`SET CONSTRAINTS ...`", "Applies to the current transaction only.", "SET CONSTRAINTS ALL DEFERRED"},
	{"`set_config(name, value, true)`", "The bare literal `true` makes the change transaction-local.", "SELECT set_config('search_path', 'app', true)"},
	{"Transaction-level advisory locks: `pg_advisory_xact_lock`, `pg_advisory_xact_lock_shared`, `pg_try_advisory_xact_lock`, `pg_try_advisory_xact_lock_shared`", "Released at the end of the transaction.", "SELECT pg_advisory_xact_lock(42)"},
	{"`DECLARE ... CURSOR` without `WITH HOLD`", "Closed at the end of the transaction.", "DECLARE c CURSOR FOR SELECT 1"},
	{"`NOTIFY` and `pg_notify(...)`", "Delivered at commit; harmless without `LISTEN`, which is rejected. Never read-only, never coalescable.", "NOTIFY jobs, 'x'"},
	{"`LOCK TABLE`", "Released at the end of the transaction.", "LOCK TABLE kv IN SHARE MODE"},
}

type AllowEntry struct {
	Name     string
	Coalesce bool
}

var AllowList = []AllowEntry{
	{"abs", true}, {"age", false}, {"array_agg", true}, {"array_append", true}, {"array_cat", true},
	{"array_length", true}, {"array_lower", true}, {"array_position", true}, {"array_positions", true},
	{"array_prepend", true}, {"array_remove", true}, {"array_replace", true}, {"array_to_string", true},
	{"array_upper", true}, {"ascii", true}, {"avg", true}, {"bit_and", true}, {"bit_length", true},
	{"bit_or", true}, {"bool_and", true}, {"bool_or", true}, {"btrim", true}, {"cardinality", true},
	{"cbrt", true}, {"ceil", true}, {"ceiling", true}, {"char_length", true}, {"character_length", true},
	{"chr", true}, {"clock_timestamp", false}, {"coalesce", true}, {"concat", true}, {"concat_ws", true},
	{"count", true}, {"cume_dist", true}, {"current_database", true}, {"current_schema", false},
	{"current_setting", false}, {"date_part", true}, {"date_trunc", true}, {"decode", true},
	{"degrees", true}, {"dense_rank", true}, {"div", true}, {"encode", true}, {"every", true},
	{"exp", true}, {"extract", true}, {"first_value", true}, {"floor", true}, {"format", true},
	{"gcd", true}, {"gen_random_uuid", false}, {"generate_series", true}, {"greatest", true},
	{"grouping", true}, {"initcap", true}, {"json_agg", true}, {"json_build_array", true},
	{"json_build_object", true}, {"json_typeof", true}, {"jsonb_agg", true},
	{"jsonb_build_array", true}, {"jsonb_build_object", true}, {"jsonb_extract_path", true},
	{"jsonb_extract_path_text", true}, {"jsonb_pretty", true}, {"jsonb_typeof", true}, {"lag", true},
	{"last_value", true}, {"lcm", true}, {"lead", true}, {"least", true}, {"left", true},
	{"length", true}, {"ln", true}, {"log", true}, {"log10", true}, {"lower", true}, {"lpad", true},
	{"ltrim", true}, {"make_date", true}, {"make_interval", true}, {"make_time", true}, {"max", true},
	{"md5", true}, {"min", true}, {"mod", true}, {"now", false}, {"nth_value", true}, {"ntile", true},
	{"nullif", true}, {"octet_length", true}, {"overlay", true}, {"percent_rank", true},
	{"pg_backend_pid", false}, {"pg_sleep", false}, {"pi", true}, {"position", true}, {"pow", true},
	{"power", true}, {"quote_ident", true}, {"quote_literal", true}, {"quote_nullable", true},
	{"radians", true}, {"random", false}, {"rank", true}, {"regexp_match", true},
	{"regexp_matches", true}, {"regexp_replace", true}, {"regexp_split_to_array", true},
	{"repeat", true}, {"replace", true}, {"reverse", true}, {"right", true}, {"round", true},
	{"row_number", true}, {"row_to_json", true}, {"rpad", true}, {"rtrim", true}, {"sign", true},
	{"split_part", true}, {"sqrt", true}, {"starts_with", true}, {"statement_timestamp", false},
	{"string_agg", true}, {"string_to_array", true}, {"strpos", true}, {"substr", true},
	{"substring", true}, {"sum", true}, {"timeofday", false}, {"to_char", true}, {"to_date", true},
	{"to_hex", true}, {"to_json", true}, {"to_jsonb", true}, {"to_number", true},
	{"to_timestamp", true}, {"transaction_timestamp", false}, {"translate", true}, {"trim", true},
	{"trunc", true}, {"unnest", true}, {"upper", true}, {"version", true}, {"width_bucket", true},
}

var rejSessionAdvisory = []string{
	"pg_advisory_lock", "pg_advisory_lock_shared", "pg_try_advisory_lock", "pg_try_advisory_lock_shared",
	"pg_advisory_unlock", "pg_advisory_unlock_shared", "pg_advisory_unlock_all",
}

var rejPerCallKeywords = []string{
	"current_date", "current_time", "current_timestamp", "localtime", "localtimestamp",
	"current_user", "session_user", "user", "current_role", "current_schema", "current_catalog",
	"system_user", "tablesample",
}

var rejWriteKeywords = []string{
	"insert", "update", "delete", "merge", "truncate", "create", "alter", "drop", "grant", "revoke",
	"comment", "call", "do", "copy", "lock", "vacuum", "analyze", "analyse", "reindex", "cluster",
	"refresh", "import", "security", "reassign", "notify", "listen", "unlisten", "set", "reset",
	"discard", "prepare", "execute", "deallocate", "declare", "begin", "start", "commit",
	"rollback", "abort", "savepoint", "release", "checkpoint", "load", "into", "operator",
	"nextval", "setval",
}

var rejLockStrength = []string{"update", "share", "no", "key"}

var rejNonCallWords = []string{
	"all", "analyse", "analyze", "and", "any", "array", "as", "asc", "asymmetric", "both", "case",
	"cast", "check", "collate", "column", "constraint", "create", "current_catalog", "current_date",
	"current_role", "current_time", "current_timestamp", "current_user", "default", "deferrable",
	"desc", "distinct", "do", "else", "end", "except", "false", "fetch", "for", "foreign", "from",
	"grant", "group", "having", "in", "initially", "intersect", "into", "lateral", "leading", "limit",
	"localtime", "localtimestamp", "not", "null", "offset", "on", "only", "or", "order", "placing",
	"primary", "references", "returning", "select", "session_user", "some", "symmetric",
	"system_user", "table", "then", "to", "trailing", "true", "union", "unique", "user", "using",
	"variadic", "when", "where", "window", "with",
	"exists", "values", "row", "between", "setof", "inout", "out", "none",
	"bigint", "bit", "boolean", "char", "character", "dec", "decimal", "float", "int", "integer",
	"interval", "national", "nchar", "numeric", "precision", "real", "smallint", "time",
	"timestamp", "varchar",
}

var rejOperators = []string{
	"+", "-", "*", "/", "%", "^", "=", "<", ">", "<=", ">=", "<>", "!=", "||", "~", "~*", "!~",
	"!~*", "~~", "~~*", "!~~", "!~~*", "@>", "<@", "&&", "->", "->>", "#>", "#>>", "?", "?|", "?&",
	"@@", "@", "&", "|", "#", "<<", ">>", "=>",
}

var rejModeledLeads = []string{
	"select", "values", "table", "with", "show", "explain", "insert", "update", "delete", "merge",
	"truncate", "create", "alter", "drop", "grant", "revoke", "comment", "analyze", "analyse",
	"vacuum", "reindex", "cluster", "refresh", "lock", "notify", "set", "declare", "fetch", "move",
	"close", "checkpoint",
}

func AllowLookup(name string) (AllowEntry, bool) { return AllowLookupBytes([]byte(name)) }

func AllowLookupBytes(name []byte) (AllowEntry, bool) {
	lo, hi := 0, len(AllowList)
	for lo < hi {
		mid := (lo + hi) / 2
		switch {
		case AllowList[mid].Name == string(name):
			return AllowList[mid], true
		case AllowList[mid].Name < string(name):
			lo = mid + 1
		default:
			hi = mid
		}
	}
	return AllowEntry{}, false
}
