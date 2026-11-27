package shell

import (
	"testing"
	"testing/synctest"

	"github.com/nickstrad/mini-gateway/internal/proxy"
)

// TestReaderPostsNoSecondReadBeforeTheEngineTakesTheFirst proves the per-connection inflight credit keeps ActDBPause bounded to one read chunk.
func TestReaderPostsNoSecondReadBeforeTheEngineTakesTheFirst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l, core := newPipeListener(), newCore()
		gate := make(chan struct{})
		data := 0
		core.onStep = func(_ proxy.Instant, ev proxy.Event) []proxy.Action {
			if ev.Kind != proxy.EvClientData {
				return nil
			}
			data++
			switch data {
			case 1:
				return []proxy.Action{act(proxy.ActClientResume, ev.Conn)}
			case 2:
				<-gate
			}
			return nil
		}
		s := startShell(t, core, testConfig(l))
		c, err := l.Dial()
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if _, err := c.Write(sslRequest()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		go func() { _, _ = c.Write([]byte("first")) }()
		synctest.Wait()
		go func() { _, _ = c.Write([]byte("second")) }()
		synctest.Wait()
		if n := len(core.of(proxy.EvClientData)); n != 2 {
			t.Fatalf("%d data events while the engine holds the first read", n)
		}
		if d := s.Snapshot().MailboxDepth.Value; d != 0 {
			t.Fatalf("a second read was posted while the engine held the first: mailbox depth %d", d)
		}
		close(gate)
		synctest.Wait()
		got := core.of(proxy.EvClientData)
		if len(got) != 3 || string(got[2].ev.Data) != "second" {
			t.Fatalf("after the engine took the first read: %v", kinds(core.events()))
		}
		closeShell(t, s)
	})
}
