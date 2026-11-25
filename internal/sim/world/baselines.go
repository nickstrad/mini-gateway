package world

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
)

const (
	HotKey         = "hot"
	BaselineKeys   = 100
	IdenticalReads = 1000
	ManyClients    = 500
	BurstLimit     = 100
	BurstWindow    = 50 * time.Millisecond
	BurstAt        = sim.Time(100 * time.Millisecond)
)

func HotRead() string { return "SELECT v FROM kv WHERE k = '" + HotKey + "'" }

func rows(n int) []fakepg.Row {
	out := []fakepg.Row{{Key: HotKey, Value: "h"}}
	for i := 0; i < n; i++ {
		out = append(out, fakepg.Row{Key: workload.DefaultKeyPrefix + strconv.Itoa(i), Value: "v"})
	}
	return out
}

func netOptions() simnet.Options {
	o := simnet.DefaultOptions()
	o.Up.Latency = simnet.Latency{Base: 100 * time.Microsecond, Jitter: 50 * time.Microsecond}
	o.Down.Latency = simnet.Latency{Base: 100 * time.Microsecond, Jitter: 50 * time.Microsecond}
	return o
}

func Scenarios() []Scenario {
	return []Scenario{
		ManyConnections(),
		ReconnectBurst(),
		IdenticalReadsScenario(),
		OpenLoopOverload(),
		MixedTransactions(),
	}
}

func ManyConnections() Scenario {
	return Scenario{
		Name:     "direct-500-connections",
		Duration: 2 * time.Second,
		Setup: func(int64) Setup {
			return Setup{
				Fake: fakepg.Config{MaxConnections: 2 * ManyClients, Rows: rows(BaselineKeys),
					Service: fakepg.Service{Fixed: 2 * time.Millisecond, JitterMax: time.Millisecond}},
				Workload: workload.Config{Options: netOptions(), Tenants: []workload.Tenant{{
					Name: "app", Clients: ManyClients, Requests: 5, Interval: 20 * time.Millisecond, Spread: true,
					ConnectFirst: true, Hold: 200 * time.Millisecond, Keys: workload.Keys{N: BaselineKeys},
				}}},
			}
		},
		Check: func(r *Result) error {
			o := r.Obs
			c := o.Clients.Total
			switch {
			case o.DBSessionsPeak != ManyClients || o.DBConnsPeak != ManyClients:
				return fmt.Errorf("database connections at peak: sessions %d, sockets %d, want %d", o.DBSessionsPeak, o.DBConnsPeak, ManyClients)
			case c.OK != 5*ManyClients || c.Intended != 5*ManyClients:
				return fmt.Errorf("requests: intended %d ok %d", c.Intended, c.OK)
			case o.DBReads != 5*ManyClients:
				return fmt.Errorf("database reads %d", o.DBReads)
			}
			return nil
		},
	}
}

func ReconnectBurst() Scenario {
	return Scenario{
		Name:     "direct-reconnect-burst",
		Duration: 2 * time.Second,
		Setup: func(int64) Setup {
			steady := workload.Tenant{Name: "steady", Clients: BurstLimit, Arrival: workload.ArrivalClosed, Requests: 30,
				Think: 10 * time.Millisecond, ConnectFirst: true, Hold: 100 * time.Millisecond, Keys: workload.Keys{N: BaselineKeys}}
			late := workload.Tenant{Name: "late", Clients: ManyClients - BurstLimit, Hold: 100 * time.Millisecond}
			return Setup{
				Fake:     fakepg.Config{MaxConnections: BurstLimit, Rows: rows(BaselineKeys), Service: fakepg.Service{Fixed: time.Millisecond}},
				Workload: workload.Config{Options: netOptions(), Tenants: []workload.Tenant{steady, late}},
				Faults:   []sim.Fault{{At: BurstAt, Kind: workload.FaultBurst, Target: 1, Arg1: ManyClients, Arg2: int64(BurstWindow)}},
			}
		},
		Check: func(r *Result) error {
			o := r.Obs
			c := o.Clients.Total
			switch {
			case o.DBSessionsPeak > BurstLimit:
				return fmt.Errorf("sessions peak %d above max_connections %d", o.DBSessionsPeak, BurstLimit)
			case o.DBRefusedTooMany < ManyClients-BurstLimit:
				return fmt.Errorf("refusals %d, want at least %d", o.DBRefusedTooMany, ManyClients-BurstLimit)
			case c.Code("53300") != o.DBRefusedTooMany:
				return fmt.Errorf("clients saw %d refusals (53300), the database counted %d", c.Code("53300"), o.DBRefusedTooMany)
			case c.BurstDrops != ManyClients:
				return fmt.Errorf("burst drops %d", c.BurstDrops)
			case c.OK == 0:
				return errors.New("no request completed")
			}
			return nil
		},
	}
}

func IdenticalReadsScenario() Scenario {
	return Scenario{
		Name:     "direct-1000-identical-reads",
		Duration: time.Second,
		Setup: func(int64) Setup {
			return Setup{
				Fake: fakepg.Config{MaxConnections: IdenticalReads + 100, Rows: rows(0), Service: fakepg.Service{Fixed: 50 * time.Millisecond}},
				Workload: workload.Config{Options: netOptions(), Tenants: []workload.Tenant{{
					Name: "app", Clients: IdenticalReads, Requests: 1, Start: 20 * time.Millisecond, ConnectFirst: true,
					Statement: HotRead(), Keys: workload.Keys{Kind: workload.KeysFixed, Fixed: HotKey},
				}}},
			}
		},
		Check: func(r *Result) error {
			o := r.Obs
			if o.DBReads != IdenticalReads || o.Clients.Total.OK != IdenticalReads {
				return fmt.Errorf("database reads %d, client successes %d, want %d each", o.DBReads, o.Clients.Total.OK, IdenticalReads)
			}
			return nil
		},
	}
}

const (
	OverloadCapacity = 4
	OverloadService  = 10 * time.Millisecond
	OverloadClients  = 40
	OverloadInterval = 50 * time.Millisecond
	OverloadRequests = 20
)

func OpenLoopOverload() Scenario {
	return Scenario{
		Name:     "direct-open-loop-overload",
		Duration: 5 * time.Second,
		Setup: func(int64) Setup {
			return Setup{
				Fake: fakepg.Config{Capacity: OverloadCapacity, Rows: rows(BaselineKeys), Service: fakepg.Service{Fixed: OverloadService}},
				Workload: workload.Config{Options: netOptions(), Tenants: []workload.Tenant{{
					Name: "app", Clients: OverloadClients, Requests: OverloadRequests, Interval: OverloadInterval,
					Spread: true, ConnectFirst: true, Keys: workload.Keys{N: BaselineKeys},
				}}},
			}
		},
		Check: func(r *Result) error {
			c := r.Obs.Clients.Total
			if c.Intended != OverloadClients*OverloadRequests || c.Sent != c.Intended || c.OK != c.Intended {
				return fmt.Errorf("intended %d sent %d ok %d", c.Intended, c.Sent, c.OK)
			}
			if r.Fake.Waiting.Peak == 0 || r.Fake.InService.Peak != OverloadCapacity {
				return fmt.Errorf("the database never queued: waiting peak %d, in service peak %d", r.Fake.Waiting.Peak, r.Fake.InService.Peak)
			}
			p50, p99 := Quantile(c.Latency, 5000), Quantile(c.Latency, 9900)
			if p99 < int64(10*OverloadService) || p50 >= p99 {
				return fmt.Errorf("latency did not grow with the backlog: p50 %d p99 %d", p50, p99)
			}
			return nil
		},
	}
}

func MixedTransactions() Scenario {
	return Scenario{
		Name:     "direct-mixed-transactions",
		Duration: 3 * time.Second,
		Setup: func(int64) Setup {
			mix := []workload.Weight{{Op: workload.OpRead, Weight: 2}, {Op: workload.OpWrite, Weight: 1}, {Op: workload.OpTx, Weight: 2}}
			t := func(name string) workload.Tenant {
				return workload.Tenant{Name: name, Clients: 10, Requests: 10, Interval: 15 * time.Millisecond, Arrival: workload.ArrivalPoisson,
					Keys: workload.Keys{Kind: workload.KeysZipf, N: 50, Zipf: 1200}, Mix: mix, TxStatements: 3, TxReads: 1,
					TxThink: 2 * time.Millisecond, Tag: true}
			}
			return Setup{
				Fake:     fakepg.Config{Rows: rows(50), Capacity: 8, Service: fakepg.Service{Fixed: time.Millisecond, JitterMax: 2 * time.Millisecond}},
				Workload: workload.Config{Options: netOptions(), Tenants: []workload.Tenant{t("a"), t("b"), t("c")}},
			}
		},
		Check: CheckWriteLog,
	}
}
