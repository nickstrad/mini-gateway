package pgwire

import (
	"bufio"
	"bytes"
	"fmt"
	"math/rand/v2"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/work"
)

type clsCase struct {
	line   int
	text   string
	want   work.Unit
	bad    bool
	noexec bool
	wasCo  bool
	isNew  bool
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
			case fl == "fits":
				c.want.SafeRead = true
			case fl == "was-co":
				c.wasCo = true
			case fl == "new":
				c.isNew = true
			case fl == "hint":
				c.want.Hinted = true
			case fl == "bad":
				c.bad = true
				c.want.Opaque = true
			case fl == "noexec":
				c.noexec = true
			default:
				t.Fatalf("corpus line %d: unknown flag %q", n, fl)
			}
		}
		c.want.Shareable = c.want.SafeRead && c.want.Class != work.ClassReject
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

func clsSame(a, b work.Unit) bool {
	return a.Class == b.Class && a.Reason == b.Reason && a.SafeRead == b.SafeRead &&
		a.Shareable == b.Shareable && a.Hinted == b.Hinted && a.Opaque == b.Opaque
}

func clsSameCourtesy(a, b work.Unit) bool {
	return a.Class == b.Class && a.Reason == b.Reason && a.Opaque == b.Opaque && a.Statements == b.Statements
}

func clsDescribe(u work.Unit) string {
	return fmt.Sprintf("class=%s reason=%s safe-read=%v shareable=%v hint=%v opaque=%v stmts=%d key=%v", u.Class, u.Reason, u.SafeRead, u.Shareable, u.Hinted, u.Opaque, u.Statements, u.HasKey)
}

func TestClassifierCorpus(t *testing.T) {
	cases := clsLoadCorpus(t)
	if len(cases) < 300 {
		t.Fatalf("corpus has %d statements, want at least 300", len(cases))
	}
	reasons := map[work.RejectReason]int{}
	fits, wasCo, stillFit := 0, 0, 0
	for _, c := range cases {
		got := ClassifyQuery([]byte(c.text), false)
		want := c.want
		if want.Class == work.ClassReject {
			reasons[want.Reason]++
		}
		if !clsSame(got, want) {
			t.Errorf("line %d %q:\n got %s\nwant %s", c.line, c.text, clsDescribe(got), clsDescribe(want))
		}
		if c.want.SafeRead {
			fits++
			if !c.wasCo && !c.isNew {
				t.Errorf("line %d %q: the first build did not coalesce it, so it must not fit the safe shape now", c.line, c.text)
			}
		}
		if c.wasCo {
			wasCo++
			if c.want.SafeRead {
				stillFit++
			}
		}
		inTx := ClassifyQuery([]byte(c.text), true)
		if want.Class != work.ClassReject && (inTx.Class != work.ClassContinueLease || inTx.Shareable || inTx.SafeRead != got.SafeRead) {
			t.Errorf("line %d %q: inside a transaction: %s", c.line, c.text, clsDescribe(inTx))
		}
	}
	for r := work.RejectSessionSet; r < work.RejectExtendedProtocol; r++ {
		if reasons[r] == 0 {
			t.Errorf("corpus has no statement rejected for %s", r)
		}
	}
	t.Logf("corpus: %d records, %d fit the safe shape; %d of the first build's %d coalescable records still fit", len(cases), fits, stillFit, wasCo)
}

func TestClassifierGeneratedStatements(t *testing.T) {
	long := "SELECT " + strings.Repeat("abs(1) + ", 50000) + "1"
	cases := []struct {
		name string
		text string
		want work.Unit
	}{
		{"long read", long, work.Unit{Class: work.ClassStartLease}},
		{"long fitting read", "SELECT " + strings.Repeat("a", ShareMaxLen-7), work.Unit{Class: work.ClassStartLease, SafeRead: true, Shareable: true}},
		{"fitting read one byte too long", "SELECT " + strings.Repeat("a", ShareMaxLen-6), work.Unit{Class: work.ClassStartLease}},
		{"long read then set", long + "; SET search_path = x", work.Unit{Class: work.ClassReject, Reason: work.RejectSessionSet}},
		{"long string", "SELECT '" + strings.Repeat("x'';SET a=1;''", 20000) + "'", work.Unit{Class: work.ClassStartLease}},
		{"long identifier", "SELECT " + strings.Repeat("pg_advisory_lock", 10) + "(1)", work.Unit{Class: work.ClassStartLease}},
		{"long pg_temp", "CREATE TABLE pg_temp_" + strings.Repeat("9", 100) + ".t (a int)", work.Unit{Class: work.ClassReject, Reason: work.RejectTempObject}},
		{"deep parens", "SELECT " + strings.Repeat("(", 20000) + "1" + strings.Repeat(")", 20000), work.Unit{Class: work.ClassStartLease}},
		{"deep parens that fit", "SELECT " + strings.Repeat("(", 1000) + "1" + strings.Repeat(")", 1000), work.Unit{Class: work.ClassStartLease, SafeRead: true, Shareable: true}},
		{"nested set_config", "SELECT " + strings.Repeat("set_config('a.b', ", 5000) + "'x'" + strings.Repeat(", true)", 5000), work.Unit{Class: work.ClassStartLease}},
		{"many statements", strings.Repeat("SELECT 1;", 20000), work.Unit{Class: work.ClassStartLease}},
		{"many statements then listen", strings.Repeat("SELECT 1;", 20000) + "LISTEN x", work.Unit{Class: work.ClassReject, Reason: work.RejectListen}},
		{"invalid utf-8 word", "SELECT a\xff FROM (SELECT 1 AS \"a\xff\") s", work.Unit{Class: work.ClassStartLease}},
		{"invalid utf-8 then lock", "SELECT '\xff'; SELECT pg_advisory_lock(1)", work.Unit{Class: work.ClassReject, Reason: work.RejectAdvisoryLock}},
		{"nul byte", "SELECT 1\x00; SET search_path = x", work.Unit{Class: work.ClassReject, Reason: work.RejectSessionSet, Opaque: true}},
		{"nul byte read", "SELECT 1\x00", work.Unit{Class: work.ClassStartLease, Opaque: true}},
	}
	for _, c := range cases {
		got := ClassifyQuery([]byte(c.text), false)
		want := c.want
		if !clsSame(got, want) {
			t.Errorf("%s: got %s want %s", c.name, clsDescribe(got), clsDescribe(want))
		}
	}
}

func TestClassifierEmptyMessages(t *testing.T) {
	for _, q := range []string{"", ";", " ; ; ", "-- x", "/* x */", "/* a */;/* b */"} {
		u := ClassifyQuery([]byte(q), false)
		if u.Class != work.ClassStartLease || u.Statements != 0 || u.SafeRead || u.Shareable || u.Opaque {
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
		t.Fatalf("a statement that is not shareable has a key: %+v", u)
	}
	u = c.Classify("bob", []byte("SELECT 1"), true)
	if u.HasKey || u.Shareable {
		t.Fatalf("inside a transaction nothing is shared: %+v", u)
	}
	hinted := "/* mg:share */ SELECT now()"
	u = c.Classify("bob", []byte(hinted), false)
	if !u.Hinted || u.SafeRead || u.Shareable || !u.HasKey || string(u.Key.Text) != hinted || u.Key.Tenant != "bob" {
		t.Fatalf("a hinted statement has a key but is neither safe-read nor shareable: %+v", u)
	}
	for _, q := range []string{hinted, "/* mg:share */ SET search_path = x"} {
		if u := c.Classify("bob", []byte(q), true); u.HasKey {
			t.Fatalf("%q inside a transaction has a key: %+v", q, u)
		}
	}
	if u := c.Classify("bob", []byte("/* mg:share */ SET search_path = x"), false); u.HasKey || u.Class != work.ClassReject {
		t.Fatalf("a rejected hinted statement has a key: %+v", u)
	}
	for _, q := range []string{" /* mg:share */ SELECT 1", "/*mg:share*/ SELECT 1", "/* MG:SHARE */ SELECT 1", "SELECT 1 /* mg:share */"} {
		if u := c.Classify("bob", []byte(q), false); u.Hinted || u.HasKey {
			t.Errorf("%q is not the exact hint: %+v", q, u)
		}
	}
	if err := c.Snapshot().Check(); err != nil {
		t.Fatal(err)
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
			if !clsSameCourtesy(base, got) {
				t.Errorf("seed %d line %d: %q\n padded %q\n base %s\n  got %s", seed, c.line, c.text, padded, clsDescribe(base), clsDescribe(got))
			}
			spaced := clsPadSpaces([]byte(c.text), rng)
			if got := ClassifyQuery(spaced, false); !clsSameCourtesy(base, got) || (base.SafeRead && !got.SafeRead) {
				t.Errorf("seed %d line %d: %q\n spaced %q\n base %s\n  got %s", seed, c.line, c.text, spaced, clsDescribe(base), clsDescribe(got))
			}
		}
	}
}

func clsPadSpaces(src []byte, rng *rand.Rand) []byte {
	toks, _ := LexAll(src)
	var out []byte
	for _, t := range toks {
		if rng.IntN(2) == 0 {
			out = append(out, ' ')
		}
		text := append([]byte(nil), t.Text(src)...)
		if t.Kind == TokenWord {
			clsMutateCase(rng, text)
		}
		out = append(out, text...)
	}
	return out
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
	if u.SafeRead || u.Shareable || (u.HasKey && !u.Hinted) {
		t.Errorf("untokenisable %q is safe-read or shareable: %s", q, clsDescribe(u))
	}
	if !u.Opaque || (u.Class != work.ClassReject && !u.Class.Forwards()) {
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
	_, clean := LexAll(q)
	fits := ShareFits(q)
	hinted := bytes.HasPrefix(q, []byte(ShareHint))
	var c Classifier
	for _, inTx := range []bool{false, true} {
		u := c.Classify("t", q, inTx)
		switch {
		case u.Class != work.ClassReject && u.Class != work.ClassStartLease && u.Class != work.ClassContinueLease:
			t.Errorf("%q: class %s", q, u.Class)
		case u.Class == work.ClassReject && (u.Reason == work.RejectNone || u.Reason >= work.NumRejectReasons):
			t.Errorf("%q: reject without a reason", q)
		case u.Class != work.ClassReject && u.Reason != work.RejectNone:
			t.Errorf("%q: reason without a reject", q)
		case u.SafeRead != fits:
			t.Errorf("%q: safe-read %v but the recogniser says %v", q, u.SafeRead, fits)
		case u.Shareable != (fits && !inTx && u.Class != work.ClassReject):
			t.Errorf("%q: shareable outside the definition: %s", q, clsDescribe(u))
		case u.SafeRead && (u.Opaque || u.Statements != 1):
			t.Errorf("%q: safe-read but the lexer disagrees: %s", q, clsDescribe(u))
		case u.Hinted != hinted:
			t.Errorf("%q: hinted %v, prefix %v", q, u.Hinted, hinted)
		case u.Opaque == clean:
			t.Errorf("%q: opaque %v, lexer clean %v", q, u.Opaque, clean)
		case u.HasKey != (!inTx && u.Class != work.ClassReject && (u.Shareable || u.Hinted)):
			t.Errorf("%q: key outside the definition: %s", q, clsDescribe(u))
		case inTx && u.Class == work.ClassStartLease, !inTx && u.Class == work.ClassContinueLease:
			t.Errorf("%q: lease class ignores the transaction state", q)
		}
	}
	if err := c.Snapshot().Check(); err != nil {
		t.Errorf("%q: %v", q, err)
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

const clsTimeGrowthLimit = 96

func clsFastest(q []byte) time.Duration {
	best := time.Duration(1<<63 - 1)
	for k := 0; k < 3; k++ {
		start := time.Now()
		ClassifyQuery(q, false)
		ShareFits(q)
		LexAll(q)
		if d := time.Since(start); d < best {
			best = d
		}
	}
	return best
}

func TestLexOperatorRunsGrowLinearly(t *testing.T) {
	clsCheckLinearShapes(t, []string{"+", "-", "+-", "*+", "=-", "<+-", "+ ", "1+"})
}

func TestClassifierSpanKeywordChecksGrowLinearly(t *testing.T) {
	clsCheckLinearShapes(t, []string{"pg_settings ", "standard_conforming_strings ", "client_encoding ", `"pg_settings" `, "pg_settings, ", "SELECT pg_settings "})
}

func clsCheckLinearShapes(t *testing.T, units []string) {
	t.Helper()
	if testing.Short() {
		t.Skip("timing test")
	}
	for _, unit := range units {
		small := []byte("SELECT " + strings.Repeat(unit, (1<<12)/len(unit)))
		large := []byte("SELECT " + strings.Repeat(unit, (1<<16)/len(unit)))
		a, b := clsFastest(small), clsFastest(large)
		if a < time.Microsecond {
			a = time.Microsecond
		}
		ratio := int64(b / a)
		t.Logf("%q: 4 KiB %v, 64 KiB %v, ratio %d", unit, a, b, ratio)
		if ratio > clsTimeGrowthLimit {
			t.Errorf("%q: 16 times the input took %d times as long (%v against %v); want at most %d", unit, ratio, b, a, clsTimeGrowthLimit)
		}
	}
}

func TestClassifierTimeGrowsLinearly(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	for _, unit := range []string{"(", "a ", ";", "'x'", "set_config(1,2,", `U&"a" `, "SELECT abs(t.a) FROM t; ", "'a'\n", "-- c\n"} {
		small := []byte(strings.Repeat(unit, (1<<16)/len(unit)))
		large := []byte(strings.Repeat(unit, (1<<20)/len(unit)))
		a, b := clsFastest(small), clsFastest(large)
		if a < time.Microsecond {
			a = time.Microsecond
		}
		ratio := int64(b / a)
		t.Logf("%q: 64 KiB %v, 1 MiB %v, ratio %d", unit, a, b, ratio)
		if ratio > clsTimeGrowthLimit {
			t.Errorf("%q: 16 times the input took %d times as long (%v against %v); want at most %d", unit, ratio, b, a, clsTimeGrowthLimit)
		}
	}
}
