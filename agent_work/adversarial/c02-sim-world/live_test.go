package adv_test

import (
	"encoding/binary"
	"github.com/nickstrad/mini-gateway/internal/testclient"
	"io"
	"net"
	"os"
	"testing"
	"time"
)

// The live oracle deliberately uses independent raw TCP framing, not world.ProbeScript.
// The three claim-1 tests always run their simulator oracle; MG_TIER_R adds Postgres.
func liveCompare(t *testing.T, q, want string) {
	t.Helper()
	if os.Getenv("MG_TIER_R") != "1" {
		return
	}
	addr := os.Getenv("MG_PG_ADDR")
	if addr == "" {
		t.Fatal("MG_TIER_R requires MG_PG_ADDR")
	}
	c, e := net.DialTimeout("tcp", addr, 5*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	var sess testclient.Session
	write := func(b []byte) {
		for len(b) > 0 {
			n, e := c.Write(b)
			if e != nil {
				t.Fatal(e)
			}
			b = b[n:]
		}
	}
	read := func() []testclient.Msg {
		var out []testclient.Msg
		for {
			var h [5]byte
			if _, e := io.ReadFull(c, h[:]); e != nil {
				t.Fatal(e)
			}
			n := int(binary.BigEndian.Uint32(h[1:]))
			if n < 4 || n > 1<<20 {
				t.Fatal("bad server frame", n)
			}
			b := make([]byte, n-4)
			if _, e := io.ReadFull(c, b); e != nil {
				t.Fatal(e)
			}
			m, e := testclient.Decode(h[0], b)
			if e != nil {
				t.Fatal(e)
			}
			out = append(out, m)
			if m.Type == 'Z' {
				return out
			}
		}
	}
	write(sess.Startup(nil, []testclient.Param{{Name: "user", Value: "postgres"}, {Name: "database", Value: "postgres"}}))
	read()
	write(sess.Query(nil, q))
	got := shape(read())
	t.Logf("tier R query=%q postgres=%s fake=%s", q, got, want)
	if got != want {
		t.Errorf("Postgres17=%s fake=%s for %q", got, want, q)
	}
	write(testclient.EncodeTerminate(nil))
}
