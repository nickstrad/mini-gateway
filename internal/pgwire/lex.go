package pgwire

type TokenKind uint8

const (
	TokenSpace TokenKind = iota + 1
	TokenLineComment
	TokenBlockComment
	TokenWord
	TokenQuotedIdent
	TokenUnicodeIdent
	TokenString
	TokenDollarString
	TokenNumber
	TokenParam
	TokenOperator
	TokenPunct
	TokenBad
	TokenKindCount
)

var lexKindNames = [TokenKindCount]string{"?", "space", "line-comment", "block-comment", "word", "quoted-ident", "unicode-ident", "string", "dollar-string", "number", "param", "operator", "punct", "bad"}

func (k TokenKind) String() string {
	if k < TokenKindCount {
		return lexKindNames[k]
	}
	return "?"
}

func (k TokenKind) Significant() bool {
	return k != TokenSpace && k != TokenLineComment && k != TokenBlockComment
}

type Token struct {
	Kind  TokenKind
	Start int
	End   int
}

func (t Token) Text(src []byte) []byte { return src[t.Start:t.End] }

type Lexer struct {
	src []byte
	pos int
}

func LexerFor(src []byte) Lexer { return Lexer{src: src} }

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

func (l *Lexer) scan() TokenKind {
	c := l.src[l.pos]
	switch {
	case lexIsSpace(c):
		for l.more(0) && lexIsSpace(l.src[l.pos]) {
			l.pos++
		}
		return TokenSpace
	case c == '-' && l.peek(1) == '-':
		for l.more(0) && l.src[l.pos] != '\n' && l.src[l.pos] != '\r' {
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
			return kind
		default:
			l.pos++
		}
	}
	return TokenBad
}

func (l *Lexer) number() {
	if l.src[l.pos] == '0' && lexIsRadix(l.peek(1)) {
		l.pos += 2
		for l.more(0) && (lexIsHexDigit(l.src[l.pos]) || l.src[l.pos] == '_') {
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
		l.pos++
	}
}

func (l *Lexer) dollar() TokenKind {
	if lexIsDigit(l.peek(1)) {
		l.pos++
		for l.more(0) && lexIsDigit(l.src[l.pos]) {
			l.pos++
		}
		return TokenParam
	}
	end := l.pos + 1
	if end < len(l.src) && lexIsIdentStart(l.src[end]) {
		for end < len(l.src) && lexIsIdentCont(l.src[end]) && l.src[end] != '$' {
			end++
		}
	}
	if end >= len(l.src) || l.src[end] != '$' {
		l.pos++
		return TokenBad
	}
	tag := l.src[l.pos : end+1]
	body := end + 1
	for i := body; i+len(tag) <= len(l.src); i++ {
		if l.src[i] == '$' && lexHasPrefix(l.src[i:], tag) {
			l.pos = i + len(tag)
			return TokenDollarString
		}
	}
	l.pos = len(l.src)
	return TokenBad
}

func lexHasPrefix(b, p []byte) bool {
	if len(b) < len(p) {
		return false
	}
	for i := range p {
		if b[i] != p[i] {
			return false
		}
	}
	return true
}

func (l *Lexer) operator() {
	for l.more(0) && lexIsOpChar(l.src[l.pos]) {
		c := l.src[l.pos]
		if (c == '-' && l.peek(1) == '-') || (c == '/' && l.peek(1) == '*') {
			return
		}
		l.pos++
	}
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
