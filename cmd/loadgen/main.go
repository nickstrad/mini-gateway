package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/nickstrad/mini-gateway/internal/loadgen"
	"github.com/nickstrad/mini-gateway/internal/loadgen/live"
	"github.com/nickstrad/mini-gateway/internal/mgctl"
)

const usage = `loadgen puts a constant, open-loop load on a running mini-gateway: every request has an
intended send time and is sent at that time whatever the replies are doing.

Usage:
  loadgen [flags]                 run the load and print a status line each second and a summary
  loadgen -plan N [flags]         print the first N operations of the plan and its digest; no network

Flags:
  -addr host:port       the gateway's client address (default 127.0.0.1:6432)
  -database name        database in the startup packet (default app)
  -user-prefix p        tenant user names are <p>0, <p>1, ... (default t)
  -password pw|-        the tenants' password ("-" reads the first line of stdin)
  -tls mode             off, require or verify-full (default off); -ca-file, -server-name
  -rate N               requests per second (default 100)
  -duration D           how long requests are intended for (default 10s)
  -conns N              connections, spread over the tenants (default 8; at least -tenants)
  -tenants N            tenants t0..t(N-1) (default 2)
  -keys N               rows k1..kN the operations touch (default 1000)
  -profile p            simple (reads and writes) or errors (also unique violations, a missing
                        relation, division by zero and a statement the proxy refuses)
  -seed N               the seed every operation, key and payload is drawn from (default: random,
                        printed at the start so the run can be repeated)
  -max-outstanding N    requests one connection may hold, queued or in flight (default 64)
  -late-after D         a send later than this after its intended time counts as late (default 25ms)
  -drain D              after the last intended send, wait this long for replies (default 5s)
  -csv-dir dir          flush the load tool's own snapshot to CSV under dir (PLAN.md 7.7)
  -quiet                no status lines

Exit status: 0 the plan ran on schedule, 1 setup or runtime failure, 2 usage error,
3 SHORTFALL (a request was late or could not be sent; the summary says how many).
`

const (
	exitOK        = 0
	exitRuntime   = 1
	exitUsage     = 2
	exitShortfall = 3
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, signalContext))
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
}

type flags struct {
	addr, database, prefix, password, tlsMode, caFile, serverName, profile, csvDir string
	rate, keys, seed, plan                                                         int64
	conns, tenants, maxOutstanding                                                 int
	duration, lateAfter, drain                                                     time.Duration
	seedSet, quiet                                                                 bool
}

func parseFlags(args []string, stderr io.Writer) (*flags, int) {
	f := &flags{}
	fs := flag.NewFlagSet("loadgen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	fs.StringVar(&f.addr, "addr", "127.0.0.1:6432", "")
	fs.StringVar(&f.database, "database", "app", "")
	fs.StringVar(&f.prefix, "user-prefix", "t", "")
	fs.StringVar(&f.password, "password", "", "")
	fs.StringVar(&f.tlsMode, "tls", "off", "")
	fs.StringVar(&f.caFile, "ca-file", "", "")
	fs.StringVar(&f.serverName, "server-name", "", "")
	fs.StringVar(&f.profile, "profile", "simple", "")
	fs.StringVar(&f.csvDir, "csv-dir", "", "")
	fs.Int64Var(&f.rate, "rate", 100, "")
	fs.Int64Var(&f.keys, "keys", loadgen.DefaultKeys, "")
	fs.Int64Var(&f.seed, "seed", 0, "")
	fs.Int64Var(&f.plan, "plan", 0, "")
	fs.IntVar(&f.conns, "conns", 8, "")
	fs.IntVar(&f.tenants, "tenants", 2, "")
	fs.IntVar(&f.maxOutstanding, "max-outstanding", loadgen.DefaultMaxOutstanding, "")
	fs.DurationVar(&f.duration, "duration", 10*time.Second, "")
	fs.DurationVar(&f.lateAfter, "late-after", time.Duration(loadgen.DefaultLateAfterNs), "")
	fs.DurationVar(&f.drain, "drain", live.DefaultDrain, "")
	fs.BoolVar(&f.quiet, "quiet", false, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, exitOK
		}
		return nil, exitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "loadgen: unexpected arguments %q\n", fs.Args())
		return nil, exitUsage
	}
	fs.Visit(func(fl *flag.Flag) {
		if fl.Name == "seed" {
			f.seedSet = true
		}
	})
	return f, -1
}

func randomSeed() int64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return int64(binary.LittleEndian.Uint64(b[:]) >> 1)
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, ctxFn func() (context.Context, context.CancelFunc)) int {
	f, code := parseFlags(args, stderr)
	if f == nil {
		return code
	}
	if !f.seedSet {
		f.seed = randomSeed()
	}
	prof, err := loadgen.ParseProfile(f.profile)
	if err != nil {
		fmt.Fprintf(stderr, "loadgen: %v\n", err)
		return exitUsage
	}
	plan := loadgen.Plan{Seed: f.seed, Rate: f.rate, DurationNs: int64(f.duration), Tenants: f.tenants, Keys: f.keys, Profile: prof}
	if err := plan.Validate(); err != nil {
		fmt.Fprintf(stderr, "loadgen: %v\n", err)
		return exitUsage
	}
	if err := loadgen.ValidTenantPrefix(f.prefix); err != nil {
		fmt.Fprintf(stderr, "loadgen: %v\n", err)
		return exitUsage
	}
	if f.plan > 0 {
		return printPlan(plan, f.plan, stdout)
	}
	if f.password == "-" {
		line, err := mgctl.ReadPasswordLine(stdin)
		if err != nil {
			fmt.Fprintf(stderr, "loadgen: reading the password: %v\n", err)
			return exitUsage
		}
		f.password = line
	}
	tlsCfg, err := mgctl.ClientTLS(f.tlsMode, f.caFile, f.serverName, f.addr)
	if err != nil {
		fmt.Fprintf(stderr, "loadgen: %v\n", err)
		return exitUsage
	}
	tcfg := loadgen.TrackerConfig{Conns: f.conns, MaxOutstanding: f.maxOutstanding, LateAfterNs: int64(f.lateAfter)}
	if _, err := loadgen.NewTracker(plan, tcfg); err != nil {
		fmt.Fprintf(stderr, "loadgen: %v\n", err)
		return exitUsage
	}
	if f.drain < 0 {
		fmt.Fprintln(stderr, "loadgen: -drain must not be negative")
		return exitUsage
	}
	fmt.Fprintf(stdout, "loadgen: seed %d profile %s rate %d/s duration %s conns %d tenants %d keys %d against %s (repeat with -seed %d)\n",
		plan.Seed, plan.Profile, plan.Rate, f.duration, f.conns, f.tenants, f.keys, f.addr, plan.Seed)
	status := stdout
	if f.quiet {
		status = io.Discard
	}
	ctx, cancel := ctxFn()
	defer cancel()
	res, err := live.Run(ctx, live.Options{Addr: f.addr, Database: f.database, UserPrefix: f.prefix, Password: f.password,
		TLS: tlsCfg, Plan: plan, Tracker: tcfg, Drain: f.drain, CSVDir: f.csvDir, Build: build(), Status: status})
	return summarize(stdout, stderr, plan, res, err)
}

func summarize(stdout, stderr io.Writer, plan loadgen.Plan, res live.Result, err error) int {
	s := res.Snapshot
	if res.Stopped {
		fmt.Fprintln(stdout, "loadgen: interrupted; the summary covers the requests intended before the stop")
	}
	fmt.Fprintf(stdout, "loadgen summary (seed %d, profile %s):\n", plan.Seed, plan.Profile)
	for _, l := range loadgen.Summary(s, loadgen.SnapshotLatencies(s.Latency), loadgen.SnapshotLatencies(s.LatencyFromSend)) {
		fmt.Fprintln(stdout, "  "+l)
	}
	if res.RunDir != "" {
		fmt.Fprintf(stdout, "  CSV run directory: %s\n", res.RunDir)
	}
	if cerr := s.Check(); cerr != nil {
		fmt.Fprintf(stderr, "loadgen: %v\n", cerr)
		return exitRuntime
	}
	if err != nil {
		fmt.Fprintf(stderr, "loadgen: %v\n", err)
		return exitRuntime
	}
	if res.FlushErr != nil || res.FlushErrs > 0 {
		fmt.Fprintf(stderr, "loadgen: CSV flush failed (%d flush errors): %v\n", res.FlushErrs, res.FlushErr)
		return exitRuntime
	}
	if s.Shortfall() {
		return exitShortfall
	}
	return exitOK
}

func printPlan(plan loadgen.Plan, n int64, stdout io.Writer) int {
	n = min(n, plan.Count())
	fmt.Fprintf(stdout, "plan: seed %d profile %s rate %d/s, %d operations planned; first %d:\n", plan.Seed, plan.Profile, plan.Rate, plan.Count(), n)
	for i := int64(0); i < n; i++ {
		op := plan.Op(i)
		fmt.Fprintf(stdout, "%d\tat %s\ttenant %d\t%s\t%s\n", op.Index, loadgen.FormatNs(op.At), op.Tenant, op.Kind, op.SQL)
	}
	fmt.Fprintf(stdout, "digest %016x\n", plan.Digest(n))
	return exitOK
}

func build() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 12 {
			return s.Value[:12]
		}
	}
	return info.Main.Version
}
