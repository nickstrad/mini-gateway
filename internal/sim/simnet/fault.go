package simnet

import (
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
)

const (
	FaultReset sim.FaultKind = iota + 1
	FaultStall
	FaultHalfOpen
	FaultHalfClose
	FaultChunk
	FaultRefuse
	FaultDialStall
	FaultSilence
	FaultClear
)

const (
	DirUp   = 1
	DirDown = 2
	DirBoth = DirUp | DirDown
)

var faultNames = [...]string{"", "net.reset", "net.stall", "net.half-open", "net.half-close", "net.chunk", "net.refuse", "net.dial-stall", "net.silence", "net.clear"}

func FaultKinds() []sim.FaultKind {
	return []sim.FaultKind{FaultReset, FaultStall, FaultHalfOpen, FaultHalfClose, FaultChunk, FaultRefuse, FaultDialStall, FaultSilence, FaultClear}
}

func FaultName(k sim.FaultKind) string {
	if int(k) < len(faultNames) && k > 0 {
		return faultNames[k]
	}
	return ""
}

func (n *Net) registerFaults() {
	for _, k := range FaultKinds() {
		_ = n.s.RegisterFaultKind(k, FaultName(k), n.apply)
	}
}

func (n *Net) apply(f sim.Fault) error {
	if f.Kind == FaultClear {
		return n.clear(f)
	}
	if !n.applyKind(f) {
		n.stats.faults.NoTarget++
		n.s.Trace(kindNoTarget, f.Target, int64(f.Kind), 0, 0)
		return nil
	}
	n.countFault(f.Kind)
	return nil
}

func (n *Net) applyKind(f sim.Fault) bool {
	switch f.Kind {
	case FaultRefuse, FaultDialStall, FaultSilence:
		l := n.listener(f.Target)
		if l == nil || l.closed {
			return false
		}
		l.set(f.Kind, true)
		return true
	}
	c := n.conn(f.Target)
	if c == nil || c.done || !c.accepted {
		return false
	}
	switch f.Kind {
	case FaultReset:
		c.reset(-1)
	case FaultStall:
		c.stall(mask(f.Arg1), time.Duration(f.Arg2))
	case FaultChunk:
		c.chunk(mask(f.Arg1), int(max(f.Arg2, 1)))
	case FaultHalfOpen:
		return c.halfOpen(int(f.Arg1 & 1))
	case FaultHalfClose:
		e := &c.ends[f.Arg1&1]
		if e.state != stOpen || e.out().finSent {
			return false
		}
		e.out().sendFin()
	}
	return true
}

func (n *Net) clear(f sim.Fault) error {
	kind := sim.FaultKind(f.Arg1)
	ok := false
	switch kind {
	case FaultRefuse, FaultDialStall, FaultSilence:
		if l := n.listener(f.Target); l != nil && !l.closed {
			l.set(kind, false)
			ok = true
		}
	case FaultStall, FaultChunk:
		if c := n.conn(f.Target); c != nil && !c.done && c.accepted {
			for d := range c.pipes {
				if kind == FaultStall {
					c.pipes[d].unstall()
				} else {
					c.pipes[d].override = 0
				}
			}
			ok = true
		}
	}
	if !ok {
		n.stats.faults.NoTarget++
		n.s.Trace(kindNoTarget, f.Target, int64(f.Kind), f.Arg1, 0)
		return nil
	}
	n.stats.faults.Clear++
	return nil
}

func (n *Net) countFault(k sim.FaultKind) {
	fs := &n.stats.faults
	switch k {
	case FaultReset:
		fs.Reset++
	case FaultStall:
		fs.Stall++
	case FaultHalfOpen:
		fs.HalfOpen++
	case FaultHalfClose:
		fs.HalfClose++
	case FaultChunk:
		fs.Chunk++
	case FaultRefuse:
		fs.Refuse++
	case FaultDialStall:
		fs.DialStall++
	case FaultSilence:
		fs.Silence++
	}
}

func mask(v int64) int64 {
	if v&DirBoth == 0 {
		return DirBoth
	}
	return v & DirBoth
}

func (l *Listener) set(k sim.FaultKind, on bool) {
	switch k {
	case FaultRefuse:
		l.refuse = on
	case FaultDialStall:
		l.dialStall = on
	case FaultSilence:
		l.silence = on
	}
}

func (c *Conn) stall(m int64, d time.Duration) {
	for dir := range c.pipes {
		if m&(1<<dir) == 0 {
			continue
		}
		p := &c.pipes[dir]
		p.stalled = true
		p.stallT.Cancel()
		p.stallT = sim.Timer{}
		if d > 0 {
			p.stallT = c.net.s.After(d, p.unstall)
		}
	}
}

func (c *Conn) chunk(m int64, size int) {
	for dir := range c.pipes {
		if m&(1<<dir) != 0 {
			c.pipes[dir].override = size
		}
	}
}

func (c *Conn) halfOpen(side int) bool {
	e := &c.ends[side]
	switch e.state {
	case stOpen, stSilent:
	case stConnecting:
		c.net.stats.connecting--
	default:
		return false
	}
	e.state = stGone
	c.pipes[1-side].blackhole = true
	c.maybeFinish()
	return true
}

func (p *pipe) unstall() {
	if !p.stalled {
		return
	}
	p.stalled = false
	p.stallT.Cancel()
	p.stallT = sim.Timer{}
	p.release()
}
