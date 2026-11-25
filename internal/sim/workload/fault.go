package workload

import (
	"slices"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/testclient"
)

const (
	FaultHalfMessage sim.FaultKind = iota + 96
	FaultStopReading
	FaultDisconnect
	FaultBurst
	FaultCancel
	FaultClear
	faultEnd
)

const (
	MaxCancelConns = 1 << 16
	CancelTimeout  = time.Second
)

var faultNames = [...]string{"wl.half-message", "wl.stop-reading", "wl.disconnect", "wl.burst", "wl.cancel", "wl.clear"}

func FaultKinds() []sim.FaultKind {
	var out []sim.FaultKind
	for k := FaultHalfMessage; k < faultEnd; k++ {
		out = append(out, k)
	}
	return out
}

func FaultName(k sim.FaultKind) string {
	if k >= FaultHalfMessage && k < faultEnd {
		return faultNames[k-FaultHalfMessage]
	}
	return ""
}

func (w *Workload) registerFaults() error {
	for _, k := range FaultKinds() {
		if err := w.s.RegisterFaultKind(k, FaultName(k), w.applyFault); err != nil {
			return err
		}
	}
	return nil
}

func (w *Workload) applyFault(f sim.Fault) error {
	w.faults.applied++
	if f.Kind == FaultBurst {
		n := max(f.Arg1, 1)
		hit := false
		for id := f.Target; id < f.Target+n; id++ {
			if c := w.client(id); c != nil {
				hit = c.burstReconnect(time.Duration(max(f.Arg2, 0))) || hit
			}
		}
		w.count(f.Kind, hit)
		return nil
	}
	c := w.client(f.Target)
	if c == nil || c.phase == cDone {
		w.count(f.Kind, false)
		return nil
	}
	w.count(f.Kind, c.applyFault(f))
	return nil
}

func (w *Workload) count(k sim.FaultKind, hit bool) {
	if hit {
		w.faults.byKind[k-FaultHalfMessage]++
		return
	}
	w.faults.noTarget++
}

func (c *client) applyFault(f sim.Fault) bool {
	switch f.Kind {
	case FaultHalfMessage:
		c.halfMessage = true
	case FaultStopReading:
		c.stopReading = true
		c.t.st.stopReads++
		c.resumeT.Cancel()
		if f.Arg2 > 0 {
			c.resumeT = c.w.s.After(time.Duration(f.Arg2), c.resumeReading)
		}
	case FaultDisconnect:
		reset := f.Arg2 != 0
		switch DisconnectPoint(f.Arg1) {
		case DisconnectNow:
			if c.cc == nil {
				return false
			}
			c.leave(reset)
		case DisconnectAfterSend:
			c.leaveOnSend, c.leaveReset = true, reset
		default:
			c.leaveAfter, c.leaveReset = true, reset
		}
	case FaultCancel:
		return c.sendCancel(f.Arg1 != 0)
	case FaultClear:
		switch sim.FaultKind(f.Arg1) {
		case FaultStopReading:
			c.resumeReading()
		case FaultHalfMessage:
			c.halfMessage = false
		case FaultDisconnect:
			c.leaveOnSend, c.leaveAfter = false, false
		default:
			return false
		}
	}
	return true
}

func (c *client) resumeReading() {
	c.resumeT.Cancel()
	if !c.stopReading {
		return
	}
	c.stopReading = false
	c.readAll()
}

func (c *client) sendCancel(wrongKey bool) bool {
	if c.phase != cReady || len(c.w.cancels) >= MaxCancelConns {
		return false
	}
	secret := slices.Clone(c.sess.Secret())
	if wrongKey && len(secret) > 0 {
		secret[0] ^= 1
	}
	p := NewProbe(c.w.n, c.w.cfg.Addr, c.w.cfg.Options)
	p.Send(testclient.EncodeCancel(nil, c.sess.ProcessID(), secret))
	c.w.cancels = append(c.w.cancels, p)
	c.w.s.After(CancelTimeout, p.Close)
	c.t.st.cancelsSent++
	return true
}

func (c *client) burstReconnect(window time.Duration) bool {
	if c.phase == cDone && c.moreArrivals() {
		return false
	}
	c.t.st.burstDrops++
	if c.cc != nil {
		c.leave(false)
	}
	if c.phase == cDone {
		c.phase = cIdle
	}
	c.burst = true
	c.wantConn = true
	c.holdT.Cancel()
	delay := time.Duration(0)
	if window > 0 {
		delay = time.Duration(c.retryR.Int64N(int64(window)))
	}
	c.connTry = 0
	c.w.s.After(delay, c.pump)
	return true
}
