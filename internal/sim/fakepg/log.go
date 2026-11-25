package fakepg

import "github.com/nickstrad/mini-gateway/internal/sim"

type WriteRecord struct {
	Seq     int64
	StmtSeq int64
	Backend int64
	TxID    int64
	Op      Kind
	Key     string
	Value   string
	Text    string
	Tag     string
	At      sim.Time
}

type TxOutcome uint8

const (
	TxCommitted TxOutcome = iota + 1
	TxRolledBack
	TxLost
)

type TxRecord struct {
	TxID    int64
	Backend int64
	Outcome TxOutcome
	Writes  int64
	At      sim.Time
}

type StatementRecord struct {
	Seq     int64
	Backend int64
	TxID    int64
	Kind    Kind
	Tag     string
	Text    string
	Start   sim.Time
	End     sim.Time
	Code    string
	Aborted bool
	Rows    int64
}

type ring[T any] struct {
	cfg     LogConfig
	items   []T
	next    int
	total   int64
	dropped int64
}

func (r *ring[T]) add(v T) {
	r.total++
	if r.cfg.Mode == LogCountOnly {
		r.dropped++
		return
	}
	if len(r.items) < r.cfg.Cap {
		r.items = append(r.items, v)
		return
	}
	r.items[r.next] = v
	r.next = (r.next + 1) % len(r.items)
	r.dropped++
}

func (r *ring[T]) all() []T {
	out := make([]T, 0, len(r.items))
	out = append(out, r.items[r.next:]...)
	return append(out, r.items[:r.next]...)
}
