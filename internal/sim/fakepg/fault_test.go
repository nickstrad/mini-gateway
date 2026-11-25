package fakepg_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
	"github.com/nickstrad/mini-gateway/internal/testclient"
)

type faultCase struct {
	name      string
	kind      sim.FaultKind
	noClr     bool
	cfg       fakepg.Config
	steps     func(w *world, f func(target, a1, a2 int64)) string
	expect    string
	after     func(w *world) string
	afterWant string
}

func (w *world) inject(kind sim.FaultKind) func(target, a1, a2 int64) {
	return func(target, a1, a2 int64) {
		w.s.ApplyFault(sim.Fault{Kind: kind, Target: target, Arg1: a1, Arg2: a2})
	}
}

func (w *world) clear(kind sim.FaultKind, target int64) {
	w.s.ApplyFault(sim.Fault{Kind: fakepg.FaultClear, Target: target, Arg1: int64(kind)})
}

func id(p *workload.Probe) int64 { return p.End().ID() }

func rowsQuery(n int) string {
	return "SELECT repeat('x', 10) FROM generate_series(1, " + itoa(n) + ")"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func elapsed(p *workload.Probe, from int, since sim.Time) time.Duration {
	for _, e := range p.Events()[from:] {
		if e.Kind == workload.EventMessage && e.Msg.Type == testclient.MsgReady {
			return time.Duration(e.At - since)
		}
	}
	return -1
}

func faultCases() []faultCase {
	return []faultCase{
		{name: "refuse", kind: fakepg.FaultRefuse, expect: "refused", steps: func(w *world, f func(int64, int64, int64)) string {
			f(0, 0, 0)
			p := w.dial()
			w.run(time.Millisecond)
			return p.Trace()
		}},
		{name: "silent-startup", kind: fakepg.FaultSilentStartup, expect: "connected | open 1", steps: func(w *world, f func(int64, int64, int64)) string {
			f(0, 0, 0)
			p := w.dial()
			p.Startup("app", "")
			w.run(time.Second)
			out := p.Trace() + " | open " + itoa(int(w.srv.Snapshot().ConnsOpen.Value))
			p.Close()
			return out
		}},
		{name: "silent-after-query", kind: fakepg.FaultSilentQuery, expect: "connected R ZI | in service 1 | aborted 1", steps: func(w *world, f func(int64, int64, int64)) string {
			p := w.client()
			f(0, 0, 0)
			p.Query("SELECT 1")
			w.run(time.Second)
			out := compact(p.Trace()) + " | in service " + itoa(int(w.srv.Snapshot().InService.Value))
			p.Close()
			w.run(time.Millisecond)
			return out + " | aborted " + itoa(int(w.srv.Snapshot().StatementsAborted))
		}},
		{name: "silent-after-query-one-target", kind: fakepg.FaultSilentQuery, expect: "connected R ZI / connected R ZI T D C:SELECT 1 ZI", steps: func(w *world, f func(int64, int64, int64)) string {
			a, b, c := w.client(), w.client(), w.client()
			f(id(a), 0, 0)
			f(id(c), 0, 0)
			a.Query("SELECT 1")
			b.Query("SELECT 1")
			w.run(time.Second)
			return compact(a.Trace()) + " / " + compact(b.Trace())
		}, after: func(w *world) string { return w.exchange(w.ps[2], "SELECT 1") }, afterWant: "T D C:SELECT 1 ZI"},
		{name: "connect-stall", kind: fakepg.FaultConnectStall, expect: "", steps: func(w *world, f func(int64, int64, int64)) string {
			f(0, 0, 0)
			p := w.dial()
			p.Startup("app", "")
			w.run(time.Second)
			p.Close()
			return p.Trace()
		}},
		{name: "slow-first-byte", kind: fakepg.FaultFirstByte, expect: "slow", steps: func(w *world, f func(int64, int64, int64)) string {
			p := w.client()
			f(0, 0, int64(150*time.Millisecond))
			from, at := len(p.Events()), w.s.Now()
			p.Query("SELECT 1")
			w.run(time.Second)
			if elapsed(p, from, at) >= 150*time.Millisecond {
				return "slow"
			}
			return "fast " + p.Trace()
		}, after: func(w *world) string { return replySpeed(w, w.client(), "SELECT 1", 150*time.Millisecond) }, afterWant: "fast"},
		{name: "slow-trickle", kind: fakepg.FaultTrickle, expect: "spaced 10ms", steps: func(w *world, f func(int64, int64, int64)) string {
			p := w.client()
			f(0, 0, int64(10*time.Millisecond))
			from := len(p.Events())
			p.Query(rowsQuery(5))
			w.run(time.Second)
			ev := p.Events()[from:]
			var rows []sim.Time
			for _, e := range ev {
				if e.Kind == workload.EventMessage && e.Msg.Type == testclient.MsgDataRow {
					rows = append(rows, e.At)
				}
			}
			if len(rows) != 5 {
				return "rows " + itoa(len(rows))
			}
			for i := 1; i < len(rows); i++ {
				if time.Duration(rows[i]-rows[i-1]) != 10*time.Millisecond {
					return "gap " + time.Duration(rows[i]-rows[i-1]).String()
				}
			}
			return "spaced 10ms"
		}, after: func(w *world) string {
			p := w.client()
			from := len(p.Events())
			p.Query(rowsQuery(5))
			w.run(time.Second)
			var first, last sim.Time
			n := 0
			for _, e := range p.Events()[from:] {
				if e.Kind == workload.EventMessage && e.Msg.Type == testclient.MsgDataRow {
					if n == 0 {
						first = e.At
					}
					last = e.At
					n++
				}
			}
			if n == 5 && first == last {
				return "together"
			}
			return "rows " + itoa(n) + " over " + time.Duration(last-first).String()
		}, afterWant: "together"},
		{name: "fragment-to-one-byte", kind: fakepg.FaultFragment, expect: "T D C:SELECT 1 ZI one-byte pieces", steps: func(w *world, f func(int64, int64, int64)) string {
			p := w.client()
			f(0, 0, 1)
			before := w.n.Snapshot().Down
			from := len(p.Events())
			p.Query("SELECT 1")
			w.run(time.Second)
			after := w.n.Snapshot().Down
			out := p.TraceFrom(from)
			if after.Chunks-before.Chunks == after.Written-before.Written {
				out += " one-byte pieces"
			}
			return out
		}, after: func(w *world) string { return pieces(w, w.ps[0]) + " / " + pieces(w, w.client()) }, afterWant: "whole / whole"},
		{name: "reset-now", kind: fakepg.FaultReset, noClr: true, expect: "reset", steps: func(w *world, f func(int64, int64, int64)) string {
			p := w.client()
			from := len(p.Events())
			f(id(p), int64(fakepg.PointNow), 0)
			w.run(time.Millisecond)
			return p.TraceFrom(from)
		}},
		{name: "reset-before-reply", kind: fakepg.FaultReset, expect: "reset", steps: func(w *world, f func(int64, int64, int64)) string {
			p := w.client()
			f(0, int64(fakepg.PointBefore), 0)
			from := len(p.Events())
			p.Query("SELECT 1")
			w.run(100 * time.Millisecond)
			return p.TraceFrom(from)
		}},
		{name: "reset-during-reply", kind: fakepg.FaultReset, expect: "T D reset", steps: func(w *world, f func(int64, int64, int64)) string {
			p := w.client()
			f(0, int64(fakepg.PointDuring), 0)
			from := len(p.Events())
			p.Query(rowsQuery(3))
			w.run(100 * time.Millisecond)
			return p.TraceFrom(from)
		}},
		{name: "reset-after-reply-one-target", kind: fakepg.FaultReset, noClr: true, expect: "T D C:SELECT 1 ZI reset", steps: func(w *world, f func(int64, int64, int64)) string {
			p := w.client()
			f(id(p), int64(fakepg.PointAfter), 0)
			from := len(p.Events())
			p.Query("SELECT 1")
			w.run(time.Second)
			return p.TraceFrom(from)
		}},
		{name: "reset-after-reply", kind: fakepg.FaultReset, expect: "T D C:SELECT 1 ZI reset", steps: func(w *world, f func(int64, int64, int64)) string {
			p := w.client()
			f(0, int64(fakepg.PointAfter), 0)
			from := len(p.Events())
			p.Query("SELECT 1")
			w.run(time.Second)
			return p.TraceFrom(from)
		}},
		{name: "reset-mid-transaction", kind: fakepg.FaultReset, expect: "C:SELECT 1 ZI C:BEGIN ZT reset | lost 1", steps: func(w *world, f func(int64, int64, int64)) string {
			p := w.client()
			f(0, int64(fakepg.PointMidTx), 0)
			from := len(p.Events())
			p.Query("SELECT 1")
			w.run(time.Second)
			p.Query("BEGIN")
			w.run(time.Second)
			return strings.ReplaceAll(p.TraceFrom(from), "T D ", "") + " | lost " + itoa(int(w.srv.Snapshot().TxLost))
		}, after: func(w *world) string {
			p := w.client()
			from := len(p.Events())
			p.Query("BEGIN")
			w.run(time.Second)
			p.Query("COMMIT")
			w.run(time.Second)
			return p.TraceFrom(from)
		}, afterWant: "C:BEGIN ZT C:COMMIT ZI"},
		{name: "garbage-short-length", kind: fakepg.FaultGarbage, expect: "T protocol-error", steps: func(w *world, f func(int64, int64, int64)) string {
			p := w.client()
			f(0, fakepg.GarbageShortLength, 0)
			from := len(p.Events())
			p.Query("SELECT 1")
			w.run(100 * time.Millisecond)
			return p.TraceFrom(from)
		}},
		{name: "garbage-huge-length-one-target", kind: fakepg.FaultGarbage, noClr: true, expect: "T protocol-error", steps: func(w *world, f func(int64, int64, int64)) string {
			p := w.client()
			f(id(p), fakepg.GarbageHugeLength, 0)
			from := len(p.Events())
			p.Query("SELECT 1")
			w.run(100 * time.Millisecond)
			return p.TraceFrom(from)
		}},
		{name: "garbage-random", kind: fakepg.FaultGarbage, expect: "closed without ready", steps: func(w *world, f func(int64, int64, int64)) string {
			p := w.client()
			f(0, fakepg.GarbageRandom, 0)
			from := len(p.Events())
			p.Query("SELECT 1")
			w.run(100 * time.Millisecond)
			tr := p.TraceFrom(from)
			if p.Closed() && !strings.Contains(tr, "ZI") {
				return "closed without ready"
			}
			return tr
		}},
		{name: "not-postgres", kind: fakepg.FaultNotPostgres, expect: "connected protocol-error", steps: func(w *world, f func(int64, int64, int64)) string {
			f(0, 0, 0)
			p := w.dial()
			p.Startup("app", "")
			w.run(time.Millisecond)
			return p.Trace()
		}},
		{name: "no-end-marker", kind: fakepg.FaultTerminate, noClr: true, expect: "E:57P01 eof", steps: func(w *world, f func(int64, int64, int64)) string {
			p := w.client()
			p.Query("SELECT pg_sleep(30)")
			w.run(10 * time.Millisecond)
			from := len(p.Events())
			f(id(p), 0, 0)
			w.run(time.Millisecond)
			return p.TraceFrom(from)
		}},
		{name: "very-large-and-many-rows", kind: fakepg.FaultBloat, expect: "rows 1001 bytes 1000001", steps: func(w *world, f func(int64, int64, int64)) string {
			w.exchange(w.client(), "INSERT INTO kv VALUES ('a', '1')")
			p := w.client()
			f(0, 1000, 1000)
			p.Query("SELECT v FROM kv WHERE k = 'a'")
			w.run(time.Second)
			n, size := 0, 0
			for _, m := range p.Messages() {
				if m.Type == testclient.MsgDataRow {
					n++
					size += len(m.Values[0])
				}
			}
			return "rows " + itoa(n) + " bytes " + itoa(size)
		}, after: func(w *world) string {
			p := w.client()
			w.exchange(p, "SELECT v FROM kv WHERE k = 'a'")
			return strings.Join(lastRows(p.Messages()), ",")
		}, afterWant: "1"},
		{name: "unexpected-state-open-transaction", kind: fakepg.FaultDirtyState, noClr: true, expect: "T D C:SELECT 1 ZT", steps: func(w *world, f func(int64, int64, int64)) string {
			p := w.client()
			f(id(p), fakepg.DirtyOpenTx, 0)
			return w.exchange(p, "SELECT 1")
		}},
		{name: "unexpected-state-setting", kind: fakepg.FaultDirtyState, noClr: true, expect: "fakepg_dirty", steps: func(w *world, f func(int64, int64, int64)) string {
			p := w.client()
			f(id(p), fakepg.DirtySetting, 0)
			w.exchange(p, "SHOW search_path")
			rows := lastRows(p.Messages())
			return rows[len(rows)-1]
		}},
		{name: "unexpected-parameter-status", kind: fakepg.FaultParamStatus, noClr: true, expect: "S:application_name fakepg-surprise-1", steps: func(w *world, f func(int64, int64, int64)) string {
			p := w.client()
			from := len(p.Events())
			f(0, 0, 0)
			w.run(time.Millisecond)
			m := p.Messages()
			return p.TraceFrom(from) + " " + m[len(m)-1].Value
		}},
		{name: "killed", kind: fakepg.FaultKill, noClr: true, expect: "reset reset | rows 1", steps: func(w *world, f func(int64, int64, int64)) string {
			a, b := w.client(), w.client()
			w.exchange(a, "INSERT INTO kv VALUES ('a', '1')")
			a.Query("SELECT pg_sleep(1)")
			w.run(time.Millisecond)
			fa, fb := len(a.Events()), len(b.Events())
			f(0, 0, 0)
			w.run(time.Millisecond)
			return a.TraceFrom(fa) + " " + b.TraceFrom(fb) + " | rows " + itoa(int(w.srv.Snapshot().TableRows))
		}},
		{name: "frozen", kind: fakepg.FaultFreeze, expect: "nothing / connected | after clear T D C:SELECT 1 ZI", steps: func(w *world, f func(int64, int64, int64)) string {
			p := w.client()
			f(0, 0, 0)
			from := len(p.Events())
			p.Query("SELECT 1")
			q := w.dial()
			q.Startup("app", "")
			w.run(time.Second)
			out := p.TraceFrom(from)
			if out == "" {
				out = "nothing"
			}
			out += " / " + q.Trace()
			w.clear(fakepg.FaultFreeze, 0)
			w.run(100 * time.Millisecond)
			q.Close()
			return out + " | after clear " + p.TraceFrom(from)
		}},
		{name: "restarted-until-clear", kind: fakepg.FaultRestart, expect: "reset / connected E:57P03 eof", steps: func(w *world, f func(int64, int64, int64)) string {
			a := w.client()
			fa := len(a.Events())
			f(0, 0, 0)
			w.run(time.Second)
			early := w.dial()
			early.Startup("app", "")
			w.run(time.Millisecond)
			return a.TraceFrom(fa) + " / " + early.Trace()
		}},
		{name: "restarted-cold", kind: fakepg.FaultRestart, noClr: true, expect: "reset / connected E:57P03 eof / rows 1 / ZI", steps: func(w *world, f func(int64, int64, int64)) string {
			a := w.client()
			w.exchange(a, "INSERT INTO kv VALUES ('a', '1')")
			fa := len(a.Events())
			f(0, 0, int64(100*time.Millisecond))
			w.run(time.Millisecond)
			early := w.dial()
			early.Startup("app", "")
			w.run(time.Millisecond)
			out := a.TraceFrom(fa) + " / " + early.Trace() + " / rows " + itoa(int(w.srv.Snapshot().TableRows))
			w.run(200 * time.Millisecond)
			late := w.client()
			late.Close()
			return out + " / ZI"
		}},
		{name: "max-connections-reached", kind: fakepg.FaultMaxConn, expect: "connected E:53300 eof", steps: func(w *world, f func(int64, int64, int64)) string {
			a := w.client()
			f(0, 1, 0)
			p := w.dial()
			p.Startup("app", "")
			w.run(time.Millisecond)
			a.Close()
			return p.Trace()
		}, after: func(w *world) string {
			a, b := w.dial(), w.dial()
			a.Startup("app", "")
			b.Startup("app", "")
			w.run(time.Millisecond)
			return a.TraceFrom(len(a.Events())-1) + " " + b.TraceFrom(len(b.Events())-1)
		}, afterWant: "ZI ZI"},
		{name: "latency-at-timeout-boundary", kind: fakepg.FaultBoundary, expect: "250.1ms", steps: func(w *world, f func(int64, int64, int64)) string {
			p := w.client()
			f(0, 0, int64(250*time.Millisecond))
			from, at := len(p.Events()), w.s.Now()
			p.Query("SELECT 1")
			w.run(time.Second)
			return elapsed(p, from, at).String()
		}},
		{name: "some-connections-slow", kind: fakepg.FaultSlowConn, expect: "slow fast", steps: func(w *world, f func(int64, int64, int64)) string {
			a, b := w.client(), w.client()
			f(id(a), 0, int64(100*time.Millisecond))
			fa, fb, at := len(a.Events()), len(b.Events()), w.s.Now()
			a.Query("SELECT 1")
			b.Query("SELECT 1")
			w.run(time.Second)
			out := "fast"
			if elapsed(a, fa, at) >= 100*time.Millisecond {
				out = "slow"
			}
			if elapsed(b, fb, at) < 100*time.Millisecond {
				out += " fast"
			}
			return out
		}, after: func(w *world) string { return replySpeed(w, w.ps[0], "SELECT 1", 100*time.Millisecond) }, afterWant: "fast"},
		{name: "half-open", kind: fakepg.FaultHalfOpen, noClr: true, expect: " | open 0 | half-open 1", steps: func(w *world, f func(int64, int64, int64)) string {
			p := w.client()
			f(id(p), 0, 0)
			from := len(p.Events())
			p.Query("SELECT 1")
			w.run(time.Second)
			out := p.TraceFrom(from) + " | open " + itoa(int(w.srv.Snapshot().ConnsOpen.Value))
			p.Close()
			w.run(time.Millisecond)
			return out + " | half-open " + itoa(int(w.n.Snapshot().Closed.HalfOpen))
		}},
		{name: "asymmetric-latency", kind: fakepg.FaultAsym, expect: "40.1ms", steps: func(w *world, f func(int64, int64, int64)) string {
			p := w.client()
			f(0, 0, int64(30*time.Millisecond))
			from, at := len(p.Events()), w.s.Now()
			p.Query("SELECT 1")
			w.run(time.Second)
			return elapsed(p, from, at).String()
		}},
		{name: "stall-window", kind: fakepg.FaultStallWindow, expect: "160.1ms", steps: func(w *world, f func(int64, int64, int64)) string {
			p := w.client()
			f(0, int64(time.Hour), int64(150*time.Millisecond))
			from, at := len(p.Events()), w.s.Now()
			p.Query("SELECT 1")
			w.run(time.Second)
			return elapsed(p, from, at).String()
		}, after: func(w *world) string { return replySpeed(w, w.client(), "SELECT 1", 150*time.Millisecond) }, afterWant: "fast"},
		{name: "latency-at-timeout-boundary-recovers", kind: fakepg.FaultBoundary, expect: "slow", steps: func(w *world, f func(int64, int64, int64)) string {
			f(0, 0, int64(time.Hour))
			return replySpeed(w, w.client(), "SELECT 1", time.Second)
		}, after: func(w *world) string { return replySpeed(w, w.client(), "SELECT 1", time.Second) }, afterWant: "fast"},
	}
}

func compact(tr string) string {
	f := strings.Fields(tr)
	var out []string
	for _, x := range f {
		if strings.HasPrefix(x, "S:") || x == "K" {
			continue
		}
		out = append(out, x)
	}
	return strings.Join(out, " ")
}

func replySpeed(w *world, p *workload.Probe, q string, limit time.Duration) string {
	from, at := len(p.Events()), w.s.Now()
	p.Query(q)
	w.run(2 * limit)
	if d := elapsed(p, from, at); d >= 0 && d < limit {
		return "fast"
	}
	return "slow"
}

func pieces(w *world, p *workload.Probe) string {
	before := w.n.Snapshot().Down
	w.exchange(p, "SELECT 1")
	after := w.n.Snapshot().Down
	if after.Written == before.Written {
		return "nothing written"
	}
	if after.Chunks-before.Chunks < after.Written-before.Written {
		return "whole"
	}
	return "one-byte pieces"
}

func runFaultCase(t *testing.T, fc faultCase, skipClear bool) error {
	cfg := fc.cfg
	if cfg.Service.Fixed == 0 {
		cfg.Service.Fixed = 10 * time.Millisecond
	}
	if skipClear {
		fakepg.PlantSkipClear(&cfg, fc.kind)
	}
	w := newWorld(t, cfg)
	w.soft = true
	traced := 0
	w.s.SetTraceTap(func(r sim.Record) {
		if r.Kind == sim.KindFault && r.A == int64(fc.kind) {
			traced++
		}
	})
	fail := func(format string, args ...any) error {
		w.closeAll()
		return fmt.Errorf(format, args...)
	}
	got := fc.steps(w, w.inject(fc.kind))
	if got != fc.expect {
		return fail("observed %q, want %q", got, fc.expect)
	}
	if n := w.srv.Snapshot().Fault(fc.kind); n == 0 || traced == 0 {
		return fail("fault counter %d, trace records %d", n, traced)
	}
	if !fc.noClr {
		w.clear(fc.kind, 0)
		if w.srv.Snapshot().Faults.Cleared == 0 {
			return fail("clear not counted")
		}
	}
	w.run(time.Second)
	if fc.after != nil {
		if got := fc.after(w); got != fc.afterWant {
			return fail("after the clear, the fault's own effect: %q, want %q", got, fc.afterWant)
		}
	}
	p := w.client()
	if got := w.exchange(p, "SELECT 1"); got != "T D C:SELECT 1 ZI" {
		return fail("after the fault, a fresh exchange: %s", got)
	}
	w.closeAll()
	w.quiet()
	return w.err
}

func TestEachDatabaseFault(t *testing.T) {
	for _, fc := range faultCases() {
		t.Run(fc.name, func(t *testing.T) {
			if err := runFaultCase(t, fc, false); err != nil {
				t.Fatal(err)
			}
		})
	}
}

var oneShotKinds = map[sim.FaultKind]bool{fakepg.FaultTerminate: true, fakepg.FaultDirtyState: true, fakepg.FaultParamStatus: true,
	fakepg.FaultKill: true, fakepg.FaultHalfOpen: true, fakepg.FaultClear: true}

func TestEveryClearPathIsChecked(t *testing.T) {
	checked := map[sim.FaultKind]bool{}
	for _, fc := range faultCases() {
		if fc.noClr {
			continue
		}
		if oneShotKinds[fc.kind] {
			t.Fatalf("%s: a one-shot kind has a clear case", fc.name)
		}
		t.Run(fc.name, func(t *testing.T) {
			if err := runFaultCase(t, fc, true); err == nil {
				t.Fatalf("with the clear of %s disabled, the case still passes", fakepg.FaultName(fc.kind))
			} else {
				t.Logf("caught: %v", err)
			}
		})
		checked[fc.kind] = true
	}
	for _, k := range fakepg.FaultKinds() {
		if !oneShotKinds[k] && !checked[k] {
			t.Errorf("%s has a clear path that no case checks", fakepg.FaultName(k))
		}
	}
}

func TestEveryFaultKindHasACase(t *testing.T) {
	covered := map[sim.FaultKind]bool{fakepg.FaultClear: true}
	for _, fc := range faultCases() {
		covered[fc.kind] = true
	}
	for _, k := range fakepg.FaultKinds() {
		if !covered[k] {
			t.Errorf("no test case for %s", fakepg.FaultName(k))
		}
	}
	_ = simnet.FaultReset
}
