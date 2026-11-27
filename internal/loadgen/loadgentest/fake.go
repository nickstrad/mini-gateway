package loadgentest

import (
	"bufio"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

type Options struct {
	Delay      time.Duration
	StallFrom  time.Duration
	StallUntil time.Duration
	NoRead     bool
}

type Fake struct {
	t     testing.TB
	ln    net.Listener
	o     Options
	start time.Time
	mu    sync.Mutex
	seen  map[string][]string
	wg    sync.WaitGroup
	conns []net.Conn
	done  chan struct{}
}

func Start(t testing.TB, o Options) *Fake {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &Fake{t: t, ln: ln, o: o, start: time.Now(), seen: map[string][]string{}, done: make(chan struct{})}
	f.wg.Add(1)
	go f.accept()
	t.Cleanup(f.close)
	return f
}

func (f *Fake) Addr() string { return f.ln.Addr().String() }

func (f *Fake) close() {
	select {
	case <-f.done:
		return
	default:
	}
	close(f.done)
	_ = f.ln.Close()
	f.mu.Lock()
	for _, c := range f.conns {
		_ = c.Close()
	}
	f.mu.Unlock()
	f.wg.Wait()
}

func (f *Fake) Statements(user string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.seen[user]...)
}

func (f *Fake) accept() {
	defer f.wg.Done()
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return
		}
		f.mu.Lock()
		f.conns = append(f.conns, c)
		f.mu.Unlock()
		f.wg.Add(1)
		go func() {
			defer f.wg.Done()
			defer c.Close()
			f.serve(c)
		}()
	}
}

func msg(typ byte, body []byte) []byte {
	b := []byte{typ, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(b[1:], uint32(len(body)+4))
	return append(b, body...)
}

func cstr(s string) []byte { return append([]byte(s), 0) }

func errorMsg(code, text string) []byte {
	var body []byte
	body = append(body, 'S')
	body = append(body, cstr("ERROR")...)
	body = append(body, 'C')
	body = append(body, cstr(code)...)
	body = append(body, 'M')
	body = append(body, cstr(text)...)
	return msg('E', append(body, 0))
}

func ready() []byte { return msg('Z', []byte{'I'}) }

func reply(sql string) []byte {
	switch {
	case strings.HasPrefix(sql, "SELECT v FROM kv"):
		var desc []byte
		desc = binary.BigEndian.AppendUint16(desc, 1)
		desc = append(desc, cstr("v")...)
		desc = append(desc, make([]byte, 18)...)
		var row []byte
		row = binary.BigEndian.AppendUint16(row, 1)
		row = binary.BigEndian.AppendUint32(row, 1)
		row = append(row, 'x')
		out := append(msg('T', desc), msg('D', row)...)
		return append(append(out, msg('C', cstr("SELECT 1"))...), ready()...)
	case strings.HasPrefix(sql, "UPDATE kv"):
		return append(msg('C', cstr("UPDATE 1")), ready()...)
	case strings.HasPrefix(sql, "INSERT INTO kv"):
		return append(errorMsg("23505", "duplicate key value violates unique constraint \"kv_pkey\""), ready()...)
	case strings.Contains(sql, "mg_load_missing"):
		return append(errorMsg("42P01", "relation \"mg_load_missing\" does not exist"), ready()...)
	case strings.Contains(sql, "/ 0"):
		return append(errorMsg("22012", "division by zero"), ready()...)
	case strings.HasPrefix(sql, "LISTEN"):
		return append(errorMsg("0A000", "mini-gateway: feature not supported by a transaction-pooling proxy"), ready()...)
	}
	return append(errorMsg("42601", "syntax error"), ready()...)
}

func (f *Fake) serve(c net.Conn) {
	r := bufio.NewReader(c)
	user, ok := startup(r, c)
	if !ok {
		return
	}
	if _, err := c.Write(append(msg('R', []byte{0, 0, 0, 0}), ready()...)); err != nil {
		return
	}
	if f.o.NoRead {
		<-f.done
		return
	}
	for {
		typ, body, err := readMsg(r)
		if err != nil || typ == 'X' {
			return
		}
		if typ != 'Q' {
			return
		}
		sql := strings.TrimSuffix(string(body), "\x00")
		f.mu.Lock()
		f.seen[user] = append(f.seen[user], sql)
		f.mu.Unlock()
		f.hold()
		if _, err := c.Write(reply(sql)); err != nil {
			return
		}
	}
}

func (f *Fake) hold() {
	if f.o.Delay > 0 {
		time.Sleep(f.o.Delay)
	}
	if f.o.StallUntil == 0 {
		return
	}
	since := time.Since(f.start)
	if since >= f.o.StallFrom && since < f.o.StallUntil {
		time.Sleep(f.o.StallUntil - since)
	}
}

func readMsg(r *bufio.Reader) (byte, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	body := make([]byte, binary.BigEndian.Uint32(hdr[1:])-4)
	_, err := io.ReadFull(r, body)
	return hdr[0], body, err
}

func startup(r *bufio.Reader, c net.Conn) (string, bool) {
	for {
		var n [4]byte
		if _, err := io.ReadFull(r, n[:]); err != nil {
			return "", false
		}
		body := make([]byte, binary.BigEndian.Uint32(n[:])-4)
		if _, err := io.ReadFull(r, body); err != nil {
			return "", false
		}
		if binary.BigEndian.Uint32(body) == 80877103 {
			if _, err := c.Write([]byte{'N'}); err != nil {
				return "", false
			}
			continue
		}
		parts := strings.Split(string(body[4:]), "\x00")
		for i := 0; i+1 < len(parts); i += 2 {
			if parts[i] == "user" {
				return parts[i+1], true
			}
		}
		return "", true
	}
}
