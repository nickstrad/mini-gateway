package pgwire

import "errors"

// ReplyDetailPrefix prefixes the stable row identifier in every proxy-built
// ErrorResponse Detail ('D') field.
const ReplyDetailPrefix = "mini-gateway error row: "

// Errors returned by ReplyBuilder.Build and ReplyReady on invalid inputs.
var (
	// ReplyErrBadRow indicates an ErrRowID outside [0, ErrRowCount).
	ReplyErrBadRow = errors.New("pgwire: no such error row")
	// ReplyErrBadStatus indicates a transaction status byte other than 'I', 'T', or 'E'.
	ReplyErrBadStatus = errors.New("pgwire: status byte must be I, T or E")
	// ReplyErrNoMessage indicates a row (such as ErrRowPassThrough) that the
	// proxy relays from Postgres rather than building itself.
	ReplyErrNoMessage = errors.New("pgwire: this error row sends no message")
)

// ReplyEnd tells the caller what end-of-reply marker ReplyBuilder.Build wrote
// or expects next.
type ReplyEnd uint8

const (
	// ReplyEndReady means a ReadyForQuery frame was appended to dst.
	ReplyEndReady ReplyEnd = iota + 1
	// ReplyEndNone means no ReadyForQuery was written (FATAL or close-only).
	ReplyEndNone
	// ReplyEndSync means the caller must discard client messages until Sync
	// and then append ReadyForQuery with ReplyReady.
	ReplyEndSync
)

// ReplyAction pairs the end-marker contract with whether to close the client
// connection after flushing the reply bytes.
type ReplyAction struct {
	End   ReplyEnd
	Close bool
}

// ReplySnapshot is a value copy of ReplyBuilder's fixed-size counters.
type ReplySnapshot struct {
	ByRow  [ErrRowCount]int64
	Total  int64
	Failed int64
}

// ReplyBuilder encodes proxy-made error replies from ErrorRows and counts
// built replies and refused build calls. The zero value is ready to use.
type ReplyBuilder struct {
	snap ReplySnapshot
}

// Snapshot returns a copy of the builder's cumulative counters.
func (b *ReplyBuilder) Snapshot() ReplySnapshot { return b.snap }

// ReplyValidStatus reports whether s is a valid Postgres ReadyForQuery status
// byte ('I' idle, 'T' in transaction, or 'E' failed transaction).
func ReplyValidStatus(s byte) bool { return s == 'I' || s == 'T' || s == 'E' }

// Build appends the wire reply for error row id with transaction status byte
// status to dst. On error dst is returned untouched and only Failed increments;
// ByRow and Total count only replies that succeeded.
func (b *ReplyBuilder) Build(dst []byte, id ErrRowID, status byte) ([]byte, ReplyAction, error) {
	out, act, err := rplBuild(dst, id, status)
	if err != nil {
		b.snap.Failed++
		return dst, ReplyAction{}, err
	}
	b.snap.ByRow[id]++
	b.snap.Total++
	return out, act, nil
}

func rplBuild(dst []byte, id ErrRowID, status byte) ([]byte, ReplyAction, error) {
	if id >= ErrRowCount {
		return dst, ReplyAction{}, ReplyErrBadRow
	}
	row := ErrorRows[id]
	switch row.Reply {
	case ErrorRowCloseOnly:
		return dst, ReplyAction{End: ReplyEndNone, Close: true}, nil
	case ErrorRowPassThrough:
		return dst, ReplyAction{}, ReplyErrNoMessage
	case ErrorRowError:
		if !ReplyValidStatus(status) {
			return dst, ReplyAction{}, ReplyErrBadStatus
		}
	}
	out, err := rplEncodeError(dst, row)
	if err != nil {
		return dst, ReplyAction{}, err
	}
	switch row.Reply {
	case ErrorRowFatal:
		// Postgres clients close immediately after FATAL and do not expect ReadyForQuery.
		return out, ReplyAction{End: ReplyEndNone, Close: true}, nil
	case ErrorRowErrorUntilSync:
		return out, ReplyAction{End: ReplyEndSync}, nil
	}
	out = AppendReadyForQuery(out, status)
	return out, ReplyAction{End: ReplyEndReady, Close: row.Conn == ErrorRowConnClosedWhenIdle && status == 'I'}, nil
}

// rplEncodeError writes an ErrorResponse with the local encoder rather than
// pgproto3 because internal/pgwire is determinism-checked.
func rplEncodeError(dst []byte, row ErrorRow) ([]byte, error) {
	sev := row.Severity()
	out, at := beginMessage(dst, MsgErrorResponse)
	for _, f := range []struct {
		code  byte
		value string
	}{{'S', sev}, {'V', sev}, {'C', row.Code}, {'M', row.Message}, {'D', ReplyDetailPrefix + row.ID}} {
		out = appendCString(append(out, f.code), f.value)
	}
	return finishMessage(append(out, 0), at), nil
}

// ReplyReady appends a ReadyForQuery frame carrying status after validating
// that status is 'I', 'T', or 'E'.
func ReplyReady(dst []byte, status byte) ([]byte, error) {
	if !ReplyValidStatus(status) {
		return dst, ReplyErrBadStatus
	}
	return AppendReadyForQuery(dst, status), nil
}

// SyncDiscardStep is the action a connection must take for one client message
// type when enforcing the extended-protocol discard-until-Sync rule.
type SyncDiscardStep uint8

const (
	// SyncDiscardPass lets the message continue to normal handling.
	SyncDiscardPass SyncDiscardStep = iota + 1
	// SyncDiscardRefuse sends ErrRowUnsupportedExtended without ReadyForQuery
	// and enters discard mode until Sync arrives.
	SyncDiscardRefuse
	// SyncDiscardRefuseAndReady sends ErrRowUnsupportedExtended followed
	// immediately by ReadyForQuery (used for standalone Sync or FunctionCall).
	SyncDiscardRefuseAndReady
	// SyncDiscardDrop drops the message silently while waiting for Sync.
	SyncDiscardDrop
	// SyncDiscardReady ends discard mode on Sync and sends ReadyForQuery.
	SyncDiscardReady
)

// SyncDiscardSnapshot is a value copy of SyncDiscard's counters.
type SyncDiscardSnapshot struct {
	Refused   int64
	Discarded int64
	Synced    int64
}

// SyncDiscard implements PostgreSQL's extended-protocol error recovery rule per
// client connection: once an extended message is refused, all following
// messages except Terminate are dropped until Sync restores ReadyForQuery.
type SyncDiscard struct {
	active bool
	snap   SyncDiscardSnapshot
}

// Active reports whether the connection is currently discarding messages until
// Sync arrives.
func (s *SyncDiscard) Active() bool { return s.active }

// Snapshot returns a copy of the state machine's counters.
func (s *SyncDiscard) Snapshot() SyncDiscardSnapshot { return s.snap }

// SyncDiscardExtended reports whether t is one of the seven extended-protocol
// frontend message types (Parse, Bind, Describe, Execute, Close, Flush, Sync).
func SyncDiscardExtended(t byte) bool {
	switch t {
	case 'P', 'B', 'D', 'E', 'C', 'H', 'S':
		return true
	}
	return false
}

// Step advances the discard-until-Sync state machine for one frontend message
// type byte t. Standalone Sync ('S') and legacy FunctionCall ('F') send both
// ErrorResponse and ReadyForQuery immediately without waiting for another Sync;
// Terminate ('X') always exits discard mode so the connection can close cleanly.
func (s *SyncDiscard) Step(t byte) SyncDiscardStep {
	if s.active {
		switch t {
		case 'S':
			s.active = false
			s.snap.Synced++
			return SyncDiscardReady
		case 'X':
			s.active = false
			return SyncDiscardPass
		}
		s.snap.Discarded++
		return SyncDiscardDrop
	}
	switch {
	case t == 'S', t == 'F':
		s.snap.Refused++
		return SyncDiscardRefuseAndReady
	case SyncDiscardExtended(t):
		s.active = true
		s.snap.Refused++
		return SyncDiscardRefuse
	}
	return SyncDiscardPass
}
