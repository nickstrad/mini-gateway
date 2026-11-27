package pgwire

import "github.com/nickstrad/mini-gateway/internal/pgwire/clscount"

// TokenKind classifies a lexical span emitted by Lexer.
type TokenKind uint8

const (
	// TokenSpace is a run of ASCII whitespace characters.
	TokenSpace TokenKind = iota + 1
	// TokenLineComment is a "--" comment extending to the next newline or EOF.
	TokenLineComment
	// TokenBlockComment is a balanced nested "/* ... */" comment.
	TokenBlockComment
	// TokenWord is an unquoted SQL identifier or keyword.
	TokenWord
	// TokenQuotedIdent is a double-quoted identifier ("...").
	TokenQuotedIdent
	// TokenUnicodeIdent is a Unicode-escaped identifier (U&"...").
	TokenUnicodeIdent
	// TokenString is a single-quoted string literal ('...', E'...', N'...',
	// B'...', X'...', or U&'...'), including newline continuations.
	TokenString
	// TokenDollarString is a dollar-quoted string literal ($tag$...$tag$).
	TokenDollarString
	// TokenNumber is a numeric literal (decimal, scientific, or Postgres 16+ radix).
	TokenNumber
	// TokenParam is a positional parameter ($1, $2, ...).
	TokenParam
	// TokenOperator is a Postgres operator token after trailing-sign splitting.
	TokenOperator
	// TokenPunct is a single punctuation byte: ( ) [ ] , ; : .
	TokenPunct
	// TokenBad marks an unclosed literal/comment or unrecognized byte; its
	// presence sets Unit.Opaque on the classified message.
	TokenBad
	// TokenKindCount is the sentinel count of TokenKind values.
	TokenKindCount
)

var lexKindNames = [TokenKindCount]string{"?", "space", "line-comment", "block-comment", "word", "quoted-ident", "unicode-ident", "string", "dollar-string", "number", "param", "operator", "punct", "bad"}

// String returns the human-readable name for the token kind.
func (k TokenKind) String() string {
	if k < TokenKindCount {
		return lexKindNames[k]
	}
	return "?"
}

// Significant reports whether the token is meaningful SQL rather than
// whitespace or a comment.
func (k TokenKind) Significant() bool {
	return k != TokenSpace && k != TokenLineComment && k != TokenBlockComment
}

// Token is a half-open byte range [Start, End) in the source slice.
type Token struct {
	Kind  TokenKind
	Start int
	End   int
}

// Text returns the raw slice of src covered by this token.
func (t Token) Text(src []byte) []byte { return src[t.Start:t.End] }

// Lexer is a single-pass PostgreSQL SQL scanner used by the courtesy reject
// classifier. It assumes standard_conforming_strings = on and never panics on
// arbitrary bytes.
type Lexer struct {
	// reads stays first because clscount.Tally is zero-sized in default
	// builds, keeping every other field offset unchanged.
	reads clscount.Tally
	src   []byte
	pos   int
	run   lexOpRun
	// Per-call work counters read by linear-growth tests in place of wall-clock
	// timing: bytes scanned in operator runs, and look-ahead bytes checked for
	// string continuations or dollar-quote tags.
	opRunBytes int
	lookahead  int
}

// lexOpRun caches one scanned operator run so repeated trailing '+'/'-' cuts
// stay O(1) per operator instead of rescanning the suffix quadratically.
type lexOpRun struct {
	start    int
	end      int
	lastKeep int
	signFrom int
}

// LexerFor returns a Lexer positioned at the start of src.
func LexerFor(src []byte) Lexer { return Lexer{src: src} }

// LexAll scans src into contiguous tokens covering every byte and reports
// whether the scan was clean (no TokenBad encountered).
func LexAll(src []byte) ([]Token, bool) {
	l := LexerFor(src)
	out := make([]Token, 0, lexEstimate(len(src)))
	clean := true
	for {
		t, ok := l.Next()
		if !ok {
			return out, clean
		}
		if t.Kind == TokenBad {
			clean = false
		}
		out = append(out, t)
	}
}

func lexEstimate(n int) int {
	if n > 64 {
		return n/4 + 16
	}
	return n + 1
}

// Next returns the next token and true, or (Token{}, false) at EOF. It always
// advances pos by at least one byte so malformed input cannot stall the loop.
func (l *Lexer) Next() (Token, bool) {
	if l.pos >= len(l.src) {
		return Token{}, false
	}
	start := l.pos
	kind := l.scan()
	if l.pos <= start {
		l.pos = start + 1
		kind = TokenBad
	}
	return Token{Kind: kind, Start: start, End: l.pos}, true
}

func (l *Lexer) peek(off int) byte {
	if l.pos+off < len(l.src) {
		return l.src[l.pos+off]
	}
	return 0
}

func (l *Lexer) more(off int) bool { return l.pos+off < len(l.src) }

func lexIsSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

func lexIsIdentStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' || c >= 0x80
}

func lexIsIdentCont(c byte) bool {
	return lexIsIdentStart(c) || lexIsDigit(c) || c == '$'
}

func lexIsDigit(c byte) bool { return c >= '0' && c <= '9' }

func lexIsOpChar(c byte) bool {
	switch c {
	case '+', '-', '*', '/', '<', '>', '=', '~', '!', '@', '#', '%', '^', '&', '|', '`', '?':
		return true
	}
	return false
}

func lexIsPunct(c byte) bool {
	switch c {
	case '(', ')', '[', ']', ',', ';', ':', '.':
		return true
	}
	return false
}

// scan reads one token starting at l.pos. TestLexLoopsCountTheirSteps enforces
// that scan and every loop in lexer scanning code open with a clscount.On bump
// so no uncounted loop can hide from -tags clscount growth checks.
func (l *Lexer) scan() TokenKind {
	if clscount.On {
		l.reads.Bump()
	}
	c := l.src[l.pos]
	switch {
	case lexIsSpace(c):
		for l.more(0) && lexIsSpace(l.src[l.pos]) {
			if clscount.On {
				l.reads.Bump()
			}
			l.pos++
		}
		return TokenSpace
	case c == '-' && l.peek(1) == '-':
		for l.more(0) && l.src[l.pos] != '\n' && l.src[l.pos] != '\r' {
			if clscount.On {
				l.reads.Bump()
			}
			l.pos++
		}
		return TokenLineComment
	case c == '/' && l.peek(1) == '*':
		return l.blockComment()
	case c == '\'':
		l.pos++
		return l.quoted('\'', false, TokenString)
	case c == '"':
		l.pos++
		return l.quoted('"', false, TokenQuotedIdent)
	case (c == 'e' || c == 'E') && l.peek(1) == '\'':
		l.pos += 2
		return l.quoted('\'', true, TokenString)
	case (c == 'n' || c == 'N' || c == 'b' || c == 'B' || c == 'x' || c == 'X') && l.peek(1) == '\'':
		l.pos += 2
		return l.quoted('\'', false, TokenString)
	case (c == 'u' || c == 'U') && l.peek(1) == '&' && l.peek(2) == '\'':
		l.pos += 3
		return l.quoted('\'', false, TokenString)
	case (c == 'u' || c == 'U') && l.peek(1) == '&' && l.peek(2) == '"':
		l.pos += 3
		return l.quoted('"', false, TokenUnicodeIdent)
	case lexIsIdentStart(c):
		for l.more(0) && lexIsIdentCont(l.src[l.pos]) {
			if clscount.On {
				l.reads.Bump()
			}
			l.pos++
		}
		return TokenWord
	case lexIsDigit(c) || (c == '.' && lexIsDigit(l.peek(1))):
		l.number()
		return TokenNumber
	case c == '$':
		return l.dollar()
	case lexIsPunct(c):
		l.pos++
		return TokenPunct
	case lexIsOpChar(c):
		l.operator()
		return TokenOperator
	}
	l.pos++
	return TokenBad
}

func (l *Lexer) blockComment() TokenKind {
	l.pos += 2
	depth := 1
	for l.more(0) {
		if clscount.On {
			l.reads.Bump()
		}
		c := l.src[l.pos]
		switch {
		case c == '/' && l.peek(1) == '*':
			depth++
			l.pos += 2
		case c == '*' && l.peek(1) == '/':
			depth--
			l.pos += 2
			if depth == 0 {
				return TokenBlockComment
			}
		default:
			l.pos++
		}
	}
	return TokenBad
}

func (l *Lexer) quoted(q byte, backslash bool, kind TokenKind) TokenKind {
	for l.more(0) {
		if clscount.On {
			l.reads.Bump()
		}
		c := l.src[l.pos]
		switch {
		case backslash && c == '\\':
			if !l.more(1) {
				l.pos = len(l.src)
				return TokenBad
			}
			l.pos += 2
		case c == q:
			if l.peek(1) == q && l.more(1) {
				l.pos += 2
				continue
			}
			l.pos++
			if q == '\'' {
				next, ok := lexContinuation(l.src, l.pos, l.reads.Ref())
				l.lookahead += next - l.pos
				if ok {
					l.pos = next
					continue
				}
			}
			return kind
		default:
			l.pos++
		}
	}
	return TokenBad
}

// lexContinuation matches PostgreSQL's rule joining adjacent single-quoted
// string literals separated by whitespace and "--" comments containing at
// least one '\n' or '\r'. Block comments do not join strings. Escape mode
// (e.g. E'...') is preserved across the continuation. On failure it returns
// the stopping index so quoted can account for look-ahead bytes; reads records
// loop steps under -tags clscount.
func lexContinuation(src []byte, i int, reads clscount.Ref) (int, bool) {
	newline := false
	for i < len(src) {
		if clscount.On {
			reads.Bump()
		}
		c := src[i]
		switch {
		case c == '\n' || c == '\r':
			newline = true
			i++
		case lexIsSpace(c):
			i++
		case c == '-' && i+1 < len(src) && src[i+1] == '-':
			for i < len(src) && src[i] != '\n' && src[i] != '\r' {
				if clscount.On {
					reads.Bump()
				}
				i++
			}
		case c == '\'' && newline:
			return i + 1, true
		default:
			return i, false
		}
	}
	return i, false
}

func (l *Lexer) number() {
	if l.src[l.pos] == '0' && lexIsRadix(l.peek(1)) {
		l.pos += 2
		for l.more(0) && (lexIsHexDigit(l.src[l.pos]) || l.src[l.pos] == '_') {
			if clscount.On {
				l.reads.Bump()
			}
			l.pos++
		}
		return
	}
	l.digits()
	if l.more(0) && l.src[l.pos] == '.' && l.peek(1) != '.' {
		l.pos++
		l.digits()
	}
	if l.more(0) && (l.src[l.pos] == 'e' || l.src[l.pos] == 'E') {
		switch {
		case lexIsDigit(l.peek(1)):
			l.pos++
			l.digits()
		case (l.peek(1) == '+' || l.peek(1) == '-') && lexIsDigit(l.peek(2)):
			l.pos += 2
			l.digits()
		}
	}
}

func lexIsRadix(c byte) bool {
	return c == 'x' || c == 'X' || c == 'o' || c == 'O' || c == 'b' || c == 'B'
}

func lexIsHexDigit(c byte) bool {
	return lexIsDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func (l *Lexer) digits() {
	for l.more(0) && (lexIsDigit(l.src[l.pos]) || (l.src[l.pos] == '_' && lexIsDigit(l.peek(1)))) {
		if clscount.On {
			l.reads.Bump()
		}
		l.pos++
	}
}

func (l *Lexer) dollar() TokenKind {
	if lexIsDigit(l.peek(1)) {
		l.pos++
		for l.more(0) && lexIsDigit(l.src[l.pos]) {
			if clscount.On {
				l.reads.Bump()
			}
			l.pos++
		}
		return TokenParam
	}
	end := l.pos + 1
	if end < len(l.src) && lexIsIdentStart(l.src[end]) {
		for end < len(l.src) && lexIsIdentCont(l.src[end]) && l.src[end] != '$' {
			if clscount.On {
				l.reads.Bump()
			}
			end++
		}
	}
	l.lookahead += end - l.pos
	if end >= len(l.src) || l.src[end] != '$' {
		l.pos++
		return TokenBad
	}
	tag := l.src[l.pos : end+1]
	body := end + 1
	for i := body; i+len(tag) <= len(l.src); i++ {
		if clscount.On {
			l.reads.Bump()
		}
		if l.src[i] == '$' && lexHasPrefix(l.src[i:], tag, l.reads.Ref()) {
			l.pos = i + len(tag)
			return TokenDollarString
		}
	}
	l.pos = len(l.src)
	return TokenBad
}

// lexHasPrefix checks whether b begins with p, taking reads so dollar-quote
// body scans count prefix-comparison steps under -tags clscount.
func lexHasPrefix(b, p []byte, reads clscount.Ref) bool {
	if len(b) < len(p) {
		return false
	}
	for i := range p {
		if clscount.On {
			reads.Bump()
		}
		if b[i] != p[i] {
			return false
		}
	}
	return true
}

func (l *Lexer) operator() {
	if l.pos < l.run.start || l.pos >= l.run.end {
		l.run = l.scanOpRun()
		l.opRunBytes += l.run.end - l.run.start
	}
	l.pos = l.run.cut(l.pos)
}

// scanOpRun scans one maximal operator run (stopping before "--" or "/*") and
// records the last character that keeps trailing '+'/'-' (PostgreSQL's rule
// that splits "71+-3" into "+", "-" while keeping "@-" whole).
func (l *Lexer) scanOpRun() lexOpRun {
	r := lexOpRun{start: l.pos, end: l.pos, lastKeep: -1}
	for r.end < len(l.src) && lexIsOpChar(l.src[r.end]) {
		if clscount.On {
			l.reads.Bump()
		}
		c := l.src[r.end]
		if r.end+1 < len(l.src) && ((c == '-' && l.src[r.end+1] == '-') || (c == '/' && l.src[r.end+1] == '*')) {
			break
		}
		r.end++
	}
	for k := r.start; k < r.end-1; k++ {
		if clscount.On {
			l.reads.Bump()
		}
		if lexKeepsTrailingSign(l.src[k]) {
			r.lastKeep = k
		}
	}
	r.signFrom = r.end
	for r.signFrom > r.start && lexIsSign(l.src[r.signFrom-1]) {
		if clscount.On {
			l.reads.Bump()
		}
		r.signFrom--
	}
	return r
}

func (r lexOpRun) cut(from int) int {
	switch {
	case r.end-from < 2, r.signFrom == r.end, r.lastKeep >= from:
		return r.end
	}
	return max(r.signFrom, from+1)
}

func lexIsSign(c byte) bool { return c == '+' || c == '-' }

func lexKeepsTrailingSign(c byte) bool {
	switch c {
	case '~', '!', '@', '#', '^', '&', '|', '`', '?', '%':
		return true
	}
	return false
}

func lexUnquote(body []byte, q byte) []byte {
	out := make([]byte, 0, len(body))
	for i := 0; i < len(body); i++ {
		out = append(out, body[i])
		if body[i] == q && i+1 < len(body) && body[i+1] == q {
			i++
		}
	}
	return out
}

func lexHexValue(s []byte) (rune, bool) {
	var v rune
	for i := 0; i < len(s); i++ {
		c := s[i]
		var d byte
		switch {
		case lexIsDigit(c):
			d = c - '0'
		case c >= 'a' && c <= 'f':
			d = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			d = c - 'A' + 10
		default:
			return 0, false
		}
		v = v*16 + rune(d)
	}
	return v, true
}

func lexValidEscape(esc byte) bool {
	return !(lexIsHexDigit(esc) || esc == '+' || esc == '\'' || esc == '"' || lexIsSpace(esc) || esc >= 0x80)
}

func lexDecodeUnicode(s []byte, esc byte) ([]byte, bool) {
	if !lexValidEscape(esc) {
		return nil, false
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		if s[i] != esc {
			out = append(out, s[i])
			i++
			continue
		}
		var digits int
		switch {
		case i+1 < len(s) && s[i+1] == esc:
			out = append(out, esc)
			i += 2
			continue
		case i+1 < len(s) && s[i+1] == '+':
			digits, i = 6, i+2
		default:
			digits, i = 4, i+1
		}
		if i+digits > len(s) {
			return nil, false
		}
		r, ok := lexHexValue(s[i : i+digits])
		if !ok || r == 0 || r > 0x10FFFF || (r >= 0xD800 && r <= 0xDFFF) {
			return nil, false
		}
		out = lexAppendRune(out, r)
		i += digits
	}
	return out, true
}

func lexAppendRune(b []byte, r rune) []byte {
	switch {
	case r < 0x80:
		return append(b, byte(r))
	case r < 0x800:
		return append(b, 0xC0|byte(r>>6), 0x80|byte(r&0x3F))
	case r < 0x10000:
		return append(b, 0xE0|byte(r>>12), 0x80|byte(r>>6&0x3F), 0x80|byte(r&0x3F))
	}
	return append(b, 0xF0|byte(r>>18), 0x80|byte(r>>12&0x3F), 0x80|byte(r>>6&0x3F), 0x80|byte(r&0x3F))
}
