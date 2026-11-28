package adv_test

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/retry"
)

const seed int64 = 12012026

func begin(t *testing.T) {
	t.Helper()
	t.Logf("seed=%d; REPLAY: /usr/local/go/bin/go test -count=1 -run '^%s$' ./agent_work/adversarial/c12-retry/...", seed, t.Name())
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func policy(t *testing.T, cap, floor int64) retry.Policy {
	t.Helper()
	p, e := retry.New(retry.Config{Kind: retry.KindPreForwardOnce, CapTokens: cap, FloorTokens: floor})
	must(t, e)
	return p
}
func eligible() retry.Failure { return retry.Failure{Deadline: 1 << 62} }

type rig struct {
	t          *testing.T
	e          *proxy.Engine
	now        proxy.Instant
	opens      []proxy.ConnID
	queries    []proxy.Action
	replies    map[proxy.ConnID]int
	errors     map[proxy.ConnID]int
	buffers    map[proxy.ConnID][]byte
	transcript []string
}

func newRig(t *testing.T, edit func(*proxy.Config)) *rig {
	t.Helper()
	return newRigSeed(t, edit, seed)
}
func newRigSeed(t *testing.T, edit func(*proxy.Config), runSeed int64) *rig {
	t.Helper()
	begin(t)
	tenants, err := pgwire.NewTenantTable([]pgwire.Tenant{{User: "adv", Verifier: pgwire.Verifier{Iterations: 4096, Salt: []byte("adversarial-salt")}}})
	must(t, err)
	rng := rand.New(rand.NewSource(runSeed))
	cfg := proxy.Config{Connections: 1, QueueCap: 8, Client: pgwire.ClientConfig{Auth: pgwire.AuthTrustForTestsOnly, Tenants: tenants, Database: "adv", Random: rng}, Backend: pgwire.BackendConfig{User: "adv", Database: "adv", Random: rng}, Retry: retry.Config{Kind: retry.KindPreForwardOnce, CapTokens: 100, FloorTokens: 10}}
	cfg.Timeouts = proxy.DefaultTimeouts()
	cfg.Timeouts.RedialDelay = time.Millisecond
	if edit != nil {
		edit(&cfg)
	}
	e, err := proxy.New(cfg)
	must(t, err)
	r := &rig{t: t, e: e, replies: map[proxy.ConnID]int{}, errors: map[proxy.ConnID]int{}, buffers: map[proxy.ConnID][]byte{}}
	r.consume(e.Start(0, nil))
	r.connect()
	r.client(1001)
	return r
}
func (r *rig) consume(as []proxy.Action) {
	for _, a := range as {
		r.transcript = append(r.transcript, fmt.Sprintf("%d:%d:%d:%x", r.now, a.Kind, a.Conn, a.Data))
		switch a.Kind {
		case proxy.ActDBOpen:
			r.opens = append(r.opens, a.Conn)
		case proxy.ActDBWrite:
			if len(a.Data) > 0 && a.Data[0] == 'Q' {
				a.Data = append([]byte(nil), a.Data...)
				r.queries = append(r.queries, a)
			} else {
				r.step(proxy.Event{Kind: proxy.EvDBWrote, Conn: a.Conn, N: len(a.Data)})
			}
		case proxy.ActClientWrite:
			r.buffers[a.Conn] = append(r.buffers[a.Conn], a.Data...)
			b := r.buffers[a.Conn]
			for len(b) >= 5 {
				n := int(binary.BigEndian.Uint32(b[1:5])) + 1
				if n > len(b) {
					break
				}
				if n < 5 {
					r.t.Fatalf("bad client frame %x", b)
				}
				if b[0] == 'Z' {
					r.replies[a.Conn]++
				}
				if b[0] == 'E' {
					r.errors[a.Conn]++
				}
				b = b[n:]
			}
			r.buffers[a.Conn] = b
			r.step(proxy.Event{Kind: proxy.EvClientWrote, Conn: a.Conn, N: len(a.Data)})
		}
	}
}
func (r *rig) step(ev proxy.Event) { r.consume(r.e.Step(r.now, ev, nil)); must(r.t, r.e.Invariants()) }
func (r *rig) advance(d time.Duration) {
	r.now += proxy.Instant(d)
	r.step(proxy.Event{Kind: proxy.EvTimer})
}
func (r *rig) connect() proxy.ConnID {
	r.t.Helper()
	if len(r.opens) == 0 {
		r.advance(time.Millisecond)
	}
	if len(r.opens) == 0 {
		r.t.Fatal("no database dial to complete")
	}
	id := r.opens[0]
	r.opens = r.opens[1:]
	r.step(proxy.Event{Kind: proxy.EvDBOpened, Conn: id})
	b := pgwire.AppendAuthentication(nil, 0, nil)
	b = pgwire.AppendBackendKeyData(b, pgwire.BackendKey{ProcessID: 42, Secret: [4]byte{0, 0, 0, 13}})
	b = pgwire.AppendReadyForQuery(b, 'I')
	r.step(proxy.Event{Kind: proxy.EvDBData, Conn: id, Data: b})
	return id
}
func (r *rig) client(id proxy.ConnID) {
	r.step(proxy.Event{Kind: proxy.EvClientAccepted, Conn: id})
	r.step(proxy.Event{Kind: proxy.EvClientData, Conn: id, Data: pgwire.AppendStartupMessage(nil, []pgwire.Param{{Name: "user", Value: "adv"}, {Name: "database", Value: "adv"}})})
	if _, ok := r.e.Tenant(id); !ok {
		r.t.Fatal("client login failed")
	}
	r.replies[id] = 0
	r.errors[id] = 0
}
func (r *rig) query(id proxy.ConnID, text string) {
	r.step(proxy.Event{Kind: proxy.EvClientData, Conn: id, Data: pgwire.AppendQuery(nil, []byte(text))})
}
func (r *rig) take() proxy.Action {
	r.t.Helper()
	if len(r.queries) == 0 {
		r.t.Fatal("no forwarded query")
	}
	a := r.queries[0]
	r.queries = r.queries[1:]
	return a
}
func (r *rig) lose(a proxy.Action, n int, refuse bool) {
	if n > 0 {
		r.step(proxy.Event{Kind: proxy.EvDBWrote, Conn: a.Conn, N: n})
	}
	if refuse {
		r.step(proxy.Event{Kind: proxy.EvDBWriteRefused, Conn: a.Conn})
	}
	r.step(proxy.Event{Kind: proxy.EvDBClosed, Conn: a.Conn})
}
func command(tag string, status byte) []byte {
	b := pgwire.AppendMessageHeader(nil, 'C', len(tag)+1)
	b = append(b, tag...)
	b = append(b, 0)
	return pgwire.AppendReadyForQuery(b, status)
}
func (r *rig) success(a proxy.Action, status byte) {
	r.step(proxy.Event{Kind: proxy.EvDBWrote, Conn: a.Conn, N: len(a.Data)})
	r.step(proxy.Event{Kind: proxy.EvDBData, Conn: a.Conn, Data: command("UPDATE 1", status)})
	if status == 'I' {
		reset := r.take()
		r.step(proxy.Event{Kind: proxy.EvDBWrote, Conn: reset.Conn, N: len(reset.Data)})
		r.step(proxy.Event{Kind: proxy.EvDBData, Conn: reset.Conn, Data: command("DISCARD ALL", 'I')})
	}
}
func (r *rig) end() {
	r.consume(r.e.Shutdown(r.now, nil))
	must(r.t, r.e.Invariants())
	must(r.t, r.e.Quiesce())
	if r.e.Snapshot().RetryPending.Value != 0 {
		r.t.Fatal("retry remains after shutdown")
	}
}
func (r *rig) retryCount() int64 {
	s, ok := r.e.RetrySnapshot()
	if !ok {
		r.t.Fatal("no retry snapshot")
	}
	return s.Retries
}
