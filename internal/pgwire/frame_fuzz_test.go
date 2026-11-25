package pgwire

import (
	"bytes"
	"strings"
	"testing"
)

func fuzzSeeds(f *testing.F) {
	f.Add(AppendSSLRequest(nil), 3, uint8(0))
	f.Add(append(AppendSSLRequest(nil), startupBytes("alice", testDatabase, nil)...), 8, uint8(1))
	f.Add(append(AppendGSSRequest(nil), AppendSSLRequest(nil)...), 9, uint8(2))
	f.Add(startupBytes("alice", testDatabase, []Param{{"application_name", "x"}}), 5, uint8(0))
	f.Add(AppendCancelRequest(nil, BackendKey{ProcessID: 1}), 2, uint8(1))
	f.Add([]byte{0, 0, 0, 8, 4, 210, 22, 47, 'p', 0, 0, 0, 9}, 1, uint8(0))
	f.Add(cat(AppendAuthenticationSASL(nil, ScramPlusMechanism, ScramMechanism), AppendAuthentication(nil, AuthSASLContinue, []byte("r=abc,s=c2FsdA==,i=1"))), 7, uint8(0))
	f.Add(cat(rowDesc(), dataRow("1"), complete("SELECT 1"), AppendReadyForQuery(nil, 'I')), 11, uint8(0))
	f.Add(cat(AppendFatal(nil, "53300", "too many"), []byte("junk")), 4, uint8(2))
	f.Add([]byte("S"), 0, uint8(1))
}

func splitAt(b []byte, k int) [][]byte {
	if k <= 0 || k >= len(b) {
		return [][]byte{b}
	}
	return [][]byte{b[:k], b[k:]}
}

func FuzzFramer(f *testing.F) {
	fuzzSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte, k int, mode uint8) {
		run := func(pieces [][]byte) string {
			fr := Framer{Untyped: mode%2 == 1, Max: 4096}
			fr.SkipBodies('D')
			var sb strings.Builder
			for _, p := range pieces {
				for len(p) > 0 {
					n, done, err := fr.Take(p)
					if fr.BufferCap() > 2*max(4096, MaxStartupPacket) || fr.Pending() > MaxStartupPacket+5 {
						t.Fatalf("framer holds %d bytes", fr.BufferCap())
					}
					if n == 0 && err == nil && !done {
						t.Fatal("no progress")
					}
					p = p[n:]
					if err != nil {
						sb.WriteString("err " + err.Error() + "\n")
						return sb.String()
					}
					if done {
						fm := fr.Frame()
						sb.WriteString(string(fm.Type) + " " + itoa(fm.Len) + " " + string(fm.Body) + "\n")
					}
				}
			}
			return sb.String()
		}
		a := run([][]byte{data})
		if b := run(splitAt(data, k)); a != b {
			t.Fatalf("split at %d differs", k)
		}
		if c := run(oneByte(0, data)); a != c {
			t.Fatal("one-byte delivery differs")
		}
	})
}

func fuzzClientCfg(t *testing.T, mode uint8) ClientConfig {
	c := testClientConfig(t, ClientTLSMode(mode%3), 1)
	if mode&4 != 0 {
		c.Auth = AuthTrustForTestsOnly
	}
	return c
}

func fuzzClientRun(t *testing.T, mode uint8, data []byte, pieces [][]byte) string {
	cfg := fuzzClientCfg(t, mode)
	s := NewClientSession(cfg)
	var acts []Action
	for i, p := range pieces {
		var n int
		acts, n = s.Receive(acts, p)
		if n < len(p) && !s.Closed() && !s.Ready() {
			t.Fatalf("piece %d: consumed %d of %d in startup", i, n, len(p))
		}
		if s.AwaitingTLS() && mode&8 != 0 {
			acts = s.HandshakeDone(acts, HandshakeResult{Outcome: HandshakeOutcome(mode>>4) % NumHandshakeOutcomes})
		}
		if s.fr.BufferCap() > 2*maxAuthMessage && s.fr.BufferCap() > 2*MaxStartupPacket {
			t.Fatalf("buffer %d", s.fr.BufferCap())
		}
	}
	acts = s.Deadline(acts)
	if err := cfg.Stats.Snapshot().Check(); err != nil {
		t.Fatal(err)
	}
	if !s.Closed() && !s.Ready() {
		t.Fatal("a deadline left the session in startup")
	}
	return strings.Join(traceOf(acts), "\n")
}

func FuzzClientSession(f *testing.F) {
	fuzzSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte, k int, mode uint8) {
		a := fuzzClientRun(t, mode, data, [][]byte{data})
		if b := fuzzClientRun(t, mode, data, splitAt(data, k)); a != b && mode&8 == 0 {
			t.Fatalf("split at %d differs:\n%s\n---\n%s", k, a, b)
		}
		if c := fuzzClientRun(t, mode, data, oneByte(0, data)); a != c && mode&8 == 0 {
			t.Fatalf("one-byte differs:\n%s\n---\n%s", a, c)
		}
	})
}

func fuzzBackendRun(t *testing.T, mode uint8, pieces [][]byte) string {
	cfg := testBackendConfig(BackendTLSMode(mode%3), 1)
	s := NewBackendSession(cfg)
	acts := s.Start(nil)
	for i, p := range pieces {
		var n int
		acts, n = s.Receive(acts, p)
		if n < len(p) && !s.Closed() && !s.Ready() {
			t.Fatalf("piece %d: consumed %d of %d", i, n, len(p))
		}
		if s.AwaitingTLS() && mode&8 != 0 {
			acts = s.HandshakeDone(acts, HandshakeResult{Outcome: HandshakeOutcome(mode>>4) % NumHandshakeOutcomes})
		}
		if len(s.params) > MaxParams || s.fr.BufferCap() > 2*DefaultMaxMessage {
			t.Fatal("unbounded state")
		}
	}
	acts = s.Deadline(acts)
	if err := cfg.Stats.Snapshot().Check(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(traceOf(acts), "\n")
}

func FuzzBackendSession(f *testing.F) {
	fuzzSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte, k int, mode uint8) {
		a := fuzzBackendRun(t, mode, [][]byte{data})
		if b := fuzzBackendRun(t, mode, splitAt(data, k)); a != b && mode&8 == 0 {
			t.Fatalf("split at %d differs:\n%s\n---\n%s", k, a, b)
		}
		if c := fuzzBackendRun(t, mode, oneByte(0, data)); a != c && mode&8 == 0 {
			t.Fatalf("one-byte differs:\n%s\n---\n%s", a, c)
		}
	})
}

func FuzzCycle(f *testing.F) {
	fuzzSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte, k int, mode uint8) {
		run := func(pieces [][]byte) string {
			st := &WireStats{}
			tr := NewCycleTracker(4096, st)
			for i := 0; i < int(mode%3); i++ {
				tr.QuerySent()
			}
			var evs []CycleEvent
			total := 0
			for _, p := range pieces {
				var n int
				evs, n = tr.Receive(evs, p)
				total += n
				if tr.BufferCap() > 2*4096 {
					t.Fatalf("tracker buffers %d", tr.BufferCap())
				}
				if n < len(p) && !tr.Closed() {
					t.Fatal("stopped early without closing")
				}
			}
			if !tr.Closed() && int64(total) != tr.Offset() {
				t.Fatal("offset mismatch")
			}
			evs = tr.PeerClosed(evs)
			ft := NewFrontendTracker(4096, st)
			var fevs []FrontEvent
			for _, p := range pieces {
				fevs, _ = ft.Receive(fevs, p)
			}
			return describeCycle(evs) + describeFront(fevs)
		}
		a := run([][]byte{data})
		if b := run(splitAt(data, k)); a != b {
			t.Fatalf("split at %d differs", k)
		}
		if c := run(oneByte(0, data)); a != c {
			t.Fatal("one-byte differs")
		}
		if bytes.Count([]byte(a), []byte("\n")) > len(data)+2 {
			t.Fatal("more events than bytes")
		}
	})
}
