package proxy

import (
	"github.com/nickstrad/mini-gateway/internal/coalesce"
	"github.com/nickstrad/mini-gateway/internal/work"
)

// DropFollowerEntryForTest deletes one live follower map entry so tests can
// prove Engine.Invariants detects follower bookkeeping drift.
func (e *Engine) DropFollowerEntryForTest() bool {
	for _, c := range e.clientList {
		if c.phase == cpCoalesced && e.followers[c.wid] == c {
			delete(e.followers, c.wid)
			return true
		}
	}
	return false
}

// MisplaceOwnerForTest corrupts an owner request's flight ID so tests can prove
// Engine.Invariants detects mismatched owner/request state.
func (e *Engine) MisplaceOwnerForTest() bool {
	for _, c := range e.clientList {
		if c.fl != 0 && c.phase != cpCoalesced && c.req != nil {
			c.req.fl++
			return true
		}
	}
	return false
}

// DisarmCoalesceTimerForTest cancels e.coalT so tests can prove Engine.Invariants
// catches an unarmed coalescer deadline timer.
func (e *Engine) DisarmCoalesceTimerForTest() bool {
	if e.coalT == nil {
		return false
	}
	e.t.cancel(&e.coalT)
	return true
}

// StrandFlightForTest inserts a flight directly into the table with no client
// attached so tests can prove Shutdown empties orphaned flights.
func (e *Engine) StrandFlightForTest(id RequestID) bool {
	if e.coal == nil {
		return false
	}
	u := work.Unit{Shareable: true, HasKey: true, Key: work.ShareKey{Text: []byte("stranded")}}
	return e.coal.Admit(e.now, id, &u).Verdict == coalesce.VerdictLead
}
