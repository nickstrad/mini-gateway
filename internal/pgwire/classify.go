package pgwire

import (
	"bytes"
	"errors"

	"github.com/nickstrad/mini-gateway/internal/work"
)

const (
	ClassifShareHint      = "mg:share"
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
	Classified     int64
	ByClass        [work.NumClasses]int64
	ByReason       [work.NumRejectReasons]int64
	Unclassifiable int64
	Opaque         int64
	ReadOnly       int64
	Coalescable    int64
	Hinted         int64
	MayLeaveOpen   [3]int64
	Statements     int64
}

func (c *Classifier) Snapshot() ClassifierSnapshot { return c.snap }

func (c *Classifier) Classify(tenant work.TenantID, text []byte, inTx bool) work.Unit {
	r := clsRun(text, inTx, c.Config.plantSetForwardable)
	u := r.unit
	u.Tenant = tenant
	if u.Coalescable {
		u.HasKey = true
		u.Key.Text = bytes.Clone(text)
		if c.Config.KeyScope == work.ScopeTenant {
			u.Key.Tenant = tenant
		}
	}
	c.count(u, r.bad)
	return u
}

func (c *Classifier) count(u work.Unit, bad bool) {
	c.snap.Classified++
	if u.Class < work.NumClasses {
		c.snap.ByClass[u.Class]++
	}
	if u.Class == work.ClassReject && u.Reason < work.NumRejectReasons {
		c.snap.ByReason[u.Reason]++
	}
	if bad {
		c.snap.Unclassifiable++
	}
	if u.Opaque {
		c.snap.Opaque++
	}
	if u.ReadOnly {
		c.snap.ReadOnly++
	}
	if u.Coalescable {
		c.snap.Coalescable++
	}
	if u.Hinted {
		c.snap.Hinted++
	}
	if m := u.MayLeaveOpen(); m <= work.Unknown {
		c.snap.MayLeaveOpen[m]++
	}
	c.snap.Statements += int64(u.Statements)
}

func ClassifyQuery(text []byte, inTx bool) work.Unit {
	return clsRun(text, inTx, false).unit
}

type clsResult struct {
	unit work.Unit
	bad  bool
}

type clsParen struct {
	match  int
	comma1 int
	comma2 int
}

type clsMsg struct {
	src     []byte
	toks    []Token
	parens  []clsParen
	bad     bool
	hinted  bool
	plant   bool
	scratch [clsNameMax]byte
}

func clsRun(text []byte, inTx, plant bool) clsResult {
	m := clsMsg{src: text, plant: plant}
	m.lex()
	m.matchParens()
	u := work.Unit{Class: work.ClassStartLease}
	if inTx {
		u.Class = work.ClassContinueLease
	}
	u.Hinted = m.hinted
	stmts := m.split()
	u.Statements = len(stmts)
	for _, s := range stmts {
		if r := m.rejectReason(s); r != work.RejectNone {
			u.Class = work.ClassReject
			u.Reason = r
			return clsResult{unit: u, bad: m.bad}
		}
	}
	if m.bad {
		u.Opaque = true
		u.Tx = work.TxUnknown
		return clsResult{unit: u, bad: true}
	}
	if !m.namesDecode() {
		u.Opaque = true
		u.Tx = work.TxUnknown
		return clsResult{unit: u}
	}
	u.Tx = m.txEffect(stmts)
	if u.Tx == work.TxUnknown {
		u.Opaque = true
		return clsResult{unit: u}
	}
	u.ReadOnly = len(stmts) > 0
	for _, s := range stmts {
		if !m.readOnly(s) {
			u.ReadOnly = false
			break
		}
	}
	u.Coalescable = u.ReadOnly && !inTx && len(stmts) == 1 && m.coalescable(stmts[0])
	return clsResult{unit: u}
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
	seen := false
	for {
		t, ok := l.Next()
		if !ok {
			return
		}
		switch {
		case t.Kind == TokenBad:
			m.bad = true
		case t.Kind == TokenBlockComment && !seen:
			if string(bytes.TrimSpace(m.src[t.Start+2:t.End-2])) == ClassifShareHint {
				m.hinted = true
			}
		}
		if t.Kind.Significant() && t.Kind != TokenBad {
			seen = true
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
	stack := make([]int, 0, opens)
	for i := range m.toks {
		m.parens[i] = clsParen{match: -1, comma1: -1, comma2: -1}
		switch {
		case m.isPunct(i, '('):
			stack = append(stack, i)
		case m.isPunct(i, ')'):
			if n := len(stack); n > 0 {
				m.parens[stack[n-1]].match = i
				m.parens[i].match = stack[n-1]
				stack = stack[:n-1]
			}
		case m.isPunct(i, ','):
			if n := len(stack); n > 0 {
				p := &m.parens[stack[n-1]]
				switch {
				case p.comma1 < 0:
					p.comma1 = i
				case p.comma2 < 0:
					p.comma2 = i
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

func (m *clsMsg) namesDecode() bool {
	for i := range m.toks {
		if m.toks[i].Kind != TokenUnicodeIdent {
			continue
		}
		if _, _, ok := m.name(i, len(m.toks)); !ok {
			return false
		}
	}
	return true
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
		i = m.parens[i].match + 1
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
		if m.isAny(i+1, []string{"local", "transaction", "constraints"}) || m.plant {
			return work.RejectNone
		}
		return work.RejectSessionSet
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
	if next >= s.hi || !m.isPunct(next, '(') {
		return work.RejectNone
	}
	switch {
	case clsInList(rejSessionAdvisory, name):
		return work.RejectAdvisoryLock
	case string(name) == "set_config" && !m.setConfigLocal(next):
		return work.RejectSetConfig
	}
	return work.RejectNone
}

func (m *clsMsg) setConfigLocal(open int) bool {
	p := m.parens[open]
	if p.match < 0 || p.comma2 < 0 {
		return false
	}
	return p.comma2+2 == p.match && m.is(p.comma2+1, "true")
}

func (m *clsMsg) txEffect(stmts []clsSpan) work.TxEffect {
	var begins, ends, within bool
	for _, s := range stmts {
		switch m.stmtTx(s) {
		case work.TxUnknown:
			return work.TxUnknown
		case work.TxBegins:
			begins = true
		case work.TxEnds:
			ends = true
		case work.TxWithin:
			within = true
		}
	}
	switch {
	case begins:
		return work.TxBegins
	case ends:
		return work.TxEnds
	case within:
		return work.TxWithin
	}
	return work.TxNone
}

func (m *clsMsg) stmtTx(s clsSpan) work.TxEffect {
	for i := s.lo; i+1 < s.hi; i++ {
		if m.is(i, "begin") && m.is(i+1, "atomic") {
			return work.TxUnknown
		}
	}
	i := s.lo
	switch {
	case m.is(i, "begin"):
		return work.TxBegins
	case m.is(i, "start"):
		if m.is(i+1, "transaction") {
			return work.TxBegins
		}
		return work.TxUnknown
	case m.isAny(i, []string{"savepoint", "release"}):
		return work.TxWithin
	case m.isAny(i, []string{"rollback", "abort"}) && (m.is(i+1, "to") || (m.isAny(i+1, []string{"work", "transaction"}) && m.is(i+2, "to"))):
		return work.TxWithin
	case m.isAny(i, []string{"commit", "end", "rollback", "abort"}):
		return m.endTx(i+1, s.hi)
	case m.isPunct(i, '('):
		return work.TxNone
	case m.isAny(i, rejModeledLeads):
		return work.TxNone
	}
	return work.TxUnknown
}

func (m *clsMsg) endTx(i, hi int) work.TxEffect {
	if m.isAny(i, []string{"work", "transaction"}) {
		i++
	}
	switch {
	case i == hi:
		return work.TxEnds
	case m.is(i, "and") && m.is(i+1, "chain") && i+2 == hi:
		return work.TxBegins
	case m.is(i, "and") && m.is(i+1, "no") && m.is(i+2, "chain") && i+3 == hi:
		return work.TxEnds
	}
	return work.TxUnknown
}

func (m *clsMsg) readOnly(s clsSpan) bool {
	if !m.readOnlyLead(s) {
		return false
	}
	for i := s.lo; i < s.hi; i++ {
		if !m.readOnlyToken(s, i) {
			return false
		}
	}
	return true
}

func (m *clsMsg) readOnlyLead(s clsSpan) bool {
	i := s.lo
	if m.is(i, "explain") {
		i = m.lead(s)
	}
	for m.isPunct(i, '(') {
		i++
	}
	return m.isAny(i, []string{"select", "values", "table", "with", "show"})
}

func (m *clsMsg) readOnlyToken(s clsSpan, i int) bool {
	t := m.toks[i]
	switch t.Kind {
	case TokenWord:
		if m.isAny(i, rejWriteKeywords) {
			return false
		}
		if m.is(i, "for") && m.isAny(i+1, rejLockStrength) {
			return false
		}
	case TokenOperator:
		return clsInList(rejOperators, m.src[t.Start:t.End])
	case TokenParam:
		return false
	case TokenPunct:
		if m.src[t.Start] == '.' && m.isName(i+1) {
			return m.dottedOK(s, i)
		}
		return true
	}
	if !m.isName(i) {
		return true
	}
	return m.callOK(s, i, false)
}

func (m *clsMsg) dottedOK(s clsSpan, dot int) bool {
	_, next, ok := m.name(dot+1, s.hi)
	if !ok {
		return false
	}
	if m.isPunct(next, '(') {
		return true
	}
	j := dot - 1
	if !m.isName(j) || j <= s.lo {
		return false
	}
	if m.isPunct(j-1, '.') {
		return true
	}
	return m.isAny(j-1, []string{"from", "join", "table", "only"})
}

func (m *clsMsg) callOK(s clsSpan, i int, coalesce bool) bool {
	name, next, ok := m.name(i, s.hi)
	if next >= s.hi || !m.isPunct(next, '(') {
		return true
	}
	if !ok {
		return false
	}
	if m.toks[i].Kind == TokenWord && m.notACall(s, i, name) {
		return true
	}
	e, found := AllowLookupBytes(name)
	if !found || (coalesce && !e.Coalesce) {
		return false
	}
	if !m.isPunct(i-1, '.') {
		return true
	}
	q := i - 2
	if q < s.lo || !m.isName(q) || m.isPunct(q-1, '.') {
		return false
	}
	qn, _, qok := m.name(q, s.hi)
	return qok && string(qn) == "pg_catalog"
}

func (m *clsMsg) notACall(s clsSpan, i int, name []byte) bool {
	switch {
	case clsInList(rejNonCallWords, name), m.isPunct(i-1, ')'), m.is(i-1, "as"), m.is(i-1, "with"):
		return true
	case m.is(i-1, "recursive") && m.is(i-2, "with"):
		return true
	case i == s.lo && m.is(i, "explain"):
		return true
	}
	return false
}

func (m *clsMsg) coalescable(s clsSpan) bool {
	if !m.is(s.lo, "select") {
		return false
	}
	for i := s.lo; i < s.hi; i++ {
		if m.isAny(i, rejPerCallKeywords) {
			return false
		}
		if m.isName(i) && !m.callOK(s, i, true) {
			return false
		}
	}
	return true
}
