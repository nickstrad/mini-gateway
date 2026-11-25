package fakepg_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/testclient"
)

type frame struct {
	typ  byte
	body string
}

func frames(data []byte, startup bool, split func(int) int) ([]frame, error) {
	f := fakepg.Framer{Startup: startup, Max: 1 << 16}
	var out []frame
	for i := 0; i < len(data); {
		n := split(len(data) - i)
		f.Feed(data[i : i+n])
		i += n
		for {
			typ, body, ok, err := f.Next()
			if err != nil {
				return out, err
			}
			if !ok {
				break
			}
			out = append(out, frame{typ, string(body)})
		}
	}
	return out, nil
}

func FuzzFramer(f *testing.F) {
	f.Add(testclient.EncodeQuery(testclient.EncodeQuery(nil, "SELECT 1"), "BEGIN"), false)
	f.Add(testclient.EncodeStartup(nil, []testclient.Param{{Name: "user", Value: "app"}}), true)
	f.Add([]byte{'Q', 0, 0, 0, 2}, false)
	f.Add([]byte{'Q', 0x7f, 0xff, 0xff, 0xff}, false)
	f.Fuzz(func(t *testing.T, data []byte, startup bool) {
		whole, errW := frames(data, startup, func(n int) int { return n })
		ones, errO := frames(data, startup, func(int) int { return 1 })
		if (errW == nil) != (errO == nil) || len(whole) != len(ones) {
			t.Fatalf("chunking changed the result: %d/%v against %d/%v", len(whole), errW, len(ones), errO)
		}
		for i := range whole {
			if whole[i] != ones[i] {
				t.Fatalf("frame %d differs", i)
			}
		}
	})
}

func FuzzMatch(f *testing.F) {
	for _, s := range []string{"SELECT 1", "SELECT v FROM kv WHERE k = 'a'", "BEGIN; SELECT 1; COMMIT", "/*c1*/ UPDATE kv SET v = 'x' WHERE k = 'y'",
		"SELECT pg_sleep(0.05)", "SELECT repeat('x', 100) FROM generate_series(1, 200000)", "SET a.b TO 'c'", "'", "/*", "SELECT ''''", ";;;",
		"SELECT repeat('ab', 5000000000000000000) FROM generate_series(1, 1)", "SELECT repeat('x', 1) FROM generate_series(-9223372036854775808, 9223372036854775807)"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, q string) {
		parts := fakepg.Split(q)
		if len(parts) > fakepg.MaxStatements+1 {
			t.Fatalf("%d statements", len(parts))
		}
		for _, p := range parts {
			st := fakepg.Match(p)
			if st.Kind >= fakepg.NumKinds || st.Rows > fakepg.MaxRowCount || st.Rows < 0 || st.Size > fakepg.MaxRowSize || st.Size < 0 || st.Sleep > fakepg.MaxSleep || st.Sleep < 0 {
				t.Fatalf("%q matched out of bounds: %+v", p, st)
			}
		}
	})
}

func FuzzServerInput(f *testing.F) {
	startup := testclient.EncodeStartup(nil, []testclient.Param{{Name: "user", Value: "app"}})
	f.Add(append(append([]byte{}, startup...), testclient.EncodeQuery(nil, "SELECT 1")...))
	f.Add(testclient.EncodeSSLRequest(nil))
	f.Add(testclient.EncodeCancel(nil, 10001, []byte{1, 2, 3, 4}))
	f.Add(append(append([]byte{}, startup...), 'P', 0, 0, 0, 4))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<12 {
			data = data[:1<<12]
		}
		w := newWorld(t, fakepg.Config{})
		p := w.dial()
		w.run(time.Millisecond)
		p.Send(bytes.Clone(data))
		w.run(100 * time.Millisecond)
		p.Close()
		w.quiet()
	})
}
