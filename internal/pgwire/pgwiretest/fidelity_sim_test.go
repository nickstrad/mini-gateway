package pgwiretest

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
)

func encryptionAnswer(t *testing.T, c Transport) string {
	t.Helper()
	var b [1]byte
	if _, err := c.Read(b[:]); err != nil {
		return "EOF"
	}
	if b[0] != 'E' {
		return string(b[:])
	}
	var head [4]byte
	if _, err := readFull(c, head[:]); err != nil {
		t.Fatal(err)
	}
	n := int(head[0])<<24 | int(head[1])<<16 | int(head[2])<<8 | int(head[3])
	body := make([]byte, n-4)
	if _, err := readFull(c, body); err != nil {
		t.Fatal(err)
	}
	f, ok := pgwire.ParseErrorFields(body)
	if !ok {
		t.Fatalf("bad error %q", body)
	}
	return fmt.Sprintf("E:%s:%s:%s", f.Severity, f.Code, f.Message)
}

func readFull(c Transport, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		k, err := c.Read(b[n:])
		if err != nil {
			return n, err
		}
		n += k
	}
	return n, nil
}

func TestFakeRefusesARepeatedEncryptionRequestAsPostgresDoes(t *testing.T) {
	const sslAgain = "E:FATAL:0A000:unsupported frontend protocol 1234.5679: server supports 3.0 to 3.0"
	const gssAgain = "E:FATAL:0A000:unsupported frontend protocol 1234.5680: server supports 3.0 to 3.0"
	cases := []struct {
		name   string
		target string
		tls    bool
		seq    []uint32
		want   string
	}{
		{"ssl-twice-off", TargetSSLOff, false, []uint32{pgwire.CodeSSLRequest, pgwire.CodeSSLRequest}, "N " + sslAgain},
		{"ssl-after-upgrade", TargetSSLOn, true, []uint32{pgwire.CodeSSLRequest}, sslAgain},
		{"gss-after-upgrade", TargetSSLOn, true, []uint32{pgwire.CodeGSSRequest}, gssAgain},
		{"gss-twice", TargetSSLOff, false, []uint32{pgwire.CodeGSSRequest, pgwire.CodeGSSRequest}, "N " + gssAgain},
		{"ssl-gss-ssl-gss", TargetSSLOff, false, []uint32{pgwire.CodeSSLRequest, pgwire.CodeGSSRequest, pgwire.CodeSSLRequest, pgwire.CodeGSSRequest}, "N N N N"},
		{"gss-ssl-gss", TargetSSLOff, false, []uint32{pgwire.CodeGSSRequest, pgwire.CodeSSLRequest, pgwire.CodeGSSRequest}, "N N N"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, err := NewSimFidelity(3)
			if err != nil {
				t.Fatal(err)
			}
			c, err := d.Dial(tc.target)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if tc.tls {
				if err := c.Write(pgwire.AppendSSLRequest(nil)); err != nil {
					t.Fatal(err)
				}
				if got := encryptionAnswer(t, c); got != "S" {
					t.Fatalf("answer %s", got)
				}
				if err := c.StartTLS(); err != nil {
					t.Fatal(err)
				}
			}
			var got []string
			for _, code := range tc.seq {
				req := pgwire.AppendSSLRequest(nil)
				if code == pgwire.CodeGSSRequest {
					req = pgwire.AppendGSSRequest(nil)
				}
				if err := c.Write(req); err != nil {
					t.Fatal(err)
				}
				got = append(got, encryptionAnswer(t, c))
			}
			if strings.Join(got, " ") != tc.want {
				t.Fatalf("got %q, want %q", strings.Join(got, " "), tc.want)
			}
		})
	}
}
