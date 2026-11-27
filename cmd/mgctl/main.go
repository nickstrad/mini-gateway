package main

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/nickstrad/mini-gateway/internal/mgctl"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
)

const usage = `mgctl asks a running mini-gateway how it is. It only reads: no subcommand changes the gateway.

Usage:
  mgctl [-admin host:port] health                   the health state and each check
  mgctl [-admin host:port] stats [-watch] [-count N] [-interval D] [-component C] [-match S]
  mgctl [-admin host:port] metrics                  the Prometheus text the gateway serves
  mgctl [-admin host:port] tenants [-config file]   per-tenant numbers from the stats
  mgctl [-admin host:port] config show [-config file]
  mgctl config check <file>                         validate a config file offline (the gateway's own validation)
  mgctl verifier -password <pw|-> [-iterations N]   a SCRAM-SHA-256 verifier for the tenant table
  mgctl [-addr host:port] query -user U -password <pw|-> [-database D] [-tls off|require|verify-full]
        [-ca-file F] [-server-name N] "<statement>"   run one statement through the proxy as a tenant

Global flags: -admin (default $MGCTL_ADMIN or 127.0.0.1:6433), -addr (default $MGCTL_ADDR or
127.0.0.1:6432), -timeout (default 10s). MGCTL_TRANSCRIPT=<file> appends a readable log of
each command and its output to the file.

Exit status: 0 success, 1 the gateway could not be reached or failed, 2 usage or configuration
error, 3 the gateway answered but not well (health not ok, an error reply to a query).
`

const (
	defaultAdmin    = "127.0.0.1:6433"
	defaultAddr     = "127.0.0.1:6432"
	defaultTimeout  = 10 * time.Second
	defaultInterval = time.Second
	maxBody         = 64 << 20
	nonceBytes      = 18
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	path := os.Getenv("MGCTL_TRANSCRIPT")
	if path == "" {
		return dispatch(args, stdin, stdout, stderr)
	}
	var out, errb bytes.Buffer
	code := dispatch(args, stdin, io.MultiWriter(stdout, &out), io.MultiWriter(stderr, &errb))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(stderr, "mgctl: MGCTL_TRANSCRIPT: %v\n", err)
		return code
	}
	defer f.Close()
	mgctl.Transcript(f, redact(args), out.String(), errb.String(), code)
	return code
}

func redact(args []string) []string {
	out := append([]string(nil), args...)
	for i := range out {
		if (out[i] == "-password" || out[i] == "--password") && i+1 < len(out) && out[i+1] != "-" {
			out[i+1] = "(password)"
		}
		if strings.HasPrefix(out[i], "-password=") || strings.HasPrefix(out[i], "--password=") {
			out[i] = out[i][:strings.Index(out[i], "=")+1] + "(password)"
		}
	}
	return out
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func dispatch(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("mgctl", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	admin := fs.String("admin", envOr("MGCTL_ADMIN", defaultAdmin), "")
	addr := fs.String("addr", envOr("MGCTL_ADDR", defaultAddr), "")
	timeout := fs.Duration("timeout", defaultTimeout, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return mgctl.ExitOK
		}
		return mgctl.ExitUsage
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprint(stderr, usage)
		return mgctl.ExitUsage
	}
	env := mgctl.Env{Admin: httpAdmin{addr: *admin, client: &http.Client{Timeout: *timeout}}, Stdin: stdin, Stdout: stdout,
		Stderr: stderr, Nonce: nonce, Salt: func(b []byte) { _, _ = rand.Read(b) }}
	switch rest[0] {
	case "health":
		return noArgs(rest, stderr, func() int { return mgctl.Health(env) })
	case "metrics":
		return noArgs(rest, stderr, func() int { return mgctl.Metrics(env) })
	case "stats":
		return stats(env, rest[1:])
	case "tenants":
		return withConfig(env, "tenants", rest[1:], mgctl.Tenants)
	case "config":
		return configCmd(env, rest[1:])
	case "verifier":
		return verifier(env, rest[1:])
	case "query":
		return query(env, *addr, *timeout, rest[1:])
	case "help", "-h", "-help", "--help":
		fmt.Fprint(stdout, usage)
		return mgctl.ExitOK
	}
	fmt.Fprintf(stderr, "mgctl: unknown command %q\n%s", rest[0], usage)
	return mgctl.ExitUsage
}

func noArgs(rest []string, stderr io.Writer, f func() int) int {
	if len(rest) > 1 {
		fmt.Fprintf(stderr, "mgctl %s: unexpected arguments %q\n", rest[0], rest[1:])
		return mgctl.ExitUsage
	}
	return f()
}

func subFlags(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("mgctl "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	return fs
}

func parse(fs *flag.FlagSet, args []string, want int) ([]string, int) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, mgctl.ExitOK
		}
		return nil, mgctl.ExitUsage
	}
	if want >= 0 && fs.NArg() != want {
		fmt.Fprintf(fs.Output(), "%s: want %d arguments, got %q\n", fs.Name(), want, fs.Args())
		return nil, mgctl.ExitUsage
	}
	return fs.Args(), -1
}

func stats(env mgctl.Env, args []string) int {
	fs := subFlags("stats", env.Stderr)
	var o mgctl.StatsOptions
	fs.BoolVar(&o.Watch, "watch", false, "")
	fs.IntVar(&o.Count, "count", 0, "")
	fs.StringVar(&o.Component, "component", "", "")
	fs.StringVar(&o.Match, "match", "", "")
	interval := fs.Duration("interval", defaultInterval, "")
	if _, code := parse(fs, args, 0); code >= 0 {
		return code
	}
	if *interval <= 0 {
		fmt.Fprintln(env.Stderr, "mgctl stats: -interval must be above 0")
		return mgctl.ExitUsage
	}
	env.Pause = func() { time.Sleep(*interval) }
	return mgctl.Stats(env, o)
}

func withConfig(env mgctl.Env, name string, args []string, f func(mgctl.Env, string) int) int {
	fs := subFlags(name, env.Stderr)
	cfg := fs.String("config", "", "")
	if _, code := parse(fs, args, 0); code >= 0 {
		return code
	}
	return f(env, *cfg)
}

func configCmd(env mgctl.Env, args []string) int {
	if len(args) == 0 {
		fmt.Fprintf(env.Stderr, "mgctl config: want show or check\n%s", usage)
		return mgctl.ExitUsage
	}
	switch args[0] {
	case "show":
		return withConfig(env, "config show", args[1:], mgctl.ConfigShow)
	case "check":
		fs := subFlags("config check", env.Stderr)
		rest, code := parse(fs, args[1:], 1)
		if code >= 0 {
			return code
		}
		return mgctl.ConfigCheck(env, rest[0])
	}
	fmt.Fprintf(env.Stderr, "mgctl config: unknown subcommand %q\n", args[0])
	return mgctl.ExitUsage
}

func verifier(env mgctl.Env, args []string) int {
	fs := subFlags("verifier", env.Stderr)
	password := fs.String("password", "", "")
	iterations := fs.Int("iterations", pgwire.DefaultScramIterations, "")
	if _, code := parse(fs, args, 0); code >= 0 {
		return code
	}
	return mgctl.Verifier(env, *password, *iterations)
}

func query(env mgctl.Env, addr string, timeout time.Duration, args []string) int {
	fs := subFlags("query", env.Stderr)
	var c mgctl.Credentials
	fs.StringVar(&c.User, "user", "", "")
	fs.StringVar(&c.Password, "password", "", "")
	fs.StringVar(&c.Database, "database", "app", "")
	mode := fs.String("tls", "off", "")
	caFile := fs.String("ca-file", "", "")
	serverName := fs.String("server-name", "", "")
	rest, code := parse(fs, args, 1)
	if code >= 0 {
		return code
	}
	if c.User == "" {
		fmt.Fprintln(env.Stderr, "mgctl query: -user is required")
		return mgctl.ExitUsage
	}
	if c.Password == "-" {
		pw, err := mgctl.ReadPasswordLine(env.Stdin)
		if err != nil {
			fmt.Fprintf(env.Stderr, "mgctl query: reading the password: %v\n", err)
			return mgctl.ExitUsage
		}
		c.Password = pw
	}
	start, err := tlsUpgrade(*mode, *caFile, *serverName, addr)
	if err != nil {
		fmt.Fprintf(env.Stderr, "mgctl query: %v\n", err)
		return mgctl.ExitUsage
	}
	env.StartTLS = start
	env.Dial = func() (io.ReadWriteCloser, error) {
		conn, err := net.DialTimeout("tcp", addr, timeout)
		if err != nil {
			return nil, err
		}
		_ = conn.SetDeadline(time.Now().Add(timeout))
		return conn, nil
	}
	return mgctl.Query(env, c, rest[0])
}

func tlsUpgrade(mode, caFile, serverName, addr string) (mgctl.StartTLS, error) {
	cfg, err := mgctl.ClientTLS(mode, caFile, serverName, addr)
	if err != nil || cfg == nil {
		return nil, err
	}
	return func(rw io.ReadWriter) (io.ReadWriter, error) {
		conn, ok := rw.(net.Conn)
		if !ok {
			return nil, errors.New("TLS needs a network connection")
		}
		tc := tls.Client(conn, cfg)
		if err := tc.Handshake(); err != nil {
			return nil, err
		}
		return tc, nil
	}, nil
}

func nonce() string {
	b := make([]byte, nonceBytes)
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

type httpAdmin struct {
	addr   string
	client *http.Client
}

func (a httpAdmin) Get(path string) (int, []byte, error) {
	resp, err := a.client.Get("http://" + a.addr + path)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	return resp.StatusCode, body, err
}
