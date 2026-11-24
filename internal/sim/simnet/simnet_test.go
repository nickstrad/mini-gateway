package simnet

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
)

func seedCount() int {
	if testing.Short() {
		return 10
	}
	return 60
}

type pump struct {
	rng      *rand.Rand
	data     []byte
	sent     int
	maxWrite int
	readMax  int
	got      []byte
	eof      bool
	stopRead bool
	noFin    bool
	closed   bool
	events   []Event
	errs     []error
}

func newPump(rng *rand.Rand, size, maxWrite, readMax int) *pump {
	p := &pump{rng: rng, maxWrite: maxWrite, readMax: readMax}
	p.data = make([]byte, size)
	for i := range p.data {
		p.data[i] = byte(rng.Uint32())
	}
	return p
}

func (p *pump) OnEvent(e *End, ev Event) {
	p.events = append(p.events, ev)
	switch ev {
	case EvConnected, EvWritable:
		p.push(e)
	case EvReadable:
		p.pull(e)
	}
}

func (p *pump) push(e *End) {
	for p.sent < len(p.data) {
		k := min(1+p.rng.IntN(p.maxWrite), len(p.data)-p.sent)
		n, err := e.Write(p.data[p.sent : p.sent+k])
		if err != nil {
			p.errs = append(p.errs, err)
			return
		}
		p.sent += n
		if n < k {
			return
		}
	}
	if !e.out().finSent && !p.noFin {
		_ = e.CloseWrite()
	}
	p.maybeClose(e)
}

func (p *pump) pull(e *End) {
	if p.stopRead {
		return
	}
	buf := make([]byte, p.readMax)
	for {
		n, err := e.Read(buf[:1+p.rng.IntN(p.readMax)])
		p.got = append(p.got, buf[:n]...)
		if err == io.EOF {
			p.eof = true
			p.maybeClose(e)
			return
		}
		if err != nil {
			p.errs = append(p.errs, err)
			return
		}
		if n == 0 {
			return
		}
	}
}

func (p *pump) maybeClose(e *End) {
	if p.eof && p.sent == len(p.data) && !p.closed {
		p.closed = true
		_ = e.Close()
	}
}

type pair struct {
	s      *sim.Sim
	n      *Net
	cli    *pump
	srv    *pump
	end    *End
	srvEnd *End
}

func newPair(seed int64, opts Options, upBytes, downBytes int) *pair {
	s := sim.New(sim.Config{Seed: seed})
	n := New(s)
	pr := &pair{s: s, n: n}
	rng := s.Stream("test")
	pr.cli = newPump(rng, upBytes, 300, 200)
	pr.srv = newPump(s.Stream("test/srv"), downBytes, 300, 200)
	_, _ = n.Listen("srv", func(e *End) {
		pr.srvEnd = e
		e.SetHandler(pr.srv)
		pr.srv.push(e)
	})
	_ = s.RegisterInvariant("window", func() error {
		for id := int64(1); n.Conn(id) != nil; id++ {
			c := n.Conn(id)
			for d := range c.pipes {
				if len(c.pipes[d].buf) > c.opts.Window {
					return fmt.Errorf("conn %d dir %d: %d bytes in flight over window %d", id, d, len(c.pipes[d].buf), c.opts.Window)
				}
			}
		}
		return nil
	})
	pr.end = n.Dial("srv", opts, pr.cli)
	return pr
}

func chunkModes() []Direction {
	return []Direction{
		{Chunking: ChunkWhole},
		{Chunking: ChunkFixed, ChunkSize: 1},
		{Chunking: ChunkFixed, ChunkSize: 7},
		{Chunking: ChunkRandom},
	}
}

func TestBytesDeliveredInOrderForEveryChunking(t *testing.T) {
	for mi, mode := range chunkModes() {
		for _, lat := range []Latency{{Base: time.Millisecond}, {Base: 10 * time.Microsecond, Jitter: 3 * time.Millisecond}} {
			for seed := int64(1); seed <= int64(seedCount()); seed++ {
				d := mode
				d.Latency = lat
				opts := Options{Window: 1 + int(seed*97%5000), Up: d, Down: d}
				pr := newPair(seed, opts, 3000+int(seed*31%4000), 2000)
				if err := pr.s.RunUntilQuiet(); err != nil {
					t.Fatalf("mode %d seed %d: %v", mi, seed, err)
				}
				if !bytes.Equal(pr.srv.got, pr.cli.data) || !bytes.Equal(pr.cli.got, pr.srv.data) {
					t.Fatalf("mode %d seed %d: bytes differ (up %d/%d, down %d/%d)", mi, seed, len(pr.srv.got), len(pr.cli.data), len(pr.cli.got), len(pr.srv.data))
				}
				if !pr.srv.eof || !pr.cli.eof {
					t.Fatalf("mode %d seed %d: EOF not seen", mi, seed)
				}
				if err := pr.s.CheckQuiesce(); err != nil {
					t.Fatalf("mode %d seed %d: %v", mi, seed, err)
				}
				snap := pr.n.Snapshot()
				if snap.Closed.Graceful != 1 || snap.Up.Delivered != int64(len(pr.cli.data)) || snap.Down.Delivered != int64(len(pr.srv.data)) {
					t.Fatalf("mode %d seed %d: snapshot %+v", mi, seed, snap)
				}
				if mode.Chunking == ChunkFixed && mode.ChunkSize == 1 && snap.Up.Chunks != snap.Up.Written {
					t.Fatalf("one-byte chunking made %d chunks for %d bytes", snap.Up.Chunks, snap.Up.Written)
				}
			}
		}
	}
}

func TestStoppedReaderBlocksWriterAtWindow(t *testing.T) {
	for seed := int64(1); seed <= int64(seedCount()); seed++ {
		window := 1 + int(seed*131%3000)
		opts := DefaultOptions()
		opts.Window = window
		opts.Up.Chunking = ChunkRandom
		pr := newPair(seed, opts, 20000, 0)
		pr.srv.stopRead = true
		if err := pr.s.RunFor(time.Second); err != nil {
			t.Fatal(err)
		}
		if pr.cli.sent != window {
			t.Fatalf("seed %d: writer got %d bytes accepted with window %d", seed, pr.cli.sent, window)
		}
		snap := pr.n.Snapshot()
		if snap.Up.InFlight != int64(window) || snap.Up.WritesRefused == 0 {
			t.Fatalf("seed %d: snapshot %+v", seed, snap.Up)
		}
		pr.srv.stopRead = false
		pr.srv.pull(pr.srvEnd)
		if err := pr.s.RunUntilQuiet(); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(pr.srv.got, pr.cli.data) {
			t.Fatalf("seed %d: back pressure lost bytes", seed)
		}
		if err := pr.s.CheckQuiesce(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestResetDeliversNothingAfterAndNotifiesBothEnds(t *testing.T) {
	for seed := int64(1); seed <= int64(seedCount()); seed++ {
		opts := DefaultOptions()
		opts.Up = Direction{Chunking: ChunkFixed, ChunkSize: 3, Latency: Latency{Base: 5 * time.Millisecond, Jitter: 5 * time.Millisecond}}
		opts.Window = 3000
		pr := newPair(seed, opts, 50000, 50000)
		pr.srv.noFin = true
		at := sim.Time(12+seed%7) * sim.Time(time.Millisecond)
		pr.s.InjectAt(sim.Fault{At: at, Kind: FaultReset, Target: 1})
		var order []string
		after := false
		pr.s.SetTraceTap(func(r sim.Record) {
			if r.Kind == kindReset {
				after = true
			}
			if after && (r.Kind == kindDeliver || r.Kind == kindRead) {
				t.Fatalf("seed %d: byte moved after the reset: %v", seed, r)
			}
		})
		wrap := func(name string, h Handler) Handler {
			return HandlerFunc(func(e *End, ev Event) {
				if ev == EvReset {
					order = append(order, name)
					if _, err := e.Read(make([]byte, 4)); !errors.Is(err, ErrReset) {
						t.Fatalf("read after reset: %v", err)
					}
					if _, err := e.Write([]byte{1}); !errors.Is(err, ErrReset) {
						t.Fatalf("write after reset: %v", err)
					}
				}
				h.OnEvent(e, ev)
			})
		}
		pr.end.SetHandler(wrap("client", pr.cli))
		pr.s.At(0, func() {})
		if err := pr.s.RunUntil(at - 1); err != nil {
			t.Fatal(err)
		}
		pr.srvEnd.SetHandler(wrap("server", pr.srv))
		if err := pr.s.RunUntilQuiet(); err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(order) != "[client server]" {
			t.Fatalf("seed %d: reset order %v", seed, order)
		}
		snap := pr.n.Snapshot()
		if snap.Closed.Reset != 1 || snap.Faults.Reset != 1 || snap.Up.Discarded+snap.Down.Discarded == 0 {
			t.Fatalf("seed %d: snapshot %+v", seed, snap)
		}
		if err := pr.s.CheckQuiesce(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLocalResetNotifiesOnlyThePeer(t *testing.T) {
	pr := newPair(3, DefaultOptions(), 10, 10)
	var cliEvents, srvEvents []Event
	pr.end.SetHandler(HandlerFunc(func(e *End, ev Event) {
		cliEvents = append(cliEvents, ev)
		if ev == EvConnected {
			_ = e.Reset()
		}
	}))
	_ = pr.s.RunUntil(sim.Time(80 * time.Microsecond))
	pr.srvEnd.SetHandler(HandlerFunc(func(e *End, ev Event) { srvEvents = append(srvEvents, ev) }))
	if err := pr.s.RunUntilQuiet(); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(cliEvents) != fmt.Sprint([]Event{EvConnected}) || fmt.Sprint(srvEvents) != fmt.Sprint([]Event{EvReset}) {
		t.Fatalf("client %v server %v", cliEvents, srvEvents)
	}
	if err := pr.s.CheckQuiesce(); err != nil {
		t.Fatal(err)
	}
}

func TestHalfOpenSurvivorNeverLearns(t *testing.T) {
	for seed := int64(1); seed <= int64(seedCount()); seed++ {
		opts := DefaultOptions()
		opts.Window = 500
		pr := newPair(seed, opts, 5000, 0)
		pr.srv.noFin = true
		pr.s.InjectAt(sim.Fault{At: sim.Time(120 * time.Microsecond), Kind: FaultHalfOpen, Target: 1, Arg1: server})
		if err := pr.s.RunFor(time.Hour); err != nil {
			t.Fatal(err)
		}
		for _, ev := range pr.cli.events {
			if ev == EvReset || ev == EvReadable {
				t.Fatalf("seed %d: survivor saw %v", seed, ev)
			}
		}
		if pr.cli.sent > 500+len(pr.srv.got) || pr.cli.sent == len(pr.cli.data) {
			t.Fatalf("seed %d: sent %d, server read %d", seed, pr.cli.sent, len(pr.srv.got))
		}
		if _, err := pr.srvEnd.Write([]byte{1}); !errors.Is(err, ErrGone) {
			t.Fatalf("gone end wrote: %v", err)
		}
		snap := pr.n.Snapshot()
		if snap.ConnsOpen != 1 || snap.Up.InFlight == 0 || pr.s.CheckQuiesce() == nil {
			t.Fatalf("seed %d: half-open connection not visible as open: %+v", seed, snap)
		}
		if err := pr.end.Close(); err != nil {
			t.Fatal(err)
		}
		if err := pr.s.RunUntilQuiet(); err != nil {
			t.Fatal(err)
		}
		if err := pr.s.CheckQuiesce(); err != nil {
			t.Fatal(err)
		}
		if snap := pr.n.Snapshot(); snap.Closed.HalfOpen != 1 || snap.Faults.HalfOpen != 1 {
			t.Fatalf("seed %d: %+v", seed, snap)
		}
	}
}

func TestStallHoldsBytesThenResumesInOrder(t *testing.T) {
	for seed := int64(1); seed <= int64(seedCount()); seed++ {
		opts := DefaultOptions()
		opts.Up.Chunking = ChunkRandom
		pr := newPair(seed, opts, 4000, 3000)
		stallAt := sim.Time(100 * time.Microsecond)
		stallFor := 40 * time.Millisecond
		pr.s.InjectAt(sim.Fault{At: stallAt, Kind: FaultStall, Target: 1, Arg1: DirBoth, Arg2: int64(stallFor)})
		pr.s.SetTraceTap(func(r sim.Record) {
			if (r.Kind == kindDeliver || r.Kind == kindFin) && r.At > stallAt && r.At < stallAt.Add(stallFor) {
				t.Fatalf("seed %d: delivery during the stall: %v", seed, r)
			}
		})
		if err := pr.s.RunUntilQuiet(); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(pr.srv.got, pr.cli.data) || !bytes.Equal(pr.cli.got, pr.srv.data) {
			t.Fatalf("seed %d: bytes differ after the stall", seed)
		}
		if pr.s.Now() < stallAt.Add(stallFor) {
			t.Fatalf("seed %d: finished before the stall ended", seed)
		}
		if err := pr.s.CheckQuiesce(); err != nil {
			t.Fatal(err)
		}
	}
}

func dialOnly(t *testing.T, kind sim.FaultKind) (*sim.Sim, *Net, *End, *[]Event, *int) {
	s := sim.New(sim.Config{Seed: 1})
	n := New(s)
	accepted := 0
	_, _ = n.Listen("srv", func(e *End) { accepted++ })
	if kind != 0 {
		s.ApplyFault(sim.Fault{Kind: kind, Target: 0})
	}
	var evs []Event
	e := n.Dial("srv", DefaultOptions(), HandlerFunc(func(e *End, ev Event) { evs = append(evs, ev) }))
	return s, n, e, &evs, &accepted
}

func TestDialRefusedStalledAndSilenced(t *testing.T) {
	s, n, e, evs, _ := dialOnly(t, FaultRefuse)
	_ = s.RunUntilQuiet()
	if fmt.Sprint(*evs) != fmt.Sprint([]Event{EvRefused}) || n.Snapshot().DialsRefused != 1 || s.CheckQuiesce() != nil {
		t.Fatalf("refused: %v %+v", *evs, n.Snapshot())
	}
	if _, err := e.Write([]byte{1}); !errors.Is(err, ErrRefused) {
		t.Fatal(err)
	}

	s2 := sim.New(sim.Config{Seed: 1})
	n2 := New(s2)
	var evs2 []Event
	n2.Dial("nobody", DefaultOptions(), HandlerFunc(func(e *End, ev Event) { evs2 = append(evs2, ev) }))
	_ = s2.RunUntilQuiet()
	if fmt.Sprint(evs2) != fmt.Sprint([]Event{EvRefused}) {
		t.Fatalf("no listener: %v", evs2)
	}

	s, n, e, evs, acc := dialOnly(t, FaultDialStall)
	_ = s.RunFor(time.Hour)
	if len(*evs) != 0 || *acc != 0 || s.CheckQuiesce() == nil {
		t.Fatalf("stalled: %v", *evs)
	}
	if _, err := e.Write([]byte{1}); !errors.Is(err, ErrNotConnected) {
		t.Fatal(err)
	}
	_ = e.Close()
	_ = s.RunUntilQuiet()
	if snap := n.Snapshot(); snap.Closed.Aborted != 1 || snap.DialsStalled != 1 || s.CheckQuiesce() != nil {
		t.Fatalf("stalled abort: %+v %v", snap, s.CheckQuiesce())
	}

	s, n, e, evs, acc = dialOnly(t, FaultSilence)
	_ = s.RunFor(time.Hour)
	if fmt.Sprint(*evs) != fmt.Sprint([]Event{EvConnected}) || *acc != 0 {
		t.Fatalf("silence: %v accepted %d", *evs, *acc)
	}
	if k, _ := e.Write(make([]byte, DefaultWindow+10)); k != DefaultWindow {
		t.Fatalf("silent end accepted %d", k)
	}
	_ = s.RunFor(time.Hour)
	if len(*evs) != 1 || n.Snapshot().Up.InFlight != DefaultWindow {
		t.Fatalf("silent end answered: %v", *evs)
	}
	_ = e.Close()
	_ = s.RunUntilQuiet()
	if snap := n.Snapshot(); snap.Closed.Silent != 1 || s.CheckQuiesce() != nil {
		t.Fatalf("silence close: %+v", snap)
	}
}

func TestClearedListenerFaultsLeaveNewDialsNormal(t *testing.T) {
	for _, k := range []sim.FaultKind{FaultRefuse, FaultDialStall, FaultSilence} {
		s, n, e, _, _ := dialOnly(t, k)
		_ = s.RunFor(time.Second)
		_ = e.Close()
		s.ApplyFault(sim.Fault{Kind: FaultClear, Target: 0, Arg1: int64(k)})
		pr := &pump{rng: s.Stream("x"), data: []byte("hello"), maxWrite: 10, readMax: 10}
		var srvGot []byte
		n.listeners[0].accept = func(e *End) {
			e.SetHandler(HandlerFunc(func(e *End, ev Event) {
				b := make([]byte, 10)
				for {
					m, err := e.Read(b)
					srvGot = append(srvGot, b[:m]...)
					if err == io.EOF {
						_ = e.Close()
					}
					if m == 0 {
						return
					}
				}
			}))
		}
		n.Dial("srv", DefaultOptions(), pr)
		_ = s.RunUntilQuiet()
		if string(srvGot) != "hello" || !pr.eof || s.CheckQuiesce() != nil {
			t.Fatalf("kind %d: after clear got %q eof %v %v", k, srvGot, pr.eof, s.CheckQuiesce())
		}
	}
}

func TestQuiesceCatchesLeakedConnectionAndByte(t *testing.T) {
	pr := newPair(1, DefaultOptions(), 100, 0)
	pr.srv.stopRead = true
	_ = pr.s.RunUntilQuiet()
	err := pr.s.CheckQuiesce()
	var qe *sim.QuiesceError
	if !errors.As(err, &qe) || qe.Name != "simnet" {
		t.Fatalf("leak not caught: %v", err)
	}
	_ = pr.end.Close()
	_ = pr.srvEnd.Close()
	_ = pr.s.RunUntilQuiet()
	if err := pr.s.CheckQuiesce(); err != nil {
		t.Fatal(err)
	}
	if snap := pr.n.Snapshot(); snap.Up.Discarded != 100 || snap.Up.InFlight != 0 {
		t.Fatalf("%+v", snap.Up)
	}
}

func TestHalfCloseFaultSendsFinAfterPendingBytes(t *testing.T) {
	pr := newPair(1, DefaultOptions(), 0, 0)
	pr.srv.noFin = true
	pr.cli.data = []byte("abc")
	pr.s.InjectAt(sim.Fault{At: sim.Time(60 * time.Microsecond), Kind: FaultHalfClose, Target: 1, Arg1: server})
	_ = pr.s.RunUntilQuiet()
	if !pr.cli.eof || string(pr.srv.got) != "abc" || pr.n.Snapshot().Faults.HalfClose != 1 {
		t.Fatalf("eof %v got %q", pr.cli.eof, pr.srv.got)
	}
}

func TestFaultsWithoutTargetAreCounted(t *testing.T) {
	s := sim.New(sim.Config{Seed: 1})
	n := New(s)
	for _, k := range FaultKinds() {
		s.ApplyFault(sim.Fault{Kind: k, Target: 42, Arg1: int64(FaultStall)})
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	if got := n.Snapshot().Faults.NoTarget; got != int64(len(FaultKinds())) {
		t.Fatalf("no-target %d", got)
	}
}
