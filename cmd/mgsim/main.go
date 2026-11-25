// Command mgsim runs named deterministic simulation scenarios outside go test,
// printing both trace and state digests and a REPLAY line for reproduction.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/simtoy"
)

const usage = `mgsim runs a named deterministic simulation scenario from a seed, outside go test.

Usage:
  mgsim run <scenario> [-seed N] [-trace]   run one scenario; prints both digests and the REPLAY line
  mgsim list                                print the scenario names

Flags of run:
  -seed N   int64 root seed (default 1)
  -trace    print every trace record before the digests

Exit status: 0 the scenario passed its checks, 1 it failed, 2 usage error.
`

type runner func(cfg simtoy.Config) *simtoy.Result

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, simtoy.Run))
}

func run(args []string, stdout, stderr io.Writer, exec runner) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "list":
		for _, sc := range simtoy.Scenarios() {
			fmt.Fprintln(stdout, sc.Name)
		}
		return 0
	case "run":
		return runScenario(args[1:], stdout, stderr, exec)
	case "help", "-h", "-help", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "mgsim: unknown command %q\n%s", args[0], usage)
	return 2
}

func runScenario(args []string, stdout, stderr io.Writer, exec runner) int {
	fs := flag.NewFlagSet("mgsim run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	seed := fs.Int64("seed", 1, "")
	trace := fs.Bool("trace", false, "")
	var name string
	// Accept the scenario name either before or after flags so both
	// "mgsim run <name> -seed N" and "mgsim run -seed N <name>" work.
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		name, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if name == "" && fs.NArg() == 1 {
		name = fs.Arg(0)
	} else if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "mgsim: unexpected arguments %v\n", fs.Args())
		return 2
	}
	sc, ok := simtoy.Lookup(name)
	if !ok {
		fmt.Fprintf(stderr, "mgsim: unknown scenario %q (see mgsim list)\n", name)
		return 2
	}
	cfg := sc.Config(*seed)
	if *trace {
		cfg.Tap = func(r sim.Record) { fmt.Fprintln(stdout, r.String()) }
	}
	res := exec(cfg)
	d := res.Sim.Digests()
	err := sc.Check(res)
	status := "pass"
	if err != nil {
		status = "fail"
	}
	fmt.Fprintf(stdout, "scenario=%s seed=%d steps=%d status=%s\n", sc.Name, *seed, res.Sim.Steps(), status)
	fmt.Fprintf(stdout, "trace-digest: %s\nstate-digest: %s\n", d.Trace, d.State)
	replay := sim.DefaultReplay(sc.Name, *seed)
	if err != nil {
		fmt.Fprintf(stderr, "mgsim: %v\n", err)
		tail := res.Sim.LogTail(20)
		fmt.Fprintf(stderr, "trace tail (%d of %s):\n", len(tail), strconv.FormatInt(res.Sim.LogLen(), 10))
		for _, line := range tail {
			fmt.Fprintf(stderr, "  %s\n", line)
		}
		fmt.Fprintln(stderr, replay)
		fmt.Fprintln(stdout, replay)
		return 1
	}
	fmt.Fprintln(stdout, replay)
	return 0
}
