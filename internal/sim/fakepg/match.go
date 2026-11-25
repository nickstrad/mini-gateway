package fakepg

import (
	"strconv"
	"strings"
	"time"
)

// Kind classifies one statement recognized by Match.
type Kind uint8

// Statement kinds recognized by the minimal hand-written matcher.
const (
	KindEmpty Kind = iota
	KindConst
	KindDivZero
	KindTxid
	KindRead
	KindSleep
	KindRows
	KindUpdate
	KindInsert
	KindDelete
	KindSet
	KindReset
	KindShow
	KindDiscard
	KindBegin
	KindCommit
	KindRollback
	KindUnknown
	NumKinds
)

var kindNames = [NumKinds]string{"empty", "const", "div-zero", "txid", "read", "sleep", "rows", "update", "insert", "delete", "set", "reset", "show", "discard", "begin", "commit", "rollback", "unknown"}

// String returns the canonical lowercase name of k.
func (k Kind) String() string {
	if k < NumKinds {
		return kindNames[k]
	}
	return "kind-" + strconv.Itoa(int(k))
}

// IsWrite reports whether k is a table-mutating statement (UPDATE, INSERT, or DELETE).
func (k Kind) IsWrite() bool { return k == KindUpdate || k == KindInsert || k == KindDelete }

// Hard bounds enforced by Split and Match to keep parsing and reply generation bounded.
const (
	MaxStatements = 64
	MaxTokens     = 64
	MaxSleep      = time.Hour
	MaxRowCount   = 10_000_000
	MaxRowSize    = 1 << 20
)

// Stmt holds the classified kind and extracted literals of one SQL statement.
type Stmt struct {
	Kind  Kind
	Text  string
	Tag   string
	Key   string
	Value string
	Name  string
	Const string
	Word  string
	Rows  int64
	Size  int64
	Sleep time.Duration
}

type tokKind uint8

const (
	tWord tokKind = iota + 1
	tNum
	tStr
	tPunct
	tBad
)

type token struct {
	kind tokKind
	text string
}

// Split divides a simple-query string on semicolons outside single-quoted
// strings and /* */ comments, dropping empty or comment-only pieces and
// stopping once more than MaxStatements pieces have been seen.
func Split(q string) []string {
	var out []string
	start, inStr, inComment := 0, false, false
	for i := 0; i < len(q); i++ {
		c := q[i]
		switch {
		case inComment:
			if c == '*' && i+1 < len(q) && q[i+1] == '/' {
				inComment = false
				i++
			}
		case inStr:
			if c == '\'' {
				inStr = false
			}
		case c == '\'':
			inStr = true
		case c == '/' && i+1 < len(q) && q[i+1] == '*':
			inComment = true
			i++
		case c == ';':
			out = appendStmt(out, q[start:i])
			start = i + 1
		}
		if len(out) > MaxStatements {
			return out
		}
	}
	return appendStmt(out, q[start:])
}

func appendStmt(out []string, s string) []string {
	if t := strings.TrimSpace(s); t != "" && !onlyComments(t) {
		out = append(out, t)
	}
	return out
}

func onlyComments(s string) bool {
	for {
		s = strings.TrimSpace(s)
		if !strings.HasPrefix(s, "/*") {
			return s == ""
		}
		end := strings.Index(s[2:], "*/")
		if end < 0 {
			return false
		}
		s = s[end+4:]
	}
}

func leadingComment(s string) (string, string) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "/*") {
		return "", s
	}
	end := strings.Index(s[2:], "*/")
	if end < 0 {
		return "", s
	}
	return strings.TrimSpace(s[2 : 2+end]), strings.TrimSpace(s[end+4:])
}

func isWordByte(c byte) bool {
	return c == '_' || c == '.' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func tokenize(s string) []token {
	var out []token
	for i := 0; i < len(s) && len(out) <= MaxTokens; {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return append(out, token{kind: tBad, text: s[i:]})
			}
			i += end + 4
		case c == '\'':
			j := i + 1
			var b strings.Builder
			closed := false
			for j < len(s) {
				if s[j] == '\'' {
					if j+1 < len(s) && s[j+1] == '\'' {
						b.WriteByte('\'')
						j += 2
						continue
					}
					closed = true
					j++
					break
				}
				b.WriteByte(s[j])
				j++
			}
			if !closed {
				return append(out, token{kind: tBad, text: s[i:]})
			}
			out = append(out, token{kind: tStr, text: b.String()})
			i = j
		case c >= '0' && c <= '9':
			j := i
			for j < len(s) && ((s[j] >= '0' && s[j] <= '9') || s[j] == '.') {
				j++
			}
			out = append(out, token{kind: tNum, text: s[i:j]})
			i = j
		case isWordByte(c):
			j := i
			for j < len(s) && isWordByte(s[j]) {
				j++
			}
			out = append(out, token{kind: tWord, text: s[i:j]})
			i = j
		default:
			out = append(out, token{kind: tPunct, text: s[i : i+1]})
			i++
		}
	}
	return out
}

type pattern []string

func (p pattern) match(toks []token) ([]token, bool) {
	if len(p) != len(toks) {
		return nil, false
	}
	var caps []token
	for i, want := range p {
		t := toks[i]
		switch want {
		case "$str":
			if t.kind != tStr {
				return nil, false
			}
			caps = append(caps, t)
		case "$num":
			if t.kind != tNum {
				return nil, false
			}
			caps = append(caps, t)
		case "$word":
			if t.kind != tWord {
				return nil, false
			}
			caps = append(caps, t)
		case "$val":
			if t.kind != tWord && t.kind != tNum && t.kind != tStr {
				return nil, false
			}
			caps = append(caps, t)
		default:
			if !strings.EqualFold(t.text, want) || t.kind == tStr {
				return nil, false
			}
		}
	}
	return caps, true
}

func words(s string) pattern { return pattern(strings.Fields(s)) }

var (
	patConst     = words("select $val")
	patDivZero   = words("select $num / 0")
	patTxid      = words("select txid_current ( ) is not null")
	patRead      = words("select v from kv where k = $str")
	patReadSleep = words("select v , pg_sleep ( $num ) is not null from kv where k = $str")
	patSleep     = words("select pg_sleep ( $num )")
	patRows      = words("select repeat ( $str , $num ) from generate_series ( $num , $num )")
	patUpdate    = words("update kv set v = $str where k = $str")
	patInsert    = words("insert into kv values ( $str , $str )")
	patInsertCol = words("insert into kv ( k , v ) values ( $str , $str )")
	patDelete    = words("delete from kv where k = $str")
	patSetEq     = words("set $word = $val")
	patSetTo     = words("set $word to $val")
	patSetSessEq = words("set session $word = $val")
	patSetSessTo = words("set session $word to $val")
	patReset     = words("reset $word")
	patShow      = words("show $word")
	patDiscard   = words("discard all")
)

var txWords = []struct {
	first string
	kind  Kind
}{
	{"begin", KindBegin}, {"start", KindBegin}, {"commit", KindCommit}, {"end", KindCommit},
	{"rollback", KindRollback}, {"abort", KindRollback},
}

// Match classifies one trimmed statement against the supported minimal SQL
// subset, extracting any leading /* tag */ comment. Unrecognized or malformed
// input returns KindUnknown so execution emits ERROR 42601.
func Match(text string) Stmt {
	tag, rest := leadingComment(text)
	st := Stmt{Text: text, Tag: tag, Kind: KindUnknown}
	toks := tokenize(rest)
	if len(toks) == 0 {
		st.Kind = KindEmpty
		return st
	}
	st.Word = toks[0].text
	if len(toks) > MaxTokens {
		return st
	}
	for _, t := range toks {
		if t.kind == tBad {
			return st
		}
	}
	if k, ok := matchTx(toks); ok {
		st.Kind = k
		return st
	}
	matchBody(&st, toks)
	return st
}

func matchTx(toks []token) (Kind, bool) {
	for _, w := range txWords {
		if !strings.EqualFold(toks[0].text, w.first) || toks[0].kind != tWord {
			continue
		}
		rest := toks[1:]
		if w.first == "start" {
			if len(rest) != 1 || !strings.EqualFold(rest[0].text, "transaction") {
				return 0, false
			}
			return w.kind, true
		}
		if len(rest) == 0 {
			return w.kind, true
		}
		if len(rest) == 1 && rest[0].kind == tWord && (strings.EqualFold(rest[0].text, "transaction") || strings.EqualFold(rest[0].text, "work")) {
			return w.kind, true
		}
		return 0, false
	}
	return 0, false
}

func matchBody(st *Stmt, toks []token) {
	if _, ok := patDivZero.match(toks); ok {
		st.Kind = KindDivZero
		return
	}
	if c, ok := patConst.match(toks); ok {
		st.Kind, st.Const = KindConst, c[0].text
		if c[0].kind == tWord {
			st.Kind = KindUnknown
		}
		return
	}
	if _, ok := patTxid.match(toks); ok {
		st.Kind = KindTxid
		return
	}
	if c, ok := patRead.match(toks); ok {
		st.Kind, st.Key = KindRead, c[0].text
		return
	}
	if c, ok := patReadSleep.match(toks); ok {
		if d, ok := parseSeconds(c[0].text); ok {
			st.Kind, st.Key, st.Sleep, st.Const = KindRead, c[1].text, d, "sleep"
		}
		return
	}
	if c, ok := patSleep.match(toks); ok {
		if d, ok := parseSeconds(c[0].text); ok {
			st.Kind, st.Sleep = KindSleep, d
		}
		return
	}
	if c, ok := patRows.match(toks); ok {
		matchRows(st, c)
		return
	}
	if c, ok := patUpdate.match(toks); ok {
		st.Kind, st.Value, st.Key = KindUpdate, c[0].text, c[1].text
		return
	}
	for _, p := range []pattern{patInsert, patInsertCol} {
		if c, ok := p.match(toks); ok {
			st.Kind, st.Key, st.Value = KindInsert, c[0].text, c[1].text
			return
		}
	}
	if c, ok := patDelete.match(toks); ok {
		st.Kind, st.Key = KindDelete, c[0].text
		return
	}
	for _, p := range []pattern{patSetEq, patSetTo, patSetSessEq, patSetSessTo} {
		if c, ok := p.match(toks); ok {
			st.Kind, st.Name, st.Value = KindSet, strings.ToLower(c[0].text), c[1].text
			return
		}
	}
	if c, ok := patReset.match(toks); ok {
		st.Kind, st.Name = KindReset, strings.ToLower(c[0].text)
		return
	}
	if c, ok := patShow.match(toks); ok {
		st.Kind, st.Name = KindShow, strings.ToLower(c[0].text)
		return
	}
	if _, ok := patDiscard.match(toks); ok {
		st.Kind = KindDiscard
	}
}

func matchRows(st *Stmt, c []token) {
	n, err1 := strconv.ParseInt(c[1].text, 10, 64)
	lo, err2 := strconv.ParseInt(c[2].text, 10, 64)
	hi, err3 := strconv.ParseInt(c[3].text, 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || len(c[0].text) == 0 {
		return
	}
	// Check n before multiplying by unit so huge repeat counts cannot overflow int64
	// to a negative size and bypass MaxRowSize / MaxReplyBytes.
	unit := int64(len(c[0].text))
	if n < 0 || n > MaxRowSize/unit {
		return
	}
	size := n * unit
	count, ok := seriesLen(lo, hi)
	if !ok {
		return
	}
	st.Kind, st.Const, st.Size, st.Rows = KindRows, c[0].text, size, count
}

func seriesLen(lo, hi int64) (int64, bool) {
	if hi < lo {
		return 0, hi == lo-1
	}
	d := uint64(hi) - uint64(lo)
	if d >= MaxRowCount {
		return 0, false
	}
	return int64(d) + 1, true
}

func parseSeconds(s string) (time.Duration, bool) {
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" && frac == "" || strings.Contains(frac, ".") {
		return 0, false
	}
	var d int64
	if whole != "" {
		w, err := strconv.ParseInt(whole, 10, 64)
		if err != nil || w > int64(MaxSleep/time.Second) {
			return 0, false
		}
		d = w * int64(time.Second)
	}
	scale := int64(time.Second / 10)
	for i := 0; i < len(frac); i++ {
		c := frac[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		d += int64(c-'0') * scale
		scale /= 10
	}
	if time.Duration(d) > MaxSleep {
		return 0, false
	}
	return time.Duration(d), true
}
