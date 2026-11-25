package pgwire

import (
	"strings"
	"testing"
)

func lexKinds(src string) (string, bool) {
	toks, clean := LexAll([]byte(src))
	var parts []string
	for _, t := range toks {
		if t.Kind == TokenSpace {
			continue
		}
		parts = append(parts, t.Kind.String()+":"+string(t.Text([]byte(src))))
	}
	return strings.Join(parts, " "), clean
}

func TestLexTokenKinds(t *testing.T) {
	cases := []struct {
		src   string
		want  string
		clean bool
	}{
		{"SELECT 1", "word:SELECT number:1", true},
		{"'it''s'", "string:'it''s'", true},
		{`E'a\'b\\'`, `string:E'a\'b\\'`, true},
		{`e'\\'`, `string:e'\\'`, true},
		{`'a\'`, `string:'a\'`, true},
		{"$$a'b$$", "dollar-string:$$a'b$$", true},
		{"$tag$ $$ $tag$", "dollar-string:$tag$ $$ $tag$", true},
		{"$a$x$b$y$a$", "dollar-string:$a$x$b$y$a$", true},
		{`"a""b"`, `quoted-ident:"a""b"`, true},
		{`U&"a\0062"`, `unicode-ident:U&"a\0062"`, true},
		{`u&'x'`, `string:u&'x'`, true},
		{"B'01' X'1f' N'n'", "string:B'01' string:X'1f' string:N'n'", true},
		{"-- c\nx", "line-comment:-- c word:x", true},
		{"/* a /* b */ c */x", "block-comment:/* a /* b */ c */ word:x", true},
		{"1.5e-3 .5 0x1F 1_000 12", "number:1.5e-3 number:.5 number:0x1F number:1_000 number:12", true},
		{"1..2", "number:1 punct:. number:.2", true},
		{"$1 $23", "param:$1 param:$23", true},
		{"a$b foo_1", "word:a$b word:foo_1", true},
		{"<= <> ||  ->> @>", "operator:<= operator:<> operator:|| operator:->> operator:@>", true},
		{"a+-- c\n1", "word:a operator:+ line-comment:-- c number:1", true},
		{"a*/* c */1", "word:a operator:* block-comment:/* c */ number:1", true},
		{"(a, b)[1]; x::int", "punct:( word:a punct:, word:b punct:) punct:[ number:1 punct:] punct:; word:x punct:: punct:: word:int", true},
		{"\xff\xfe", "word:\xff\xfe", true},
		{"'open", "bad:'open", false},
		{`E'open\'`, `bad:E'open\'`, false},
		{`E'x\`, `bad:E'x\`, false},
		{`"open`, `bad:"open`, false},
		{"$$open", "bad:$$open", false},
		{"$a$ open $b$", "bad:$a$ open $b$", false},
		{"/* open /* */", "bad:/* open /* */", false},
		{`a \ b`, `word:a bad:\ word:b`, false},
		{"{", "bad:{", false},
		{"$ x", "bad:$ word:x", false},
		{"x\x00y", "word:x bad:\x00 word:y", false},
		{"", "", true},
	}
	for _, c := range cases {
		got, clean := lexKinds(c.src)
		if got != c.want || clean != c.clean {
			t.Errorf("%q:\n got %s (clean %v)\nwant %s (clean %v)", c.src, got, clean, c.want, c.clean)
		}
	}
}

func TestLexUnicodeDecode(t *testing.T) {
	cases := []struct {
		in   string
		esc  byte
		want string
		ok   bool
	}{
		{`pg\005ftemp`, '\\', "pg_temp", true},
		{`d\+000061t`, '\\', "dat", true},
		{`a\\b`, '\\', `a\b`, true},
		{`a!0062`, '!', "ab", true},
		{`a!!`, '!', "a!", true},
		{`\00e9`, '\\', "é", true},
		{`\+01F600`, '\\', "\U0001F600", true},
		{`\0000`, '\\', "", false},
		{`\D800`, '\\', "", false},
		{`\12`, '\\', "", false},
		{`\zzzz`, '\\', "", false},
		{`\+110000`, '\\', "", false},
		{`x`, 'a', "", false},
		{`x`, '+', "", false},
		{`x`, ' ', "", false},
	}
	for _, c := range cases {
		got, ok := lexDecodeUnicode([]byte(c.in), c.esc)
		if ok != c.ok || (ok && string(got) != c.want) {
			t.Errorf("%q esc %q: got %q %v want %q %v", c.in, c.esc, got, ok, c.want, c.ok)
		}
	}
}

func lexCheckSpans(t testing.TB, src []byte) {
	t.Helper()
	toks, clean := LexAll(src)
	if len(toks) > len(src) {
		t.Fatalf("%q: %d tokens for %d bytes", src, len(toks), len(src))
	}
	pos := 0
	sawBad := false
	for _, tk := range toks {
		if tk.Start != pos || tk.End <= tk.Start || tk.End > len(src) {
			t.Fatalf("%q: token %+v does not continue at %d", src, tk, pos)
		}
		if tk.Kind < TokenSpace || tk.Kind >= TokenKindCount {
			t.Fatalf("%q: token kind %d", src, tk.Kind)
		}
		if tk.Kind == TokenBad {
			sawBad = true
		}
		pos = tk.End
	}
	if pos != len(src) {
		t.Fatalf("%q: tokens end at %d of %d", src, pos, len(src))
	}
	if clean == sawBad {
		t.Fatalf("%q: clean=%v but bad token seen=%v", src, clean, sawBad)
	}
	var rebuilt []byte
	for _, tk := range toks {
		rebuilt = append(rebuilt, tk.Text(src)...)
	}
	if string(rebuilt) != string(src) {
		t.Fatalf("%q: round trip gives %q", src, rebuilt)
	}
}

func TestLexSpansCoverTheCorpus(t *testing.T) {
	for _, c := range clsLoadCorpus(t) {
		lexCheckSpans(t, []byte(c.text))
	}
}

func FuzzLex(f *testing.F) {
	for _, c := range clsLoadCorpus(f) {
		f.Add([]byte(c.text))
	}
	f.Add([]byte("$a$$a$$$"))
	f.Add([]byte("U&\"\\+\" UESCAPE '+'"))
	f.Fuzz(func(t *testing.T, src []byte) {
		lexCheckSpans(t, src)
	})
}
