package world_test

import (
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/world"
)

type writeCase struct {
	initial []fakepg.Row
	final   []fakepg.Row
	stmts   []fakepg.StatementRecord
	txs     []fakepg.TxRecord
	writes  []fakepg.WriteRecord
}

func baseWriteCase() writeCase {
	return writeCase{
		initial: []fakepg.Row{{Key: "a", Value: "0"}},
		final:   []fakepg.Row{{Key: "a", Value: "2"}, {Key: "b", Value: "1"}},
		stmts: []fakepg.StatementRecord{
			{Seq: 1, Backend: 1, Kind: fakepg.KindUpdate, Rows: 1},
			{Seq: 2, Backend: 2, TxID: 1, Kind: fakepg.KindBegin},
			{Seq: 3, Backend: 2, TxID: 1, Kind: fakepg.KindInsert, Rows: 1},
			{Seq: 4, Backend: 2, TxID: 1, Kind: fakepg.KindUpdate, Rows: 1},
			{Seq: 5, Backend: 2, TxID: 1, Kind: fakepg.KindCommit},
			{Seq: 6, Backend: 1, TxID: 2, Kind: fakepg.KindUpdate, Rows: 1},
			{Seq: 7, Backend: 1, TxID: 2, Kind: fakepg.KindRollback},
			{Seq: 8, Backend: 1, Kind: fakepg.KindUpdate, Rows: 0},
			{Seq: 9, Backend: 1, Kind: fakepg.KindInsert, Code: "23505"},
			{Seq: 10, Backend: 3, TxID: 3, Kind: fakepg.KindDelete, Rows: 1},
		},
		txs: []fakepg.TxRecord{
			{TxID: 1, Backend: 2, Outcome: fakepg.TxCommitted, Writes: 2},
			{TxID: 2, Backend: 1, Outcome: fakepg.TxRolledBack, Writes: 1},
			{TxID: 3, Backend: 3, Outcome: fakepg.TxLost, Writes: 1},
		},
		writes: []fakepg.WriteRecord{
			{Seq: 1, StmtSeq: 1, Backend: 1, Op: fakepg.KindUpdate, Key: "a", Value: "1"},
			{Seq: 2, StmtSeq: 3, Backend: 2, TxID: 1, Op: fakepg.KindInsert, Key: "b", Value: "1"},
			{Seq: 3, StmtSeq: 4, Backend: 2, TxID: 1, Op: fakepg.KindUpdate, Key: "a", Value: "2"},
		},
	}
}

func (c writeCase) check() error {
	return world.CheckWrites(c.initial, c.final, c.stmts, c.txs, c.writes)
}

// Proves CheckWrites accepts an exact write, statement, and transaction log matching the final table.
func TestCheckWritesAcceptsAnExactLog(t *testing.T) {
	if err := baseWriteCase().check(); err != nil {
		t.Fatal(err)
	}
}

// Proves CheckWrites detects all 13 classes of missing, duplicated, rolled-back, phantom, misordered, and cross-backend/cross-client write mismatches.
func TestCheckWritesFindsEveryKindOfMismatch(t *testing.T) {
	cases := []struct {
		name   string
		change func(c *writeCase)
		want   string
	}{
		{"missing autocommit write", func(c *writeCase) {
			c.writes = c.writes[1:]
			c.initial[0].Value = "1"
		}, "statement 1 wrote a row and committed but is missing"},
		{"missing committed write", func(c *writeCase) {
			c.writes = c.writes[:2]
			c.final[0].Value = "1"
		}, "statement 4 wrote a row and committed but is missing"},
		{"duplicated write", func(c *writeCase) {
			c.writes = append(c.writes, fakepg.WriteRecord{Seq: 4, StmtSeq: 4, TxID: 1, Op: fakepg.KindUpdate, Key: "a", Value: "2"})
		}, "applied twice"},
		{"write of a rolled-back transaction", func(c *writeCase) {
			c.writes = append(c.writes, fakepg.WriteRecord{Seq: 4, StmtSeq: 6, TxID: 2, Op: fakepg.KindUpdate, Key: "a", Value: "2"})
		}, "not a successful write of a committed transaction"},
		{"write of a lost transaction", func(c *writeCase) {
			c.writes = append(c.writes, fakepg.WriteRecord{Seq: 4, StmtSeq: 10, TxID: 3, Op: fakepg.KindDelete, Key: "b"})
		}, "not a successful write of a committed transaction"},
		{"write of a statement that changed no row", func(c *writeCase) {
			c.writes = append(c.writes, fakepg.WriteRecord{Seq: 4, StmtSeq: 8, Op: fakepg.KindUpdate, Key: "a", Value: "2"})
		}, "not a successful write"},
		{"write of a failed statement", func(c *writeCase) {
			c.writes = append(c.writes, fakepg.WriteRecord{Seq: 4, StmtSeq: 9, Op: fakepg.KindInsert, Key: "a", Value: "2"})
		}, "not a successful write"},
		{"phantom write", func(c *writeCase) { c.final[1].Value = "x" }, "replaying the write log"},
		{"missing row", func(c *writeCase) { c.final = c.final[:1] }, "replaying the write log gives 2 rows, the table has 1"},
		{"out of order", func(c *writeCase) { c.writes[1].Seq, c.writes[2].Seq = 3, 2 }, "out of order"},
		{"transaction ended twice", func(c *writeCase) { c.txs = append(c.txs, c.txs[0]) }, "ended twice"},
		{"transaction on two backends", func(c *writeCase) { c.stmts[3].Backend = 9 }, "ran statements on backends"},
		{"transaction of two clients", func(c *writeCase) { c.stmts[3].Tag = "c9" }, "holds statements of clients"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := baseWriteCase()
			tc.change(&c)
			err := c.check()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// Proves CheckWriteLog refuses to certify any run whose write, statement, or transaction log dropped entries.
func TestCheckWriteLogRefusesIncompleteLogs(t *testing.T) {
	sc := world.MixedTransactions()
	r := world.Run(sc, 1)
	if err := world.CheckWriteLog(r); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(s *fakepg.Snapshot){
		func(s *fakepg.Snapshot) { s.WriteLogDropped = 1 },
		func(s *fakepg.Snapshot) { s.StatementLogDropped = 1 },
		func(s *fakepg.Snapshot) { s.TxLogDropped = 1 },
	} {
		cp := *r
		change(&cp.Fake)
		if err := world.CheckWriteLog(&cp); err == nil || !strings.Contains(err.Error(), "dropped") {
			t.Fatalf("a log with dropped entries was accepted: %v", err)
		}
	}
	cp := *r
	cp.Server = nil
	if world.CheckWriteLog(&cp) == nil {
		t.Fatal("no server accepted")
	}
}

// Proves Quantile picks the exact bucket upper bound at every basis-point boundary and returns 0 when empty.
func TestQuantileReadsTheBucketAtTheRank(t *testing.T) {
	h := obs.HistogramSnapshot{Count: 10, Hi: 99, Buckets: []obs.Bucket{{Lo: 0, Hi: 9, Count: 5}, {Lo: 10, Hi: 19, Count: 4}, {Lo: 20, Hi: 29, Count: 1}}}
	for _, c := range []struct{ bp, want int64 }{{0, 9}, {5000, 9}, {5001, 19}, {9000, 19}, {9001, 29}, {10000, 29}, {20000, 29}, {-5, 9}} {
		if got := world.Quantile(h, c.bp); got != c.want {
			t.Fatalf("quantile %d: %d, want %d", c.bp, got, c.want)
		}
	}
	if world.Quantile(obs.HistogramSnapshot{}, 5000) != 0 {
		t.Fatal("empty histogram")
	}
}
