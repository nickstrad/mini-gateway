package simnet

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
)

type endLog struct {
	sent   []byte
	got    []byte
	eof    bool
	reset  bool
	events int
}

func TestRandomOperationsKeepEveryLaw(t *testing.T) {
	steps := 400
	if testing.Short() {
		steps = 150
	}
	for seed := int64(1); seed <= int64(seedCount()); seed++ {
		s := sim.New(sim.Config{Seed: seed})
		n := New(s)
		r := s.Stream("ops")
		logs := map[*End]*endLog{}
		var ends []*End
		handler := HandlerFunc(func(e *End, ev Event) {
			l := logs[e]
			l.events++
			switch ev {
			case EvReset:
				l.reset = true
			case EvReadable:
				buf := make([]byte, 1+r.IntN(64))
				for {
					k, err := e.Read(buf)
					l.got = append(l.got, buf[:k]...)
					if err != nil || k == 0 {
						l.eof = l.eof || err != nil && err.Error() == "EOF"
						return
					}
				}
			}
		})
		track := func(e *End) {
			logs[e] = &endLog{}
			ends = append(ends, e)
			e.SetHandler(handler)
		}
		_, _ = n.Listen("srv", track)
		_ = s.RegisterInvariant("window", func() error {
			for id := int64(1); n.Conn(id) != nil; id++ {
				c := n.Conn(id)
				for d := range c.pipes {
					p := &c.pipes[d]
					if len(p.buf) > p.window || p.ready+p.held > len(p.buf) || p.ready < 0 || p.held < 0 {
						return fmt.Errorf("conn %d dir %d: buf %d ready %d held %d window %d", id, d, len(p.buf), p.ready, p.held, p.window)
					}
				}
			}
			return nil
		})
		for i := 0; i < steps; i++ {
			switch op := r.IntN(12); {
			case op == 0 || len(ends) == 0:
				o := DefaultOptions()
				o.Window = 1 + r.IntN(300)
				o.Up = Direction{Chunking: Chunking(r.IntN(3)), ChunkSize: 1 + r.IntN(5), Latency: Latency{Base: time.Duration(r.IntN(1000)), Jitter: time.Duration(r.IntN(1000))}}
				o.Down = Direction{Chunking: Chunking(r.IntN(3)), ChunkSize: 1 + r.IntN(5), Latency: Latency{Base: time.Duration(r.IntN(1000))}}
				e := n.Dial("srv", o, handler)
				logs[e] = &endLog{}
				ends = append(ends, e)
			case op < 6:
				e := ends[r.IntN(len(ends))]
				b := make([]byte, 1+r.IntN(100))
				for j := range b {
					b[j] = byte(r.Uint32())
				}
				k, err := e.Write(b)
				if err == nil {
					logs[e].sent = append(logs[e].sent, b[:k]...)
				}
			case op == 6:
				_ = ends[r.IntN(len(ends))].CloseWrite()
			case op == 7:
				_ = ends[r.IntN(len(ends))].Close()
			case op == 8 && r.IntN(4) == 0:
				_ = ends[r.IntN(len(ends))].Reset()
			case op == 9:
				f := sim.Fault{Kind: FaultKinds()[r.IntN(len(FaultKinds()))], Target: int64(r.IntN(len(ends) + 1)), Arg1: int64(r.IntN(4)), Arg2: int64(r.IntN(3000))}
				if f.Kind == FaultClear {
					f.Arg1 = int64(FaultKinds()[r.IntN(len(FaultKinds()))])
				}
				s.ApplyFault(f)
			default:
				if err := s.RunFor(time.Duration(r.IntN(3000))); err != nil {
					t.Fatalf("seed %d: %v", seed, err)
				}
			}
			if s.Err() != nil {
				t.Fatalf("seed %d: %v", seed, s.Err())
			}
		}
		for _, l := range n.listeners {
			l.refuse, l.dialStall, l.silence = false, false, false
		}
		for id := int64(1); n.Conn(id) != nil; id++ {
			for d := range n.Conn(id).pipes {
				n.Conn(id).pipes[d].unstall()
			}
		}
		if err := s.RunFor(time.Second); err != nil {
			t.Fatal(err)
		}
		for _, e := range ends {
			peer := e.peer()
			got, sent := logs[e].got, []byte(nil)
			if pl, ok := logs[peer]; ok {
				sent = pl.sent
			}
			if !bytes.HasPrefix(sent, got) {
				t.Fatalf("seed %d conn %d: received bytes are not a prefix of the sent bytes", seed, e.ID())
			}
			c := e.Conn()
			if !c.done && peer.state == stOpen && e.state == stOpen && !c.pipes[1-e.side].blackhole && len(got) != len(sent) && !e.readClosed {
				if e.in().ready == 0 && len(c.pipes[1-e.side].buf) == 0 {
					t.Fatalf("seed %d conn %d: %d of %d bytes arrived with nothing in flight", seed, e.ID(), len(got), len(sent))
				}
			}
		}
		for _, e := range ends {
			_ = e.Reset()
			_ = e.Close()
		}
		_ = s.RunUntilQuiet()
		if err := s.CheckQuiesce(); err != nil {
			for id := int64(1); n.Conn(id) != nil; id++ {
				c := n.Conn(id)
				if !c.done {
					t.Logf("conn %d states %d %d silent %v", id, c.ends[0].state, c.ends[1].state, c.silent)
				}
			}
			t.Fatalf("seed %d: %v", seed, err)
		}
		snap := n.Snapshot()
		for _, d := range []DirSnapshot{snap.Up, snap.Down} {
			if d.Written != d.Delivered+d.Discarded || d.InFlight != 0 {
				t.Fatalf("seed %d: %+v", seed, d)
			}
		}
		total := snap.Closed.Graceful + snap.Closed.Reset + snap.Closed.HalfOpen + snap.Closed.Silent + snap.Closed.Aborted
		if total+snap.DialsRefused != snap.DialsAttempted {
			t.Fatalf("seed %d: dials %d != closed %d + refused %d", seed, snap.DialsAttempted, total, snap.DialsRefused)
		}
	}
}
