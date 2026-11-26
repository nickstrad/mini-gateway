package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/nickstrad/mini-gateway/internal/config"
	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/enginesrc"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/shell"
)

const usage = `mini-gateway is a PostgreSQL gateway: it holds a fixed set of database connections and
lends one to a client for each transaction.

Usage:
  mini-gateway -config <file>                 run until SIGTERM or SIGINT
  mini-gateway -config <file> -check          validate the config, print its fingerprint
  mini-gateway -verifier -password <pw|->     print a SCRAM-SHA-256 verifier for the tenant table
               [-iterations N]                ("-" reads the password from the first line of stdin)

Exit status: 0 clean stop, 1 runtime failure, 2 usage or configuration error.
`

const (
	exitOK      = 0
	exitRuntime = 1
	exitUsage   = 2
	saltLen     = pgwire.ScramSaltLen
	maxPassword = 1 << 10
	revisionLen = 12
)

type cryptoRandom struct{}

func (cryptoRandom) Uint64() uint64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return binary.LittleEndian.Uint64(b[:])
}

type contextFunc func() (context.Context, context.CancelFunc)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, signalContext))
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, ctxFn contextFunc) int {
	fs := flag.NewFlagSet("mini-gateway", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	configPath := fs.String("config", "", "")
	check := fs.Bool("check", false, "")
	verifier := fs.Bool("verifier", false, "")
	password := fs.String("password", "", "")
	iterations := fs.Int("iterations", pgwire.DefaultScramIterations, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "mini-gateway: unexpected arguments %q\n%s", fs.Args(), usage)
		return exitUsage
	}
	if *verifier {
		return printVerifier(*password, *iterations, stdin, stdout, stderr)
	}
	if *configPath == "" {
		fmt.Fprintf(stderr, "mini-gateway: -config is required\n%s", usage)
		return exitUsage
	}
	f, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "mini-gateway: %s: %v\n", *configPath, err)
		return exitUsage
	}
	if *check {
		fmt.Fprintf(stdout, "config %s is valid; fingerprint %s\n", *configPath, f.Fingerprint())
		return exitOK
	}
	ctx, cancel := ctxFn()
	defer cancel()
	return serve(ctx, f, stdout, stderr)
}

func printVerifier(password string, iterations int, stdin io.Reader, stdout, stderr io.Writer) int {
	if password == "-" {
		line, err := bufio.NewReaderSize(io.LimitReader(stdin, maxPassword), maxPassword).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			fmt.Fprintf(stderr, "mini-gateway: reading the password: %v\n", err)
			return exitUsage
		}
		password = strings.TrimRight(line, "\r\n")
	}
	if password == "" {
		fmt.Fprintf(stderr, "mini-gateway: -verifier needs -password\n%s", usage)
		return exitUsage
	}
	salt := make([]byte, saltLen)
	_, _ = rand.Read(salt)
	v, err := config.DeriveVerifier(password, salt, iterations)
	if err != nil {
		fmt.Fprintf(stderr, "mini-gateway: %v\n", err)
		return exitUsage
	}
	fmt.Fprintln(stdout, v)
	return exitOK
}

func serve(ctx context.Context, f *config.File, stdout, stderr io.Writer) int {
	r := cryptoRandom{}
	pcfg, err := f.Proxy(r, pgwire.NewKeyIssuer(r))
	if err != nil {
		fmt.Fprintf(stderr, "mini-gateway: %v\n", err)
		return exitUsage
	}
	engine, err := proxy.New(pcfg)
	if err != nil {
		fmt.Fprintf(stderr, "mini-gateway: %v\n", err)
		return exitUsage
	}
	ln, err := net.Listen("tcp", f.Listen)
	if err != nil {
		fmt.Fprintf(stderr, "mini-gateway: listen: %v\n", err)
		return exitRuntime
	}
	admin, err := net.Listen("tcp", f.AdminListen)
	if err != nil {
		_ = ln.Close()
		fmt.Fprintf(stderr, "mini-gateway: admin_listen: %v\n", err)
		return exitRuntime
	}
	sh, err := shell.New(engine, shellConfig(f, engine, ln, admin))
	if err != nil {
		_ = ln.Close()
		_ = admin.Close()
		fmt.Fprintf(stderr, "mini-gateway: %v\n", err)
		return exitRuntime
	}
	fmt.Fprintf(stdout, "mini-gateway %s listening on %s, admin on %s, config fingerprint %s\n", f.Instance, ln.Addr(),
		admin.Addr(), f.Fingerprint())
	if err := sh.Run(ctx); err != nil {
		fmt.Fprintf(stderr, "mini-gateway: %v\n", err)
		return exitRuntime
	}
	return exitOK
}

func shellConfig(f *config.File, engine *proxy.Engine, ln, admin net.Listener) shell.Config {
	return shell.Config{
		Listener:        ln,
		AdminListener:   admin,
		DBAddress:       f.Database.Address,
		ClientTLS:       f.ClientTLSConfig(),
		BackendTLS:      f.BackendTLSConfig(),
		Timeouts:        f.ProxyTimeouts(),
		PublishInterval: f.PublishInterval(),
		Obs: shell.ObsConfig{Instance: f.Instance, Build: build(), ConfigFingerprint: f.Fingerprint(), Detail: f.Obs.Detail,
			CSVDir: f.Obs.CSV.Dir, CSVMaxBytes: f.Obs.CSV.MaxBytes},
		Sources: func(c *obs.Collector, sh func() proxy.ShellSnapshot) error {
			return enginesrc.Register(c, engine, sh)
		},
		HealthRules: enginesrc.HealthRules(),
	}
}

func build() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	rev, dirty := "", false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return info.Main.Version
	}
	if len(rev) > revisionLen {
		rev = rev[:revisionLen]
	}
	if dirty {
		rev += "-dirty"
	}
	return rev
}
