package shell

import (
	"sync/atomic"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/proxy"
)

type gauge struct {
	value atomic.Int64
	peak  atomic.Int64
}

func (g *gauge) add(d int64) { g.peakAt(g.value.Add(d)) }

func (g *gauge) peakAt(v int64) {
	for {
		p := g.peak.Load()
		if v <= p || g.peak.CompareAndSwap(p, v) {
			return
		}
	}
}

func (g *gauge) load() obs.Gauge { return obs.Gauge{Value: g.value.Load(), Peak: g.peak.Load()} }

type counters struct {
	mailbox          gauge
	mailboxFull      atomic.Int64
	goroutines       gauge
	clientReaders    gauge
	clientWriters    gauge
	dbReaders        gauge
	dbWriters        gauge
	accepted         atomic.Int64
	refused          atomic.Int64
	acceptErrors     atomic.Int64
	adminRequests    atomic.Int64
	handshakesFailed atomic.Int64
	writeDeadlines   atomic.Int64
	readDeadlines    atomic.Int64
	queueOverflows   atomic.Int64
	dropped          atomic.Int64
}

// Snapshot returns a plain value copy of the shell's mailbox, goroutine, and
// socket counters for observability export.
func (s *Shell) Snapshot() proxy.ShellSnapshot {
	st := &s.stats
	mb := st.mailbox.load()
	mb.Value = int64(len(s.mailbox))
	mb.Peak = max(mb.Peak, mb.Value)
	return proxy.ShellSnapshot{
		MailboxDepth:     mb,
		MailboxCap:       int64(cap(s.mailbox)),
		MailboxFull:      st.mailboxFull.Load(),
		Goroutines:       st.goroutines.load(),
		ClientReaders:    st.clientReaders.load(),
		ClientWriters:    st.clientWriters.load(),
		DBReaders:        st.dbReaders.load(),
		DBWriters:        st.dbWriters.load(),
		Accepted:         st.accepted.Load(),
		AdminRequests:    st.adminRequests.Load(),
		HandshakesFailed: st.handshakesFailed.Load(),
		WriteDeadlines:   st.writeDeadlines.Load(),
		ReadDeadlines:    st.readDeadlines.Load(),
		AcceptRefused:    st.refused.Load(),
		AcceptErrors:     st.acceptErrors.Load(),
		QueueOverflows:   st.queueOverflows.Load(),
		StaleEvents:      st.dropped.Load(),
	}
}

// Extra exposes shell-only refusal and overflow counters used by unit tests.
type Extra struct {
	Refused        int64
	AcceptErrors   int64
	QueueOverflows int64
	Dropped        int64
}

// Extra returns a point-in-time copy of the shell's refusal and overflow counters.
func (s *Shell) Extra() Extra {
	return Extra{Refused: s.stats.refused.Load(), AcceptErrors: s.stats.acceptErrors.Load(),
		QueueOverflows: s.stats.queueOverflows.Load(), Dropped: s.stats.dropped.Load()}
}
