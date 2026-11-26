package pgwire

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/nickstrad/mini-gateway/internal/work"
)

const rplGoldenFile = "testdata/reply-golden.txt"

// TestErrorRowsAreWellFormed proves every ErrorRows entry has a unique ID and
// counter key, valid SQLSTATE, consistent severity/close rules, and the five
// private pre-forward codes 53M01..53M05.
func TestErrorRowsAreWellFormed(t *testing.T) {
	ids := map[string]bool{}
	keys := map[string]bool{}
	private := 0
	for id := ErrRowID(0); id < ErrRowCount; id++ {
		r := id.Row()
		if r.ID == "" || ids[r.ID] {
			t.Errorf("row %d: empty or duplicate id %q", id, r.ID)
		}
		ids[r.ID] = true
		if keys[r.CounterKey()] {
			t.Errorf("row %s: duplicate counter key", r.ID)
		}
		keys[r.CounterKey()] = true
		if r.Situation == "" || r.Retry == "" || r.Reply == 0 || r.Conn == 0 || r.Chunk < 4 || r.Chunk > 16 {
			t.Errorf("row %s: incomplete: %+v", r.ID, r)
		}
		if r.HasMessage() {
			if !rplValidSQLState(r.Code) || r.Message == "" {
				t.Errorf("row %s: bad SQLSTATE %q or empty message", r.ID, r.Code)
			}
			if r.Code[2] == 'M' {
				private++
				if r.Code[:2] != "53" || r.Reached != work.No {
					t.Errorf("row %s: private code %s must be in class 53 and refused before forwarding", r.ID, r.Code)
				}
			}
		} else if r.Code != "" || r.Message != "" {
			t.Errorf("row %s sends no message but has a code or text", r.ID)
		}
		if r.Reply == ErrorRowFatal && r.Conn != ErrorRowConnClosed {
			t.Errorf("row %s: FATAL must close the connection", r.ID)
		}
		if r.Refusal != work.RefusalNone && (r.Reached != work.No || r.WriteApplied != work.No) {
			t.Errorf("row %s: a refusal must not have reached the database", r.ID)
		}
	}
	if private != 5 {
		t.Errorf("want 5 private codes 53M01..53M05, got %d", private)
	}
	for i, want := range []string{"53M01", "53M02", "53M03", "53M04", "53M05"} {
		if ErrorRows[i].Code != want {
			t.Errorf("row %d: code %s want %s", i, ErrorRows[i].Code, want)
		}
	}
	if ErrRowCount.Row().ID != "" {
		t.Error("an out-of-range id must give the zero row")
	}
}

func rplValidSQLState(c string) bool {
	if len(c) != 5 {
		return false
	}
	for i := 0; i < 5; i++ {
		ch := c[i]
		if !(ch >= '0' && ch <= '9') && !(ch >= 'A' && ch <= 'Z') {
			return false
		}
	}
	return true
}

// TestErrRowForEveryRefusal proves every non-none work.Refusal maps to an
// ErrorRowError row and RefusalNone does not map.
func TestErrRowForEveryRefusal(t *testing.T) {
	for r := work.RefusalShed; r < work.NumRefusals; r++ {
		id, ok := ErrRowFor(r)
		if !ok || ErrorRows[id].Refusal != r || ErrorRows[id].Reply != ErrorRowError {
			t.Errorf("refusal %s: no ERROR row (%d %v)", r, id, ok)
		}
	}
	if _, ok := ErrRowFor(work.RefusalNone); ok {
		t.Error("RefusalNone maps to a row")
	}
	if id, _ := ErrRowFor(work.RefusalUnsupported); id != ErrRowUnsupportedFeature {
		t.Errorf("unsupported maps to %s", ErrorRows[id].ID)
	}
}

func rplStatuses(r ErrorRow) []byte {
	if r.Reply == ErrorRowError {
		return []byte("ITE")
	}
	return []byte("I")
}

func rplBuildAll(t *testing.T) []string {
	var b ReplyBuilder
	var lines []string
	for id := ErrRowID(0); id < ErrRowCount; id++ {
		r := ErrorRows[id]
		if !r.HasMessage() {
			continue
		}
		for _, st := range rplStatuses(r) {
			out, _, err := b.Build(nil, id, st)
			if err != nil {
				t.Fatalf("%s %c: %v", r.ID, st, err)
			}
			lines = append(lines, fmt.Sprintf("%s %c %s", r.ID, st, hex.EncodeToString(out)))
		}
	}
	return lines
}

// TestReplyGoldenBytes proves the exact wire bytes of every message-bearing
// ErrorRows entry match testdata/reply-golden.txt across valid status bytes.
func TestReplyGoldenBytes(t *testing.T) {
	got := rplBuildAll(t)
	if os.Getenv("MG_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(rplGoldenFile, []byte(strings.Join(got, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.Open(rplGoldenFile)
	if err != nil {
		t.Fatalf("%v (MG_UPDATE_GOLDEN=1 writes it)", err)
	}
	defer f.Close()
	var want []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		want = append(want, sc.Text())
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("built replies differ from %s:\n got %v\nwant %v", rplGoldenFile, got, want)
	}
}

func rplHandShed(status byte) []byte {
	var body []byte
	for _, f := range []struct {
		code byte
		val  string
	}{{'S', "ERROR"}, {'V', "ERROR"}, {'C', "53M01"}, {'M', "mini-gateway: request shed by the admission queue"}, {'D', "mini-gateway error row: shed"}} {
		body = append(body, f.code)
		body = append(body, f.val...)
		body = append(body, 0)
	}
	body = append(body, 0)
	out := []byte{'E', 0, 0, 0, 0}
	binary.BigEndian.PutUint32(out[1:], uint32(4+len(body)))
	out = append(out, body...)
	return append(out, 'Z', 0, 0, 0, 5, status)
}

// TestReplyBytesMatchAHandEncoding proves ReplyBuilder's wire encoding for
// ErrRowShed matches an independent byte-by-byte encoding.
func TestReplyBytesMatchAHandEncoding(t *testing.T) {
	var b ReplyBuilder
	for _, st := range []byte("ITE") {
		out, act, err := b.Build(nil, ErrRowShed, st)
		if err != nil || act.End != ReplyEndReady || act.Close {
			t.Fatalf("%c: %v %+v", st, err, act)
		}
		if want := rplHandShed(st); string(out) != string(want) {
			t.Fatalf("%c:\n got %x\nwant %x", st, out, want)
		}
	}
}

type rplMsg struct {
	typ  byte
	body []byte
}

func rplSplit(t *testing.T, b []byte) []rplMsg {
	t.Helper()
	var out []rplMsg
	for len(b) > 0 {
		if len(b) < 5 {
			t.Fatalf("short message %x", b)
		}
		n := int(binary.BigEndian.Uint32(b[1:5]))
		if n < 4 || 1+n > len(b) {
			t.Fatalf("bad length %d in %x", n, b)
		}
		out = append(out, rplMsg{b[0], b[5 : 1+n]})
		b = b[1+n:]
	}
	return out
}

// TestReplyEveryRowIsWellFormedWithTheRightMarker proves every ErrorRows entry
// decodes via pgproto3, carries the right end marker and close action, and
// conserves ReplyBuilder counters across all status bytes.
func TestReplyEveryRowIsWellFormedWithTheRightMarker(t *testing.T) {
	var b ReplyBuilder
	for id := ErrRowID(0); id < ErrRowCount; id++ {
		r := ErrorRows[id]
		for _, st := range []byte("ITE") {
			before := b.Snapshot()
			out, act, err := b.Build([]byte("prefix"), id, st)
			after := b.Snapshot()
			built := int64(1)
			if r.Reply == ErrorRowPassThrough {
				built = 0
			}
			if after.ByRow[id] != before.ByRow[id]+built || after.Total != before.Total+built {
				t.Errorf("%s: counted %d built, want %d", r.ID, after.Total-before.Total, built)
			}
			switch r.Reply {
			case ErrorRowPassThrough:
				if err == nil || after.Failed != before.Failed+1 {
					t.Errorf("%s: pass-through must not be built", r.ID)
				}
				continue
			case ErrorRowCloseOnly:
				if err != nil || string(out) != "prefix" || !act.Close || act.End != ReplyEndNone {
					t.Errorf("%s: close-only row built %q %+v %v", r.ID, out, act, err)
				}
				continue
			}
			if err != nil {
				t.Fatalf("%s %c: %v", r.ID, st, err)
			}
			if string(out[:6]) != "prefix" {
				t.Fatalf("%s: dst not kept", r.ID)
			}
			msgs := rplSplit(t, out[6:])
			var e pgproto3.ErrorResponse
			if msgs[0].typ != 'E' || e.Decode(msgs[0].body) != nil {
				t.Fatalf("%s: first message is not an ErrorResponse", r.ID)
			}
			if e.Code != r.Code || e.Severity != r.Severity() || e.SeverityUnlocalized != r.Severity() || e.Message != r.Message || e.Detail != ReplyDetailPrefix+r.ID {
				t.Errorf("%s: decoded %+v", r.ID, e)
			}
			switch r.Reply {
			case ErrorRowError:
				var z pgproto3.ReadyForQuery
				if len(msgs) != 2 || msgs[1].typ != 'Z' || z.Decode(msgs[1].body) != nil || z.TxStatus != st || act.End != ReplyEndReady {
					t.Errorf("%s %c: end marker wrong: %v %+v", r.ID, st, msgs, act)
				}
				wantClose := r.Conn == ErrorRowConnClosedWhenIdle && st == 'I'
				if act.Close != wantClose {
					t.Errorf("%s %c: close %v", r.ID, st, act.Close)
				}
			case ErrorRowFatal:
				if len(msgs) != 1 || act.End != ReplyEndNone || !act.Close {
					t.Errorf("%s: FATAL must have no marker and close: %v %+v", r.ID, msgs, act)
				}
			case ErrorRowErrorUntilSync:
				if len(msgs) != 1 || act.End != ReplyEndSync || act.Close {
					t.Errorf("%s: until-sync row: %v %+v", r.ID, msgs, act)
				}
			}
		}
	}
	s := b.Snapshot()
	var sum int64
	for _, n := range s.ByRow {
		sum += n
	}
	if sum != s.Total {
		t.Errorf("conservation: total %d, sum by row %d", s.Total, sum)
	}
}

// TestReplyRefusesBadInput proves invalid row IDs, invalid status bytes, and
// pass-through rows return errors, leave dst untouched, and count only in Failed.
func TestReplyRefusesBadInput(t *testing.T) {
	var b ReplyBuilder
	if _, _, err := b.Build(nil, ErrRowCount, 'I'); err != ReplyErrBadRow {
		t.Errorf("bad row: %v", err)
	}
	for _, st := range []byte{0, 'X', 'i'} {
		if out, _, err := b.Build(nil, ErrRowShed, st); err != ReplyErrBadStatus || out != nil {
			t.Errorf("status %q: %v %x", st, err, out)
		}
	}
	if out, _, err := b.Build(nil, ErrRowPassThrough, 'I'); err != ReplyErrNoMessage || out != nil {
		t.Errorf("pass-through row: %v %x", err, out)
	}
	s := b.Snapshot()
	if s.Failed != 5 || s.Total != 0 || s.ByRow != ([ErrRowCount]int64{}) {
		t.Errorf("refused builds must count as failures only: %+v", s)
	}
	if _, _, err := b.Build(nil, ErrRowShed, 'I'); err != nil {
		t.Fatal(err)
	}
	if s := b.Snapshot(); s.Failed != 5 || s.Total != 1 || s.ByRow[ErrRowShed] != 1 {
		t.Errorf("a built reply must count once: %+v", s)
	}
	if _, err := ReplyReady(nil, 'Q'); err != ReplyErrBadStatus {
		t.Error("ReplyReady accepted a bad status")
	}
	if out, err := ReplyReady(nil, 'T'); err != nil || string(out) != "Z\x00\x00\x00\x05T" {
		t.Errorf("ReplyReady: %x %v", out, err)
	}
}

// TestSyncDiscardRule proves SyncDiscard's state transitions and counters
// across extended-protocol sequences, standalone Sync/FunctionCall, and Terminate.
func TestSyncDiscardRule(t *testing.T) {
	cases := []struct {
		in   string
		want []SyncDiscardStep
	}{
		{"Q", []SyncDiscardStep{SyncDiscardPass}},
		{"PBDES", []SyncDiscardStep{SyncDiscardRefuse, SyncDiscardDrop, SyncDiscardDrop, SyncDiscardDrop, SyncDiscardReady}},
		{"PQXQ", []SyncDiscardStep{SyncDiscardRefuse, SyncDiscardDrop, SyncDiscardPass, SyncDiscardPass}},
		{"S", []SyncDiscardStep{SyncDiscardRefuseAndReady}},
		{"FQ", []SyncDiscardStep{SyncDiscardRefuseAndReady, SyncDiscardPass}},
		{"HSQ", []SyncDiscardStep{SyncDiscardRefuse, SyncDiscardReady, SyncDiscardPass}},
		{"PSPS", []SyncDiscardStep{SyncDiscardRefuse, SyncDiscardReady, SyncDiscardRefuse, SyncDiscardReady}},
		{"CPS", []SyncDiscardStep{SyncDiscardRefuse, SyncDiscardDrop, SyncDiscardReady}},
	}
	for _, c := range cases {
		var s SyncDiscard
		for i := 0; i < len(c.in); i++ {
			if got := s.Step(c.in[i]); got != c.want[i] {
				t.Errorf("%s at %d (%c): got %d want %d", c.in, i, c.in[i], got, c.want[i])
			}
		}
		if s.Active() {
			t.Errorf("%s: still discarding at the end", c.in)
		}
	}
	var s SyncDiscard
	for _, m := range []byte("PBEDSFS") {
		s.Step(m)
	}
	if snap := s.Snapshot(); snap.Refused != 3 || snap.Discarded != 3 || snap.Synced != 1 {
		t.Errorf("counters: %+v", snap)
	}
}

// TestSyncDiscardRepliesEndWithReadyAfterSync proves an extended-protocol
// batch produces one ErrorResponse followed by one ReadyForQuery on Sync.
func TestSyncDiscardRepliesEndWithReadyAfterSync(t *testing.T) {
	var b ReplyBuilder
	var s SyncDiscard
	var out []byte
	for _, m := range []byte("PBES") {
		switch s.Step(m) {
		case SyncDiscardRefuse, SyncDiscardRefuseAndReady:
			var act ReplyAction
			var err error
			out, act, err = b.Build(out, ErrRowUnsupportedExtended, 'I')
			if err != nil || act.End != ReplyEndSync {
				t.Fatal(err)
			}
		case SyncDiscardReady:
			out, _ = ReplyReady(out, 'I')
		}
	}
	msgs := rplSplit(t, out)
	if len(msgs) != 2 || msgs[0].typ != 'E' || msgs[1].typ != 'Z' || string(msgs[1].body) != "I" {
		t.Fatalf("got %v", msgs)
	}
	if b.Snapshot().ByRow[ErrRowUnsupportedExtended] != 1 {
		t.Fatal("not counted once")
	}
}
