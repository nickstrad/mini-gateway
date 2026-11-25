package pgwire

type CycleKind uint8

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

func (k CycleKind) String() string {
	if int(k) < len(cycleNames) {
		return cycleNames[k]
	}
	return "?"
}

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

type ReplyTracker struct {
	st          *DatabaseWireSnapshot
	fr          Framer
	offset      int64
	outstanding int64
	closed      bool
	midMessage  bool
}

func NewReplyTracker(maxMessage int, stats *WireStats) *ReplyTracker {
	if stats == nil {
		stats = &WireStats{}
	}
	t := &ReplyTracker{st: &stats.snap.Database}
	t.fr.Max = maxMessage
	for c := 0; c < 256; c++ {
		switch byte(c) {
		case MsgReadyForQuery, MsgErrorResponse, MsgParameterStatus, MsgNoticeResponse, MsgNotificationResponse:
		default:
			t.fr.SkipBodies(byte(c))
		}
	}
	return t
}

func (t *ReplyTracker) QuerySent()         { t.outstanding++ }
func (t *ReplyTracker) Outstanding() int64 { return t.outstanding }
func (t *ReplyTracker) Closed() bool       { return t.closed }
func (t *ReplyTracker) Offset() int64      { return t.offset }
func (t *ReplyTracker) BufferCap() int     { return t.fr.BufferCap() }
func (t *ReplyTracker) MidMessage() bool   { return t.fr.Pending() > 0 && !t.closed }

func (t *ReplyTracker) Receive(evs []CycleEvent, b []byte) ([]CycleEvent, int) {
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

func (t *ReplyTracker) violation(evs []CycleEvent, v Violation) []CycleEvent {
	t.st.Violations[v]++
	t.closed = true
	return append(evs, CycleEvent{Kind: CycleViolation, Violation: v, End: t.offset, InCycle: t.outstanding > 0})
}

func (t *ReplyTracker) event(f Frame) CycleEvent {
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

func (t *ReplyTracker) PeerClosed(evs []CycleEvent) []CycleEvent {
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

type FrontKind uint8

const (
	FrontQuery FrontKind = iota + 1
	FrontTerminate
	FrontExtended
	FrontViolation
)

var frontNames = [...]string{"?", "query", "terminate", "extended", "violation"}

func (k FrontKind) String() string {
	if int(k) < len(frontNames) {
		return frontNames[k]
	}
	return "?"
}

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

func IsExtendedMessage(t byte) bool { return extendedTypes[t] }

type FrontendTracker struct {
	st     *ClientWireSnapshot
	fr     Framer
	offset int64
	closed bool
}

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

func (t *FrontendTracker) Closed() bool { return t.closed }

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
			ev.Kind, ev.Body = FrontQuery, f.Body
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
