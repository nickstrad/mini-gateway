package proxy_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/admission"
	"github.com/nickstrad/mini-gateway/internal/coalesce"
	"github.com/nickstrad/mini-gateway/internal/proxy"
)

const (
	otherRead    = "SELECT v FROM kv WHERE k = 'b'"
	quietRemoval = 20 * time.Duration(admission.DefaultInterval)
)

type defaultRuleRig struct {
	*coreRig
	conn    proxy.ConnID
	clients []proxy.ConnID
	next    proxy.ConnID
}

func newDefaultRuleRig(t *testing.T, join coalesce.JoinMode, edit func(*proxy.Config)) *defaultRuleRig {
	t.Helper()
	r := newCoreRig(t, func(c *proxy.Config) {
		c.Connections = 1
		c.QueueCap = proxy.DefaultQueueCap
		c.Admission = admission.Config{Rule: admission.RuleTimeout, LIFO: admission.LIFOAdaptive}
		c.Coalesce = coalesce.Config{Mode: coalesce.ModeAuto, Join: join}
		if edit != nil {
			edit(c)
		}
	})
	a := r.e.Config().AdmissionConfig()
	if a.Rule != admission.RuleTimeout || a.Target != admission.DefaultTarget || a.Interval != admission.DefaultInterval ||
		a.LIFO != admission.LIFOAdaptive || a.Capacity != admission.DefaultCapacity || a.TenantCapacity != admission.DefaultTenantCapacity {
		t.Fatalf("premise: not the binary's default admission rule: %+v", a)
	}
	d := &defaultRuleRig{coreRig: r, next: 100}
	d.conn = r.firstLease(10, "SELECT pg_sleep(1)")
	d.clients = append(d.clients, 10)
	return d
}

func (d *defaultRuleRig) send(tenant int, sql string) (proxy.ConnID, []proxy.Action) {
	d.t.Helper()
	id := d.next
	d.next++
	d.login(id, tenant)
	d.clients = append(d.clients, id)
	return id, d.q(id, sql)
}

func (d *defaultRuleRig) flight(sql string, tenants ...int) []proxy.ConnID {
	d.t.Helper()
	before := d.e.Snapshot()
	var ids []proxy.ConnID
	for _, tn := range tenants {
		id, _ := d.send(tn, sql)
		ids = append(ids, id)
	}
	after := d.e.Snapshot()
	if after.CoalesceLed-before.CoalesceLed != 1 || after.CoalesceFollowed-before.CoalesceFollowed != int64(len(tenants)-1) {
		d.t.Fatalf("premise: %q led %d followed %d", sql, after.CoalesceLed-before.CoalesceLed, after.CoalesceFollowed-before.CoalesceFollowed)
	}
	return ids
}

func (d *defaultRuleRig) shedAll(a []proxy.Action, ids ...proxy.ConnID) {
	d.t.Helper()
	for _, id := range ids {
		if !shedReply(written(a, proxy.ActClientWrite, id)) {
			d.t.Fatalf("client %d got %q, want one 53M01", id, written(a, proxy.ActClientWrite, id))
		}
	}
}

func (d *defaultRuleRig) fill(tenant, n int) []proxy.Action {
	d.t.Helper()
	var all []proxy.Action
	for i := range n {
		_, a := d.send(tenant, "INSERT INTO kv VALUES ('f"+strconv.Itoa(i)+"', 'v')")
		all = append(all, a...)
	}
	return all
}

func (d *defaultRuleRig) drain() {
	d.t.Helper()
	d.tick(time.Second)
	for range 4 {
		if _, ok := d.e.LentTo(d.conn); !ok {
			break
		}
		d.db(d.conn, readReply("end"))
		d.tick(time.Second)
	}
	if d.pool().Resetting.Value > 0 {
		d.db(d.conn, complete("DISCARD ALL", 'I'))
	}
	d.agree()
	for _, id := range d.clients {
		d.ev(proxy.EvClientClosed, id, nil, 0)
	}
	d.tick(quietRemoval)
	if err := d.e.Quiesce(); err != nil {
		d.t.Fatal(err)
	}
}

func TestJoinUnderTheDefaultAdmissionRuleRuleAndFullQueueShedsEndFlightsWithFollowers(t *testing.T) {
	for _, join := range joinModes {
		d := newDefaultRuleRig(t, join, nil)

		queued := d.flight(sharedRead, 0, 1, 2)
		a := d.tick(time.Duration(admission.DefaultInterval) + time.Millisecond)
		d.shedAll(a, queued...)
		if _, as := d.agree(); as.Shed[admission.ReasonRule] != 1 {
			t.Fatalf("%v: rule sheds %d after a queued flight waited past the interval", join, as.Shed[admission.ReasonRule])
		}

		handed := d.flight(sharedRead, 0, 1, 2)
		d.ev(proxy.EvClientClosed, handed[0], nil, 0)
		if s, _ := d.agree(); s.CoalesceHandoffs != 1 {
			t.Fatalf("%v: handoffs %d", join, s.CoalesceHandoffs)
		}
		a = d.tick(time.Duration(admission.DefaultInterval) + time.Millisecond)
		d.shedAll(a, handed[1:]...)
		s, as := d.agree()
		if as.Shed[admission.ReasonRule] != 2 || s.CoalesceErrors != 3 {
			t.Fatalf("%v: rule sheds %d, shared errors %d after the handed-off flight waited", join, as.Shed[admission.ReasonRule], s.CoalesceErrors)
		}

		full := d.flight(sharedRead, 0, 1)
		a = d.fill(0, admission.DefaultCapacity)
		d.shedAll(a, full...)
		s, as = d.agree()
		if as.Shed[admission.ReasonOverflow]+as.Shed[admission.ReasonTotal] != 1 || s.CoalesceErrors != 4 {
			t.Fatalf("%v: full-queue sheds %v, shared errors %d", join, as.Shed, s.CoalesceErrors)
		}
		d.tick(time.Second)
		d.agree()

		if join == coalesce.JoinStrict {
			d.promoteIntoAFullQueue()
		}
		d.drain()
	}
}

func (d *defaultRuleRig) promoteIntoAFullQueue() {
	d.t.Helper()
	d.db(d.conn, complete("SELECT 1", 'I'))
	d.db(d.conn, complete("DISCARD ALL", 'I'))
	running := d.flight(sharedRead, 0)
	if lent, ok := d.e.LentTo(d.conn); !ok || lent != running[0] {
		d.t.Fatalf("premise: the read is not running: lent to %d", lent)
	}
	before := d.e.CoalesceSnapshot()
	held := d.flight(sharedRead, 1, 2)
	if d.e.CoalesceSnapshot().Held != before.Held+1 {
		d.t.Fatal("premise: the read arriving behind the running one was not held")
	}
	victim := d.flight(otherRead, 1, 2)
	d.fill(1, admission.DefaultCapacity-1)
	if _, as := d.agree(); as.Queued.Value != admission.DefaultCapacity {
		d.t.Fatalf("premise: queued %d, want the queue full", as.Queued.Value)
	}
	sb, ab := d.agree()
	a := d.db(d.conn, readReply("x"))
	d.shedAll(a, victim...)
	s, as := d.agree()
	if s.CoalescePromoted != sb.CoalescePromoted+1 || as.Shed[admission.ReasonOverflow]+as.Shed[admission.ReasonTotal] != ab.Shed[admission.ReasonOverflow]+ab.Shed[admission.ReasonTotal]+1 {
		d.t.Fatalf("promoted %d -> %d, sheds %v -> %v", sb.CoalescePromoted, s.CoalescePromoted, ab.Shed, as.Shed)
	}
	for _, id := range held {
		if got := written(a, proxy.ActClientWrite, id); len(got) != 0 {
			d.t.Fatalf("the promoted flight's waiter %d was answered at the promotion: %q", id, got)
		}
	}
}

func TestJoinAFlightDeadlineAndAnAdmissionDeadlineAtTheSameInstantAnswerEachWaiterOnce(t *testing.T) {
	for _, join := range joinModes {
		d := newDefaultRuleRig(t, join, func(c *proxy.Config) { c.Coalesce.Deadline = coalesce.Instant(admission.DefaultInterval) })
		ids := d.flight(sharedRead, 0, 1, 2)
		a := d.tick(time.Duration(admission.DefaultInterval) - 1)
		for _, id := range ids {
			if got := written(a, proxy.ActClientWrite, id); len(got) != 0 {
				t.Fatalf("%v: client %d answered before both deadlines: %q", join, id, got)
			}
		}
		a = d.tick(1)
		d.shedAll(a, ids...)
		s, as := d.agree()
		if s.Shed != 1 || as.ShedTotal() != 1 || s.CoalesceErrors != 2 {
			t.Fatalf("%v: engine shed %d admission %v shared errors %d", join, s.Shed, as.Shed, s.CoalesceErrors)
		}
		d.drain()
	}
}
