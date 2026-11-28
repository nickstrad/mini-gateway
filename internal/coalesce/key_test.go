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

func TestADeadlineNearTheLargestInstantSaturates(t *testing.T) {
	tb := mustTable(t, Config{Mode: ModeAuto, Deadline: Second, NoShareFor: Second, MaxReplyBytes: 1})
	now := Instant(maxInstant - 100)
	j := tb.Join(now, 1, read("", "x"))
	if next := tb.Next(); next < now {
		t.Fatalf("the deadline wrapped: now %d next %d", now, next)
	}
	tb.Sweep(now)
	if s := tb.Snapshot(); s.Expired != 0 {
		t.Fatalf("expired %d", s.Expired)
	}
	tb.Started(now, j.Flight)
	if tb.Stored(now, j.Flight, 2) {
		t.Fatal("the reply cap did not detach")
	}
	if at, ok := tb.noShare.next(); !ok || at < now {
		t.Fatalf("the no-share expiry wrapped: now %d at %d", now, at)
	}
	tb.Sweep(now + 50)
	if s := tb.Snapshot(); s.Expired != 0 || s.NoShareExpired != 0 {
		t.Fatalf("expired %d no-share expired %d", s.Expired, s.NoShareExpired)
	}
}

func TestSaturatingAdd(t *testing.T) {
	for _, c := range []struct{ a, b, want Instant }{
		{1, 2, 3},
		{maxInstant - 1, 1, maxInstant},
		{maxInstant - 1, 2, maxInstant},
		{maxInstant, maxInstant, maxInstant},
		{0, maxInstant, maxInstant},
	} {
		if got := after(c.a, c.b); got != c.want {
			t.Fatalf("after(%d, %d) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
