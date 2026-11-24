package simnet

import (
	"math"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/sim"
)

func TestMaximumJitterDrawsWithoutPanicking(t *testing.T) {
	w := newWorld(1)
	o := DefaultOptions()
	o.Up.Latency = Latency{Jitter: math.MaxInt64}
	o.Down.Latency = Latency{}
	cli := &recorder{}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("dial panicked: %v", r)
		}
	}()
	w.n.Dial("srv", o, cli)
	_ = w.s.RunUntilQuiet()
	if err := w.s.Err(); err != nil {
		t.Fatal(err)
	}
	if len(cli.events) != 1 || len(w.srv) != 1 {
		t.Fatalf("client %v, %d accepted", cli.events, len(w.srv))
	}
}

func TestBasePlusJitterSaturates(t *testing.T) {
	w := newWorld(1)
	o := DefaultOptions()
	o.Up.Latency = Latency{Base: math.MaxInt64 - 100, Jitter: 1000}
	o.Down.Latency = Latency{}
	w.n.Dial("srv", o, &recorder{})
	_ = w.s.RunUntil(sim.MaxTime - 101)
	if len(w.srv) != 0 {
		t.Fatalf("accepted at %d, before the base latency", w.s.Now())
	}
	_ = w.s.RunUntilQuiet()
	if len(w.srv) != 1 || w.s.Now() < sim.MaxTime-100 {
		t.Fatalf("accepted %d at %d", len(w.srv), w.s.Now())
	}
}

func TestRefusalRoundTripSaturates(t *testing.T) {
	w := newWorld(1)
	o := DefaultOptions()
	o.Up.Latency = Latency{Base: math.MaxInt64}
	o.Down.Latency = Latency{Base: 1}
	cli := &recorder{}
	w.n.Dial("nobody", o, cli)
	_ = w.s.RunUntilQuiet()
	if len(cli.events) != 1 || cli.events[0] != "9223372036854775807:refused" {
		t.Fatalf("client %v", cli.events)
	}
}
