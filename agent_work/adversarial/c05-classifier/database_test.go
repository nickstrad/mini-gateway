package adv_test

import (
	"encoding/binary"
	"io"
	"net"
	"os"
	"testing"
	"time"

	p "github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
	"github.com/nickstrad/mini-gateway/internal/work"
)

// Native wire oracle: no pgproto import, no test corpus reuse.
func packet(kind byte, body []byte) []byte {
	b := make([]byte, 5+len(body))
	b[0] = kind
	binary.BigEndian.PutUint32(b[1:], uint32(4+len(body)))
	copy(b[5:], body)
	return b
}
func receive(t *testing.T, c net.Conn) (byte, []string) {
	t.Helper()
	var errors []string
	for {
		var h [5]byte
		if _, e := io.ReadFull(c, h[:]); e != nil {
			t.Fatal(e)
		}
		n := int(binary.BigEndian.Uint32(h[1:]))
		if n < 4 || n > 1<<20 {
			t.Fatalf("bad wire length %d", n)
		}
		body := make([]byte, n-4)
		if _, e := io.ReadFull(c, body); e != nil {
			t.Fatal(e)
		}
		if h[0] == 'E' {
			errors = append(errors, string(body))
		}
		if h[0] == 'Z' {
			return body[0], errors
		}
	}
}
func realQuery(t *testing.T, q string) (byte, []string) {
	t.Helper()
	c, e := net.DialTimeout("tcp", os.Getenv("MG_PG_ADDR"), 3*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	body := append([]byte{0, 3, 0, 0}, []byte("user\x00postgres\x00database\x00postgres\x00\x00")...)
	start := make([]byte, len(body)+4)
	binary.BigEndian.PutUint32(start, uint32(len(start)))
	copy(start[4:], body)
	if _, e = c.Write(start); e != nil {
		t.Fatal(e)
	}
	if s, errs := receive(t, c); s != 'I' || len(errs) > 0 {
		t.Fatalf("startup %c %v", s, errs)
	}
	if _, e = c.Write(packet('Q', append([]byte(q), 0))); e != nil {
		t.Fatal(e)
	}
	return receive(t, c)
}
func txOracle(t *testing.T, qs []string) {
	t.Helper()
	begin(t)
	if os.Getenv("MG_TIER_R") != "1" {
		t.Skip("MG_TIER_R=1 required; run run-tier-r.sh")
	}
	checked := 0
	for _, q := range qs {
		u := p.ClassifyQuery([]byte(q), false)
		s, errs := realQuery(t, q)
		t.Logf("query=%q prediction=%v status=%c errors=%v", q, u.MayLeaveOpen(), s, errs)
		if u.Class.Forwards() && u.MayLeaveOpen() == work.No {
			checked++
			if s != 'I' {
				t.Errorf("predicted no open transaction for %q but status=%c", q, s)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no negative prediction exercised")
	}
}
func TestAdv_6_ChainAndNoChain(t *testing.T) {
	txOracle(t, []string{"COMMIT WORK AND CHAIN", "ABORT WORK AND NO CHAIN", "END TRANSACTION AND NO CHAIN", "ROLLBACK TRANSACTION AND CHAIN"})
}
func TestAdv_6_FaultBeforeAndAfterBegin(t *testing.T) {
	txOracle(t, []string{"SELECT 1/0; BEGIN WORK", "BEGIN WORK; SELECT 1/0; END", "SELECT 1/0; END", "VALUES (1); START TRANSACTION READ ONLY; SELECT missing_adv_column"})
}
func TestAdv_6_UnicodeWhitespaceAndControl(t *testing.T) {
	txOracle(t, []string{"/*\n*/start\rtransaction; values (45)", "SAVEPOINT adv_no_tx", "ROLLBACK WORK TO SAVEPOINT adv_missing", "(VALUES (41)); COMMIT AND NO CHAIN"})
}
func fakeFilter(t *testing.T, qs []string) {
	t.Helper()
	begin(t)
	for _, ablate := range []bool{false, true} {
		s := sim.New(sim.Config{Seed: seed})
		n := simnet.New(s)
		srv, e := fakepg.New(n, fakepg.Config{Addr: "adv", Users: []string{"adv"}, Databases: []string{"adv"}})
		if e != nil {
			t.Fatal(e)
		}
		client := workload.NewProbe(n, "adv", simnet.Options{})
		client.Startup("adv", "adv")
		run := func() {
			t.Helper()
			if e := s.RunUntilQuiet(); e != nil {
				t.Fatal(e)
			}
		}
		run()
		if srv.Snapshot().StartupsCompleted != 1 {
			t.Fatal("dead fake")
		}
		var classifier p.Classifier
		var replies p.ReplyBuilder
		for _, q := range qs { // Decode our own Query frame before classification.
			frame := packet('Q', append([]byte(q), 0))
			u := classifier.Classify("adv", frame[5:len(frame)-1], false)
			if u.Class.Forwards() || ablate {
				client.Query(q)
			} else {
				b, _, e := replies.Build(nil, p.ErrRowUnsupportedFeature, 'I')
				if e != nil || len(frames(t, b)) != 2 {
					t.Fatal("bad refusal")
				}
			}
			run()
		}
		before := srv.Snapshot().StatementsStarted
		if !ablate && before != 0 {
			t.Errorf("reject-list statements reached fake: %d queries=%q", before, qs)
		}
		if ablate && before == 0 {
			t.Fatal("ablation did not reach fake")
		}
		if !ablate && replies.Snapshot().Total != int64(len(qs)) {
			t.Errorf("missing refusals: %+v", replies.Snapshot())
		}
		client.Query("SELECT 1")
		run()
		if srv.Snapshot().StatementsStarted != before+1 {
			t.Fatal("no recovery read")
		}
		client.Terminate()
		run()
		client.Close()
		run()
		if e := s.CheckQuiesce(); e != nil {
			t.Fatal(e)
		}
		if e := classifier.Snapshot().Check(); e != nil {
			t.Fatal(e)
		}
	}
}
func TestAdv_10_MultiStatementRejectAtomicity(t *testing.T) {
	fakeFilter(t, []string{"SELECT 41; SET application_name = 'adv'", "SELECT 42; LISTEN adv", "SELECT 43; DISCARD PLANS"})
}
func TestAdv_10_NestedFunctionReject(t *testing.T) {
	fakeFilter(t, []string{"SELECT coalesce(pg_try_advisory_lock(818), false)", "SELECT (SELECT set_config('application_name','adv',false))"})
}
func TestAdv_10_EscapeContinuationLeak(t *testing.T) {
	fakeFilter(t, []string{"SELECT E'ok'\n'\\'; SET application_name = 'adv_escape'; --'"})
}
