package fakepg

import "github.com/nickstrad/mini-gateway/internal/sim"

// WriteRecord records one committed (or autocommit) row-changing write applied
// to the kv table, linking it to its statement, backend, and transaction.
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

// TxOutcome records how a transaction ended.
type TxOutcome uint8

const (
	// TxCommitted means the transaction's pending writes were applied to the table.
	TxCommitted TxOutcome = iota + 1
	// TxRolledBack means the transaction was explicitly or implicitly rolled back.
	TxRolledBack
	// TxLost means the backend closed or reset while the transaction was open.
	TxLost
)

// TxRecord records the final outcome and pending-write count of one transaction.
type TxRecord struct {
	TxID    int64
	Backend int64
	Outcome TxOutcome
	Writes  int64
	At      sim.Time
}

// StatementRecord records one statement executed or aborted on a backend,
// including affected row count (Rows) and optional client comment Tag.
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
