package proxy_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
)

// TestFragmentedQueryOutlivesTheMidMessageDeadline proves completing a fragmented query cancels client_mid_message so slow statements succeed.
func TestFragmentedQueryOutlivesTheMidMessageDeadline(t *testing.T) {
	mid := 100 * time.Millisecond
	r := newRig(t, func(c *proxy.Config) { c.Timeouts.ClientMidMessage = mid })
	r.client(11)
	q := pgwire.AppendQuery(nil, []byte("SELECT v FROM kv WHERE k = 'slow'"))
	r.ev(proxy.EvClientData, 11, q[:3], 0)
	r.at(r.now + proxy.Instant(5*time.Millisecond))
	r.ack(r.ev(proxy.EvClientData, 11, q[3:], 0))
	r.at(r.now + proxy.Instant(3*mid))
	if a := r.ev(proxy.EvTimer, 0, nil, 0); did(a, proxy.ActClientClose, 11) {
		t.Fatalf("a complete query was closed by the mid-message deadline: %q", written(a, proxy.ActClientWrite, 11))
	}
	reply := complete("SELECT 1", 'I')
	a := r.ev(proxy.EvDBData, r.db, reply, 0)
	if got := written(a, proxy.ActClientWrite, 11); !bytes.Equal(got, reply) || did(a, proxy.ActClientClose, 11) {
		t.Fatalf("reply %q closed %v", got, did(a, proxy.ActClientClose, 11))
	}
	if s := r.e.Snapshot(); s.Completed != 1 || s.ByRow[pgwire.ErrRowProtocolViolation] != 0 {
		t.Fatalf("completed %d violations %d", s.Completed, s.ByRow[pgwire.ErrRowProtocolViolation])
	}
}
