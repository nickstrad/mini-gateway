package pgwire

import (
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
)

func rowDesc() []byte {
	b, at := beginMessage(nil, MsgRowDescription)
	b = binary.BigEndian.AppendUint16(b, 1)
	b = appendCString(b, "x")
	b = append(b, make([]byte, 18)...)
	return finishMessage(b, at)
}

func dataRow(v string) []byte {
	b, at := beginMessage(nil, MsgDataRow)
	b = binary.BigEndian.AppendUint16(b, 1)
	b = binary.BigEndian.AppendUint32(b, uint32(len(v)))
	b = append(b, v...)
	return finishMessage(b, at)
}

func complete(tag string) []byte {
	b, at := beginMessage(nil, MsgCommandComplete)
	return finishMessage(appendCString(b, tag), at)
}

func cat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func describeCycle(evs []CycleEvent) string {
	var sb strings.Builder
	for _, e := range evs {
		fmt.Fprintf(&sb, "%v:%c:%d:%d:%v:%v:%c:%s:%s:%s=%s:%v\n", e.Kind, e.Type, e.Len, e.End, e.Known, e.InCycle, e.Status, e.Severity, e.Code, e.Name, e.Value, e.Violation)
	}
	return sb.String()
}

func runCycle(stream []byte, queries int, eof bool, chunk func([]byte) [][]byte) (string, *CycleTracker, *WireStats) {
	st := &WireStats{}
	tr := NewCycleTracker(0, st)
	for i := 0; i < queries; i++ {
		tr.QuerySent()
	}
	var evs []CycleEvent
	for _, piece := range chunk(stream) {
		evs, _ = tr.Receive(evs, piece)
	}
	if eof {
		evs = tr.PeerClosed(evs)
	}
	return describeCycle(evs), tr, st
}

func requireCycleChunkings(t *testing.T, stream []byte, queries int, eof bool) (string, WireSnapshot) {
	t.Helper()
	base, _, st := runCycle(stream, queries, eof, func(b []byte) [][]byte { return [][]byte{b} })
	got, _, _ := runCycle(stream, queries, eof, func(b []byte) [][]byte { return oneByte(0, b) })
	if got != base {
		t.Fatalf("one-byte delivery differs:\n%s\n%s", base, got)
	}
	for k := 1; k < len(stream); k++ {
		got, _, _ := runCycle(stream, queries, eof, func(b []byte) [][]byte { return [][]byte{b[:k], b[k:]} })
		if got != base {
			t.Fatalf("split at %d differs:\n%s\n%s", k, base, got)
		}
	}
	return base, st.Snapshot()
}

// TestCycleTrackerProbeShapes proves CycleTracker produces the right events,
// status byte, and end offset for every Phase 1 probe reply shape across every
// byte-boundary split.
func TestCycleTrackerProbeShapes(t *testing.T) {
	cases := []struct {
		name   string
		stream []byte
		eof    bool
		want   []string
		status byte
	}{
		{"single-result", cat(rowDesc(), dataRow("1"), complete("SELECT 1"), AppendReadyForQuery(nil, 'I')), false,
			[]string{"message:T", "message:D", "message:C", "ready:Z"}, 'I'},
		{"two-result-sets-one-message", cat(rowDesc(), dataRow("1"), complete("SELECT 1"), rowDesc(), dataRow("2"), complete("SELECT 1"), AppendReadyForQuery(nil, 'T')), false,
			[]string{"message:T", "message:D", "message:C", "message:T", "message:D", "message:C", "ready:Z"}, 'T'},
		{"error-then-ready", cat(AppendErrorResponse(nil, SeverityError, "42P01", "no table"), AppendReadyForQuery(nil, 'I')), false,
			[]string{"error:E", "ready:Z"}, 'I'},
		{"error-inside-a-transaction", cat(AppendErrorResponse(nil, SeverityError, "22012", "division by zero"), AppendReadyForQuery(nil, 'E')), false,
			[]string{"error:E", "ready:Z"}, 'E'},
		{"fatal-then-end-of-stream", cat(rowDesc(), AppendFatal(nil, "57P01", "terminating connection due to administrator command")), true,
			[]string{"message:T", "error:E", "end-of-stream:\x00"}, 0},
		{"unsolicited-parameter-status", cat(complete("SET"), AppendParameterStatus(nil, "application_name", "x"), AppendReadyForQuery(nil, 'I')), false,
			[]string{"message:C", "parameter-status:S", "ready:Z"}, 'I'},
		{"unknown-type-passed-through", cat(AppendMessageHeader(nil, 'q', 3), []byte("abc"), complete("SELECT 0"), AppendReadyForQuery(nil, 'I')), false,
			[]string{"message:q", "message:C", "ready:Z"}, 'I'},
		{"notice-and-notification", cat(noticeMsg(), AppendMessageHeader(nil, 'A', 4), []byte{0, 0, 0, 1}, AppendReadyForQuery(nil, 'I')), false,
			[]string{"notice:N", "notification:A", "ready:Z"}, 'I'},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, snap := requireCycleChunkings(t, tc.stream, 1, tc.eof)
			lines := strings.Split(strings.TrimSpace(out), "\n")
			if len(lines) != len(tc.want) {
				t.Fatalf("events:\n%s", out)
			}
			for i, w := range tc.want {
				if !strings.HasPrefix(lines[i], w) {
					t.Fatalf("event %d = %s, want %s", i, lines[i], w)
				}
			}
			if tc.status != 0 && !strings.Contains(lines[len(lines)-1], ":"+string(tc.status)+":") {
				t.Fatalf("status: %s", lines[len(lines)-1])
			}
			last := strings.Split(lines[len(lines)-1], ":")
			if last[3] != fmt.Sprint(len(tc.stream)) {
				t.Fatalf("end offset %s, stream %d", last[3], len(tc.stream))
			}
			if err := (WireSnapshot{Database: snap.Database}).Check(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func noticeMsg() []byte {
	b, at := beginMessage(nil, MsgNoticeResponse)
	b = append(b, 'S')
	b = appendCString(b, "NOTICE")
	b = append(b, 'C')
	b = appendCString(b, "00000")
	b = append(b, 0)
	return finishMessage(b, at)
}

// TestCycleTrackerFatalEndsTheStreamWithoutAMarker proves EOF after FATAL or
// mid-message counts as EndWithoutMarker, while clean EOF after Ready does not.
func TestCycleTrackerFatalEndsTheStreamWithoutAMarker(t *testing.T) {
	_, tr, st := runCycle(cat(rowDesc(), AppendFatal(nil, "57P01", "bye")), 1, true, func(b []byte) [][]byte { return [][]byte{b} })
	if !tr.Closed() || st.Snapshot().Database.EndWithoutMarker != 1 || st.Snapshot().Database.FatalErrors != 1 {
		t.Fatalf("%+v", st.Snapshot().Database)
	}
	out, _, st := runCycle(cat(rowDesc(), dataRow("1"), complete("SELECT 1"), AppendReadyForQuery(nil, 'I')), 1, true, func(b []byte) [][]byte { return [][]byte{b} })
	if st.Snapshot().Database.EndWithoutMarker != 0 || !strings.Contains(out, "end-of-stream") {
		t.Fatalf("a clean end counted as an end without marker: %s", out)
	}
	out, _, _ = runCycle(dataRow("abc")[:7], 0, true, func(b []byte) [][]byte { return [][]byte{b} })
	if !strings.Contains(out, "end-of-stream:\x00:0:7:false:true") {
		t.Fatalf("mid-message end: %s", out)
	}
}

// TestCycleTrackerStreamsManyRowsWithoutBuffering proves 200,000 DataRow
// messages stream through CycleTracker with a 0-byte body buffer.
func TestCycleTrackerStreamsManyRowsWithoutBuffering(t *testing.T) {
	const rows = 200_000
	st := &WireStats{}
	tr := NewCycleTracker(0, st)
	tr.QuerySent()
	row := dataRow(strings.Repeat("v", 90))
	var evs []CycleEvent
	feed := func(b []byte, rowsOnly bool) {
		for len(b) > 0 {
			piece := b[:min(len(b), 64<<10-3)]
			var n int
			evs, n = tr.Receive(evs[:0], piece)
			if n != len(piece) {
				t.Fatalf("consumed %d of %d", n, len(piece))
			}
			b = b[n:]
			if rowsOnly && tr.BufferCap() != 0 {
				t.Fatalf("tracker buffers %d bytes of row data", tr.BufferCap())
			}
		}
	}
	feed(rowDesc(), true)
	chunk := make([]byte, 0, 1000*len(row))
	for i := 0; i < 1000; i++ {
		chunk = append(chunk, row...)
	}
	for i := 0; i < rows/1000; i++ {
		feed(chunk, true)
	}
	feed(complete("SELECT 200000"), true)
	feed(AppendReadyForQuery(nil, 'I'), false)
	snap := st.Snapshot().Database
	if snap.MessagesIn != rows+3 || snap.Replies != 1 || tr.Outstanding() != 0 || snap.BytesIn != tr.Offset() {
		t.Fatalf("%+v", snap)
	}
	if len(evs) != 1 || evs[0].Kind != CycleReady {
		t.Fatalf("%v", evs)
	}
}

// TestCycleTrackerSurprises proves malformed lengths, bad control bodies, and
// oversized relayed headers close the tracker as violations across all splits.
func TestCycleTrackerSurprises(t *testing.T) {
	for _, stream := range [][]byte{{'D', 0, 0, 0, 1}, {'Z', 0, 0, 0, 5, 'Q'}, cat(AppendMessageHeader(nil, 'E', 2), []byte{'S', 'x'}),
		cat(AppendMessageHeader(nil, 'S', 2), []byte{'a', 0}), {'Z', 0x40, 0, 0, 0}} {
		out, snap := requireCycleChunkings(t, stream, 1, false)
		if !strings.Contains(out, "violation") || sumInt64(snap.Database.Violations[:]) != 1 {
			t.Fatalf("%x: %s", stream, out)
		}
	}
	big := cat(AppendMessageHeader(nil, 'D', MaxRelayedMessage+1))
	_, tr, st := runCycle(big, 1, false, func(b []byte) [][]byte { return [][]byte{b} })
	if !tr.Closed() || st.Snapshot().Database.Oversized != 1 {
		t.Fatal("oversized relayed message not refused from its header")
	}
}

// TestCycleTrackerReportsUnsolicitedMessagesOutsideACycle proves an unsolicited
// ParameterStatus outside an active query is surfaced with InCycle=false.
func TestCycleTrackerReportsUnsolicitedMessagesOutsideACycle(t *testing.T) {
	out, _ := requireCycleChunkings(t, AppendParameterStatus(nil, "application_name", "fakepg-surprise-1"), 0, false)
	if !strings.HasPrefix(out, "parameter-status:S:") || !strings.Contains(out, ":false:") || !strings.Contains(out, "application_name=fakepg-surprise-1") {
		t.Fatalf("%s", out)
	}
}

func describeFront(evs []FrontEvent) string {
	var sb strings.Builder
	for _, e := range evs {
		fmt.Fprintf(&sb, "%v:%c:%d:%d:%q:%v\n", e.Kind, e.Type, e.Len, e.End, e.Body, e.Violation)
	}
	return sb.String()
}

func runFront(stream []byte, chunk func([]byte) [][]byte) (string, *WireStats) {
	st := &WireStats{}
	tr := NewFrontendTracker(0, st)
	var out strings.Builder
	for _, piece := range chunk(stream) {
		evs, _ := tr.Receive(nil, piece)
		out.WriteString(describeFront(evs))
	}
	return out.String(), st
}

// TestFrontendTrackerRecognisesQueriesTerminateAndExtended proves
// FrontendTracker classifies Query, Terminate, all 11 extended-protocol types,
// and violations identically under every byte chunking.
func TestFrontendTrackerRecognisesQueriesTerminateAndExtended(t *testing.T) {
	ext := cat(AppendMessageHeader(nil, MsgParse, 3), []byte("s\x00\x00"), AppendMessageHeader(nil, MsgBind, 0), AppendMessageHeader(nil, MsgExecute, 0),
		AppendMessageHeader(nil, MsgDescribe, 0), AppendMessageHeader(nil, MsgClose, 0), AppendMessageHeader(nil, MsgFlush, 0),
		AppendMessageHeader(nil, MsgSync, 0), AppendMessageHeader(nil, MsgCopyData, 1), []byte{1}, AppendMessageHeader(nil, MsgCopyDone, 0),
		AppendMessageHeader(nil, MsgCopyFail, 0), AppendMessageHeader(nil, MsgFunctionCall, 0))
	stream := cat(AppendQuery(nil, []byte("SELECT 1")), ext, AppendQuery(nil, []byte("SELECT 2")), AppendTerminate(nil), []byte("ignored"))
	base, st := runFront(stream, func(b []byte) [][]byte { return [][]byte{b} })
	one, _ := runFront(stream, func(b []byte) [][]byte { return oneByte(0, b) })
	if one != base {
		t.Fatalf("chunkings differ\n%s\n%s", base, one)
	}
	for k := 1; k < len(stream); k++ {
		if got, _ := runFront(stream, func(b []byte) [][]byte { return [][]byte{b[:k], b[k:]} }); got != base {
			t.Fatalf("split %d differs", k)
		}
	}
	lines := strings.Split(strings.TrimSpace(base), "\n")
	if len(lines) != 14 || !strings.HasPrefix(lines[0], `query:Q:9:14:"SELECT 1\x00"`) || !strings.HasPrefix(lines[13], "terminate:X") {
		t.Fatalf("%s", base)
	}
	for _, l := range lines[1:12] {
		if !strings.HasPrefix(l, "extended:") {
			t.Fatalf("not extended: %s", l)
		}
	}
	s := st.Snapshot().Client
	if s.QueriesSeen != 2 || s.ExtendedRefused != 11 || s.Terminated != 1 {
		t.Fatalf("%+v", s)
	}
	for _, bad := range [][]byte{AppendMessageHeader(nil, 'p', 0), AppendMessageHeader(nil, 'Q', 0), cat(AppendMessageHeader(nil, 'Q', 1), []byte{'x'}), {'Q', 0, 0, 0, 2}} {
		out, _ := runFront(bad, func(b []byte) [][]byte { return [][]byte{b} })
		if !strings.HasPrefix(out, "violation") {
			t.Fatalf("%x: %s", bad, out)
		}
	}
}

// TestCycleTrackerPassesARowLargerThanItsMessageLimit proves a skipped DataRow
// larger than the inspected-message cap passes without buffering or error.
func TestCycleTrackerPassesARowLargerThanItsMessageLimit(t *testing.T) {
	const limit = 128
	rows := cat(rowDesc(), dataRow(strings.Repeat("r", 4096)), complete("SELECT 1"))
	stream := cat(rows, AppendReadyForQuery(nil, StatusIdle))
	for _, chunk := range []func(int, []byte) [][]byte{whole, oneByte} {
		st := &WireStats{}
		tr := NewCycleTracker(limit, st)
		tr.QuerySent()
		var evs []CycleEvent
		fed := 0
		for _, piece := range chunk(0, stream) {
			evs, _ = tr.Receive(evs, piece)
			fed += len(piece)
			if fed <= len(rows) && tr.BufferCap() != 0 {
				t.Fatalf("tracker buffers %d bytes of row data", tr.BufferCap())
			}
		}
		snap := st.Snapshot().Database
		if tr.Closed() || snap.Oversized != 0 || snap.Replies != 1 || len(evs) != 4 || evs[3].Kind != CycleReady {
			t.Fatalf("%s %+v", describeCycle(evs), snap)
		}
	}
}
