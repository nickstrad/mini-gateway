package pgwire

import (
	"bytes"
	"errors"

	"github.com/nickstrad/mini-gateway/internal/work"
)

const (
	ClassifMemoryFactor   = 80
	ClassifMemoryConstant = 64 << 10
)

var ClassifErrConservation = errors.New("pgwire: classifier counters do not add up")

type ClassifierConfig struct {
	KeyScope            work.KeyScope
	plantSetForwardable bool
}

type Classifier struct {
	Config ClassifierConfig
	snap   ClassifierSnapshot
}

type ClassifierSnapshot struct {
	Classified int64
	ByClass    [work.NumClasses]int64
	ByReason   [work.NumRejectReasons]int64
	Opaque     int64
	SafeRead   int64
	Shareable  int64
	Hinted     int64
	Keys       int64
	Statements int64
}

func (c *Classifier) Snapshot() ClassifierSnapshot { return c.snap }

func (c *Classifier) Classify(tenant work.TenantID, text []byte, inTx bool) work.Unit {
	u := clsRun(text, inTx, c.Config.plantSetForwardable)
	u.Tenant = tenant
	if !inTx && u.Class != work.ClassReject && (u.Shareable || u.Hinted) {
		u.HasKey = true
		u.Key.Text = bytes.Clone(text)
		if c.Config.KeyScope == work.ScopeTenant {
			u.Key.Tenant = tenant
		}
	}
	c.count(u)
	return u
}

func (c *Classifier) count(u work.Unit) {
	c.snap.Classified++
	if u.Class < work.NumClasses {
		c.snap.ByClass[u.Class]++
	}
	if u.Class == work.ClassReject && u.Reason < work.NumRejectReasons {
		c.snap.ByReason[u.Reason]++
	}
	c.snap.Opaque += clsCount(u.Opaque)
	c.snap.SafeRead += clsCount(u.SafeRead)
	c.snap.Shareable += clsCount(u.Shareable)
	c.snap.Hinted += clsCount(u.Hinted)
	c.snap.Keys += clsCount(u.HasKey)
	c.snap.Statements += int64(u.Statements)
}

func clsCount(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func ClassifyQuery(text []byte, inTx bool) work.Unit {
	return clsRun(text, inTx, false)
}

type clsParen struct {
	match  int32
	comma1 int32
	comma2 int32
}

type clsMsg struct {
	src     []byte
	toks    []Token
	parens  []clsParen
	bad     bool
	plant   bool
	scratch [clsNameMax]byte
}

func clsRun(text []byte, inTx, plant bool) work.Unit {
	m := clsMsg{src: text, plant: plant}
	m.lex()
	m.matchParens()
	u := work.Unit{Class: work.ClassStartLease}
	if inTx {
		u.Class = work.ClassContinueLease
	}
	u.Hinted = bytes.HasPrefix(text, []byte(ShareHint))
	u.Opaque = m.bad
	u.SafeRead = ShareFits(text)
	stmts := m.split()
	u.Statements = len(stmts)
	for _, s := range stmts {
		if r := m.rejectReason(s); r != work.RejectNone {
			u.Class = work.ClassReject
			u.Reason = r
			return u
		}
	}
	u.Shareable = u.SafeRead && !inTx
	return u
}

type clsSpan struct{ lo, hi int }

const clsNameMax = 63

func (m *clsMsg) lex() {
	n := 0
	l := LexerFor(m.src)
	for {
		t, ok := l.Next()
		if !ok {
			break
		}
		if t.Kind.Significant() && t.Kind != TokenBad {
			n++
		}
	}
	m.toks = make([]Token, 0, n)
	l = LexerFor(m.src)
	for {
		t, ok := l.Next()
		if !ok {
			return
		}
		if t.Kind == TokenBad {
			m.bad = true
		}
		if t.Kind.Significant() && t.Kind != TokenBad {
			m.toks = append(m.toks, t)
		}
	}
}

func (m *clsMsg) isPunct(i int, c byte) bool {
	return i >= 0 && i < len(m.toks) && m.toks[i].Kind == TokenPunct && m.src[m.toks[i].Start] == c
}

func (m *clsMsg) matchParens() {
	m.parens = make([]clsParen, len(m.toks))
	opens := 0
	for i := range m.toks {
		if m.isPunct(i, '(') {
			opens++
		}
	}
	stack := make([]int32, 0, opens)
	for i := range m.toks {
		m.parens[i] = clsParen{match: -1, comma1: -1, comma2: -1}
		switch {
		case m.isPunct(i, '('):
			stack = append(stack, int32(i))
		case m.isPunct(i, ')'):
			if n := len(stack); n > 0 {
				m.parens[stack[n-1]].match = int32(i)
				m.parens[i].match = stack[n-1]
				stack = stack[:n-1]
			}
		case m.isPunct(i, ','):
			if n := len(stack); n > 0 {
				p := &m.parens[stack[n-1]]
				switch {
				case p.comma1 < 0:
					p.comma1 = int32(i)
				case p.comma2 < 0:
					p.comma2 = int32(i)
				}
			}
		case m.isPunct(i, ';'):
			stack = stack[:0]
		}
	}
}

func (m *clsMsg) split() []clsSpan {
	n := 1
	for i := range m.toks {
		if m.isPunct(i, ';') {
			n++
		}
	}
	out := make([]clsSpan, 0, n)
	lo := 0
	for i := range m.toks {
		if m.isPunct(i, ';') {
			if i > lo {
				out = append(out, clsSpan{lo, i})
			}
			lo = i + 1
		}
	}
	if len(m.toks) > lo {
		out = append(out, clsSpan{lo, len(m.toks)})
	}
	return out
}

func (m *clsMsg) word(i int) bool { return i >= 0 && i < len(m.toks) && m.toks[i].Kind == TokenWord }

func (m *clsMsg) is(i int, kw string) bool {
	if !m.word(i) {
		return false
	}
	t := m.toks[i]
	if t.End-t.Start != len(kw) {
		return false
	}
	for k := 0; k < len(kw); k++ {
		if clsFold(m.src[t.Start+k]) != kw[k] {
			return false
		}
	}
	return true
}

func (m *clsMsg) isAny(i int, list []string) bool {
	if !m.word(i) {
		return false
	}
	for _, kw := range list {
		if m.is(i, kw) {
			return true
		}
	}
	return false
}

func clsFold(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

func (m *clsMsg) isName(i int) bool {
	if i < 0 || i >= len(m.toks) {
		return false
	}
	k := m.toks[i].Kind
	return k == TokenWord || k == TokenQuotedIdent || k == TokenUnicodeIdent
}

func (m *clsMsg) name(i, hi int) ([]byte, int, bool) {
	t := m.toks[i]
	switch t.Kind {
	case TokenWord:
		k := min(t.End-t.Start, clsNameMax)
		for j := 0; j < k; j++ {
			m.scratch[j] = clsFold(m.src[t.Start+j])
		}
		return m.scratch[:k], i + 1, true
	case TokenQuotedIdent:
		return clsClip(lexUnquote(m.src[t.Start+1:t.End-1], '"')), i + 1, true
	case TokenUnicodeIdent:
		esc := byte('\\')
		next := i + 1
		if next+1 < hi && m.is(next, "uescape") && m.toks[next+1].Kind == TokenString {
			lit := m.toks[next+1]
			body := m.src[lit.Start:lit.End]
			if len(body) != 3 || body[0] != '\'' {
				return nil, next + 2, false
			}
			esc = body[1]
			next += 2
		}
		s, ok := lexDecodeUnicode(lexUnquote(m.src[t.Start+3:t.End-1], '"'), esc)
		return clsClip(s), next, ok
	}
	return nil, i + 1, false
}

func clsClip(b []byte) []byte {
	if len(b) > clsNameMax {
		return b[:clsNameMax]
	}
	return b
}

func clsInList(list []string, b []byte) bool {
	for _, x := range list {
		if string(b) == x {
			return true
		}
	}
	return false
}

func (m *clsMsg) lead(s clsSpan) int {
	i := s.lo
	if !m.is(i, "explain") {
		return i
	}
	i++
	if m.isPunct(i, '(') {
		if m.parens[i].match < 0 {
			return s.hi
		}
		i = int(m.parens[i].match) + 1
	}
	for m.isAny(i, []string{"analyze", "analyse", "verbose"}) {
		i++
	}
	return i
}

func (m *clsMsg) rejectReason(s clsSpan) work.RejectReason {
	if r := m.leadReject(s); r != work.RejectNone {
		return r
	}
	for i := s.lo; i < s.hi; i++ {
		if r := m.tokenReject(s, i); r != work.RejectNone {
			return r
		}
	}
	return work.RejectNone
}

func (m *clsMsg) leadReject(s clsSpan) work.RejectReason {
	i := m.lead(s)
	switch {
	case m.is(i, "set"):
		if m.plant {
			return work.RejectNone
		}
		if !m.transactionScopedSet(i + 1) {
			return work.RejectSessionSet
		}
		if m.is(i+1, "local") && m.localPathMayNameTemp(i+2, s.hi) {
			return work.RejectTempObject
		}
		return work.RejectNone
	case m.is(i, "reset"):
		return work.RejectReset
	case m.is(i, "prepare"):
		if m.is(i+1, "transaction") {
			return work.RejectTwoPhase
		}
		return work.RejectPrepare
	case m.is(i, "execute"):
		return work.RejectExecute
	case m.is(i, "deallocate"):
		return work.RejectDeallocate
	case m.is(i, "listen"):
		return work.RejectListen
	case m.is(i, "unlisten"):
		return work.RejectUnlisten
	case m.is(i, "discard"):
		return work.RejectDiscard
	case m.is(i, "load"):
		return work.RejectLoad
	case m.is(i, "copy"):
		return work.RejectCopy
	case m.isAny(i, []string{"commit", "rollback"}) && m.is(i+1, "prepared"):
		return work.RejectTwoPhase
	case m.is(i, "declare"):
		return m.declareReject(i+1, s.hi)
	}
	return work.RejectNone
}

func (m *clsMsg) transactionScopedSet(i int) bool {
	if !m.isAny(i, []string{"local", "transaction", "constraints"}) || m.is(i+1, "names") {
		return false
	}
	next := m.toks[min(i+1, len(m.toks)-1)]
	switch {
	case i+1 >= len(m.toks), m.isPunct(i+1, '.'), m.isPunct(i+1, ';'), m.is(i+1, "to"):
		return false
	case next.Kind == TokenOperator && string(m.src[next.Start:next.End]) == "=":
		return false
	}
	return true
}

func (m *clsMsg) declareReject(i, hi int) work.RejectReason {
	for ; i < hi && !m.is(i, "for"); i++ {
		if m.is(i, "with") && m.is(i+1, "hold") {
			return work.RejectCursorWithHold
		}
	}
	return work.RejectNone
}

func (m *clsMsg) tokenReject(s clsSpan, i int) work.RejectReason {
	switch {
	case i > s.lo && m.isAny(i, []string{"temp", "temporary"}) && m.isAny(i-1, []string{"create", "global", "local", "into", "replace"}):
		if m.is(i-1, "into") && m.isAny(i-2, []string{"insert", "merge"}) {
			return work.RejectNone
		}
		return work.RejectTempObject
	case i > s.lo && m.is(i, "execute") && m.is(i-1, "as"):
		return work.RejectExecute
	case !m.isName(i):
		return work.RejectNone
	}
	name, next, ok := m.name(i, s.hi)
	if !ok {
		return work.RejectNone
	}
	if string(name) == "pg_temp" || bytes.HasPrefix(name, []byte("pg_temp_")) {
		return work.RejectTempObject
	}
	if string(name) == "pg_settings" && m.spanHas(s, "update") {
		return work.RejectSessionSet
	}
	if clsInList(rejLexingSettings, clsLowerASCII(name)) && (m.spanHas(s, "set") || m.spanHas(s, "reset")) {
		return work.RejectSessionSet
	}
	if next >= s.hi || !m.isPunct(next, '(') {
		return work.RejectNone
	}
	switch {
	case clsInList(rejSessionAdvisory, name):
		return work.RejectAdvisoryLock
	case string(name) == "set_config" && !m.setConfigLocal(next):
		return work.RejectSetConfig
	case string(name) == "set_config" && m.setConfigPathMayNameTemp(next):
		return work.RejectTempObject
	}
	return work.RejectNone
}

func (m *clsMsg) localPathMayNameTemp(i, hi int) bool {
	if i >= hi {
		return false
	}
	if !m.is(i, "schema") {
		name, next, ok := m.name(i, hi)
		if ok && !clsEqualFold(name, rejSearchPath) {
			return false
		}
		i = next - 1
	}
	for k := i + 1; k < hi; k++ {
		if m.stringMayContain(k, rejTempSchema) {
			return true
		}
	}
	return false
}

func (m *clsMsg) setConfigPathMayNameTemp(open int) bool {
	p := m.parens[open]
	lit := m.toks[open+1]
	if !clsEqualFold(m.src[lit.Start+1:lit.End-1], rejSearchPath) {
		return false
	}
	v := int(p.comma1) + 1
	if int(p.comma2) != v+1 || !m.isString(v) {
		return true
	}
	return m.stringMayContain(v, rejTempSchema)
}

func (m *clsMsg) isString(i int) bool {
	if i < 0 || i >= len(m.toks) {
		return false
	}
	k := m.toks[i].Kind
	return k == TokenString || k == TokenDollarString
}

func (m *clsMsg) stringMayContain(i int, words ...string) bool {
	if !m.isString(i) {
		return false
	}
	t := m.toks[i]
	raw := m.src[t.Start:t.End]
	if t.Kind == TokenString && clsContinued(raw) {
		return true
	}
	switch clsFold(raw[0]) {
	case 'e':
		if bytes.IndexByte(raw, '\\') >= 0 {
			return true
		}
	case 'u':
		if bytes.IndexByte(raw, '\\') >= 0 || m.is(i+1, "uescape") {
			return true
		}
	}
	for _, w := range words {
		if clsContainsFold(raw, w) {
			return true
		}
	}
	return false
}

func clsContinued(raw []byte) bool {
	open := bytes.IndexByte(raw, '\'')
	body := raw[open+1 : len(raw)-1]
	for k := 0; k < len(body); k++ {
		if body[k] != '\'' {
			continue
		}
		if k+1 < len(body) && body[k+1] == '\'' {
			k++
			continue
		}
		return true
	}
	return false
}

func clsContainsFold(b []byte, w string) bool {
	for i := 0; i+len(w) <= len(b); i++ {
		if clsEqualFold(b[i:i+len(w)], w) {
			return true
		}
	}
	return false
}

func clsEqualFold(b []byte, w string) bool {
	if len(b) != len(w) {
		return false
	}
	for k := range b {
		if clsFold(b[k]) != clsFold(w[k]) {
			return false
		}
	}
	return true
}

func (m *clsMsg) spanHas(s clsSpan, kw string) bool {
	for i := s.lo; i < s.hi; i++ {
		if m.is(i, kw) {
			return true
		}
	}
	return false
}

func (m *clsMsg) setConfigLocal(open int) bool {
	p := m.parens[open]
	if p.match < 0 || p.comma2 < 0 || int(p.comma1) != open+2 {
		return false
	}
	lit := m.toks[open+1]
	if lit.Kind != TokenString || m.src[lit.Start] != '\'' || clsContinued(m.src[lit.Start:lit.End]) {
		return false
	}
	setting := clsLowerASCII(lexUnquote(m.src[lit.Start+1:lit.End-1], '\''))
	if clsInList(rejLexingSettings, setting) {
		return false
	}
	return p.comma2+2 == p.match && m.is(int(p.comma2)+1, "true")
}

func clsLowerASCII(b []byte) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		out[i] = clsFold(c)
	}
	return out
}
