package proxy_test

import (
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/coalesce"
	"github.com/nickstrad/mini-gateway/internal/proxy"
)

const repairStatement = 400 * time.Millisecond

func TestAQueuedFlightAtItsDeadlineIsShedThroughTheAdmissionQueue(t *testing.T) {
	r := newRig(t, func(c *proxy.Config) {
		c.Admission = admission.Config{Rule: admission.RuleTimeout}
		c.Coalesce = coalesce.Config{Mode: coalesce.ModeAuto, Join: coalesce.JoinStrict}
		c.Timeouts.Statement = repairStatement
		c.Timeouts.QueueWait = 3 * time.Second
	})
	r.client(11)
	r.client(12)
	r.ack(r.query(11, sharedRead))
	r.at(2)
	r.query(12, sharedRead)
	if s := r.e.CoalesceSnapshot(); s.Held != 1 {
		t.Fatalf("held %d", s.Held)
	}
	r.at(2 + proxy.Instant(repairStatement) - 1)
	r.ack(r.ev(proxy.EvDBData, r.db, readReply("x"), 0))
	if s := r.e.Snapshot(); s.Queued.Value != 1 || s.CoalescePromoted != 1 {
		t.Fatalf("queued %d promoted %d", s.Queued.Value, s.CoalescePromoted)
	}
	r.at(2 + proxy.Instant(repairStatement))
	a := r.ev(proxy.EvTimer, 0, nil, 0)
	if !shedReply(written(a, proxy.ActClientWrite, 12)) {
		t.Fatalf("the queued flight's owner was not shed at its deadline: %q", written(a, proxy.ActClientWrite, 12))
	}
	s, as := r.e.Snapshot(), r.e.AdmissionSnapshot()
	coreShed := int64(0)
	for _, n := range as.Shed {
		coreShed += n
	}
	if s.Shed != 1 || coreShed != 1 || s.CoalesceExpired != 1 {
		t.Fatalf("engine shed %d, admission shed %d, expired %d", s.Shed, coreShed, s.CoalesceExpired)
	}
}
