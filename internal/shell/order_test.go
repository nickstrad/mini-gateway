package shell

import (
	"io"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/nickstrad/mini-gateway/internal/proxy"
)

func dbWriteThenGone(t *testing.T, accept int) []string {
	t.Helper()
	var got []string
	synctest.Test(t, func(t *testing.T) {
		l, dbl, core := newPipeListener(), newPipeListener(), newCore()
		core.onStart = func(proxy.Instant) []proxy.Action { return []proxy.Action{{Kind: proxy.ActDBOpen, Conn: 7}} }
		core.onStep = func(_ proxy.Instant, ev proxy.Event) []proxy.Action {
			if ev.Kind == proxy.EvDBOpened {
				return []proxy.Action{writeAct(proxy.ActDBWrite, ev.Conn, make([]byte, 64))}
			}
			return nil
		}
		cfg := testConfig(l)
		cfg.Dial = dbl.DialContext
		s := startShell(t, core, cfg)
		db, err := dbl.Accept()
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if accept > 0 {
			if _, err := io.ReadFull(db, make([]byte, accept)); err != nil {
				t.Fatal(err)
			}
		}
		_ = db.Close()
		synctest.Wait()
		got = kinds(core.events())
		closeShell(t, s)
	})
	return got
}

func TestDatabaseCloseIsPostedAfterTheWriterReports(t *testing.T) {
	got := dbWriteThenGone(t, 10)
	want := []string{"db-opened", "db-wrote", "db-write-refused", "db-closed"}
	if !slices.Equal(got, want) {
		t.Fatalf("events %v, want %v", got, want)
	}
}

func TestDatabaseWriteRefusedWithZeroBytesIsReportedBeforeTheClose(t *testing.T) {
	got := dbWriteThenGone(t, 0)
	want := []string{"db-opened", "db-write-refused", "db-closed"}
	if !slices.Equal(got, want) {
		t.Fatalf("events %v, want %v", got, want)
	}
}
