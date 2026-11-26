package pgwire

type clsFirstAllowEntry struct {
	Name     string
	Coalesce bool
}

var clsFirstAllowList = []clsFirstAllowEntry{
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

var clsFirstTemporalTypes = []string{"date", "time", "timetz", "timestamp", "timestamptz"}

var clsFirstRelativeTimeWords = []string{"now", "today", "tomorrow", "yesterday"}

var clsFirstPerCallKeywords = []string{
	"current_date", "current_time", "current_timestamp", "localtime", "localtimestamp",
	"current_user", "session_user", "user", "current_role", "current_schema", "current_catalog",
	"system_user", "tablesample",
}

var clsFirstWriteKeywords = []string{
	"insert", "update", "delete", "merge", "truncate", "create", "alter", "drop", "grant", "revoke",
	"comment", "call", "do", "copy", "lock", "vacuum", "analyze", "analyse", "reindex", "cluster",
	"refresh", "import", "security", "reassign", "notify", "listen", "unlisten", "set", "reset",
	"discard", "prepare", "execute", "deallocate", "declare", "begin", "start", "commit",
	"rollback", "abort", "savepoint", "release", "checkpoint", "load", "into", "operator",
	"nextval", "setval",
}

var clsFirstLockStrength = []string{"update", "share", "no", "key"}

var clsFirstNonCallWords = []string{
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

var clsFirstOperators = []string{
	"+", "-", "*", "/", "%", "^", "=", "<", ">", "<=", ">=", "<>", "!=", "||", "~", "~*", "!~",
	"!~*", "~~", "~~*", "!~~", "!~~*", "@>", "<@", "&&", "->", "->>", "#>", "#>>", "?", "?|", "?&",
	"@@", "@", "&", "|", "#", "<<", ">>", "=>",
}

var clsFirstModeledLeads = []string{
	"select", "values", "table", "with", "show", "explain", "insert", "update", "delete", "merge",
	"truncate", "create", "alter", "drop", "grant", "revoke", "comment", "analyze", "analyse",
	"vacuum", "reindex", "cluster", "refresh", "lock", "notify", "set", "declare", "fetch", "move",
	"close", "checkpoint",
}
