package pgwire

import (
	"testing"

	"github.com/nickstrad/mini-gateway/internal/work"
)

func TestClassifierSnapshotCountersMoveAndConserve(t *testing.T) {
	var c Classifier
	if err := c.Snapshot().Check(); err != nil {
		t.Fatal(err)
	}
	for _, x := range clsLoadCorpus(t) {
		c.Classify("t", []byte(x.text), false)
		c.Classify("t", []byte(x.text), true)
	}
	s := c.Snapshot()
	if err := s.Check(); err != nil {
		t.Fatalf("%v: %+v", err, s)
	}
	for cl := work.ClassReject; cl <= work.ClassContinueLease; cl++ {
		if s.ByClass[cl] == 0 {
			t.Errorf("class %s did not move", cl)
		}
	}
	for r := work.RejectSessionSet; r < work.RejectExtendedProtocol; r++ {
		if s.ByReason[r] == 0 {
			t.Errorf("reason %s did not move", r)
		}
	}
	if s.Opaque == 0 || s.SafeRead == 0 || s.Shareable == 0 || s.Hinted == 0 || s.Keys == 0 || s.Statements == 0 || s.Classified == 0 {
		t.Errorf("a counter did not move: %+v", s)
	}
	if s.Shareable >= s.SafeRead {
		t.Errorf("inside a transaction nothing is shareable, so shareable %d must be below safe-read %d", s.Shareable, s.SafeRead)
	}
	bad := s
	bad.ByClass[work.ClassReject]++
	if bad.Check() == nil {
		t.Error("Check accepts counters that do not add up")
	}
	bad = s
	bad.Shareable = bad.SafeRead + 1
	if bad.Check() == nil {
		t.Error("Check accepts more shareable than safe-read")
	}
	bad = s
	bad.ByReason[work.RejectCopy]++
	if bad.Check() == nil {
		t.Error("Check accepts more reasons than rejects")
	}
}
