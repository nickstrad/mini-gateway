package obs

import (
	"fmt"
	"strconv"
	"strings"
)

// Snapshotter is implemented by components that export their metrics as a
// plain, deterministic value of type T.
type Snapshotter[T any] interface {
	Snapshot() T
}

// Gauge tracks a current integer level alongside the highest level observed.
type Gauge struct {
	Value int64
	Peak  int64
}

// Set updates Value to v and raises Peak if v exceeds it.
func (g *Gauge) Set(v int64) {
	g.Value = v
	if v > g.Peak {
		g.Peak = v
	}
}

// Add shifts Value by d and updates Peak if the new value is higher.
func (g *Gauge) Add(d int64) { g.Set(g.Value + d) }

// Term is a named integer quantity participating in a Conserve check.
type Term struct {
	Name  string
	Value int64
}

// T constructs a Term for Conserve.
func T(name string, v int64) Term { return Term{Name: name, Value: v} }

// ConservationError describes a mismatch between a total and the sum of its
// constituent parts, naming every term and which side is higher.
type ConservationError struct {
	Total Term
	Parts []Term
	Sum   int64
}

// Error formats the broken conservation identity with every term and difference.
func (e *ConservationError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "conservation: %s %d != ", e.Total.Name, e.Total.Value)
	for i, p := range e.Parts {
		if i > 0 {
			b.WriteString(" + ")
		}
		b.WriteString(p.Name + " " + strconv.FormatInt(p.Value, 10))
	}
	diff := e.Total.Value - e.Sum
	side := e.Total.Name + " is higher"
	if diff < 0 {
		diff = -diff
		side = "the parts are higher"
	}
	fmt.Fprintf(&b, " = %d (%s by %d)", e.Sum, side, diff)
	return b.String()
}

// Conserve verifies that total equals the sum of parts, returning a
// *ConservationError when they disagree.
func Conserve(total Term, parts ...Term) error {
	sum := int64(0)
	for _, p := range parts {
		sum += p.Value
	}
	if sum == total.Value {
		return nil
	}
	return &ConservationError{Total: total, Parts: parts, Sum: sum}
}

// Counters holds the standard request-accounting terms shared by gateway
// components: Accepted must always equal Completed + Failed + Shed + Queued + InFlight.
type Counters struct {
	Accepted  int64
	Completed int64
	Failed    int64
	Shed      int64
	Queued    int64
	InFlight  int64
}

// Check verifies the conservation law across all six Counters fields.
func (c Counters) Check() error {
	return Conserve(T("accepted", c.Accepted),
		T("completed", c.Completed), T("failed", c.Failed), T("shed", c.Shed),
		T("queued", c.Queued), T("in flight", c.InFlight))
}
