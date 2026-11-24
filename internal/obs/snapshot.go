package obs

import (
	"fmt"
	"strconv"
	"strings"
)

type Snapshotter[T any] interface {
	Snapshot() T
}

type Gauge struct {
	Value int64
	Peak  int64
}

func (g *Gauge) Set(v int64) {
	g.Value = v
	if v > g.Peak {
		g.Peak = v
	}
}

func (g *Gauge) Add(d int64) { g.Set(g.Value + d) }

type Term struct {
	Name  string
	Value int64
}

func T(name string, v int64) Term { return Term{Name: name, Value: v} }

type ConservationError struct {
	Total Term
	Parts []Term
	Sum   int64
}

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

type Counters struct {
	Accepted  int64
	Completed int64
	Failed    int64
	Shed      int64
	Queued    int64
	InFlight  int64
}

func (c Counters) Check() error {
	return Conserve(T("accepted", c.Accepted),
		T("completed", c.Completed), T("failed", c.Failed), T("shed", c.Shed),
		T("queued", c.Queued), T("in flight", c.InFlight))
}
