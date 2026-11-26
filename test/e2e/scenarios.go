package e2e

import (
	"strings"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/work"
)

type Scenario struct {
	Spec   Spec
	Tamper Tamper
	Check  func(r *SimRun) error
}

const (
	backendOne  = 1
	stepGap     = 20 * time.Millisecond
	shortWrite  = 300 * time.Millisecond
	shortStmt   = 500 * time.Millisecond
	shortQueue  = time.Second
	shortConn   = 300 * time.Millisecond
	shortReset  = 200 * time.Millisecond
	bigRows     = 2000
	bigRowWidth = 1000
)

func key(c int, k string) string { return "c" + itoa(c) + k }

func read(c int, k string) string { return "SELECT v FROM kv WHERE k = '" + key(c, k) + "'" }

func update(c int, k, v string) string {
	return "UPDATE kv SET v = '" + v + "' WHERE k = '" + key(c, k) + "'"
}

func steps(sqls ...string) []Step {
	out := make([]Step, len(sqls))
	for i, s := range sqls {
		out[i] = Step{SQL: s, Delay: stepGap}
	}
	return out
}

func clientRows(n int) []fakepg.Row {
	var rows []fakepg.Row
	for c := 0; c < n; c++ {
		for _, k := range []string{"a", "b"} {
			rows = append(rows, fakepg.Row{Key: key(c, k), Value: "init"})
		}
	}
	return append(rows, fakepg.Row{Key: "shared", Value: "clean"})
}

func resultsScript(c, tenants int) Script {
	return Script{Tenant: c % tenants, Start: time.Duration(c) * time.Millisecond, Steps: steps(
		read(c, "a"),
		update(c, "a", "v1"),
		read(c, "a"),
		"BEGIN",
		update(c, "b", "t1"),
		read(c, "b"),
		"COMMIT",
		"BEGIN",
		update(c, "a", "lost"),
		"ROLLBACK",
		read(c, "a"),
		"SELECT 1 / 0",
		"SELECT 7",
	)}
}

func ResultsSpec(name string, fragment bool) Spec {
	clients := 12
	if fragment {
		clients = 5
	}
	sp := Spec{Name: name, Tenants: 3, Conns: 3, QueueCap: 64, Rows: clientRows(clients), Duration: 2 * time.Second,
		Fragment: fragment}
	for c := 0; c < clients; c++ {
		sp.Clients = append(sp.Clients, resultsScript(c, sp.Tenants))
	}
	return sp
}

func txSpec(plant proxy.Plant) Spec {
	const clients = 16
	sp := Spec{Name: "e2e-tx-integrity", Tenants: 4, Conns: 2, QueueCap: 64, Rows: clientRows(clients), Plant: plant,
		Duration: 3 * time.Second}
	for c := 0; c < clients; c++ {
		var st []Step
		for round := 0; round < 3; round++ {
			st = append(st,
				Step{SQL: Tag(c, "BEGIN"), Delay: time.Duration(c%5) * time.Millisecond},
				Step{SQL: Tag(c, update(c, "a", "r"+itoa(round))), Delay: 2 * time.Millisecond},
				Step{SQL: Tag(c, read(c, "b")), Delay: 3 * time.Millisecond},
				Step{SQL: Tag(c, update(c, "b", "r"+itoa(round))), Delay: time.Millisecond},
				Step{SQL: Tag(c, "COMMIT"), Delay: 2 * time.Millisecond},
				Step{SQL: Tag(c, read(c, "a")), Delay: time.Millisecond},
			)
		}
		sp.Clients = append(sp.Clients, Script{Tenant: c % sp.Tenants, Start: time.Duration(c) * 500 * time.Microsecond, Steps: st})
	}
	return sp
}

func TxIntegritySpec() Spec { return txSpec(0) }

func TxIntegrityCanarySpec() Spec { return txSpec(proxy.PlantReleaseOnAnyReady) }

func rejectSpec() Spec {
	sp := Spec{Name: "e2e-reject-list", Tenants: 2, Conns: 2, QueueCap: 16, Rows: clientRows(2), Duration: 2 * time.Second}
	var st []Step
	for _, r := range pgwire.RejectRules {
		if r.Reason == work.RejectExtendedProtocol {
			continue
		}
		st = append(st, Step{SQL: r.Example, Delay: stepGap}, Step{SQL: "SELECT 1", Delay: stepGap})
	}
	sp.Clients = append(sp.Clients, Script{Tenant: 0, Steps: st})
	ext := Step{Raw: ExtendedMessages(), Delay: stepGap}
	sp.Clients = append(sp.Clients, Script{Tenant: 1, Start: time.Millisecond, Steps: []Step{ext, {SQL: "SELECT 1", Delay: stepGap}, ext, {SQL: "SELECT 2", Delay: stepGap}}})
	return sp
}

func ExtendedMessages() []byte {
	parse := append([]byte{0}, "SELECT 1"...)
	parse = append(parse, 0, 0, 0)
	b := pgwire.AppendMessageHeader(nil, 'P', len(parse))
	b = append(b, parse...)
	b = pgwire.AppendMessageHeader(b, 'B', 8)
	b = append(b, 0, 0, 0, 0, 0, 0, 0, 0)
	b = pgwire.AppendMessageHeader(b, 'E', 5)
	b = append(b, 0, 0, 0, 0, 0)
	return pgwire.AppendMessageHeader(b, 'S', 0)
}

var sessionSettings = []struct{ name, value, def string }{
	{"work_mem", "'64MB'", "4MB"},
	{"search_path", "hostile", `"$user", public`},
	{"statement_timeout", "'5s'", "0"},
	{"lock_timeout", "'7s'", "0"},
	{"application_name", "'hostile'", ""},
}

func sessionSpec(plant proxy.Plant) Spec {
	const hostile, observers = 6, 6
	sp := Spec{Name: "e2e-session-state", Tenants: 3, Conns: 2, QueueCap: 64, Rows: clientRows(hostile + observers),
		Plant: plant | proxy.PlantSkipCourtesy, Duration: 3 * time.Second}
	for c := 0; c < hostile; c++ {
		var st []Step
		for i, s := range sessionSettings {
			form := "SET " + s.name + " = " + s.value
			switch (c + i) % 3 {
			case 1:
				form = "SET " + s.name + " TO " + s.value
			case 2:
				form = "SET SESSION " + s.name + " = " + s.value
			}
			st = append(st, Step{SQL: Tag(c, form), Delay: time.Duration(1+c%3) * time.Millisecond})
		}
		st = append(st, Step{SQL: Tag(c, "RESET lock_timeout"), Delay: time.Millisecond})
		sp.Clients = append(sp.Clients, Script{Tenant: c % sp.Tenants, Start: time.Duration(c) * 700 * time.Microsecond, Steps: st})
	}
	for o := 0; o < observers; o++ {
		c := hostile + o
		var st []Step
		for round := 0; round < 2; round++ {
			for _, s := range sessionSettings {
				st = append(st, Step{SQL: Tag(c, "SHOW "+s.name), Delay: time.Duration(1+o%2) * time.Millisecond})
			}
		}
		sp.Clients = append(sp.Clients, Script{Tenant: (c + 1) % sp.Tenants, Start: time.Duration(o)*900*time.Microsecond + 2*time.Millisecond, Steps: st})
	}
	return sp
}

func SessionSpec() Spec { return sessionSpec(0) }

func SessionAblationSpec() Spec { return sessionSpec(proxy.PlantNoReset) }

func SessionLendEarlySpec() Spec { return sessionSpec(proxy.PlantLendBeforeResetReply) }

func resetTamperSpec(t Tamper) Spec {
	sp := Spec{Name: "e2e-reset-" + t.String(), Tenants: 2, Conns: 1, QueueCap: 16, Rows: clientRows(2),
		Duration: 6 * time.Second}
	sp.Clients = []Script{
		{Tenant: 0, Steps: steps(read(0, "a"), read(0, "b"))},
		{Tenant: 1, Start: 2 * time.Millisecond, Steps: []Step{{SQL: read(1, "a"), Delay: 5 * time.Millisecond}, {SQL: "SELECT 3", Delay: 4 * time.Second}}},
	}
	return sp
}

func vanishSpec() Spec {
	sp := Spec{Name: "e2e-client-vanish", Tenants: 2, Conns: 2, QueueCap: 16, Rows: clientRows(4), Duration: 3 * time.Second,
		Service: fakepg.Service{PerKey: []fakepg.KeyTime{{Key: key(1, "a"), Time: 200 * time.Millisecond}}}}
	sp.Clients = []Script{
		{Tenant: 0, Steps: []Step{{SQL: "BEGIN"}, {SQL: "UPDATE kv SET v = 'dirty' WHERE k = 'shared'", Delay: stepGap, Vanish: VanishAfterReply}}},
		{Tenant: 1, Start: time.Millisecond, Steps: []Step{{SQL: "BEGIN"}, {SQL: update(1, "a", "dirty"), Delay: stepGap, Vanish: VanishAfterSend}}},
	}
	for c := 2; c < 4; c++ {
		sp.Clients = append(sp.Clients, Script{Tenant: c % 2, Start: 500 * time.Millisecond,
			Steps: steps("SELECT v FROM kv WHERE k = 'shared'", read(1, "a"), "BEGIN", "SELECT 1", "COMMIT")})
	}
	return sp
}

func killSpec(name string, point int64, victim Step) Spec {
	sp := Spec{Name: name, Tenants: 1, Conns: 1, QueueCap: 16, Rows: clientRows(1), Duration: 2 * time.Second}
	sp.Faults = []sim.Fault{{At: sim.Time(time.Millisecond), Kind: fakepg.FaultReset, Target: backendOne, Arg1: point}}
	victim.Delay = 50 * time.Millisecond
	sp.Clients = []Script{{Tenant: 0, Steps: []Step{victim, {SQL: "SELECT 5", Delay: 500 * time.Millisecond}}}}
	return sp
}

func rowsQuery(n, width int) string {
	return "SELECT repeat('x', " + itoa(width) + ") FROM generate_series(1, " + itoa(n) + ")"
}

func slowReaderSpec() Spec {
	sp := Spec{Name: "e2e-slow-reader", Tenants: 2, Conns: 2, QueueCap: 16, Rows: clientRows(3), ReplyCap: 2 * proxy.MaxRead,
		Timeouts: proxy.Timeouts{ClientWrite: shortWrite}, Duration: 3 * time.Second}
	sp.Clients = []Script{
		{Tenant: 0, Steps: []Step{{SQL: rowsQuery(bigRows, bigRowWidth), Delay: stepGap, StopReading: true, StallFor: 2 * time.Second}}},
		{Tenant: 1, Start: 10 * time.Millisecond, Steps: steps(read(1, "a"), read(1, "b"), "SELECT 1")},
		{Tenant: 1, Start: time.Second, Steps: steps(read(2, "a"), read(2, "b"), "SELECT 1")},
	}
	return sp
}

func queueFullSpec() Spec {
	sp := Spec{Name: "e2e-queue-full", Tenants: 2, Conns: 1, QueueCap: 2, Rows: clientRows(6), Duration: 3 * time.Second,
		Service: fakepg.Service{PerKind: []fakepg.KindTime{{Kind: fakepg.KindRead, Time: 100 * time.Millisecond}}}}
	for c := 0; c < 6; c++ {
		sp.Clients = append(sp.Clients, Script{Tenant: c % 2, Start: 50 * time.Millisecond,
			Steps: []Step{{SQL: read(c, "a")}, {SQL: "SELECT 9", Delay: time.Second}}})
	}
	return sp
}

func silentStartupSpec() Spec {
	sp := Spec{Name: "e2e-silent-db-startup", Tenants: 1, Conns: 1, QueueCap: 16, Rows: clientRows(1), Duration: 4 * time.Second,
		Timeouts: proxy.Timeouts{DBConnect: shortConn, QueueWait: shortQueue, Reset: shortReset}}
	sp.Faults = []sim.Fault{{At: 0, Kind: fakepg.FaultSilentStartup}, {At: sim.Time(1500 * time.Millisecond), Kind: fakepg.FaultClear, Arg1: int64(fakepg.FaultSilentStartup)}}
	sp.Clients = []Script{{Tenant: 0, Steps: []Step{{SQL: read(0, "a"), Delay: 50 * time.Millisecond}, {SQL: read(0, "b"), Delay: 1500 * time.Millisecond}}}}
	return sp
}

func silentQuerySpec() Spec {
	sp := Spec{Name: "e2e-silent-db-query", Tenants: 1, Conns: 1, QueueCap: 16, Rows: clientRows(1), Duration: 3 * time.Second,
		Timeouts: proxy.Timeouts{Statement: shortStmt}}
	sp.Faults = []sim.Fault{{At: sim.Time(time.Millisecond), Kind: fakepg.FaultSilentQuery, Target: backendOne}}
	sp.Clients = []Script{{Tenant: 0, Steps: []Step{{SQL: read(0, "a"), Delay: 50 * time.Millisecond}, {SQL: update(0, "b", "after"), Delay: 200 * time.Millisecond}}}}
	return sp
}

func Scenarios() []Scenario {
	out := []Scenario{
		{Spec: ResultsSpec("e2e-results", false), Check: checkResults},
		{Spec: ResultsSpec("e2e-fragment", true), Check: checkResults},
		{Spec: TxIntegritySpec(), Check: checkTxIntegrity},
		{Spec: rejectSpec(), Check: checkRejects},
		{Spec: SessionSpec(), Check: checkSession},
		{Spec: vanishSpec(), Check: checkVanish},
		{Spec: killSpec("e2e-kill-before-read", int64(fakepg.PointBefore), Step{SQL: read(0, "a")}), Check: checkKill(pgwire.ErrRowLostAfterRead)},
		{Spec: killSpec("e2e-kill-before-write", int64(fakepg.PointBefore), Step{SQL: update(0, "a", "w")}), Check: checkKill(pgwire.ErrRowLostAfterWrite)},
		{Spec: killSpec("e2e-kill-during-reply", int64(fakepg.PointDuring), Step{SQL: rowsQuery(200, 100)}), Check: checkKill(pgwire.ErrRowPartialReply)},
		{Spec: killSpec("e2e-kill-mid-transaction", int64(fakepg.PointMidTx), Step{SQL: "BEGIN"}), Check: checkKill(pgwire.ErrRowLostInTransaction)},
		{Spec: killSpec("e2e-kill-after-reply", int64(fakepg.PointAfter), Step{SQL: read(0, "a")}), Check: checkKillAfter},
		{Spec: slowReaderSpec(), Check: checkSlowReader},
		{Spec: queueFullSpec(), Check: checkQueueFull},
		{Spec: silentStartupSpec(), Check: checkSilentStartup},
		{Spec: silentQuerySpec(), Check: checkSilentQuery},
	}
	for t := TamperError; t < NumTampers; t++ {
		out = append(out, Scenario{Spec: resetTamperSpec(t), Tamper: t, Check: checkTamper(t)})
	}
	return out
}

func Lookup(name string) (Scenario, bool) {
	for _, sc := range Scenarios() {
		if sc.Spec.Name == name {
			return sc, true
		}
	}
	return Scenario{}, false
}

func joinSteps(r ClientResult) string { return strings.TrimSpace(r.String()) }
