package main

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/world"
)

// listWorld prints every registered simulated-world scenario name.
func listWorld(stdout io.Writer) {
	for _, sc := range world.Scenarios() {
		fmt.Fprintln(stdout, sc.Name)
	}
}

// runWorld executes one simulated-world scenario from a seed, printing digests,
// the summary counter/latency table, and a REPLAY line.
func runWorld(sc world.Scenario, seed int64, trace bool, stdout, stderr io.Writer) int {
	d := &world.SimDriver{Seed: seed, Scenario: sc.Name}
	if trace {
		d.Tap = func(r sim.Record) { fmt.Fprintln(stdout, r.String()) }
	}
	res := world.RunWith(sc, seed, d)
	err := sc.Verify(res)
	status := "pass"
	if err != nil {
		status = "fail"
	}
	steps := int64(0)
	if res.Sim != nil {
		steps = res.Sim.Steps()
		dg := res.Sim.Digests()
		fmt.Fprintf(stdout, "scenario=%s seed=%d steps=%d status=%s\n", sc.Name, seed, steps, status)
		fmt.Fprintf(stdout, "trace-digest: %s\nstate-digest: %s\n", dg.Trace, dg.State)
	}
	printTable(stdout, res)
	replay := sim.DefaultReplay(sc.Name, seed)
	if err != nil {
		fmt.Fprintf(stderr, "mgsim: %v\n", err)
		if res.Sim != nil {
			for _, line := range res.Sim.LogTail(20) {
				fmt.Fprintf(stderr, "  %s\n", line)
			}
		}
		fmt.Fprintln(stderr, replay)
		fmt.Fprintln(stdout, replay)
		return 1
	}
	fmt.Fprintln(stdout, replay)
	return 0
}

func printTable(stdout io.Writer, res *world.Result) {
	o := res.Obs
	c := o.Clients.Total
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	rows := []struct {
		name string
		v    int64
	}{
		{"db.connections.accepted", o.DBConnsAccepted},
		{"db.connections.peak", o.DBConnsPeak},
		{"db.sessions.peak", o.DBSessionsPeak},
		{"db.refused.too_many", o.DBRefusedTooMany},
		{"db.statements", o.DBStatements},
		{"db.reads", o.DBReads},
		{"db.writes", o.DBWrites},
		{"clients.intended", c.Intended},
		{"clients.sent", c.Sent},
		{"clients.ok", c.OK},
		{"clients.errors", c.Errors},
		{"clients.failed_before_send", c.FailedBeforeSend},
		{"clients.lost", c.Lost},
		{"clients.timed_out", c.TimedOut},
		{"clients.retries", c.Retries},
		{"clients.reconnects", c.Reconnects},
		{"latency.p50_ns", world.Quantile(c.Latency, 5000)},
		{"latency.p99_ns", world.Quantile(c.Latency, 9900)},
		{"latency.max_ns", c.Latency.Max},
	}
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%d\n", r.name, r.v)
	}
	_ = tw.Flush()
}
