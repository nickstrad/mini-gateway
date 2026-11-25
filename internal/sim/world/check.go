package world

import (
	"errors"
	"fmt"
	"strings"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
)

func CheckWriteLog(r *Result) error {
	if r.Server == nil {
		return errors.New("no server")
	}
	if r.Fake.WriteLogDropped != 0 || r.Fake.StatementLogDropped != 0 || r.Fake.TxLogDropped != 0 {
		return errors.New("a log dropped entries; exact checks need full logs")
	}
	if r.Fake.WritesApplied == 0 {
		return errors.New("no write was applied")
	}
	return CheckWrites(r.Server.Config().Rows, r.Server.Rows(), r.Server.StatementLog(), r.Server.TransactionLog(), r.Server.WriteLog())
}

func CheckWrites(initial, final []fakepg.Row, stmts []fakepg.StatementRecord, txs []fakepg.TxRecord, writes []fakepg.WriteRecord) error {
	if err := checkTransactions(stmts, writes); err != nil {
		return err
	}
	if err := checkWriteSet(stmts, txs, writes); err != nil {
		return err
	}
	return checkReplay(initial, final, writes)
}

func checkWriteSet(stmts []fakepg.StatementRecord, txs []fakepg.TxRecord, writes []fakepg.WriteRecord) error {
	outcome := make(map[int64]fakepg.TxOutcome, len(txs))
	for _, t := range txs {
		if _, dup := outcome[t.TxID]; dup {
			return fmt.Errorf("transaction %d ended twice", t.TxID)
		}
		outcome[t.TxID] = t.Outcome
	}
	expected := map[int64]bool{}
	var order []int64
	for _, s := range stmts {
		if !s.Kind.IsWrite() || s.Code != "" || s.Aborted || s.Rows == 0 {
			continue
		}
		if s.TxID != 0 && outcome[s.TxID] != fakepg.TxCommitted {
			continue
		}
		expected[s.Seq] = true
		order = append(order, s.Seq)
	}
	seen := make(map[int64]bool, len(writes))
	for _, w := range writes {
		if seen[w.StmtSeq] {
			return fmt.Errorf("statement %d applied twice: %+v", w.StmtSeq, w)
		}
		seen[w.StmtSeq] = true
		if !expected[w.StmtSeq] {
			return fmt.Errorf("write of statement %d is not a successful write of a committed transaction: %+v", w.StmtSeq, w)
		}
	}
	for _, seq := range order {
		if !seen[seq] {
			return fmt.Errorf("statement %d wrote a row and committed but is missing from the write log", seq)
		}
	}
	return nil
}

func checkReplay(initial, final []fakepg.Row, writes []fakepg.WriteRecord) error {
	table := make(map[string]string, len(initial))
	for _, r := range initial {
		table[r.Key] = r.Value
	}
	prev := int64(0)
	for _, w := range writes {
		if w.Seq <= prev {
			return fmt.Errorf("write log out of order at sequence %d", w.Seq)
		}
		prev = w.Seq
		if w.Op == fakepg.KindDelete {
			delete(table, w.Key)
			continue
		}
		table[w.Key] = w.Value
	}
	if len(table) != len(final) {
		return fmt.Errorf("replaying the write log gives %d rows, the table has %d", len(table), len(final))
	}
	for _, r := range final {
		if v, ok := table[r.Key]; !ok || v != r.Value {
			return fmt.Errorf("replaying the write log gives %s=%q (present %v), the table has %q", r.Key, v, ok, r.Value)
		}
	}
	return nil
}

func checkTransactions(stmts []fakepg.StatementRecord, writes []fakepg.WriteRecord) error {
	owner := map[int64]int64{}
	tagOf := map[int64]string{}
	for _, s := range stmts {
		if s.TxID == 0 {
			continue
		}
		if b, found := owner[s.TxID]; found && b != s.Backend {
			return fmt.Errorf("transaction %d ran statements on backends %d and %d", s.TxID, b, s.Backend)
		}
		owner[s.TxID] = s.Backend
		if t, found := tagOf[s.TxID]; found && t != s.Tag {
			return fmt.Errorf("transaction %d holds statements of clients %q and %q", s.TxID, t, s.Tag)
		}
		tagOf[s.TxID] = s.Tag
	}
	for _, w := range writes {
		if w.Tag != "" && !strings.HasPrefix(w.Tag, "c") {
			return fmt.Errorf("write with tag %q", w.Tag)
		}
	}
	return nil
}

func Quantile(h obs.HistogramSnapshot, bp int64) int64 {
	if h.Count == 0 {
		return 0
	}
	bp = max(0, min(bp, obs.BasisPoints))
	rank := (h.Count*bp + obs.BasisPoints - 1) / obs.BasisPoints
	rank = max(rank, 1)
	seen := int64(0)
	for _, b := range h.Buckets {
		seen += b.Count
		if seen >= rank {
			return b.Hi
		}
	}
	return h.Hi
}
