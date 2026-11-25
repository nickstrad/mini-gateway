package pgwire

// CycleKind classifies an event observed on a database reply stream after startup.
type CycleKind uint8

// Database reply-stream event kinds produced by CycleTracker.
const (
	CycleMessage CycleKind = iota + 1
	CycleReady
	CycleError
	CycleParameterStatus
	CycleNotice
	CycleNotification
	CycleEndOfStream
	CycleViolation
)

var cycleNames = [...]string{"?", "message", "ready", "error", "parameter-status", "notice", "notification",
	"end-of-stream", "violation"}

// String returns the canonical hyphenated name of k.
func (k CycleKind) String() string {
	if int(k) < len(cycleNames) {
		return cycleNames[k]
	}
	return "?"
}

// CycleEvent describes one completed backend wire message or stream end.
// End is the cumulative byte offset immediately after the message, allowing
// callers to slice raw relay buffers on exact message boundaries.
type CycleEvent struct {
	Kind      CycleKind
	Type      byte
	Len       int
	End       int64
	Known     bool
	InCycle   bool
	Status    byte
	Severity  string
	Code      string
	Name      string
	Value     string
	Violation Violation
}

var backendTypes = func() [256]bool {
	var t [256]bool
	for _, c := range "123ACDEGHIKNRSTVWZcdnstv" {
		t[c] = true
	}
	return t
}()

// CycleTracker follows a database reply stream across arbitrary byte chunkings
// without buffering relayed row payloads. Only ReadyForQuery, ErrorResponse,
// ParameterStatus, NoticeResponse, and NotificationResponse bodies are buffered
// for inspection; all other bodies (including DataRow and unknown well-framed
// message types) are skipped in place.
type CycleTracker struct {
	st          *DatabaseWireSnapshot
	fr          Framer
	offset      int64
	outstanding int64
	closed      bool
}

// NewCycleTracker creates a CycleTracker whose inspected-message limit is
// maxMessage (defaulting to DefaultMaxMessage when <= 0).
func NewCycleTracker(maxMessage int, stats *WireStats) *CycleTracker {
	if stats == nil {
		stats = &WireStats{}
	}
	t := &CycleTracker{st: &stats.snap.Database}
	t.fr.Max = maxMessage
	// Skip every message body except the five control/async types we inspect so
	// large DataRow streams pass through with a 0-byte body buffer.
	for c := 0; c < 256; c++ {
		switch byte(c) {
		case MsgReadyForQuery, MsgErrorResponse, MsgParameterStatus, MsgNoticeResponse, MsgNotificationResponse:
		default:
			t.fr.SkipBodies(byte(c))
		}
	}
	return t
}

// QuerySent records that one query has been sent and awaits its ReadyForQuery.
func (t *CycleTracker) QuerySent() { t.outstanding++ }

// Outstanding returns the number of sent queries still awaiting ReadyForQuery.
func (t *CycleTracker) Outstanding() int64 { return t.outstanding }

// Closed reports whether the tracker stopped due to EOF or a protocol violation.
func (t *CycleTracker) Closed() bool { return t.closed }

// Offset returns total database bytes processed so far.
func (t *CycleTracker) Offset() int64 { return t.offset }

// BufferCap returns the underlying framer's body buffer capacity.
func (t *CycleTracker) BufferCap() int { return t.fr.BufferCap() }

// MidMessage reports whether the tracker holds an incomplete wire frame.
func (t *CycleTracker) MidMessage() bool { return t.fr.Pending() > 0 && !t.closed }

// Receive consumes database reply bytes, appending one CycleEvent per completed
// message or violation, and returns total bytes consumed.
func (t *CycleTracker) Receive(evs []CycleEvent, b []byte) ([]CycleEvent, int) {
	n := 0
	for n < len(b) && !t.closed {
		c, done, err := t.fr.Take(b[n:])
		n += c
		t.offset += int64(c)
		if err != nil {
			v := ViolationBadLength
			if isTooLarge(err) {
				v = ViolationTooLarge
				t.st.Oversized++
			}
			evs = t.violation(evs, v)
			break
		}
		if !done {
			break
		}
		t.st.MessagesIn++
		evs = append(evs, t.event(t.fr.Frame()))
		if evs[len(evs)-1].Kind == CycleViolation {
			t.closed = true
		}
	}
	t.st.BytesIn += int64(n)
	return evs, n
}

func (t *CycleTracker) violation(evs []CycleEvent, v Violation) []CycleEvent {
	t.st.Violations[v]++
	t.closed = true
	return append(evs, CycleEvent{Kind: CycleViolation, Violation: v, End: t.offset, InCycle: t.outstanding > 0})
}

func (t *CycleTracker) event(f Frame) CycleEvent {
	ev := CycleEvent{Kind: CycleMessage, Type: f.Type, Len: f.Len, End: t.offset, Known: backendTypes[f.Type],
		InCycle: t.outstanding > 0}
	if !ev.Known {
		t.st.UnknownPassThrough++
	}
	switch f.Type {
	case MsgReadyForQuery:
		if len(f.Body) != 1 || !validStatus(f.Body[0]) {
			t.st.Violations[ViolationBadMessage]++
			ev.Kind, ev.Violation = CycleViolation, ViolationBadMessage
			return ev
		}
		ev.Kind, ev.Status = CycleReady, f.Body[0]
		t.st.Replies++
		t.st.ReadyByStatus[statusIndex(f.Body[0])]++
		t.outstanding = max(0, t.outstanding-1)
	case MsgErrorResponse, MsgNoticeResponse:
		e, ok := ParseErrorFields(f.Body)
		if !ok {
			t.st.Violations[ViolationBadMessage]++
			ev.Kind, ev.Violation = CycleViolation, ViolationBadMessage
			return ev
		}
		ev.Severity, ev.Code = e.Severity, e.Code
		if f.Type == MsgNoticeResponse {
			ev.Kind = CycleNotice
			t.st.Notices++
			return ev
		}
		ev.Kind = CycleError
		t.st.Errors++
		if e.Fatal() {
			t.st.FatalErrors++
		}
	case MsgParameterStatus:
		name, rest, ok1 := cutCString(f.Body)
		value, rest, ok2 := cutCString(rest)
		if !ok1 || !ok2 || len(rest) != 0 {
			t.st.Violations[ViolationBadMessage]++
			ev.Kind, ev.Violation = CycleViolation, ViolationBadMessage
			return ev
		}
		ev.Kind, ev.Name, ev.Value = CycleParameterStatus, name, value
		t.st.ParameterStatus++
	case MsgNotificationResponse:
		ev.Kind = CycleNotification
		t.st.Notifications++
	}
	return ev
}

func validStatus(b byte) bool {
	return b == StatusIdle || b == StatusInTransaction || b == StatusFailed
}

func statusIndex(b byte) int {
	switch b {
	case StatusInTransaction:
		return 1
	case StatusFailed:
		return 2
	}
	return 0
}

// PeerClosed records end of stream from the database, flagging InCycle and
// incrementing EndWithoutMarker when a query was still open or a frame was
// truncated mid-message.
func (t *CycleTracker) PeerClosed(evs []CycleEvent) []CycleEvent {
	if t.closed {
		return evs
	}
	t.closed = true
	if t.outstanding == 0 && t.fr.Pending() == 0 {
		return append(evs, CycleEvent{Kind: CycleEndOfStream, End: t.offset, Known: true})
	}
	t.st.EndWithoutMarker++
	return append(evs, CycleEvent{Kind: CycleEndOfStream, End: t.offset, InCycle: true})
}

// FrontKind classifies a client-to-proxy message received during the query phase.
type FrontKind uint8

// Client query-phase event kinds produced by FrontendTracker.
const (
	FrontQuery FrontKind = iota + 1
	FrontTerminate
	FrontExtended
	FrontViolation
)

var frontNames = [...]string{"?", "query", "terminate", "extended", "violation"}

// String returns the canonical hyphenated name of k.
func (k FrontKind) String() string {
	if int(k) < len(frontNames) {
		return frontNames[k]
	}
	return "?"
}

// FrontEvent describes one client query-phase message or framing violation.
// For FrontQuery, Body is an independent copy of the NUL-terminated SQL payload.
type FrontEvent struct {
	Kind      FrontKind
	Type      byte
	Len       int
	End       int64
	Body      []byte
	Violation Violation
}

var extendedTypes = func() [256]bool {
	var t [256]bool
	for _, c := range []byte{MsgParse, MsgBind, MsgExecute, MsgDescribe, MsgClose, MsgFlush, MsgSync,
		MsgFunctionCall, MsgCopyData, MsgCopyDone, MsgCopyFail} {
		t[c] = true
	}
	return t
}()

// IsExtendedMessage reports whether t is an extended-protocol, function-call,
// or COPY frontend message type refused by the simple-query proxy.
func IsExtendedMessage(t byte) bool { return extendedTypes[t] }

// FrontendTracker frames client messages after ReadyForQuery, copying simple
// Query bodies and skipping extended-protocol message bodies while marking them
// as FrontExtended for the caller to refuse.
type FrontendTracker struct {
	st     *ClientWireSnapshot
	fr     Framer
	offset int64
	closed bool
}

// NewFrontendTracker creates a FrontendTracker with inspected-body cap maxMessage.
func NewFrontendTracker(maxMessage int, stats *WireStats) *FrontendTracker {
	if stats == nil {
		stats = &WireStats{}
	}
	t := &FrontendTracker{st: &stats.snap.Client}
	t.fr.Max = maxMessage
	for c := 0; c < 256; c++ {
		if extendedTypes[c] {
			t.fr.SkipBodies(byte(c))
		}
	}
	return t
}

// Closed reports whether the client sent Terminate or triggered a violation.
func (t *FrontendTracker) Closed() bool { return t.closed }

// Receive consumes client query-phase bytes, appending one FrontEvent per
// complete message or violation, and returns bytes consumed.
func (t *FrontendTracker) Receive(evs []FrontEvent, b []byte) ([]FrontEvent, int) {
	n := 0
	for n < len(b) && !t.closed {
		c, done, err := t.fr.Take(b[n:])
		n += c
		t.offset += int64(c)
		if err != nil {
			v := ViolationBadLength
			if isTooLarge(err) {
				v = ViolationTooLarge
				t.st.Oversized++
			}
			t.st.Violations[v]++
			t.closed = true
			evs = append(evs, FrontEvent{Kind: FrontViolation, Violation: v, End: t.offset})
			break
		}
		if !done {
			break
		}
		t.st.MessagesIn++
		f := t.fr.Frame()
		ev := FrontEvent{Type: f.Type, Len: f.Len, End: t.offset}
		switch {
		case f.Type == MsgQuery:
			if f.Len == 0 || f.Body[f.Len-1] != 0 {
				t.st.Violations[ViolationBadMessage]++
				ev.Kind, ev.Violation = FrontViolation, ViolationBadMessage
				t.closed = true
				break
			}
			ev.Kind, ev.Body = FrontQuery, append([]byte(nil), f.Body...)
			t.st.QueriesSeen++
		case f.Type == MsgTerminate:
			ev.Kind = FrontTerminate
			t.st.Terminated++
			t.closed = true
		case extendedTypes[f.Type]:
			ev.Kind = FrontExtended
			t.st.ExtendedRefused++
		default:
			t.st.Violations[ViolationUnknownMessage]++
			ev.Kind, ev.Violation = FrontViolation, ViolationUnknownMessage
			t.closed = true
		}
		evs = append(evs, ev)
	}
	t.st.BytesIn += int64(n)
	return evs, n
}

// NewReplyTracker is a compatibility alias for NewCycleTracker retained for
// external tests written before the ReplyTracker rename.
func NewReplyTracker(maxMessage int, stats *WireStats) *CycleTracker {
	return NewCycleTracker(maxMessage, stats)
}
