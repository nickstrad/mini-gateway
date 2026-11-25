// Phase 1 probe (evidence for PLAN.md sections on the backend interface and the codec).
// Not product code. Run: see agent_work/INDEX.md.
package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgproto3"
)

const user, password, database = "postgres", "probe-pw", "postgres"

func must(err error) {
	if err != nil {
		panic(err)
	}
}

// ---- Part A: sans-I/O use of pgproto3 (no Frontend/Backend reader) ----

// framer is the whole incremental framing layer a sans-I/O core needs:
// 1 type byte + 4 length bytes (length includes itself), then the body.
type framer struct{ buf []byte }

func (f *framer) feed(p []byte) { f.buf = append(f.buf, p...) }
func (f *framer) next() (typ byte, body []byte, ok bool) {
	if len(f.buf) < 5 {
		return 0, nil, false
	}
	n := int(binary.BigEndian.Uint32(f.buf[1:5]))
	if len(f.buf) < 1+n {
		return 0, nil, false
	}
	typ, body = f.buf[0], f.buf[5:1+n]
	f.buf = f.buf[1+n:]
	return typ, body, true
}

func partA() {
	fmt.Println("== A. pgproto3 message structs used sans-I/O (own framer, 1-byte feeds) ==")
	var wire []byte
	var err error
	wire, err = (&pgproto3.Query{String: "SELECT 1"}).Encode(wire)
	must(err)
	wire, err = (&pgproto3.Query{String: "BEGIN; SELECT 2"}).Encode(wire)
	must(err)
	wire, err = (&pgproto3.Terminate{}).Encode(wire)
	must(err)
	var f framer
	got := 0
	for i := range wire { // worst-case fragmentation: one byte per read
		f.feed(wire[i : i+1])
		for {
			typ, body, ok := f.next()
			if !ok {
				break
			}
			switch typ {
			case 'Q':
				var q pgproto3.Query
				must(q.Decode(body))
				fmt.Printf("  frontend msg %q decoded: Query %q\n", typ, q.String)
			case 'X':
				var t pgproto3.Terminate
				must(t.Decode(body))
				fmt.Printf("  frontend msg %q decoded: Terminate\n", typ)
			}
			got++
		}
	}
	fmt.Printf("  %d bytes fed one at a time -> %d messages; leftover %d bytes\n", len(wire), got, len(f.buf))

	var back []byte
	back, _ = (&pgproto3.ErrorResponse{Severity: "ERROR", Code: "53000", Message: "shed"}).Encode(back)
	back, _ = (&pgproto3.ReadyForQuery{TxStatus: 'I'}).Encode(back)
	var g framer
	g.feed(back)
	typ, body, _ := g.next()
	var e pgproto3.ErrorResponse
	must(e.Decode(body))
	typ2, body2, _ := g.next()
	var r pgproto3.ReadyForQuery
	must(r.Decode(body2))
	fmt.Printf("  backend msgs built and re-decoded: %q code=%s, %q TxStatus=%q (a proxy-made error reply is %d bytes)\n",
		typ, e.Code, typ2, r.TxStatus, len(back))
}

// ---- Part B: real Postgres ----

type conn struct {
	c   net.Conn
	f   *pgproto3.Frontend
	pid uint32
}

func scram(f *pgproto3.Frontend, mechs []string) {
	ok := false
	for _, m := range mechs {
		ok = ok || m == "SCRAM-SHA-256"
	}
	if !ok {
		panic("no SCRAM-SHA-256")
	}
	nb := make([]byte, 18)
	_, _ = rand.Read(nb)
	cnonce := base64.RawStdEncoding.EncodeToString(nb)
	bare := "n=,r=" + cnonce
	f.Send(&pgproto3.SASLInitialResponse{AuthMechanism: "SCRAM-SHA-256", Data: []byte("n,," + bare)})
	must(f.Flush())
	m, err := f.Receive()
	must(err)
	sc := m.(*pgproto3.AuthenticationSASLContinue)
	serverFirst := string(sc.Data)
	var nonce, salt string
	var iters int
	for _, kv := range strings.Split(serverFirst, ",") {
		switch kv[0] {
		case 'r':
			nonce = kv[2:]
		case 's':
			salt = kv[2:]
		case 'i':
			iters, _ = strconv.Atoi(kv[2:])
		}
	}
	saltB, err := base64.StdEncoding.DecodeString(salt)
	must(err)
	salted, err := pbkdf2.Key(sha256.New, password, saltB, iters, 32)
	must(err)
	mac := func(k []byte, s string) []byte { h := hmac.New(sha256.New, k); h.Write([]byte(s)); return h.Sum(nil) }
	clientKey := mac(salted, "Client Key")
	stored := sha256.Sum256(clientKey)
	finalNoProof := "c=biws,r=" + nonce
	authMsg := bare + "," + serverFirst + "," + finalNoProof
	sig := mac(stored[:], authMsg)
	proof := make([]byte, len(clientKey))
	for i := range proof {
		proof[i] = clientKey[i] ^ sig[i]
	}
	f.Send(&pgproto3.SASLResponse{Data: []byte(finalNoProof + ",p=" + base64.StdEncoding.EncodeToString(proof))})
	must(f.Flush())
	m, err = f.Receive()
	must(err)
	fin, isFin := m.(*pgproto3.AuthenticationSASLFinal)
	if !isFin {
		panic(fmt.Sprintf("SCRAM failed: %#v", m))
	}
	want := "v=" + base64.StdEncoding.EncodeToString(mac(mac(salted, "Server Key"), authMsg))
	if string(fin.Data) != want {
		panic("server signature mismatch")
	}
}

func dial(addr string, verbose bool) *conn {
	c, err := net.DialTimeout("tcp", addr, 3*time.Second)
	must(err)
	f := pgproto3.NewFrontend(c, c)
	f.Send(&pgproto3.StartupMessage{ProtocolVersion: pgproto3.ProtocolVersionNumber,
		Parameters: map[string]string{"user": user, "database": database}})
	must(f.Flush())
	cn := &conn{c: c, f: f}
	var seq []string
	nparam := 0
	for {
		m, err := f.Receive()
		must(err)
		switch v := m.(type) {
		case *pgproto3.AuthenticationSASL:
			seq = append(seq, fmt.Sprintf("AuthenticationSASL%v", v.AuthMechanisms))
			scram(f, v.AuthMechanisms)
			seq = append(seq, "SASLContinue", "SASLFinal(server signature verified)")
		case *pgproto3.AuthenticationOk:
			seq = append(seq, "AuthenticationOk")
		case *pgproto3.ParameterStatus:
			nparam++
		case *pgproto3.BackendKeyData:
			cn.pid = v.ProcessID
			seq = append(seq, "BackendKeyData")
		case *pgproto3.ReadyForQuery:
			seq = append(seq, fmt.Sprintf("ReadyForQuery(%q)", v.TxStatus))
			if verbose {
				fmt.Printf("  startup: %s; %d ParameterStatus messages\n", strings.Join(seq, " -> "), nparam)
			}
			return cn
		case *pgproto3.ErrorResponse:
			panic(v.Message)
		default:
			seq = append(seq, fmt.Sprintf("%T", m))
		}
	}
}

// drain reads until n ReadyForQuery messages were seen (or the stream ends) and
// returns a one-line summary per ReadyForQuery-delimited reply.
func (cn *conn) drain(n int) []string {
	var out []string
	var parts []string
	rows, bytesN := 0, 0
	flush := func(tail string) {
		if rows > 0 {
			parts = append(parts, fmt.Sprintf("DataRow x%d (%d bytes)", rows, bytesN))
		}
		out = append(out, strings.Join(append(parts, tail), ", "))
		parts, rows, bytesN = nil, 0, 0
	}
	for len(out) < n {
		_ = cn.c.SetReadDeadline(time.Now().Add(15 * time.Second))
		m, err := cn.f.Receive()
		if err != nil {
			e := "stream ended: " + err.Error()
			if err == io.ErrUnexpectedEOF || err == io.EOF || strings.Contains(err.Error(), "EOF") {
				e = "stream ended: EOF, no ReadyForQuery"
			}
			flush(e)
			return out
		}
		switch v := m.(type) {
		case *pgproto3.DataRow:
			rows++
			for _, c := range v.Values {
				bytesN += len(c)
			}
			if rows == 1 && len(v.Values) == 1 && len(v.Values[0]) < 40 {
				parts = append(parts, fmt.Sprintf("first row=%q", v.Values[0]))
			}
		case *pgproto3.RowDescription:
		case *pgproto3.CommandComplete:
			if rows > 0 {
				parts = append(parts, fmt.Sprintf("DataRow x%d (%d bytes)", rows, bytesN))
				rows, bytesN = 0, 0
			}
			parts = append(parts, "CommandComplete("+string(v.CommandTag)+")")
		case *pgproto3.ErrorResponse:
			parts = append(parts, fmt.Sprintf("ErrorResponse(%s %s: %s)", v.Severity, v.Code, v.Message))
		case *pgproto3.ParameterStatus:
			parts = append(parts, fmt.Sprintf("ParameterStatus(%s=%s)", v.Name, v.Value))
		case *pgproto3.EmptyQueryResponse:
			parts = append(parts, "EmptyQueryResponse")
		case *pgproto3.NoticeResponse:
			parts = append(parts, "NoticeResponse")
		case *pgproto3.ReadyForQuery:
			flush(fmt.Sprintf("ReadyForQuery(%q)", v.TxStatus))
		default:
			parts = append(parts, fmt.Sprintf("%T", m))
		}
	}
	return out
}

// send writes all the given Query messages in ONE write call.
func (cn *conn) send(qs ...string) {
	var b []byte
	for _, q := range qs {
		b, _ = (&pgproto3.Query{String: q}).Encode(b)
	}
	_, err := io.Copy(cn.c, bytes.NewReader(b))
	must(err)
}

func (cn *conn) run(title string, qs ...string) {
	fmt.Printf("-- %s\n", title)
	cn.send(qs...)
	for i, line := range cn.drain(len(qs)) {
		q := "(no further query)"
		if i < len(qs) {
			q = qs[i]
		}
		fmt.Printf("  Query %-34q => %s\n", q, line)
	}
}

func partB(addr string) {
	fmt.Println("== B. real Postgres ==")
	fmt.Println("-- B0 startup and SCRAM-SHA-256 done with the standard library (crypto/pbkdf2, hmac, sha256) plus pgproto3 message types")
	a := dial(addr, true)
	a.run("B1 one Query message, two statements, no COMMIT: is the connection free afterwards?", "BEGIN; SELECT 1")
	a.run("B2 an error inside the open transaction, then ROLLBACK", "SELECT 1/0", "ROLLBACK")
	a.run("B3 one Query message with two statements: one reply unit, two result sets", "SELECT 1; SELECT 2")
	a.run("B4 three Query messages in one write (pipelined, as if from three clients); the middle one fails",
		"SELECT 'a'", "SELECT 1/0", "SELECT 'c'")
	a.run("B5 two Query messages in one write; the first opens a transaction", "BEGIN", "SELECT txid_current() IS NOT NULL")
	a.run("B5 cleanup", "ROLLBACK")
	a.run("B6 session state outlives the transaction that set it", "SET statement_timeout = 1234", "SET application_name = 'tenant-a'", "SHOW statement_timeout")
	a.run("B6 cleanup with DISCARD ALL", "DISCARD ALL", "SHOW statement_timeout")
	a.run("B7 empty query string", "")
	a.run("B8 reply size is set by the data, not the request", "SELECT repeat('x', 100) FROM generate_series(1, 200000)")
	fmt.Println("-- B9 backend terminated while a query is in flight (second connection calls pg_terminate_backend)")
	a.send("SELECT pg_sleep(30)")
	time.Sleep(300 * time.Millisecond)
	b := dial(addr, false)
	b.run("   (killer connection)", fmt.Sprintf("SELECT pg_terminate_backend(%d)", a.pid))
	for _, line := range a.drain(1) {
		fmt.Printf("  Query %-34q => %s\n", "SELECT pg_sleep(30)", line)
	}
}

func partC(addr string) {
	fmt.Println("-- B10 a read already running does not see a write that commits while it runs (statement snapshot)")
	a, b := dial(addr, false), dial(addr, false)
	a.run("   setup", "DROP TABLE IF EXISTS mg_probe; CREATE TABLE mg_probe(v int); INSERT INTO mg_probe VALUES (1)")
	a.send("SELECT v, pg_sleep(1) IS NOT NULL FROM mg_probe") // the in-flight read ("leader")
	time.Sleep(200 * time.Millisecond)
	t0 := time.Now()
	b.run("   (writer, during the read)", "UPDATE mg_probe SET v = 2")
	fmt.Printf("  write acknowledged %d ms after it was sent, while the read was still running\n", time.Since(t0).Milliseconds())
	cn := a
	for {
		m, err := cn.f.Receive()
		must(err)
		if d, ok := m.(*pgproto3.DataRow); ok {
			fmt.Printf("  the in-flight read returned v=%s AFTER the write of v=2 was acknowledged\n", d.Values[0])
		}
		if _, ok := m.(*pgproto3.ReadyForQuery); ok {
			break
		}
	}
	b.run("   a read started after the write", "SELECT v FROM mg_probe", "DROP TABLE mg_probe")
}

func main() {
	partA()
	if len(os.Args) > 1 {
		partB(os.Args[1])
		partC(os.Args[1])
	}
}
