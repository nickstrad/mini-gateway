package simnet

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
)

type recorder struct {
	events []string
	got    []byte
	eof    bool
	onEv   func(e *End, ev Event)
}

func (r *recorder) OnEvent(e *End, ev Event) {
	r.events = append(r.events, fmt.Sprintf("%d:%s", int64(e.c.net.s.Now()), ev))
	if ev == EvReadable {
		buf := make([]byte, 1024)
		for {
			n, err := e.Read(buf)
			r.got = append(r.got, buf[:n]...)
			if err == io.EOF {
				r.eof = true
			}
			if n == 0 || err != nil {
				break
			}
		}
	}
	if r.onEv != nil {
		r.onEv(e, ev)
	}
}

type world struct {
	s   *sim.Sim
	n   *Net
	srv []*End
	rec []*recorder
}

func newWorld(seed int64) *world {
	w := &world{s: sim.New(sim.Config{Seed: seed})}
	w.n = New(w.s)
	_, _ = w.n.Listen("srv", func(e *End) {
		r := &recorder{}
		w.srv = append(w.srv, e)
		w.rec = append(w.rec, r)
		e.SetHandler(r)
	})
	return w
}

func fixed(lat time.Duration) Options {
	o := DefaultOptions()
	o.Up.Latency = Latency{Base: lat}
	o.Down.Latency = Latency{Base: lat}
	return o
}

func TestConnectedPrecedesReadableEvenWhenServerWritesFirst(t *testing.T) {
	o := DefaultOptions()
	o.Down.Latency = Latency{Base: time.Millisecond, Jitter: 500 * time.Microsecond}
	for seed := int64(1); seed <= 30; seed++ {
		w := newWorld(seed)
		cli := &recorder{}
		w.n.Listen("greeter", func(e *End) {
			e.SetHandler(&recorder{})
			_, _ = e.Write([]byte("hello"))
		})
		w.n.Dial("greeter", o, cli)
		_ = w.s.RunFor(time.Second)
		if len(cli.events) < 2 || !bytes.HasSuffix([]byte(cli.events[0]), []byte(":connected")) || string(cli.got) != "hello" {
			t.Fatalf("seed %d: events %v got %q", seed, cli.events, cli.got)
		}
	}
}

func TestHandshakeTakesOneRoundTripOnTheVirtualClock(t *testing.T) {
	w := newWorld(1)
	o := DefaultOptions()
	o.Up.Latency = Latency{Base: 3 * time.Millisecond}
	o.Down.Latency = Latency{Base: 5 * time.Millisecond}
	cli := &recorder{}
	w.n.Dial("srv", o, cli)
	_ = w.s.RunUntil(sim.Time(3*time.Millisecond) - 1)
	if len(w.srv) != 0 {
		t.Fatal("accepted before the up latency")
	}
	_ = w.s.RunUntil(sim.Time(3 * time.Millisecond))
	if len(w.srv) != 1 || len(cli.events) != 0 {
		t.Fatalf("accept at 3ms: %d servers, client %v", len(w.srv), cli.events)
	}
	_ = w.s.RunUntil(sim.Time(8 * time.Millisecond))
	if !slices.Equal(cli.events, []string{"8000000:connected"}) {
		t.Fatalf("client %v", cli.events)
	}
	_, _ = w.srv[0].Write([]byte("x"))
	_ = w.s.RunUntil(sim.Time(13*time.Millisecond) - 1)
	if len(cli.got) != 0 {
		t.Fatal("down byte early")
	}
	_ = w.s.RunUntil(sim.Time(13 * time.Millisecond))
	if string(cli.got) != "x" {
		t.Fatal("down byte not delivered after its latency")
	}
}

func TestAsymmetricLatencyAndJitterAreSeeded(t *testing.T) {
	trace := func(seed int64) []sim.Record {
		w := newWorld(seed)
		var recs []sim.Record
		w.s.SetTraceTap(func(r sim.Record) { recs = append(recs, r) })
		o := DefaultOptions()
		o.Up = Direction{Chunking: ChunkRandom, Latency: Latency{Base: time.Millisecond, Jitter: time.Millisecond}}
		o.Down = Direction{Chunking: ChunkFixed, ChunkSize: 5, Latency: Latency{Base: 10 * time.Microsecond, Jitter: 7 * time.Microsecond}}
		cli := &recorder{onEv: func(e *End, ev Event) {
			if ev == EvConnected {
				_, _ = e.Write(bytes.Repeat([]byte("abcdefg"), 100))
			}
		}}
		w.n.Dial("srv", o, cli)
		_ = w.s.RunFor(time.Second)
		return recs
	}
	a, b, c := trace(5), trace(5), trace(6)
	if !slices.Equal(a, b) {
		t.Fatal("same seed, different traces")
	}
	if slices.Equal(a, c) {
		t.Fatal("different seeds, same trace")
	}
	for _, r := range a {
		if r.Kind == kindDeliver && r.B == up && (r.C < 1 || r.C > 700) {
			t.Fatalf("random piece of %d bytes", r.C)
		}
	}
}

func TestRandomChunkingBoundsPieceCount(t *testing.T) {
	w := newWorld(3)
	o := DefaultOptions()
	o.Up.Chunking = ChunkRandom
	cli := &recorder{onEv: func(e *End, ev Event) {
		if ev == EvConnected {
			for i := 0; i < 50; i++ {
				_, _ = e.Write(make([]byte, 1000))
			}
		}
	}}
	o.Window = MaxWindow
	w.n.Dial("srv", o, cli)
	_ = w.s.RunFor(time.Second)
	snap := w.n.Snapshot()
	if snap.Up.Chunks > 50*MaxRandomPieces || snap.Up.Chunks <= 50 {
		t.Fatalf("%d pieces for 50 writes", snap.Up.Chunks)
	}
	if len(w.rec[0].got) != 50000 {
		t.Fatalf("got %d", len(w.rec[0].got))
	}
}

func TestWriteAfterCloseWriteAndReadAfterClose(t *testing.T) {
	w := newWorld(1)
	cli := &recorder{}
	e := w.n.Dial("srv", fixed(time.Millisecond), cli)
	if _, err := e.Write([]byte("x")); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("write while connecting: %v", err)
	}
	if _, err := e.Read(make([]byte, 1)); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("read while connecting: %v", err)
	}
	if err := e.CloseWrite(); !errors.Is(err, ErrNotConnected) {
		t.Fatal(err)
	}
	_ = w.s.RunFor(10 * time.Millisecond)
	if err := e.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Write([]byte("x")); !errors.Is(err, ErrClosed) {
		t.Fatalf("write after CloseWrite: %v", err)
	}
	_, _ = w.srv[0].Write([]byte("still flowing"))
	_ = w.s.RunFor(10 * time.Millisecond)
	if string(cli.got) != "still flowing" || !w.rec[0].eof {
		t.Fatalf("half-closed client could not read: %q eof %v", cli.got, w.rec[0].eof)
	}
	_ = e.Close()
	if _, err := e.Read(make([]byte, 1)); !errors.Is(err, ErrClosed) {
		t.Fatalf("read after close: %v", err)
	}
	if err := e.Close(); !errors.Is(err, ErrClosed) {
		t.Fatalf("second close: %v", err)
	}
	if err := e.Reset(); !errors.Is(err, ErrClosed) {
		t.Fatalf("reset after close: %v", err)
	}
	_ = w.srv[0].Close()
	_ = w.s.RunUntilQuiet()
	if err := w.s.CheckQuiesce(); err != nil {
		t.Fatal(err)
	}
	if w.n.Snapshot().Closed.Graceful != 1 {
		t.Fatal("not graceful")
	}
}

func TestCloseDiscardsUnreadAndLaterArrivals(t *testing.T) {
	w := newWorld(1)
	cli := &recorder{onEv: func(e *End, ev Event) {
		if ev == EvConnected {
			_, _ = e.Write([]byte("0123456789"))
		}
	}}
	w.n.Listen("lazy", func(e *End) { w.srv = append(w.srv, e) })
	e := w.n.Dial("lazy", fixed(time.Millisecond), cli)
	_ = w.s.RunFor(2500 * time.Microsecond)
	srv := w.srv[0]
	if srv.Buffered() != 0 {
		t.Fatal("bytes arrived early")
	}
	_ = w.s.RunFor(time.Millisecond)
	if srv.Buffered() != 10 {
		t.Fatalf("buffered %d", srv.Buffered())
	}
	_ = srv.Close()
	_, _ = e.Write([]byte("abc"))
	_ = w.s.RunFor(10 * time.Millisecond)
	snap := w.n.Snapshot()
	if snap.Up.Discarded != 13 || snap.Up.Delivered != 0 || snap.Up.InFlight != 0 {
		t.Fatalf("%+v", snap.Up)
	}
	if !cli.eof {
		t.Fatal("client did not see the server's FIN")
	}
	_ = e.Close()
	_ = w.s.RunUntilQuiet()
	if err := w.s.CheckQuiesce(); err != nil {
		t.Fatal(err)
	}
}

func TestNotificationsAreCoalescedPerEvent(t *testing.T) {
	w := newWorld(1)
	o := fixed(time.Millisecond)
	o.Up.Chunking, o.Up.ChunkSize = ChunkFixed, 1
	cli := &recorder{onEv: func(e *End, ev Event) {
		if ev == EvConnected {
			_, _ = e.Write([]byte("abcdefghij"))
		}
	}}
	w.n.Dial("srv", o, cli)
	_ = w.s.RunFor(10 * time.Millisecond)
	if string(w.rec[0].got) != "abcdefghij" {
		t.Fatalf("got %q", w.rec[0].got)
	}
	readable := 0
	for _, ev := range w.rec[0].events {
		if bytes.HasSuffix([]byte(ev), []byte("readable")) {
			readable++
		}
	}
	if readable != 1 {
		t.Fatalf("ten pieces at one instant gave %d readable events: %v", readable, w.rec[0].events)
	}
}

func TestWaitWritableWakesTheWriter(t *testing.T) {
	w := newWorld(1)
	o := fixed(time.Millisecond)
	o.Window = 10
	wakes := 0
	var e *End
	cli := &recorder{onEv: func(e *End, ev Event) {
		switch ev {
		case EvConnected:
			_, _ = e.Write(make([]byte, 8))
			if e.Space() != 2 {
				t.Fatalf("space %d", e.Space())
			}
			e.WaitWritable()
		case EvWritable:
			wakes++
		}
	}}
	e = w.n.Dial("srv", o, cli)
	_ = w.s.RunFor(10 * time.Millisecond)
	if wakes != 1 || e.Space() != 10 {
		t.Fatalf("wakes %d space %d", wakes, e.Space())
	}
}

func TestWritableNeverFiresForAWriterThatWasNotRefused(t *testing.T) {
	w := newWorld(1)
	wakes := 0
	cli := &recorder{onEv: func(e *End, ev Event) {
		if ev == EvConnected {
			_, _ = e.Write([]byte("abc"))
		}
		if ev == EvWritable {
			wakes++
		}
	}}
	w.n.Dial("srv", fixed(time.Millisecond), cli)
	_ = w.s.RunFor(10 * time.Millisecond)
	if wakes != 0 {
		t.Fatalf("%d writable events", wakes)
	}
}

func TestListenErrorsAndClosedListener(t *testing.T) {
	w := newWorld(1)
	if _, err := w.n.Listen("srv", nil); !errors.Is(err, ErrAddrInUse) {
		t.Fatalf("got %v", err)
	}
	l, _ := w.n.Listen("x", nil)
	if l.Name() != "x" || l.Index() != 1 {
		t.Fatal("listener identity")
	}
	l.Close()
	if w.n.Snapshot().Listeners != 1 {
		t.Fatal("closed listener counted")
	}
	cli := &recorder{}
	w.n.Dial("x", fixed(time.Millisecond), cli)
	_ = w.s.RunFor(time.Second)
	if !slices.Equal(cli.events, []string{"2000000:refused"}) {
		t.Fatalf("dial to a closed listener: %v", cli.events)
	}
	if _, err := w.n.Listen("x", nil); err != nil {
		t.Fatalf("a closed name cannot be reused: %v", err)
	}
	for i := 0; i < MaxListeners; i++ {
		if _, err := w.n.Listen(fmt.Sprint("l", i), nil); err != nil {
			if !errors.Is(err, ErrTooManyListen) {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatal("no listener limit")
}

func TestAbortOfAcceptedButUnconnectedDialResetsServer(t *testing.T) {
	w := newWorld(1)
	o := DefaultOptions()
	o.Up.Latency = Latency{Base: time.Millisecond}
	o.Down.Latency = Latency{Base: 10 * time.Millisecond}
	cli := &recorder{}
	e := w.n.Dial("srv", o, cli)
	_ = w.s.RunFor(2 * time.Millisecond)
	if len(w.srv) != 1 {
		t.Fatal("not accepted")
	}
	_ = e.Close()
	_ = w.s.RunUntilQuiet()
	if len(cli.events) != 0 || !slices.Equal(w.rec[0].events, []string{"2000000:reset"}) {
		t.Fatalf("client %v server %v", cli.events, w.rec[0].events)
	}
	snap := w.n.Snapshot()
	if snap.Closed.Aborted != 1 || snap.ConnsOpen != 0 || snap.ConnsConnecting != 0 {
		t.Fatalf("%+v", snap)
	}
	if err := w.s.CheckQuiesce(); err != nil {
		t.Fatal(err)
	}
}

func TestResetOfAnAcceptedConnectingDial(t *testing.T) {
	w := newWorld(1)
	o := DefaultOptions()
	o.Down.Latency = Latency{Base: 10 * time.Millisecond}
	cli := &recorder{}
	w.n.Dial("srv", o, cli)
	_ = w.s.RunFor(time.Millisecond)
	w.s.ApplyFault(sim.Fault{Kind: FaultReset, Target: 1})
	_ = w.s.RunUntilQuiet()
	if len(cli.events) != 1 || cli.events[0] != "1000000:reset" || w.rec[0].events[0] != "1000000:reset" {
		t.Fatalf("client %v server %v", cli.events, w.rec[0].events)
	}
	if err := w.s.CheckQuiesce(); err != nil {
		t.Fatal(err)
	}
}

func TestStallOneDirectionOnly(t *testing.T) {
	w := newWorld(1)
	cli := &recorder{onEv: func(e *End, ev Event) {
		if ev == EvConnected {
			_, _ = e.Write([]byte("up"))
		}
	}}
	w.n.Dial("srv", fixed(time.Millisecond), cli)
	_ = w.s.RunFor(1500 * time.Microsecond)
	w.s.ApplyFault(sim.Fault{Kind: FaultStall, Target: 1, Arg1: DirUp})
	_, _ = w.srv[0].Write([]byte("down"))
	_ = w.s.RunFor(time.Second)
	if string(cli.got) != "down" || len(w.rec[0].got) != 0 {
		t.Fatalf("down %q up %q", cli.got, w.rec[0].got)
	}
	w.s.ApplyFault(sim.Fault{Kind: FaultClear, Target: 1, Arg1: int64(FaultStall)})
	_ = w.s.RunFor(0)
	if string(w.rec[0].got) != "up" {
		t.Fatalf("held bytes not released on clear: %q", w.rec[0].got)
	}
	snap := w.n.Snapshot()
	if snap.Faults.Stall != 1 || snap.Faults.Clear != 1 {
		t.Fatalf("%+v", snap.Faults)
	}
}

func TestStallFillsWindowThenWriterResumes(t *testing.T) {
	pr := newPair(4, Options{Window: 100, Up: Direction{Latency: Latency{Base: time.Millisecond}}, Down: Direction{Latency: Latency{Base: time.Millisecond}}}, 1000, 0)
	pr.srv.noFin = true
	pr.s.InjectAt(sim.Fault{At: sim.Time(1500 * time.Microsecond), Kind: FaultStall, Target: 1, Arg1: DirUp, Arg2: int64(time.Second)})
	_ = pr.s.RunFor(500 * time.Millisecond)
	if pr.cli.sent > 100+len(pr.srv.got) {
		t.Fatalf("window exceeded during stall: sent %d read %d", pr.cli.sent, len(pr.srv.got))
	}
	if pr.n.Snapshot().Up.InFlight != 100 {
		t.Fatalf("in flight %d", pr.n.Snapshot().Up.InFlight)
	}
	_ = pr.s.RunFor(2 * time.Second)
	if !bytes.Equal(pr.srv.got, pr.cli.data) {
		t.Fatal("bytes lost across the stall")
	}
}

func TestChunkFaultOverridesAndClears(t *testing.T) {
	w := newWorld(1)
	var e *End
	cli := &recorder{}
	e = w.n.Dial("srv", fixed(time.Millisecond), cli)
	_ = w.s.RunFor(3 * time.Millisecond)
	w.s.ApplyFault(sim.Fault{Kind: FaultChunk, Target: 1, Arg1: DirUp, Arg2: 2})
	_, _ = e.Write([]byte("abcdef"))
	if c := w.n.Snapshot().Up.Chunks; c != 3 {
		t.Fatalf("override gave %d chunks", c)
	}
	w.s.ApplyFault(sim.Fault{Kind: FaultClear, Target: 1, Arg1: int64(FaultChunk)})
	_, _ = e.Write([]byte("abcdef"))
	if c := w.n.Snapshot().Up.Chunks; c != 4 {
		t.Fatalf("after clear %d chunks", c)
	}
	_, _ = w.srv[0].Write([]byte("xyz"))
	if c := w.n.Snapshot().Down.Chunks; c != 1 {
		t.Fatalf("down affected: %d", c)
	}
	_ = w.s.RunFor(10 * time.Millisecond)
	if string(w.rec[0].got) != "abcdefabcdef" {
		t.Fatalf("got %q", w.rec[0].got)
	}
}

func TestHalfOpenOfTheClientEnd(t *testing.T) {
	w := newWorld(1)
	cli := &recorder{}
	e := w.n.Dial("srv", fixed(time.Millisecond), cli)
	_ = w.s.RunFor(3 * time.Millisecond)
	w.s.ApplyFault(sim.Fault{Kind: FaultHalfOpen, Target: 1, Arg1: client})
	srv := w.srv[0]
	total := 0
	for i := 0; i < 100; i++ {
		n, err := srv.Write(make([]byte, 4096))
		if err != nil {
			t.Fatal(err)
		}
		total += n
	}
	_ = w.s.RunFor(time.Hour)
	if total != DefaultWindow || len(cli.events) != 1 || len(w.rec[0].events) != 0 {
		t.Fatalf("accepted %d, client %v, server %v", total, cli.events, w.rec[0].events)
	}
	if _, err := e.Read(make([]byte, 1)); !errors.Is(err, ErrGone) {
		t.Fatal(err)
	}
	if err := e.Close(); !errors.Is(err, ErrGone) {
		t.Fatal(err)
	}
	_ = srv.Reset()
	_ = w.s.RunUntilQuiet()
	if snap := w.n.Snapshot(); snap.Closed.Reset != 1 || snap.Down.Discarded != DefaultWindow || w.s.CheckQuiesce() != nil {
		t.Fatalf("%+v", snap)
	}
}

func TestBytesAlreadyInFlightFromAGoneEndStillArrive(t *testing.T) {
	w := newWorld(1)
	cli := &recorder{}
	w.n.Dial("srv", fixed(time.Millisecond), cli)
	_ = w.s.RunFor(3 * time.Millisecond)
	_, _ = w.srv[0].Write([]byte("last words"))
	w.s.ApplyFault(sim.Fault{Kind: FaultHalfOpen, Target: 1, Arg1: server})
	_ = w.s.RunFor(time.Second)
	if string(cli.got) != "last words" || cli.eof {
		t.Fatalf("got %q eof %v", cli.got, cli.eof)
	}
}

func TestFaultOnOneConnectionLeavesAnotherByteIdentical(t *testing.T) {
	kinds := []sim.Fault{
		{Kind: FaultReset},
		{Kind: FaultStall, Arg1: DirBoth, Arg2: int64(50 * time.Millisecond)},
		{Kind: FaultHalfOpen, Arg1: server},
		{Kind: FaultHalfClose, Arg1: server},
		{Kind: FaultChunk, Arg1: DirBoth, Arg2: 1},
	}
	run := func(seed int64, f *sim.Fault) ([]sim.Record, []byte, []byte) {
		s := sim.New(sim.Config{Seed: seed})
		n := New(s)
		var recs []sim.Record
		s.SetTraceTap(func(r sim.Record) {
			if r.Kind != sim.KindFault && r.A == 2 {
				recs = append(recs, r)
			}
		})
		var srvB *pump
		count := 0
		_, _ = n.Listen("srv", func(e *End) {
			count++
			p := newPump(s.Stream(fmt.Sprint("srv", e.ID())), 3000, 200, 150)
			if e.ID() == 2 {
				srvB = p
			}
			e.SetHandler(p)
			p.push(e)
		})
		o := DefaultOptions()
		o.Window = 700
		o.Up = Direction{Chunking: ChunkRandom, Latency: Latency{Base: time.Millisecond, Jitter: time.Millisecond}}
		o.Down = o.Up
		a := newPump(s.Stream("a"), 3000, 200, 150)
		b := newPump(s.Stream("b"), 3000, 200, 150)
		n.Dial("srv", o, a)
		n.Dial("srv", o, b)
		if f != nil {
			ff := *f
			ff.At, ff.Target = sim.Time(3*time.Millisecond), 1
			s.InjectAt(ff)
		}
		_ = s.RunFor(10 * time.Second)
		return recs, b.got, srvB.got
	}
	for seed := int64(1); seed <= int64(seedCount()); seed++ {
		r0, b0, s0 := run(seed, nil)
		for _, f := range kinds {
			r1, b1, s1 := run(seed, &f)
			if !slices.Equal(r0, r1) || !bytes.Equal(b0, b1) || !bytes.Equal(s0, s1) {
				t.Fatalf("seed %d: fault %s on connection 1 changed connection 2", seed, FaultName(f.Kind))
			}
		}
	}
}

func TestStateDigestHasNoResidueAfterClear(t *testing.T) {
	digest := func(faults ...sim.Fault) sim.Digest {
		w := newWorld(2)
		cli := &recorder{}
		e := w.n.Dial("srv", fixed(time.Millisecond), cli)
		_ = w.s.RunFor(1500 * time.Microsecond)
		for _, f := range faults {
			w.s.ApplyFault(f)
		}
		_ = w.s.RunFor(2 * time.Millisecond)
		_, _ = e.Write([]byte("payload"))
		_ = w.s.RunFor(10 * time.Millisecond)
		return w.s.StateDigest()
	}
	plain := digest()
	cleared := digest(
		sim.Fault{Kind: FaultStall, Target: 1, Arg1: DirBoth},
		sim.Fault{Kind: FaultChunk, Target: 1, Arg1: DirBoth, Arg2: 1},
		sim.Fault{Kind: FaultRefuse, Target: 0},
		sim.Fault{Kind: FaultClear, Target: 1, Arg1: int64(FaultStall)},
		sim.Fault{Kind: FaultClear, Target: 1, Arg1: int64(FaultChunk)},
		sim.Fault{Kind: FaultClear, Target: 0, Arg1: int64(FaultRefuse)},
	)
	if plain != cleared {
		t.Fatal("cleared faults changed the state digest")
	}
	if digest(sim.Fault{Kind: FaultStall, Target: 1, Arg1: DirBoth}) == plain {
		t.Fatal("an uncleared stall left no trace in the state digest")
	}
}

func TestSnapshotIsDeterministicAndConserves(t *testing.T) {
	snap := func(seed int64) string {
		pr := newPair(seed, Options{Window: 333, Up: Direction{Chunking: ChunkRandom, Latency: Latency{Base: time.Millisecond, Jitter: time.Millisecond}}, Down: Direction{Chunking: ChunkFixed, ChunkSize: 2}}, 5000, 4000)
		pr.s.InjectAt(sim.Fault{At: sim.Time(4 * time.Millisecond), Kind: FaultReset, Target: 1})
		var out []string
		_ = pr.s.RegisterInvariant("snap", func() error {
			sn := pr.n.Snapshot()
			for _, d := range []DirSnapshot{sn.Up, sn.Down} {
				if d.Written != d.Delivered+d.Discarded+d.InFlight {
					return fmt.Errorf("conservation %+v", d)
				}
			}
			out = append(out, fmt.Sprintf("%+v", sn))
			return nil
		})
		if err := pr.s.RunUntilQuiet(); err != nil {
			t.Fatal(err)
		}
		return fmt.Sprint(out)
	}
	if snap(8) != snap(8) {
		t.Fatal("snapshots differ for one seed")
	}
}

func TestEventNames(t *testing.T) {
	for ev := EvConnected; ev <= EvReset; ev++ {
		if ev.String() == "?" {
			t.Fatalf("event %d has no name", ev)
		}
	}
	if Event(0).String() != "?" || FaultName(0) != "" || FaultName(99) != "" {
		t.Fatal("unknown names")
	}
	for _, k := range FaultKinds() {
		if FaultName(k) == "" {
			t.Fatalf("kind %d has no name", k)
		}
	}
}

func TestOptionsNormalize(t *testing.T) {
	o := Options{Window: MaxWindow * 2, Up: Direction{ChunkSize: -1, Latency: Latency{Base: -5, Jitter: -1}}}.normalized()
	if o.Window != MaxWindow || o.Up.ChunkSize != 1 || o.Up.Latency.Base != 0 || o.Up.Latency.Jitter != 0 {
		t.Fatalf("%+v", o)
	}
	if (Options{}).normalized().Window != DefaultWindow {
		t.Fatal("default window")
	}
}
