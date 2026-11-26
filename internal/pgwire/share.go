package pgwire

const (
	ShareMaxLen = 4096
	ShareHint   = "/* mg:share */"
	ShareLead   = "select"
)

const shareAlphabetPunct = "_,*=<>()' "

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

var ShareParenKeywords = []string{
	"select", "from", "where", "and", "or", "not", "in", "on", "exists", "any", "all", "some",
	"when", "then", "else", "having", "union", "intersect", "except", "as", "limit", "offset",
	"distinct", "between", "case",
}

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

var ShareTimeWords = []string{"now", "today", "tomorrow", "yesterday"}

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
