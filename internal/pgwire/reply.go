package pgwire

import "errors"

const ReplyDetailPrefix = "mini-gateway error row: "

var (
	ReplyErrBadRow    = errors.New("pgwire: no such error row")
	ReplyErrBadStatus = errors.New("pgwire: status byte must be I, T or E")
	ReplyErrNoMessage = errors.New("pgwire: this error row sends no message")
)

type ReplyEnd uint8

const (
	ReplyEndReady ReplyEnd = iota + 1
	ReplyEndNone
	ReplyEndSync
)

type ReplyAction struct {
	End   ReplyEnd
	Close bool
}

type ReplySnapshot struct {
	ByRow  [ErrRowCount]int64
	Total  int64
	Failed int64
}

type ReplyBuilder struct {
	snap ReplySnapshot
}

func (b *ReplyBuilder) Snapshot() ReplySnapshot { return b.snap }

func ReplyValidStatus(s byte) bool { return s == 'I' || s == 'T' || s == 'E' }

func (b *ReplyBuilder) Build(dst []byte, id ErrRowID, status byte) ([]byte, ReplyAction, error) {
	if id >= ErrRowCount {
		b.snap.Failed++
		return dst, ReplyAction{}, ReplyErrBadRow
	}
	row := ErrorRows[id]
	b.snap.ByRow[id]++
	b.snap.Total++
	switch row.Reply {
	case ErrorRowCloseOnly:
		return dst, ReplyAction{End: ReplyEndNone, Close: true}, nil
	case ErrorRowPassThrough:
		b.snap.Failed++
		return dst, ReplyAction{}, ReplyErrNoMessage
	case ErrorRowError:
		if !ReplyValidStatus(status) {
			b.snap.Failed++
			return dst, ReplyAction{}, ReplyErrBadStatus
		}
	}
	out, err := rplEncodeError(dst, row)
	if err != nil {
		b.snap.Failed++
		return dst, ReplyAction{}, err
	}
	switch row.Reply {
	case ErrorRowFatal:
		return out, ReplyAction{End: ReplyEndNone, Close: true}, nil
	case ErrorRowErrorUntilSync:
		return out, ReplyAction{End: ReplyEndSync}, nil
	}
	out = AppendReadyForQuery(out, status)
	return out, ReplyAction{End: ReplyEndReady, Close: row.Conn == ErrorRowConnClosedWhenIdle && status == 'I'}, nil
}

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

func ReplyReady(dst []byte, status byte) ([]byte, error) {
	if !ReplyValidStatus(status) {
		return dst, ReplyErrBadStatus
	}
	return AppendReadyForQuery(dst, status), nil
}

type SyncDiscardStep uint8

const (
	SyncDiscardPass SyncDiscardStep = iota + 1
	SyncDiscardRefuse
	SyncDiscardRefuseAndReady
	SyncDiscardDrop
	SyncDiscardReady
)

type SyncDiscardSnapshot struct {
	Refused   int64
	Discarded int64
	Synced    int64
}

type SyncDiscard struct {
	active bool
	snap   SyncDiscardSnapshot
}

func (s *SyncDiscard) Active() bool                  { return s.active }
func (s *SyncDiscard) Snapshot() SyncDiscardSnapshot { return s.snap }

func SyncDiscardExtended(t byte) bool {
	switch t {
	case 'P', 'B', 'D', 'E', 'C', 'H', 'S':
		return true
	}
	return false
}

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
