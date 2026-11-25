// Package workload implements simulated Postgres client actors over simnet:
// multi-tenant open-loop (constant and Poisson) and closed-loop traffic
// generation with intended-send-time latency measurement, Zipf/uniform key
// selection, configurable retries, client-side faults, and a hand-driven Probe.
package workload

import (
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
)

var (
	kindArrive  = sim.K("wl.arr")
	kindSend    = sim.K("wl.send")
	kindDone    = sim.K("wl.done")
	kindConnect = sim.K("wl.conn")
	kindRetry   = sim.K("wl.retry")
	kindDrop    = sim.K("wl.drop")
)

type tenantState struct {
	cfg      Tenant
	idx      int
	st       tenantStats
	latency  *obs.Histogram
	fromSend *obs.Histogram
}

// Workload manages a set of simulated tenant client actors and their registered
// fault handlers, conservation invariants, state writer, and quiesce check.
type Workload struct {
	s       *sim.Sim
	n       *simnet.Net
	cfg     Config
	tenants []*tenantState
	clients []*client
	cancels []*Probe
	readBuf [readSize]byte
	started bool
	faults  faultStats
}

type faultStats struct {
	applied  int64
	byKind   [6]int64
	noTarget int64
}

// New validates cfg, constructs all client actors with isolated per-client
// random streams, and registers workload faults, invariants, and quiesce checks.
// Call Start to schedule initial arrivals and connections.
func New(n *simnet.Net, cfg Config) (*Workload, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	w := &Workload{s: n.Sim(), n: n, cfg: cfg}
	if w.cfg.Options.Window == 0 {
		w.cfg.Options = simnet.DefaultOptions()
	}
	id := 0
	for i, t := range cfg.Tenants {
		t = t.normalized()
		ts := &tenantState{cfg: t, idx: i, latency: obs.NewDurationHistogram(), fromSend: obs.NewDurationHistogram()}
		w.tenants = append(w.tenants, ts)
		for j := 0; j < t.Clients; j++ {
			id++
			w.clients = append(w.clients, newClient(w, ts, id, j))
		}
	}
	if err := w.registerFaults(); err != nil {
		return nil, err
	}
	_ = w.s.RegisterQuiesce("workload", w.quiesce)
	_ = w.s.RegisterInvariant("workload.conservation", w.conservation)
	_ = w.s.RegisterState("workload", w.writeState)
	return w, nil
}

// Start schedules every client's initial arrival and/or pre-connect event once.
func (w *Workload) Start() {
	if w.started {
		return
	}
	w.started = true
	for _, c := range w.clients {
		c.start()
	}
}

// Clients returns the total number of client actors across all tenants.
func (w *Workload) Clients() int { return len(w.clients) }

// Done reports whether every client actor has finished its requests and closed.
func (w *Workload) Done() bool {
	for _, c := range w.clients {
		if !c.finished() {
			return false
		}
	}
	return true
}

func (w *Workload) client(id int64) *client {
	if id < 1 || id > int64(len(w.clients)) {
		return nil
	}
	return w.clients[id-1]
}

func (w *Workload) quiesce() error {
	var errs []error
	for _, c := range w.clients {
		if !c.finished() {
			errs = append(errs, fmt.Errorf("client %d not finished: %s", c.id, c.describe()))
			if len(errs) >= 5 {
				break
			}
		}
	}
	for _, p := range w.cancels {
		if !p.Closed() {
			errs = append(errs, errors.New("a cancel connection is open"))
			break
		}
	}
	return errors.Join(errs...)
}

func (w *Workload) conservation() error {
	var errs []error
	for _, t := range w.tenants {
		if err := t.st.check(); err != nil {
			errs = append(errs, fmt.Errorf("tenant %s: %w", t.cfg.Name, err))
		}
	}
	return errors.Join(errs...)
}

func (w *Workload) writeState(sk sim.StateSink) {
	for _, t := range w.tenants {
		sk.String(t.cfg.Name)
		t.st.write(sk)
		sk.Int(t.latency.Count(), t.latency.Quantile(5000), t.latency.Quantile(9900))
	}
	for _, c := range w.clients {
		sk.Int(int64(c.phase), int64(len(c.backlog)), c.made, boolInt(c.cur != nil))
	}
}

// Tenant returns the 0-based index of the named tenant in Snapshot().Tenants, or -1.
func (w *Workload) Tenant(name string) int {
	return slices.IndexFunc(w.tenants, func(t *tenantState) bool { return t.cfg.Name == name })
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func label(parts ...string) string {
	out := "workload"
	for _, p := range parts {
		out += "/" + p
	}
	return out
}

func itoa(v int) string { return strconv.Itoa(v) }
