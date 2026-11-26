package pgwire

import (
	"bufio"
	"fmt"
	"math/rand/v2"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/work"
)

type clsCase struct {
	line   int
	text   string
	want   work.Unit
	bad    bool
	noexec bool
}

func clsLoadCorpus(t testing.TB) []clsCase {
	t.Helper()
	f, err := os.Open("testdata/classifier-corpus.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []clsCase
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	n := 0
	for sc.Scan() {
		n++
		line := sc.Text()
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		flags, text, ok := strings.Cut(line, "\t")
		if !ok {
			t.Fatalf("corpus line %d: no tab", n)
		}
		if strings.HasPrefix(text, `"`) {
			text, err = strconv.Unquote(text)
			if err != nil {
				t.Fatalf("corpus line %d: %v", n, err)
			}
		}
		c := clsCase{line: n, text: text, want: work.Unit{Class: work.ClassStartLease}}
		for _, fl := range strings.Fields(flags) {
			switch {
			case fl == "F":
			case strings.HasPrefix(fl, "R:"):
				c.want.Class = work.ClassReject
				c.want.Reason = clsReasonByName(t, fl[2:])
			case fl == "ro":
				c.want.ReadOnly = true
			case fl == "co":
				c.want.Coalescable = true
			case fl == "opaque":
				c.want.Opaque = true
			case fl == "hint":
				c.want.Hinted = true
			case fl == "bad":
				c.bad = true
			case fl == "noexec":
				c.noexec = true
			case strings.HasPrefix(fl, "tx:"):
				c.want.Tx = clsTxByName(t, fl[3:])
			default:
				t.Fatalf("corpus line %d: unknown flag %q", n, fl)
			}
		}
		out = append(out, c)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func clsReasonByName(t testing.TB, s string) work.RejectReason {
	for r := work.RejectNone; r < work.NumRejectReasons; r++ {
		if r.String() == s {
			return r
		}
	}
	t.Fatalf("unknown reason %q", s)
	return 0
}

func clsTxByName(t testing.TB, s string) work.TxEffect {
	for x := work.TxNone; x < work.NumTxEffects; x++ {
		if x.String() == s {
			return x
		}
	}
	t.Fatalf("unknown tx effect %q", s)
	return 0
}

func clsSame(a, b work.Unit) bool {
	return a.Class == b.Class && a.Reason == b.Reason && a.Tx == b.Tx && a.ReadOnly == b.ReadOnly &&
		a.Coalescable == b.Coalescable && a.Hinted == b.Hinted && a.Opaque == b.Opaque
}

func clsDescribe(u work.Unit) string {
	return fmt.Sprintf("class=%s reason=%s tx=%s ro=%v co=%v hint=%v opaque=%v stmts=%d", u.Class, u.Reason, u.Tx, u.ReadOnly, u.Coalescable, u.Hinted, u.Opaque, u.Statements)
}

func TestClassifierCorpus(t *testing.T) {
	cases := clsLoadCorpus(t)
	if len(cases) < 300 {
		t.Fatalf("corpus has %d statements, want at least 300", len(cases))
	}
	reasons := map[work.RejectReason]int{}
	for _, c := range cases {
		got := ClassifyQuery([]byte(c.text), false)
		want := c.want
		if want.Class == work.ClassReject {
			want.Tx, want.ReadOnly, want.Coalescable, want.Opaque = got.Tx, false, false, false
			reasons[want.Reason]++
		}
		if !clsSame(got, want) {
			t.Errorf("line %d %q:\n got %s\nwant %s", c.line, c.text, clsDescribe(got), clsDescribe(want))
		}
		r := clsRun([]byte(c.text), false, false)
		if r.bad != c.bad {
			t.Errorf("line %d %q: lexer bad=%v, corpus says %v", c.line, c.text, r.bad, c.bad)
		}
		inTx := ClassifyQuery([]byte(c.text), true)
		if want.Class != work.ClassReject && (inTx.Class != work.ClassContinueLease || inTx.Coalescable || inTx.ReadOnly != got.ReadOnly) {
			t.Errorf("line %d %q: inside a transaction: %s", c.line, c.text, clsDescribe(inTx))
		}
	}
	for r := work.RejectSessionSet; r < work.RejectExtendedProtocol; r++ {
		if reasons[r] == 0 {
			t.Errorf("corpus has no statement rejected for %s", r)
		}
	}
}

func TestClassifierGeneratedStatements(t *testing.T) {
	long := "SELECT " + strings.Repeat("abs(1) + ", 50000) + "1"
	cases := []struct {
		name string
		text string
		want work.Unit
	}{
		{"long read", long, work.Unit{Class: work.ClassStartLease, ReadOnly: true, Coalescable: true}},
		{"long read then set", long + "; SET search_path = x", work.Unit{Class: work.ClassReject, Reason: work.RejectSessionSet}},
		{"long string", "SELECT '" + strings.Repeat("x'';SET a=1;''", 20000) + "'", work.Unit{Class: work.ClassStartLease, ReadOnly: true, Coalescable: true}},
		{"long identifier", "SELECT " + strings.Repeat("pg_advisory_lock", 10) + "(1)", work.Unit{Class: work.ClassStartLease}},
		{"long pg_temp", "CREATE TABLE pg_temp_" + strings.Repeat("9", 100) + ".t (a int)", work.Unit{Class: work.ClassReject, Reason: work.RejectTempObject}},
		{"deep parens", "SELECT " + strings.Repeat("(", 20000) + "1" + strings.Repeat(")", 20000), work.Unit{Class: work.ClassStartLease, ReadOnly: true, Coalescable: true}},
		{"nested set_config", "SELECT " + strings.Repeat("set_config('a.b', ", 5000) + "'x'" + strings.Repeat(", true)", 5000), work.Unit{Class: work.ClassStartLease}},
		{"many statements", strings.Repeat("SELECT 1;", 20000), work.Unit{Class: work.ClassStartLease, ReadOnly: true}},
		{"many statements then listen", strings.Repeat("SELECT 1;", 20000) + "LISTEN x", work.Unit{Class: work.ClassReject, Reason: work.RejectListen}},
		{"invalid utf-8 word", "SELECT a\xff FROM (SELECT 1 AS \"a\xff\") s", work.Unit{Class: work.ClassStartLease, ReadOnly: true, Coalescable: true}},
		{"invalid utf-8 then lock", "SELECT '\xff'; SELECT pg_advisory_lock(1)", work.Unit{Class: work.ClassReject, Reason: work.RejectAdvisoryLock}},
		{"nul byte", "SELECT 1\x00; SET search_path = x", work.Unit{Class: work.ClassReject, Reason: work.RejectSessionSet}},
		{"nul byte read", "SELECT 1\x00", work.Unit{Class: work.ClassStartLease, Tx: work.TxUnknown, Opaque: true}},
	}
	for _, c := range cases {
		got := ClassifyQuery([]byte(c.text), false)
		want := c.want
		if want.Class == work.ClassReject {
			want.Tx = got.Tx
		}
		if !clsSame(got, want) {
			t.Errorf("%s: got %s want %s", c.name, clsDescribe(got), clsDescribe(want))
		}
	}
}

func TestClassifierEmptyMessages(t *testing.T) {
	for _, q := range []string{"", ";", " ; ; ", "-- x", "/* x */", "/* a */;/* b */"} {
		u := ClassifyQuery([]byte(q), false)
		if u.Class != work.ClassStartLease || u.Statements != 0 || u.ReadOnly || u.Coalescable || u.Tx != work.TxNone || u.MayLeaveOpen() != work.No {
			t.Errorf("%q: %s", q, clsDescribe(u))
		}
	}
}

func TestClassifierShareKeyAndScope(t *testing.T) {
	var c Classifier
	q := []byte("SELECT v FROM kv WHERE k = 'a'")
	u := c.Classify("alice", q, false)
	if !u.HasKey || string(u.Key.Text) != string(q) || u.Key.Tenant != "" || u.Tenant != "alice" {
		t.Fatalf("global scope: %+v", u)
	}
	q[0] = 'X'
	if string(u.Key.Text) != "SELECT v FROM kv WHERE k = 'a'" {
		t.Fatal("the key aliases the caller's buffer")
	}
	c.Config.KeyScope = work.ScopeTenant
	u = c.Classify("bob", []byte("SELECT 1"), false)
	if !u.HasKey || u.Key.Tenant != "bob" {
		t.Fatalf("tenant scope: %+v", u)
	}
	for _, q := range []string{"SELECT 1", "SELECT  1", "select 1"} {
		a := c.Classify("bob", []byte(q), false)
		if string(a.Key.Text) != q {
			t.Errorf("key is not the exact text: %q", a.Key.Text)
		}
	}
	u = c.Classify("bob", []byte("SELECT now()"), false)
	if u.HasKey || u.Key.Text != nil {
		t.Fatalf("a statement that is not coalescable has a key: %+v", u)
	}
	u = c.Classify("bob", []byte("SELECT 1"), true)
	if u.HasKey || u.Coalescable {
		t.Fatalf("inside a transaction nothing is shared: %+v", u)
	}
}

const clsPadSeeds = 6

func clsPadding(rng *rand.Rand) string {
	switch rng.IntN(7) {
	case 0:
		return " "
	case 1:
		return "\n\t "
	case 2:
		return " /* pad */ "
	case 3:
		return " -- pad ; SET x = 1\n"
	case 4:
		return " /* nested /* pad */ ; */ "
	case 5:
		return "\r\n\f\v"
	}
	return " /**/"
}

func clsMutateCase(rng *rand.Rand, b []byte) {
	for i := range b {
		c := b[i]
		if c >= 'a' && c <= 'z' && rng.IntN(2) == 0 {
			b[i] = c - 'a' + 'A'
		} else if c >= 'A' && c <= 'Z' && rng.IntN(2) == 0 {
			b[i] = c - 'A' + 'a'
		}
	}
}

func clsPad(src []byte, rng *rand.Rand) []byte {
	toks, _ := LexAll(src)
	var out []byte
	for i, t := range toks {
		if rng.IntN(2) == 0 {
			out = append(out, clsPadding(rng)...)
		}
		text := append([]byte(nil), t.Text(src)...)
		if t.Kind == TokenWord {
			clsMutateCase(rng, text)
		}
		out = append(out, text...)
		if t.Kind == TokenBad && t.End == len(src) && i == len(toks)-1 {
			return out
		}
	}
	if rng.IntN(2) == 0 {
		out = append(out, clsPadding(rng)...)
	}
	return out
}

func TestClassifierPaddingAndCaseProperty(t *testing.T) {
	cases := clsLoadCorpus(t)
	for seed := uint64(1); seed <= clsPadSeeds; seed++ {
		rng := rand.New(rand.NewPCG(seed, 0x5eed))
		for _, c := range cases {
			base := ClassifyQuery([]byte(c.text), false)
			padded := clsPad([]byte(c.text), rng)
			got := ClassifyQuery(padded, false)
			if !clsSame(base, got) || base.Statements != got.Statements {
				t.Errorf("seed %d line %d: %q\n padded %q\n base %s\n  got %s", seed, c.line, c.text, padded, clsDescribe(base), clsDescribe(got))
			}
		}
	}
}

func clsMutate(rng *rand.Rand, src []byte) []byte {
	b := append([]byte(nil), src...)
	inserts := []string{"'", "\"", "$$", "/*", "E'", "U&\"", "$a$", "\\", "{", "\x00", "--", ";", "(", ")"}
	for k := rng.IntN(3) + 1; k > 0; k-- {
		switch rng.IntN(3) {
		case 0:
			if len(b) > 0 {
				b = b[:rng.IntN(len(b))]
			}
		case 1:
			at := 0
			if len(b) > 0 {
				at = rng.IntN(len(b) + 1)
			}
			ins := inserts[rng.IntN(len(inserts))]
			b = append(b[:at], append([]byte(ins), b[at:]...)...)
		default:
			if len(b) > 0 {
				b[rng.IntN(len(b))] = byte(rng.IntN(256))
			}
		}
	}
	return b
}

func TestClassifierConservativeWhenUntokenisable(t *testing.T) {
	cases := clsLoadCorpus(t)
	bad := 0
	for seed := uint64(1); seed <= 20; seed++ {
		rng := rand.New(rand.NewPCG(seed, 0xbad))
		for _, c := range cases {
			q := clsMutate(rng, []byte(c.text))
			if _, clean := LexAll(q); clean {
				continue
			}
			bad++
			u := ClassifyQuery(q, false)
			clsCheckConservative(t, q, u)
		}
	}
	if bad < 1000 {
		t.Fatalf("only %d untokenisable mutants: the property was not exercised", bad)
	}
}

func clsCheckConservative(t testing.TB, q []byte, u work.Unit) {
	t.Helper()
	if u.ReadOnly || u.Coalescable || u.HasKey {
		t.Errorf("untokenisable %q is read-only or coalescable: %s", q, clsDescribe(u))
	}
	if u.Class != work.ClassReject && (!u.Opaque || u.Tx != work.TxUnknown || !u.Class.Forwards()) {
		t.Errorf("untokenisable %q is not forwarded as opaque: %s", q, clsDescribe(u))
	}
}

func TestClassifierInvariantsOverMutants(t *testing.T) {
	cases := clsLoadCorpus(t)
	for seed := uint64(1); seed <= 10; seed++ {
		rng := rand.New(rand.NewPCG(seed, 0x1))
		for _, c := range cases {
			q := clsMutate(rng, []byte(c.text))
			clsCheckInvariants(t, q)
		}
	}
}

func clsCheckInvariants(t testing.TB, q []byte) {
	t.Helper()
	for _, inTx := range []bool{false, true} {
		u := ClassifyQuery(q, inTx)
		switch {
		case u.Class != work.ClassReject && u.Class != work.ClassStartLease && u.Class != work.ClassContinueLease:
			t.Errorf("%q: class %s", q, u.Class)
		case u.Class == work.ClassReject && (u.Reason == work.RejectNone || u.Reason >= work.NumRejectReasons):
			t.Errorf("%q: reject without a reason", q)
		case u.Class != work.ClassReject && u.Reason != work.RejectNone:
			t.Errorf("%q: reason without a reject", q)
		case u.Coalescable && (!u.ReadOnly || u.Statements != 1 || inTx):
			t.Errorf("%q: coalescable outside the definition: %s", q, clsDescribe(u))
		case u.ReadOnly && (u.Opaque || u.Statements == 0 || u.Class == work.ClassReject):
			t.Errorf("%q: read-only outside the definition: %s", q, clsDescribe(u))
		case u.Opaque && u.Tx != work.TxUnknown:
			t.Errorf("%q: opaque with a known transaction effect", q)
		case inTx && u.Class == work.ClassStartLease, !inTx && u.Class == work.ClassContinueLease:
			t.Errorf("%q: lease class ignores the transaction state", q)
		}
	}
}

func TestClassifierMemoryIsBounded(t *testing.T) {
	shapes := []struct {
		name string
		unit string
	}{
		{"parens", "("},
		{"words", "a "},
		{"dots", "a."},
		{"semicolons", ";"},
		{"calls", "f(1,"},
		{"set_config", "set_config(1,2,"},
		{"strings", "'x'"},
		{"quoted", `"a"`},
		{"unicode", `U&"a" `},
		{"commas", ","},
		{"select", "SELECT abs(t.a) FROM t; "},
	}
	worst := int64(0)
	for _, sh := range shapes {
		for _, size := range []int{1 << 10, 1 << 16, 1 << 20} {
			q := []byte(strings.Repeat(sh.unit, size/len(sh.unit)))
			var c Classifier
			c.Classify("t", q, false)
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			c.Classify("t", q, false)
			runtime.ReadMemStats(&after)
			used := int64(after.TotalAlloc - before.TotalAlloc)
			limit := ClassifMemoryFactor*int64(len(q)) + ClassifMemoryConstant
			if used > limit {
				t.Errorf("%s, %d bytes: classification allocated %d bytes, bound %d", sh.name, len(q), used, limit)
			}
			if size == 1<<20 && used/int64(len(q)) > worst {
				worst = used / int64(len(q))
			}
		}
	}
	t.Logf("worst measured allocation per input byte at 1 MiB: %d (bound %d per byte + %d)", worst, ClassifMemoryFactor, ClassifMemoryConstant)
}

func FuzzClassify(f *testing.F) {
	for _, c := range clsLoadCorpus(f) {
		f.Add([]byte(c.text))
	}
	f.Fuzz(func(t *testing.T, q []byte) {
		clsCheckInvariants(t, q)
		if _, clean := LexAll(q); !clean {
			clsCheckConservative(t, q, ClassifyQuery(q, false))
		}
	})
}
