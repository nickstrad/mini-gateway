package pgwire

import (
	"testing"

	"github.com/nickstrad/mini-gateway/internal/work"
)

func TestLexStringContinuesAcrossANewline(t *testing.T) {
	cases := []struct{ src, want string }{
		{"'a'\n'b'", "string:'a'\n'b'"},
		{"'a'\r'b'", "string:'a'\r'b'"},
		{"'a' \t\n\v 'b'", "string:'a' \t\n\v 'b'"},
		{"'a'\v\n'b'", "string:'a'\v\n'b'"},
		{"'a' -- c\n'b'", "string:'a' -- c\n'b'"},
		{"'a'\n--c\n\n  'b'", "string:'a'\n--c\n\n  'b'"},
		{"'a'\n'b'\n'c'", "string:'a'\n'b'\n'c'"},
		{"E'a'\n'b'", "string:E'a'\n'b'"},
		{"B'01'\n'10'", "string:B'01'\n'10'"},
		{"X'0f'\n'f0'", "string:X'0f'\n'f0'"},
		{"N'a'\n'b'", "string:N'a'\n'b'"},
		{"U&'a'\n'b'", "string:U&'a'\n'b'"},
		{"'a' 'b'", "string:'a' string:'b'"},
		{"'a' /*\n*/ 'b'", "string:'a' block-comment:/*\n*/ string:'b'"},
		{"'a'\n/* c */'b'", "string:'a' block-comment:/* c */ string:'b'"},
		{"'a'\nE'b'", "string:'a' string:E'b'"},
		{"$$a$$\n'b'", "dollar-string:$$a$$ string:'b'"},
		{"'a'\n --y", "string:'a' line-comment:--y"},
		{"\"a\"\n\"b\"", "quoted-ident:\"a\" quoted-ident:\"b\""},
		{"'a'\nb", "string:'a' word:b"},
	}
	for _, c := range cases {
		got, clean := lexKinds(c.src)
		if got != c.want || !clean {
			t.Errorf("%q:\n got %q (clean %v)\nwant %q", c.src, got, clean, c.want)
		}
	}
	hidden := "SELECT E'ok'\n'\\' ; SELECT ' ; SET search_path = hidden ; --'"
	got, clean := lexKinds(hidden)
	if want := "word:SELECT string:E'ok'\n'\\' ; SELECT ' punct:; word:SET word:search_path operator:= word:hidden punct:; line-comment:--'"; got != want || !clean {
		t.Errorf("an escape string keeps its escapes across a continuation:\n got %q\nwant %q", got, want)
	}
}

func TestClassifierRejectsWhatHidesBehindAStringContinuation(t *testing.T) {
	for _, c := range []struct {
		q string
		r work.RejectReason
	}{
		{"SELECT E'ok'\n'\\' ; SELECT ' ; SET search_path = hidden ; --'", work.RejectSessionSet},
		{"SELECT E'ok'\n'\\' || ' || set_config('search_path', 'hidden_1stmt', false) --'", work.RejectSetConfig},
		{"BEGIN; SET LOCAL search_path = 'pg_'\n'temp'; CREATE TABLE sneaky_c1 (a int); COMMIT", work.RejectTempObject},
		{"BEGIN; SET LOCAL SCHEMA 'pg_'\n'temp'; CREATE TABLE sneaky_c2 (a int); COMMIT", work.RejectTempObject},
		{"SELECT set_config('search_path', 'pg_'\n'temp', true)", work.RejectTempObject},
		{"SELECT set_config('search_'\n'path', 'pg_temp', true)", work.RejectSetConfig},
		{"SELECT set_config('standard_conforming_'\n'strings', 'off', true)", work.RejectSetConfig},
	} {
		for _, inTx := range []bool{false, true} {
			u := ClassifyQuery([]byte(c.q), inTx)
			if u.Class != work.ClassReject || u.Reason != c.r {
				t.Errorf("%q (in tx %v): %s, want reject %s", c.q, inTx, clsDescribe(u), c.r)
			}
		}
	}
	for _, q := range []string{
		"SELECT E'ok'\n'\\' || ' || nextval('s')::text --'",
		"SELECT E'ok'\n'\\' ; SELECT ' ; INSERT INTO w VALUES (42) ; --'",
		"SELECT a FROM t_ts WHERE ts < 'n'\n'ow'",
		"SELECT 'n'\n'ow'",
		"SELECT 'foo'\n'bar'",
		"SELECT 'foo' --c\n'bar'",
		"SELECT 'foo' /*\n*/ 'bar'",
		"SELECT 'n'\r'ow'",
		"SELECT U&'n'\n'ow'",
		"SELECT E'n'\n'ow'",
		"SELECT a FROM t_ts WHERE ts < ('n' || 'ow')",
		"SELECT B'01'\n'10'",
		"SELECT 'a'\nE'b'",
		"SELECT ('[' || 'n' || 'ow,)')::tstzrange::text",
		"SELECT ('[' || 'n' || 'ow,)')::tsrange::text",
		"SELECT ('{[' || 'n' || 'ow,)}')::tstzmultirange::text",
		"SELECT ('{' || 'n' || 'ow}')::_timestamptz::text",
		"SELECT ('{' || 'n' || 'ow}')::_timestamp::text",
		"SELECT ('{' || 'n' || 'ow}')::_time::text",
		"SELECT ('{' || 'n' || 'ow}')::_timetz::text",
		"SELECT ('[' || 't' || 'oday,)')::daterange::text",
		"SELECT ('{' || 't' || 'oday}')::_date::text",
	} {
		for _, inTx := range []bool{false, true} {
			if u := ClassifyQuery([]byte(q), inTx); u.SafeRead || u.Shareable || u.HasKey {
				t.Errorf("%q (in tx %v): %s, want neither safe-read nor shareable", q, inTx, clsDescribe(u))
			}
		}
	}
}
