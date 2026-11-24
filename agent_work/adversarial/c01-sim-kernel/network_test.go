package adv_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
)

type pair struct {
	s    *sim.Sim
	n    *simnet.Net
	c, b *simnet.End
}

func connected(t *testing.T, o simnet.Options) pair {
	t.Helper()
	s := sim.New(sim.Config{Seed: seed})
	n := simnet.New(s)
	var b *simnet.End
	_, e := n.Listen("peer", func(x *simnet.End) { b = x })
	must(t, e)
	c := n.Dial("peer", o, nil)
	must(t, s.RunUntilQuiet())
	if b == nil {
		t.Fatal("not accepted")
	}
	return pair{s, n, c, b}
}
func apply(p pair, k sim.FaultKind, a, b int64) {
	p.s.ApplyFault(sim.Fault{Kind: k, Target: p.c.ID(), Arg1: a, Arg2: b})
}
func transfer(t *testing.T, mode simnet.Chunking, sd int64) {
	t.Helper()
	o := simnet.Options{Window: 17, Up: simnet.Direction{Chunking: mode, ChunkSize: 1, Latency: simnet.Latency{Base: 3, Jitter: 19}}, Down: simnet.Direction{Chunking: mode, ChunkSize: 3, Latency: simnet.Latency{Base: 7, Jitter: 31}}}
	p := connected(t, o)
	r := sim.NewStream(sd, "payload")
	want := make([]byte, 513)
	for i := range want {
		want[i] = byte(r.Uint64())
	}
	var got [2][]byte
	ends := []*simnet.End{p.c, p.b}
	var sent [2]int
	var wakes [2]int
	for i, e := range ends {
		e.SetHandler(simnet.HandlerFunc(func(_ *simnet.End, ev simnet.Event) {
			if ev == simnet.EvWritable {
				wakes[i]++
			}
		}))
	}
	for steps := 0; steps < 10000 && (len(got[0]) < len(want) || len(got[1]) < len(want)); steps++ {
		for i, e := range ends {
			if sent[i] < len(want) {
				end := min(len(want), sent[i]+1+r.IntN(33))
				n, err := e.Write(want[sent[i]:end])
				must(t, err)
				sent[i] += n
			}
		}
		must(t, p.s.RunUntilQuiet())
		for i, e := range ends {
			if steps%3 != 0 {
				buf := make([]byte, 1+r.IntN(11))
				n, err := e.Read(buf)
				must(t, err)
				got[1-i] = append(got[1-i], buf[:n]...)
			}
		}
		st := p.n.Snapshot()
		if st.Up.InFlight > 17 || st.Down.InFlight > 17 {
			t.Fatalf("seed=%d window exceeded %+v", sd, st)
		}
	}
	for i := range got {
		if !bytes.Equal(got[i], want) {
			t.Fatalf("seed=%d direction=%d received %d of %d or corrupted", sd, i, len(got[i]), len(want))
		}
		if wakes[i] == 0 {
			t.Fatalf("seed=%d writer %d never woke", sd, i)
		}
	}
	must(t, p.c.Close())
	must(t, p.b.Close())
	must(t, p.s.RunUntilQuiet())
	must(t, p.s.CheckQuiesce())
}
func TestAdv_4_DuplexTinyWindow(t *testing.T) {
	mark(t)
	for _, m := range []simnet.Chunking{simnet.ChunkWhole, simnet.ChunkFixed, simnet.ChunkRandom} {
		for i := int64(0); i < 16; i++ {
			transfer(t, m, seed+i)
		}
	}
}
func TestAdv_4_MaximumJitter(t *testing.T) {
	mark(t)
	defer func() {
		if v := recover(); v != nil {
			t.Fatalf("valid nonnegative jitter panicked: %v", v)
		}
	}()
	o := simnet.Options{Window: 1, Up: simnet.Direction{Latency: simnet.Latency{Jitter: time.Duration(math.MaxInt64)}}}
	p := connected(t, o)
	_, e := p.c.Write([]byte{1})
	must(t, e)
	must(t, p.s.RunUntilQuiet())
	b := make([]byte, 1)
	n, e := p.b.Read(b)
	must(t, e)
	if n != 1 {
		t.Fatalf("received %d bytes", n)
	}
}
func TestAdv_4_LatencySumSaturates(t *testing.T) {
	mark(t)
	o := simnet.Options{Up: simnet.Direction{Latency: simnet.Latency{Base: time.Duration(math.MaxInt64 - 100), Jitter: 1000}}}
	p := connected(t, o)
	if p.s.Now() < sim.Time(math.MaxInt64-100) {
		t.Fatalf("positive latency wrapped: connected at %d before base %d", p.s.Now(), int64(math.MaxInt64-100))
	}
}
func TestAdv_3_RefusalRoundTripOverflow(t *testing.T) {
	mark(t)
	s := sim.New(sim.Config{Seed: seed})
	n := simnet.New(s)
	at := sim.Time(-1)
	n.Dial("absent", simnet.Options{Up: simnet.Direction{Latency: simnet.Latency{Base: math.MaxInt64}}, Down: simnet.Direction{Latency: simnet.Latency{Base: 1}}}, simnet.HandlerFunc(func(_ *simnet.End, e simnet.Event) {
		if e == simnet.EvRefused {
			at = s.Now()
		}
	}))
	must(t, s.RunUntilQuiet())
	if at != sim.MaxTime {
		t.Fatalf("round trip overflow: refusal at %d, want %d", at, sim.MaxTime)
	}
}
func TestAdv_5_StalledFINBothEnds(t *testing.T) {
	mark(t)
	for side := 0; side < 2; side++ {
		p := connected(t, simnet.Options{Window: 9})
		ends := []*simnet.End{p.c, p.b}
		a, b := ends[side], ends[1-side]
		apply(p, simnet.FaultStall, int64(1<<side), 0)
		n, e := a.Write([]byte("fin-first"))
		must(t, e)
		if n != 9 {
			t.Fatal(n)
		}
		must(t, a.CloseWrite())
		must(t, p.s.RunUntilQuiet())
		buf := make([]byte, 20)
		if n, e := b.Read(buf); n != 0 || e != nil {
			t.Fatalf("stalled bytes/FIN escaped: n=%d err=%v", n, e)
		}
		apply(p, simnet.FaultClear, int64(simnet.FaultStall), 0)
		must(t, p.s.RunUntilQuiet())
		n, e = b.Read(buf)
		must(t, e)
		if string(buf[:n]) != "fin-first" {
			t.Fatalf("held bytes %q", buf[:n])
		}
		if _, e = b.Read(buf); e != io.EOF {
			t.Fatalf("missing EOF: %v", e)
		}
		if _, e = a.Write([]byte{1}); !errors.Is(e, simnet.ErrClosed) {
			t.Fatalf("write after FIN: %v", e)
		}
	}
}
func TestAdv_5_ResetWhileStalled(t *testing.T) {
	mark(t)
	p := connected(t, simnet.Options{Window: 4})
	var events []bool
	for _, e := range []*simnet.End{p.c, p.b} {
		e.SetHandler(simnet.HandlerFunc(func(e *simnet.End, v simnet.Event) {
			if v == simnet.EvReset {
				events = append(events, e.IsClient())
			}
		}))
	}
	apply(p, simnet.FaultStall, 0, 9)
	p.c.Write([]byte("abcd"))
	p.b.Write([]byte("efgh"))
	apply(p, simnet.FaultReset, 0, 0)
	must(t, p.s.RunUntilQuiet())
	if !reflect.DeepEqual(events, []bool{true, false}) {
		t.Fatalf("reset order %v", events)
	}
	for _, e := range []*simnet.End{p.c, p.b} {
		if n, err := e.Read(make([]byte, 4)); n != 0 || err != simnet.ErrReset {
			t.Fatalf("post-reset read %d %v", n, err)
		}
	}
	st := p.n.Snapshot()
	if st.Up.Discarded != 4 || st.Down.Discarded != 4 {
		t.Fatalf("discard accounting %+v", st)
	}
	must(t, p.s.CheckQuiesce())
}
func TestAdv_5_HalfOpenDuringHandshake(t *testing.T) {
	mark(t)
	s := sim.New(sim.Config{Seed: seed})
	n := simnet.New(s)
	var b *simnet.End
	n.Listen("p", func(e *simnet.End) { b = e })
	var evs []simnet.Event
	c := n.Dial("p", simnet.Options{Down: simnet.Direction{Latency: simnet.Latency{Base: 100}}}, simnet.HandlerFunc(func(_ *simnet.End, e simnet.Event) { evs = append(evs, e) }))
	must(t, s.RunUntil(0))
	s.ApplyFault(sim.Fault{Kind: simnet.FaultHalfOpen, Target: c.ID(), Arg1: 0})
	must(t, b.Close())
	must(t, s.RunUntilQuiet())
	if len(evs) != 0 {
		t.Fatalf("gone client saw events %v", evs)
	}
	must(t, s.CheckQuiesce())
	if n.Snapshot().Closed.HalfOpen != 1 {
		t.Fatalf("wrong close reason %+v", n.Snapshot())
	}
}
func TestAdv_5_ListenerClearDoesNotReviveOldDial(t *testing.T) {
	mark(t)
	for _, k := range []sim.FaultKind{simnet.FaultRefuse, simnet.FaultDialStall, simnet.FaultSilence} {
		t.Run(fmt.Sprint(k), func(t *testing.T) {
			s := sim.New(sim.Config{Seed: seed})
			n := simnet.New(s)
			accepted := 0
			n.Listen("p", func(*simnet.End) { accepted++ })
			s.ApplyFault(sim.Fault{Kind: k, Target: 0})
			var events []simnet.Event
			c := n.Dial("p", simnet.Options{Window: 1}, simnet.HandlerFunc(func(_ *simnet.End, e simnet.Event) { events = append(events, e) }))
			must(t, s.RunUntilQuiet())
			s.ApplyFault(sim.Fault{Kind: simnet.FaultClear, Target: 0, Arg1: int64(k)})
			n.Dial("p", simnet.Options{}, nil)
			must(t, s.RunUntilQuiet())
			if accepted != 1 {
				t.Fatalf("accepts=%d want fresh dial only", accepted)
			}
			switch k {
			case simnet.FaultRefuse:
				if !reflect.DeepEqual(events, []simnet.Event{simnet.EvRefused}) {
					t.Fatal(events)
				}
			case simnet.FaultDialStall:
				if len(events) != 0 {
					t.Fatal(events)
				}
				if _, e := c.Write([]byte{1}); e != simnet.ErrNotConnected {
					t.Fatal(e)
				}
			case simnet.FaultSilence:
				if !reflect.DeepEqual(events, []simnet.Event{simnet.EvConnected}) {
					t.Fatal(events)
				}
				if n, e := c.Write([]byte{1, 2}); n != 1 || e != nil {
					t.Fatalf("silence window %d %v", n, e)
				}
			}
		})
	}
}
func TestAdv_6_ClearBeforeArrival(t *testing.T) {
	mark(t)
	var states [2]sim.Digest
	for i := range states {
		p := connected(t, simnet.Options{Up: simnet.Direction{Latency: simnet.Latency{Base: 100}}})
		p.c.Write([]byte("already travelling"))
		if i == 1 {
			apply(p, simnet.FaultStall, 1, 1000)
			apply(p, simnet.FaultClear, int64(simnet.FaultStall), 0)
		}
		must(t, p.s.RunUntilQuiet())
		b := make([]byte, 32)
		n, e := p.b.Read(b)
		must(t, e)
		if string(b[:n]) != "already travelling" {
			t.Fatalf("got %q", b[:n])
		}
		states[i] = p.s.StateDigest()
	}
	if states[0] != states[1] {
		t.Fatal("pre-arrival clear left state residue")
	}
}
func TestAdv_6_ChunkClearRestoresRandomSequence(t *testing.T) {
	mark(t)
	var ds [2]sim.Digests
	for i := range ds {
		p := connected(t, simnet.Options{Up: simnet.Direction{Chunking: simnet.ChunkRandom, Latency: simnet.Latency{Jitter: 100}}})
		if i == 1 {
			apply(p, simnet.FaultChunk, 1, 1)
			apply(p, simnet.FaultClear, int64(simnet.FaultChunk), 0)
		}
		var records []sim.Record
		p.s.SetTraceTap(func(r sim.Record) { records = append(records, r) })
		p.c.Write(bytes.Repeat([]byte{42}, 333))
		must(t, p.s.RunUntilQuiet())
		p.b.Read(make([]byte, 333))
		s := sim.New(sim.Config{Seed: seed})
		for _, r := range records {
			s.At(r.At, func() { s.Trace(r.Kind, r.A, r.B, r.C, r.D) })
		}
		must(t, s.RunUntilQuiet())
		ds[i] = sim.Digests{Trace: s.TraceDigest(), State: p.s.StateDigest()}
	}
	if ds[0] != ds[1] {
		t.Fatalf("clear shifted random traffic: %v", ds)
	}
}
func TestAdv_6_FaultedNeighborTraffic(t *testing.T) {
	mark(t)
	run := func(fault bool) []sim.Record {
		p := connected(t, simnet.Options{})
		c := p.n.Dial("peer", simnet.Options{Up: simnet.Direction{Chunking: simnet.ChunkRandom, Latency: simnet.Latency{Jitter: 91}}}, nil)
		must(t, p.s.RunUntilQuiet())
		var out []sim.Record
		p.s.SetTraceTap(func(r sim.Record) {
			if r.A == c.ID() && r.Kind != sim.KindFault {
				out = append(out, r)
			}
		})
		for i := 0; i < 30; i++ {
			if fault {
				apply(p, simnet.FaultStall, 0, 1)
				apply(p, simnet.FaultChunk, 0, 1)
				p.c.Write([]byte("noise"))
				apply(p, simnet.FaultClear, int64(simnet.FaultStall), 0)
			}
			c.Write([]byte("neighbor"))
			must(t, p.s.RunUntil(p.s.Now()+100))
			c.Conn().Server().Read(make([]byte, 100))
		}
		return out
	}
	a, b := run(false), run(true)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("neighbor trace differs: control=%v faulted=%v", a, b)
	}
}
func TestAdv_7_ConnectingLeakWithoutTimers(t *testing.T) {
	mark(t)
	s := sim.New(sim.Config{Seed: seed})
	n := simnet.New(s)
	n.Listen("p", nil)
	s.ApplyFault(sim.Fault{Kind: simnet.FaultDialStall})
	c := n.Dial("p", simnet.Options{}, nil)
	must(t, s.RunUntilQuiet())
	if s.Snapshot().HeapLen != 0 {
		t.Fatal("attack has timers")
	}
	if s.CheckQuiesce() == nil {
		t.Fatal("undetected stalled connection")
	}
	must(t, c.Close())
	must(t, s.CheckQuiesce())
}
func TestAdv_7_HeldBytesWithoutTimers(t *testing.T) {
	mark(t)
	p := connected(t, simnet.Options{})
	apply(p, simnet.FaultStall, 0, 0)
	p.c.Write([]byte("held"))
	must(t, p.s.RunUntilQuiet())
	if p.s.Snapshot().HeapLen != 0 {
		t.Fatal("attack has timers")
	}
	if e := p.s.CheckQuiesce(); e == nil || !bytes.Contains([]byte(e.Error()), []byte("bytes in flight")) {
		t.Fatalf("held bytes not reported: %v", e)
	}
	must(t, p.c.Reset())
	must(t, p.s.RunUntilQuiet())
	must(t, p.s.CheckQuiesce())
}
func TestAdv_4_LongSeedCampaign(t *testing.T) {
	mark(t)
	if os.Getenv("ADV_LONG") != "1" {
		t.Skip("ADV_LONG=1 enables 2048 additional duplex seeds")
	}
	for i := int64(0); i < 2048; i++ {
		transfer(t, simnet.ChunkRandom, seed+i*7919)
	}
}

func TestAdv_4_ClosedReaderFreesBlockedWriter(t *testing.T) {
	mark(t)
	for side := 0; side < 2; side++ {
		t.Run(fmt.Sprint(side), func(t *testing.T) {
			p := connected(t, simnet.Options{Window: 2})
			ends := []*simnet.End{p.c, p.b}
			writer, reader := ends[side], ends[1-side]
			wakes := 0
			writer.SetHandler(simnet.HandlerFunc(func(_ *simnet.End, ev simnet.Event) {
				if ev == simnet.EvWritable {
					wakes++
				}
			}))
			n, e := writer.Write([]byte("full"))
			must(t, e)
			if n != 2 {
				t.Fatalf("accepted=%d want 2", n)
			}
			must(t, p.s.RunUntilQuiet())
			if writer.Space() != 0 {
				t.Fatal("window did not fill")
			}
			must(t, reader.Close())
			must(t, p.s.RunUntilQuiet())
			if writer.Space() != 2 || wakes != 1 {
				t.Fatalf("discard freed window but writable missing: space=%d wakes=%d want space=2 wakes=1", writer.Space(), wakes)
			}
			n, e = writer.Write([]byte("ok"))
			must(t, e)
			if n != 2 {
				t.Fatalf("recovery accepted=%d", n)
			}
			must(t, p.s.RunUntilQuiet())
			must(t, writer.Close())
			must(t, p.s.RunUntilQuiet())
			must(t, p.s.CheckQuiesce())
		})
	}
}
