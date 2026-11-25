package fakepg

import (
	"slices"
	"strconv"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
)

// Database-side fault kinds registered by fakepg (IDs 32..55, disjoint from
// simnet's 1..9 and workload's 96..101).
const (
	FaultRefuse sim.FaultKind = iota + 32
	FaultSilentStartup
	FaultSilentQuery
	FaultConnectStall
	FaultFirstByte
	FaultTrickle
	FaultFragment
	FaultReset
	FaultGarbage
	FaultNotPostgres
	FaultTerminate
	FaultBloat
	FaultDirtyState
	FaultParamStatus
	FaultKill
	FaultFreeze
	FaultRestart
	FaultMaxConn
	FaultBoundary
	FaultSlowConn
	FaultHalfOpen
	FaultAsym
	FaultStallWindow
	FaultClear
	faultEnd
)

// NumFaults is the total number of fault kinds registered by fakepg, including FaultClear.
const NumFaults = int(faultEnd - FaultRefuse)

type point uint8

// Arg1 values for FaultReset selecting when a backend resets its connection.
const (
	PointNow point = iota
	PointBefore
	PointDuring
	PointAfter
	PointMidTx
	numPoints
)

const (
	pointBefore = PointBefore
	pointDuring = PointDuring
	pointAfter  = PointAfter
	pointMidTx  = PointMidTx
)

// Arg1 modes for FaultGarbage choosing random bytes or malformed frame lengths.
const (
	GarbageRandom int64 = iota
	GarbageShortLength
	GarbageHugeLength
)

// Arg1 modes for FaultDirtyState injecting an unexpected open transaction or changed setting.
const (
	DirtyOpenTx int64 = iota
	DirtySetting
)

// SurpriseApp is the application_name prefix emitted by FaultParamStatus.
const SurpriseApp = "fakepg-surprise"

var faultNames = [...]string{"pg.refuse", "pg.silent-startup", "pg.silent-query", "pg.connect-stall", "pg.first-byte",
	"pg.trickle", "pg.fragment", "pg.reset", "pg.garbage", "pg.not-postgres", "pg.terminate", "pg.bloat",
	"pg.dirty-state", "pg.param-status", "pg.kill", "pg.freeze", "pg.restart", "pg.max-conn", "pg.boundary",
	"pg.slow-conn", "pg.half-open", "pg.asym", "pg.stall-window", "pg.clear"}

// FaultKinds returns every sim.FaultKind registered by fakepg in ascending order.
func FaultKinds() []sim.FaultKind {
	out := make([]sim.FaultKind, 0, NumFaults)
	for k := FaultRefuse; k < faultEnd; k++ {
		out = append(out, k)
	}
	return out
}

// FaultName returns the trace/diagnostic name for a fakepg fault kind, or "" if out of range.
func FaultName(k sim.FaultKind) string {
	if k >= FaultRefuse && k < faultEnd {
		return faultNames[k-FaultRefuse]
	}
	return ""
}

type faultState struct {
	counts        [NumFaults]int64
	cleared       int64
	noTarget      int64
	silentStartup bool
	silentQuery   bool
	firstByte     time.Duration
	trickle       time.Duration
	asym          time.Duration
	fragment      int64
	arm           [numPoints]bool
	garbageOn     bool
	garbageMode   int64
	notPostgres   bool
	bloatRows     int64
	bloatSize     int64
	frozen        bool
	freezeTimer   sim.Timer
	restarting    bool
	restartTimer  sim.Timer
	maxConnOn     bool
	maxConn       int64
	boundaryOn    bool
	boundary      time.Duration
	stalls        []Stall
	surprises     int64
}

func (srv *Server) registerFaults() error {
	for _, k := range FaultKinds() {
		if err := srv.s.RegisterFaultKind(k, FaultName(k), srv.applyFault); err != nil {
			return err
		}
	}
	return nil
}

func (srv *Server) applyFault(f sim.Fault) error {
	if f.Kind == FaultClear {
		if srv.clearFault(f) {
			srv.faults.cleared++
		} else {
			srv.faults.noTarget++
		}
		return nil
	}
	if srv.applyKind(f) {
		srv.faults.counts[f.Kind-FaultRefuse]++
	} else {
		srv.faults.noTarget++
	}
	return nil
}

func (srv *Server) targets(id int64) []*backend {
	if id == 0 {
		return slices.Clone(srv.backends)
	}
	if b := srv.backend(id); b != nil {
		return []*backend{b}
	}
	return nil
}

func (srv *Server) netFault(kind sim.FaultKind, target, a1, a2 int64) {
	srv.s.ApplyFault(sim.Fault{Kind: kind, Target: target, Arg1: a1, Arg2: a2})
}

func (srv *Server) applyKind(f sim.Fault) bool {
	fs := &srv.faults
	switch f.Kind {
	case FaultRefuse:
		srv.netFault(simnet.FaultRefuse, srv.l.Index(), 0, 0)
	case FaultConnectStall:
		srv.netFault(simnet.FaultDialStall, srv.l.Index(), 0, 0)
	case FaultSilentStartup:
		fs.silentStartup = true
	case FaultFirstByte:
		fs.firstByte = time.Duration(max(f.Arg2, 1))
	case FaultTrickle:
		fs.trickle = time.Duration(max(f.Arg2, 1))
	case FaultAsym:
		fs.asym = time.Duration(max(f.Arg2, 1))
	case FaultNotPostgres:
		fs.notPostgres = true
	case FaultBloat:
		fs.bloatRows, fs.bloatSize = min(max(f.Arg1, 1), MaxRowCount), min(max(f.Arg2, 0), MaxRowSize)
	case FaultMaxConn:
		// Clamp to Config.MaxConnections so the fault can only tighten the limit
		// and never violate the registered sessions <= MaxConnections invariant.
		fs.maxConnOn, fs.maxConn = true, min(max(f.Arg1, 0), int64(srv.cfg.MaxConnections))
	case FaultBoundary:
		fs.boundaryOn, fs.boundary = true, time.Duration(max(f.Arg2, 0))
	case FaultStallWindow:
		srv.addStall(time.Duration(max(f.Arg1, 1)), time.Duration(max(f.Arg2, 0)))
	case FaultKill:
		srv.kill()
	case FaultFreeze:
		srv.freeze(time.Duration(max(f.Arg2, 0)))
	case FaultRestart:
		srv.restart(time.Duration(max(f.Arg2, 0)))
	default:
		return srv.applyTargeted(f)
	}
	return true
}

func (srv *Server) applyTargeted(f sim.Fault) bool {
	fs := &srv.faults
	switch f.Kind {
	case FaultSilentQuery:
		if f.Target == 0 {
			fs.silentQuery = true
			return true
		}
	case FaultFragment:
		if f.Target == 0 {
			fs.fragment = max(f.Arg2, 1)
			for _, b := range srv.targets(0) {
				srv.netFault(simnet.FaultChunk, b.id, simnet.DirDown, fs.fragment)
			}
			return true
		}
	case FaultReset:
		p := point(min(max(f.Arg1, 0), int64(numPoints)-1))
		if f.Target == 0 {
			if p == PointNow {
				return false
			}
			fs.arm[p] = true
			return true
		}
	case FaultGarbage:
		if f.Target == 0 {
			fs.garbageMode = garbageMode(f.Arg1)
			fs.garbageOn = true
			return true
		}
	case FaultDirtyState, FaultSlowConn, FaultHalfOpen:
		if f.Target == 0 {
			return false
		}
	}
	hit := false
	for _, b := range srv.targets(f.Target) {
		if srv.applyTo(b, f) {
			hit = true
		}
	}
	return hit
}

func garbageMode(arg int64) int64 { return min(max(arg, 0), GarbageHugeLength) }

func (srv *Server) applyTo(b *backend, f sim.Fault) bool {
	switch f.Kind {
	case FaultDirtyState, FaultParamStatus:
		if b.phase != phaseReady {
			return false
		}
	}
	switch f.Kind {
	case FaultSilentQuery:
		b.muteQueries = true
	case FaultFragment:
		srv.netFault(simnet.FaultChunk, b.id, simnet.DirDown, max(f.Arg2, 1))
	case FaultReset:
		p := point(min(max(f.Arg1, 0), int64(numPoints)-1))
		if p == PointNow {
			_ = b.end.Reset()
			b.finalize()
			return true
		}
		b.arm[p] = true
	case FaultGarbage:
		b.garbage = true
		b.garbageMode = garbageMode(f.Arg1)
	case FaultTerminate:
		b.terminate()
	case FaultDirtyState:
		b.dirty(f.Arg1)
	case FaultParamStatus:
		b.surprise()
	case FaultSlowConn:
		b.slowExtra = time.Duration(max(f.Arg2, 0))
	case FaultHalfOpen:
		srv.netFault(simnet.FaultHalfOpen, b.id, 1, 0)
		b.finalize()
	}
	return true
}

func (b *backend) armed(p point) bool {
	if b.arm[p] {
		b.arm[p] = false
		b.srv.s.Trace(kindFire, b.id, int64(FaultReset), int64(p), 0)
		return true
	}
	if b.srv.faults.arm[p] {
		b.srv.s.Trace(kindFire, b.id, int64(FaultReset), int64(p), 1)
		return true
	}
	return false
}

func (b *backend) terminate() {
	if b.phase != phaseReady {
		b.after = afterClose
		b.advance()
		return
	}
	if r := b.cur; r != nil {
		b.leaveService(r)
		b.cur = nil
		b.srv.stats.aborted++
		b.srv.logStatement(b, r, "57P01", true)
	}
	// Drop queued segments not yet started, but leave b.outBuf intact so an
	// already-begun wire frame finishes cleanly before FATAL 57P01 is emitted.
	b.msg = nil
	b.out = nil
	b.emit(encError(nil, errorf("FATAL", "57P01", "terminating connection due to administrator command")))
	b.after = afterClose
	b.advance()
}

func (b *backend) dirty(mode int64) {
	if mode == DirtySetting {
		if i := findSetting(b.settings, "search_path"); i >= 0 {
			b.settings[i].value = "fakepg_dirty"
		}
		return
	}
	if b.tx == nil {
		b.tx = b.srv.newTx(true)
	}
}

func (b *backend) surprise() {
	b.srv.faults.surprises++
	v := SurpriseApp + "-" + strconv.FormatInt(b.srv.faults.surprises, 10)
	if i := findSetting(b.settings, "application_name"); i >= 0 {
		b.settings[i].value = v
	}
	b.reportChanges()
	b.advance()
}

func (srv *Server) addStall(window, extra time.Duration) {
	now := srv.s.Now()
	kept := srv.faults.stalls[:0]
	for _, w := range srv.faults.stalls {
		if w.To > now {
			kept = append(kept, w)
		}
	}
	if len(kept) >= MaxStallWindows {
		kept = kept[1:]
	}
	srv.faults.stalls = append(kept, Stall{From: now, To: sim.Later(now, window), Extra: extra})
	if extra > 0 && !srv.faults.boundaryOn {
		srv.stallInService(extra)
	}
}

func (srv *Server) stallInService(extra time.Duration) {
	for _, b := range srv.backends {
		r := b.cur
		if r == nil || r.state != runServing {
			continue
		}
		if srv.faults.frozen || !r.timer.Pending() {
			r.remaining = sim.AddDurations(r.remaining, extra)
			continue
		}
		at := sim.Later(r.timer.When(), extra)
		r.timer.Cancel()
		r.timer = srv.s.At(at, func() { b.serviceDone(r) })
	}
}

func (srv *Server) kill() {
	for _, b := range srv.targets(0) {
		_ = b.end.Reset()
		b.finalize()
	}
}

func (srv *Server) freeze(d time.Duration) {
	fs := &srv.faults
	if !fs.frozen {
		fs.frozen = true
		now := srv.s.Now()
		for _, b := range srv.backends {
			if r := b.cur; r != nil && r.state == runServing && r.timer.Pending() {
				r.remaining = r.timer.When().Sub(now)
				r.timer.Cancel()
			}
			if b.resetTimer.Pending() {
				b.resetLeft = b.resetTimer.When().Sub(now)
				b.resetTimer.Cancel()
				b.resetHeld = true
			}
			b.pumpTimer.Cancel()
		}
	}
	fs.freezeTimer.Cancel()
	fs.freezeTimer = sim.Timer{}
	if d > 0 {
		fs.freezeTimer = srv.s.After(d, srv.thaw)
	}
}

func (srv *Server) thaw() {
	fs := &srv.faults
	fs.freezeTimer.Cancel()
	fs.freezeTimer = sim.Timer{}
	if !fs.frozen {
		return
	}
	fs.frozen = false
	for _, b := range srv.targets(0) {
		if r := b.cur; r != nil && r.state == runServing && !r.timer.Pending() {
			r.timer = srv.s.After(r.remaining, func() { b.serviceDone(r) })
		}
		if b.resetHeld {
			b.resetHeld = false
			b.resetTimer = srv.s.After(b.resetLeft, b.delayedReset)
		}
	}
	srv.admitWaiter()
	for _, b := range srv.targets(0) {
		b.advance()
	}
}

func (srv *Server) restart(d time.Duration) {
	fs := &srv.faults
	srv.kill()
	fs.restarting = true
	fs.restartTimer.Cancel()
	fs.restartTimer = sim.Timer{}
	if d > 0 {
		fs.restartTimer = srv.s.After(d, srv.endRestart)
	}
}

func (srv *Server) endRestart() {
	srv.faults.restartTimer.Cancel()
	srv.faults.restartTimer = sim.Timer{}
	srv.faults.restarting = false
}

func (srv *Server) applyNewConnFaults(b *backend) {
	if srv.faults.fragment > 0 {
		srv.netFault(simnet.FaultChunk, b.id, simnet.DirDown, srv.faults.fragment)
	}
}

func (srv *Server) clearFault(f sim.Fault) bool {
	fs := &srv.faults
	kind := sim.FaultKind(f.Arg1)
	if kind != 0 && kind == srv.cfg.plant.skipClear {
		return true
	}
	switch kind {
	case FaultRefuse:
		srv.netFault(simnet.FaultClear, srv.l.Index(), int64(simnet.FaultRefuse), 0)
	case FaultConnectStall:
		srv.netFault(simnet.FaultClear, srv.l.Index(), int64(simnet.FaultDialStall), 0)
	case FaultSilentStartup:
		fs.silentStartup = false
	case FaultSilentQuery:
		fs.silentQuery = false
		for _, b := range srv.backends {
			b.muteQueries = false
		}
	case FaultFirstByte:
		fs.firstByte = 0
	case FaultTrickle:
		fs.trickle = 0
	case FaultAsym:
		fs.asym = 0
	case FaultFragment:
		fs.fragment = 0
		for _, b := range srv.targets(0) {
			srv.netFault(simnet.FaultClear, b.id, int64(simnet.FaultChunk), 0)
		}
	case FaultReset:
		fs.arm = [numPoints]bool{}
		for _, b := range srv.backends {
			b.arm = [numPoints]bool{}
		}
	case FaultGarbage:
		fs.garbageOn = false
		for _, b := range srv.backends {
			b.garbage = false
		}
	case FaultNotPostgres:
		fs.notPostgres = false
	case FaultBloat:
		fs.bloatRows, fs.bloatSize = 0, 0
	case FaultFreeze:
		srv.thaw()
	case FaultRestart:
		srv.endRestart()
	case FaultMaxConn:
		fs.maxConnOn = false
	case FaultBoundary:
		fs.boundaryOn = false
	case FaultSlowConn:
		for _, b := range srv.targets(f.Target) {
			b.slowExtra = 0
		}
	case FaultStallWindow:
		fs.stalls = slices.Clone(srv.cfg.Service.Stalls)
	default:
		return false
	}
	return true
}
