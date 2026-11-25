package adv_test

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	p "github.com/nickstrad/mini-gateway/internal/pgwire"
	pt "github.com/nickstrad/mini-gateway/internal/pgwire/pgwiretest"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// These conversations are authored from claim 11, independently of StartupScript.
// The optional live run records only the real response, even when the fake disagrees.
func firstReply(r io.Reader) (string, error) {
	var typ [1]byte
	if _, e := io.ReadFull(r, typ[:]); e != nil {
		return "EOF", nil
	}
	if typ[0] == 'N' || typ[0] == 'S' || typ[0] == 'G' {
		return string(typ[:]), nil
	}
	var h [4]byte
	if _, e := io.ReadFull(r, h[:]); e != nil {
		return "", e
	}
	n := binary.BigEndian.Uint32(h[:])
	if n < 4 || n > 65536 {
		return "", fmt.Errorf("bad response length %d", n)
	}
	b := make([]byte, n-4)
	if _, e := io.ReadFull(r, b); e != nil {
		return "", e
	}
	switch typ[0] {
	case 'E':
		f, ok := p.ParseErrorFields(b)
		if !ok {
			return "", fmt.Errorf("bad error %q", b)
		}
		return "E:" + f.Severity + ":" + f.Code, nil
	case 'R':
		if len(b) < 4 {
			return "", fmt.Errorf("short auth")
		}
		return fmt.Sprintf("R:%d", binary.BigEndian.Uint32(b)), nil
	default:
		return fmt.Sprintf("%c:%x", typ[0], b), nil
	}
}

type rw interface {
	io.Reader
	Write([]byte) (int, error)
}
type simRW struct{ pt.Transport }

func (s simRW) Write(b []byte) (int, error) { e := s.Transport.Write(b); return len(b), e }
func conversation(t *testing.T, c rw, kind string) string {
	t.Helper()
	var b []byte
	var prefix string
	switch kind {
	case "repeat-ssl":
		b = p.AppendSSLRequest(nil)
		if _, e := c.Write(b); e != nil {
			t.Fatal(e)
		}
		v, e := firstReply(c)
		if e != nil {
			t.Fatal(e)
		}
		prefix = v + " "
	case "ssl-with-body":
		b = append(p.AppendSSLRequest(nil), 0, 0, 0, 0)
		binary.BigEndian.PutUint32(b, 12)
	case "startup-missing-terminator":
		b = p.AppendStartupMessage(nil, []p.Param{{Name: "user", Value: "postgres"}, {Name: "database", Value: "postgres"}})
		b = b[:len(b)-1]
		binary.BigEndian.PutUint32(b, uint32(len(b)))
	default:
		t.Fatal(kind)
	}
	if _, e := c.Write(b); e != nil {
		t.Fatal(e)
	}
	v, e := firstReply(c)
	if e != nil {
		t.Fatal(e)
	}
	return prefix + v
}
func fidelityAttack(t *testing.T, kind string) {
	t.Helper()
	mark(t)
	path := "oracle-" + kind + ".json"
	var want string
	if os.Getenv("MG_TIER_R") == "1" {
		addr := os.Getenv("MG_PG_SCRAM_ADDR")
		if addr == "" {
			t.Fatal("MG_PG_SCRAM_ADDR required")
		}
		c, e := net.DialTimeout("tcp", addr, 3*time.Second)
		if e != nil {
			t.Fatal(e)
		}
		defer c.Close()
		if e = c.SetDeadline(time.Now().Add(3 * time.Second)); e != nil {
			t.Fatal(e)
		}
		want = conversation(t, c, kind)
		if os.Getenv("ADV_RECORD") == "1" {
			b, _ := json.MarshalIndent(map[string]string{"response": want, "server": os.Getenv("ADV_PG_VERSION"), "seed": fmt.Sprint(seed)}, "", "  ")
			if e := os.WriteFile(path, append(b, '\n'), 0644); e != nil {
				t.Fatal(e)
			}
		}
	} else {
		b, e := os.ReadFile(path)
		if e != nil {
			t.Fatal("run tier-r.sh to establish independent oracle: ", e)
		}
		var v map[string]string
		if e = json.Unmarshal(b, &v); e != nil {
			t.Fatal(e)
		}
		if !strings.HasPrefix(v["server"], "17.") {
			t.Fatal("oracle is not Postgres 17")
		}
		want = v["response"]
	}
	d, e := pt.NewSimFidelity(seed)
	if e != nil {
		t.Fatal(e)
	}
	c, e := d.Dial(pt.TargetSSLOff)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	got := conversation(t, simRW{c}, kind)
	if got != want {
		t.Fatalf("conversation=%s fake=%q Postgres17=%q", kind, got, want)
	}
}
func TestAdv_11_RepeatedSSLAfterRefusal(t *testing.T) { fidelityAttack(t, "repeat-ssl") }
func TestAdv_11_SSLRequestWithBody(t *testing.T)      { fidelityAttack(t, "ssl-with-body") }
func TestAdv_11_StartupMissingTerminator(t *testing.T) {
	fidelityAttack(t, "startup-missing-terminator")
}
