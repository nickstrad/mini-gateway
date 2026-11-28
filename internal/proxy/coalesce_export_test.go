package proxy

import (
	"github.com/nickstrad/mini-gateway/internal/coalesce"
	"github.com/nickstrad/mini-gateway/internal/work"
)

func (e *Engine) DropFollowerEntryForTest() bool {
	for _, c := range e.clientList {
		if c.phase == cpCoalesced && e.followers[c.wid] == c {
			delete(e.followers, c.wid)
			return true
		}
	}
	return false
}

func (e *Engine) MisplaceOwnerForTest() bool {
	for _, c := range e.clientList {
		if c.fl != 0 && c.phase != cpCoalesced && c.req != nil {
			c.req.fl++
			return true
		}
	}
	return false
}

func (e *Engine) DisarmCoalesceTimerForTest() bool {
	if e.coalT == nil {
		return false
	}
	e.t.cancel(&e.coalT)
	return true
}

func (e *Engine) StrandFlightForTest(id RequestID) bool {
	if e.coal == nil {
		return false
	}
	u := work.Unit{Shareable: true, HasKey: true, Key: work.ShareKey{Text: []byte("stranded")}}
	return e.coal.Admit(e.now, id, &u).Verdict == coalesce.VerdictLead
}
