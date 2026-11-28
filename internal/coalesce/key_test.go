package coalesce

import (
	"math/rand/v2"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/work"
)

const keyPropertyPairs = 20000

func keyBytes(r *rand.Rand) []byte {
	alphabet := []byte{0, 1, 'a', 'b', 0xff}
	b := make([]byte, r.IntN(6))
	for i := range b {
		b[i] = alphabet[r.IntN(len(alphabet))]
	}
	return b
}

func TestDifferentShareKeysNeverMapToOneFlightKey(t *testing.T) {
	r := rand.New(rand.NewPCG(11, 0x6b6579))
	seen := make(map[string]work.ShareKey)
	for range keyPropertyPairs {
		k := work.ShareKey{Tenant: work.TenantID(keyBytes(r)), Text: keyBytes(r)}
		s := keyOf(k)
		if prev, ok := seen[s]; ok && !prev.Equal(k) {
			t.Fatalf("(%q, %q) and (%q, %q) share the flight key %q", prev.Tenant, prev.Text, k.Tenant, k.Text, s)
		}
		seen[s] = k
		if keyOf(work.ShareKey{Tenant: k.Tenant, Text: append([]byte(nil), k.Text...)}) != s {
			t.Fatalf("equal keys (%q, %q) map to different flight keys", k.Tenant, k.Text)
		}
	}
}

func FuzzShareKeyIsUnambiguous(f *testing.F) {
	f.Add("a", "b\x00c", "a\x00b", "c")
	f.Add("", "\x00", "\x00", "")
	f.Fuzz(func(t *testing.T, t1, x1, t2, x2 string) {
		a := work.ShareKey{Tenant: work.TenantID(t1), Text: []byte(x1)}
		b := work.ShareKey{Tenant: work.TenantID(t2), Text: []byte(x2)}
		if (keyOf(a) == keyOf(b)) != a.Equal(b) {
			t.Fatalf("(%q, %q) and (%q, %q): equal keys %v, equal flight keys %v", t1, x1, t2, x2, a.Equal(b), keyOf(a) == keyOf(b))
		}
		if tenant, text := splitKey(keyOf(a)); tenant != t1 || text != x1 {
			t.Fatalf("(%q, %q) decodes as (%q, %q)", t1, x1, tenant, text)
		}
	})
}

func TestTenantAndTextWithAZeroByteStartTwoFlights(t *testing.T) {
	tb := mustTable(t, Config{Mode: ModeAuto})
	a := read("a", "b\x00c")
	a.Key.Tenant = "a"
	b := read("a\x00b", "c")
	b.Key.Tenant = "a\x00b"
	if v := tb.Join(0, 1, a).Verdict; v != VerdictLead {
		t.Fatalf("first %s", v)
	}
	if v := tb.Join(0, 2, b).Verdict; v != VerdictLead {
		t.Fatalf("a different key joined the first flight: %s", v)
	}
}
