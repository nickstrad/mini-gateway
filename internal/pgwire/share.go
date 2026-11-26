package pgwire

// Bounds and markers for the safe-shape recogniser.
//
// This file intentionally imports nothing and references no identifier from
// lex.go, classify.go, or rejectlist.go (enforced structurally by an AST test)
// so a lexer bug can never widen SafeRead or Shareable.
const (
	// ShareMaxLen caps the raw byte length of a Query that can fit the safe shape.
	ShareMaxLen = 4096
	// ShareHint is the exact leading byte prefix reported as Unit.Hinted.
	// Because it contains '/' and '*', a hinted text never fits the safe shape.
	ShareHint = "/* mg:share */"
	// ShareLead is the sole allowed leading keyword of a safe-shape Query.
	ShareLead = "select"
)

// Punctuation permitted in the safe-shape alphabet; dots, semicolons, comments,
// quotes other than single quote, backslashes, and dollar signs are excluded so
// multi-statement text, attribute-notation calls (t.f), and complex quoting
// rules cannot appear.
const shareAlphabetPunct = "_,*=<>()' "

// ShareStopWords lists words forbidden anywhere outside strings in a safe-shape
// query: clauses that write or lock, non-SELECT statement leads, and the
// paren-free per-call/per-session SQL-value functions ported from PostgreSQL's
// gram.y func_expr_common_subexpr production.
var ShareStopWords = []string{
	"into", "for", "with", "tablesample",
	"insert", "update", "delete", "merge", "truncate", "create", "alter", "drop", "grant",
	"revoke", "call", "do", "copy", "lock", "set", "reset", "show", "prepare", "execute",
	"deallocate", "listen", "notify", "unlisten", "declare", "fetch", "move", "close", "begin",
	"start", "commit", "rollback", "abort", "savepoint", "release", "discard", "load", "vacuum",
	"analyze", "explain", "refresh", "reindex", "cluster", "checkpoint", "comment", "import",
	"security", "values", "table",
	"current_date", "current_time", "current_timestamp", "localtime", "localtimestamp",
	"current_role", "current_user", "session_user", "system_user", "user", "current_catalog",
	"current_schema",
}

// ShareParenKeywords lists SQL keywords that may precede '(' without being a
// function call. Every word here is a reserved (R) or column-name (C) keyword
// in PostgreSQL 17, so no unqualified function call can share its name;
// unreserved/type keywords such as by, join, like, and is are intentionally
// excluded.
var ShareParenKeywords = []string{
	"select", "from", "where", "and", "or", "not", "in", "on", "exists", "any", "all", "some",
	"when", "then", "else", "having", "union", "intersect", "except", "as", "limit", "offset",
	"distinct", "between", "case",
}

// ShareAllowList is the sorted list of built-in function names permitted before
// '(' in a safe-shape query. Functions with side effects, per-call or
// per-session results, locale/timezone formatting, or window/SRF syntax are
// excluded.
var ShareAllowList = []string{
	"abs", "ascii", "avg", "bit_and", "bit_length", "bit_or", "bool_and", "bool_or", "btrim",
	"cbrt", "ceil", "ceiling", "char_length", "character_length", "chr", "coalesce", "concat",
	"concat_ws", "count", "degrees", "div", "every", "exp", "floor", "gcd", "greatest",
	"initcap", "lcm", "least", "left", "length", "ln", "log", "log10", "lower", "lpad", "ltrim",
	"max", "md5", "min", "mod", "nullif", "octet_length", "pi", "pow", "power", "radians",
	"repeat", "replace", "reverse", "right", "round", "rpad", "rtrim", "sign", "split_part",
	"sqrt", "starts_with", "string_agg", "strpos", "substr", "sum", "to_hex", "translate",
	"trunc", "upper",
}

// ShareTimeWords lists PostgreSQL's relative-time input words. Any string
// literal containing one as an ASCII case-insensitive substring fails the safe
// shape so typed literals like date 'today' are never shared.
var ShareTimeWords = []string{"now", "today", "tomorrow", "yesterday"}

// ShareFits reports whether raw Query bytes fit the safe shape for SafeRead
// and automatic coalescing. It allocates nothing and scans at most ShareMaxLen
// bytes without invoking the SQL lexer or reject classifier.
func ShareFits(text []byte) bool { return shareFits(text, false) }

// shareFits implements ShareFits with a test-only switch that bypasses the
// alphabet check for the property canary; production callers always pass false.
func shareFits(text []byte, skipAlphabet bool) bool {
	if len(text) == 0 || len(text) > ShareMaxLen {
		return false
	}
	if !skipAlphabet && !shareAlphabetOnly(text) {
		return false
	}
	return shareStringsOK(text) && shareLeadsWithSelect(text) && shareWordsOK(text)
}

func shareLetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

func shareDigit(c byte) bool { return c >= '0' && c <= '9' }

func shareWordByte(c byte) bool { return shareLetter(c) || shareDigit(c) || c == '_' }

func shareAlphabetOnly(text []byte) bool {
	for _, c := range text {
		if !shareLetter(c) && !shareDigit(c) && !shareIsPunct(c) {
			return false
		}
	}
	return true
}

func shareIsPunct(c byte) bool {
	for i := 0; i < len(shareAlphabetPunct); i++ {
		if shareAlphabetPunct[i] == c {
			return true
		}
	}
	return false
}

func shareLower(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

func shareEqualFold(b []byte, w string) bool {
	if len(b) != len(w) {
		return false
	}
	for i := range b {
		if shareLower(b[i]) != w[i] {
			return false
		}
	}
	return true
}

func shareInList(list []string, b []byte) bool {
	for _, w := range list {
		if shareEqualFold(b, w) {
			return true
		}
	}
	return false
}

// shareStringsOK verifies balanced single quotes, rejects prefixed quotes such
// as E'...' or B'...', and checks each closed string span for relative-time
// words. Because none of ShareTimeWords contains a quote, scanning the raw
// span including any doubled ” is identical to scanning the unescaped text.
func shareStringsOK(text []byte) bool {
	in := false
	start := 0
	for i, c := range text {
		if c != '\'' {
			continue
		}
		if !in {
			if i > 0 && shareWordByte(text[i-1]) {
				return false
			}
			if i == 0 || text[i-1] != '\'' {
				start = i + 1
			}
		} else if (i+1 >= len(text) || text[i+1] != '\'') && shareMentionsTime(text[start:i]) {
			return false
		}
		in = !in
	}
	return !in
}

func shareMentionsTime(content []byte) bool {
	for _, w := range ShareTimeWords {
		if shareContainsFold(content, w) {
			return true
		}
	}
	return false
}

func shareContainsFold(b []byte, w string) bool {
	for j := 0; j+len(w) <= len(b); j++ {
		if shareEqualFold(b[j:j+len(w)], w) {
			return true
		}
	}
	return false
}

// shareLeadsWithSelect enforces the strict reading of rule 6: only spaces may
// precede the opening "select" word, so "(SELECT 1)" and "'a' SELECT" fail.
func shareLeadsWithSelect(text []byte) bool {
	i := 0
	for i < len(text) && text[i] == ' ' {
		i++
	}
	j := i
	for j < len(text) && shareWordByte(text[j]) {
		j++
	}
	return shareEqualFold(text[i:j], ShareLead)
}

func shareWordsOK(text []byte) bool {
	in := false
	for i := 0; i < len(text); {
		c := text[i]
		if c == '\'' {
			in = !in
			i++
			continue
		}
		if in || !shareWordByte(c) {
			i++
			continue
		}
		j := i
		for j < len(text) && shareWordByte(text[j]) {
			j++
		}
		if !shareWordOK(text[i:j], shareParenFollows(text, j)) {
			return false
		}
		i = j
	}
	return true
}

func shareParenFollows(text []byte, j int) bool {
	for j < len(text) && text[j] == ' ' {
		j++
	}
	return j < len(text) && text[j] == '('
}

func shareWordOK(w []byte, call bool) bool {
	if shareInList(ShareStopWords, w) {
		return false
	}
	return !call || shareInList(ShareParenKeywords, w) || shareInList(ShareAllowList, w)
}
