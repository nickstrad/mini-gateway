package simnet

import (
	"testing"
	"time"
)

func blockedWriter(t *testing.T, w *world, o Options, wakes *int) *End {
	t.Helper()
	cli := &recorder{onEv: func(e *End, ev Event) {
		switch ev {
		case EvConnected:
			if n, _ := e.Write(make([]byte, 2*o.Window)); n != o.Window {
				t.Fatalf("first write accepted %d", n)
			}
		case EvWritable:
			*wakes++
		}
	}}
	return w.n.Dial("lazy", o, cli)
}

func TestReceiverCloseWakesABlockedWriterWhenItDiscardsBufferedBytes(t *testing.T) {
	w := newWorld(1)
	w.n.Listen("lazy", func(e *End) { w.srv = append(w.srv, e) })
	o := fixed(time.Millisecond)
	o.Window = 10
	wakes := 0
	e := blockedWriter(t, w, o, &wakes)
	_ = w.s.RunFor(10 * time.Millisecond)
	if w.srv[0].Buffered() != 10 || e.Space() != 0 || wakes != 0 {
		t.Fatalf("buffered %d space %d wakes %d", w.srv[0].Buffered(), e.Space(), wakes)
	}
	_ = w.srv[0].Close()
	_ = w.s.RunFor(10 * time.Millisecond)
	if e.Space() != 10 || wakes != 1 {
		t.Fatalf("after close: space %d wakes %d", e.Space(), wakes)
	}
	if snap := w.n.Snapshot(); snap.Up.Discarded != 10 {
		t.Fatalf("%+v", snap.Up)
	}
	_ = e.Close()
	_ = w.s.RunUntilQuiet()
	if err := w.s.CheckQuiesce(); err != nil {
		t.Fatal(err)
	}
}

func TestReceiverCloseWakesABlockedWriterWhenLaterArrivalsAreDiscarded(t *testing.T) {
	w := newWorld(1)
	w.n.Listen("lazy", func(e *End) {
		w.srv = append(w.srv, e)
		_ = e.Close()
	})
	o := fixed(time.Millisecond)
	o.Window = 10
	wakes := 0
	e := blockedWriter(t, w, o, &wakes)
	_ = w.s.RunFor(2500 * time.Microsecond)
	if e.Space() != 0 || wakes != 0 {
		t.Fatalf("before arrival: space %d wakes %d", e.Space(), wakes)
	}
	_ = w.s.RunFor(10 * time.Millisecond)
	if e.Space() != 10 || wakes != 1 {
		t.Fatalf("after arrival: space %d wakes %d", e.Space(), wakes)
	}
	_ = e.Close()
	_ = w.s.RunUntilQuiet()
	if err := w.s.CheckQuiesce(); err != nil {
		t.Fatal(err)
	}
}
