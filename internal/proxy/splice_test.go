package proxy_test

import (
	"testing"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
)

// TestInputOverflowAfterAPartialReplyClosesWithoutSplicingAnError proves a pipelining overflow mid-reply closes the client without splicing FATAL 08P01.
func TestInputOverflowAfterAPartialReplyClosesWithoutSplicingAnError(t *testing.T) {
	r := newRig(t, nil)
	r.client(11)
	r.ack(r.query(11, "SELECT 1"))
	r.ev(proxy.EvDBData, r.db, message(pgwire.MsgRowDescription, []byte{0, 0}), 0)
	a := r.ev(proxy.EvClientData, 11, make([]byte, 4*proxy.MaxRead+1), 0)
	if !did(a, proxy.ActClientClose, 11) {
		t.Fatal("overflowing client not closed")
	}
	if got := written(a, proxy.ActClientWrite, 11); len(got) != 0 {
		t.Fatalf("an error was spliced after a partial reply: %q", got)
	}
	if s := r.e.Snapshot(); s.Check() != nil || s.ReachedYes != 1 {
		t.Fatalf("check %v reached yes %d", s.Check(), s.ReachedYes)
	}
}
