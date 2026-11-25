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
	if r.Fake.WriteLogDropped != 0 || r.Fake.StatementLogDropped != 0 {
		return errors.New("a log dropped entries; exact checks need full logs")
	}
	if r.Fake.WritesApplied == 0 {
		return errors.New("no write was applied")
	}
	writes := r.Server.WriteLog()
	if int64(len(writes)) != r.Fake.WritesApplied {
		return fmt.Errorf("write log has %d records, the counter says %d", len(writes), r.Fake.WritesApplied)
	}
	seen := make(map[int64]bool, len(writes))
	for _, w := range writes {
		if seen[w.StmtSeq] {
			return fmt.Errorf("statement %d applied twice: %+v", w.StmtSeq, w)
		}
		seen[w.StmtSeq] = true
	}
	return checkTransactions(r.Server.StatementLog(), writes)
}

func checkTransactions(stmts []fakepg.StatementRecord, writes []fakepg.WriteRecord) error {
	owner := map[int64]int64{}
	tagOf := map[int64]string{}
	ok := map[int64]bool{}
	for _, s := range stmts {
		if s.TxID != 0 {
			if b, found := owner[s.TxID]; found && b != s.Backend {
				return fmt.Errorf("transaction %d ran statements on backends %d and %d", s.TxID, b, s.Backend)
			}
			owner[s.TxID] = s.Backend
			if t, found := tagOf[s.TxID]; found && t != s.Tag {
				return fmt.Errorf("transaction %d holds statements of clients %q and %q", s.TxID, t, s.Tag)
			}
			tagOf[s.TxID] = s.Tag
		}
		if s.Code == "" && !s.Aborted && s.Kind.IsWrite() {
			ok[s.Seq] = true
		}
	}
	for _, w := range writes {
		if !ok[w.StmtSeq] {
			return fmt.Errorf("write of statement %d has no successful statement in the log", w.StmtSeq)
		}
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
