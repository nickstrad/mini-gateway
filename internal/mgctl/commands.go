package mgctl

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/nickstrad/mini-gateway/internal/config"
	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/jsonstats"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
)

const (
	ExitOK       = 0
	ExitRuntime  = 1
	ExitUsage    = 2
	ExitAnswered = 3

	PathHealth  = "/healthz"
	PathStats   = "/stats"
	PathMetrics = "/metrics"

	maxPassword  = 1 << 10
	MaxWatch     = 1 << 20
	statusOK     = 200
	statusFailed = 503
)

type Admin interface {
	Get(path string) (int, []byte, error)
}

type Dialer func() (io.ReadWriteCloser, error)

type Env struct {
	Admin    Admin
	Dial     Dialer
	StartTLS StartTLS
	Nonce    func() string
	Salt     func([]byte)
	Pause    func()
	Stdin    io.Reader
	Stdout   io.Writer
	Stderr   io.Writer
}

func (e Env) failf(code int, format string, args ...any) int {
	fmt.Fprintf(e.Stderr, "mgctl: "+format+"\n", args...)
	return code
}

func (e Env) get(path string) ([]byte, int, error) {
	if e.Admin == nil {
		return nil, 0, errors.New("no admin address")
	}
	code, body, err := e.Admin.Get(path)
	if err != nil {
		return nil, 0, err
	}
	return body, code, nil
}

type healthDoc struct {
	State  string `json:"state"`
	Checks []struct {
		Name   string `json:"name"`
		State  string `json:"state"`
		Reason string `json:"reason"`
	} `json:"checks"`
}

func Health(e Env) int {
	body, code, err := e.get(PathHealth)
	if err != nil {
		return e.failf(ExitRuntime, "health: %v", err)
	}
	if code != statusOK && code != statusFailed {
		return e.failf(ExitRuntime, "health: HTTP %d: %s", code, strings.TrimSpace(string(body)))
	}
	var h healthDoc
	if err := json.Unmarshal(body, &h); err != nil {
		return e.failf(ExitRuntime, "health: unreadable reply: %v", err)
	}
	fmt.Fprintf(e.Stdout, "health: %s\n", h.State)
	tw := tabwriter.NewWriter(e.Stdout, 0, 4, 2, ' ', 0)
	for _, c := range h.Checks {
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", c.Name, c.State, c.Reason)
	}
	_ = tw.Flush()
	if h.State != obs.StateOK.String() {
		return ExitAnswered
	}
	return ExitOK
}

func (e Env) snapshot() (obs.InstanceSnapshot, error) {
	body, code, err := e.get(PathStats)
	if err != nil {
		return obs.InstanceSnapshot{}, err
	}
	if code != statusOK {
		return obs.InstanceSnapshot{}, fmt.Errorf("HTTP %d: %s", code, strings.TrimSpace(string(body)))
	}
	return jsonstats.Decode(body)
}

type StatsOptions struct {
	Component string
	Match     string
	Watch     bool
	Count     int
}

func labelText(l obs.Labels) string {
	var parts []string
	for _, kv := range [][2]string{{"tenant", l.Tenant}, {"outcome", l.Outcome}, {"reason", l.Reason}, {"mode", l.Mode}} {
		if kv[1] != "" {
			parts = append(parts, kv[0]+"="+kv[1])
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ",")
}

func (o StatsOptions) keep(p obs.Point) bool {
	if o.Component != "" && p.Component != o.Component {
		return false
	}
	return o.Match == "" || strings.Contains(p.Component+"."+p.Metric, o.Match)
}

func header(w io.Writer, s obs.InstanceSnapshot) {
	fmt.Fprintf(w, "instance %s  build %s  config %s  seq %d\n", s.Instance, s.Build, s.ConfigFingerprint, s.Seq)
}

func printStats(w io.Writer, s obs.InstanceSnapshot, prev *obs.InstanceSnapshot, o StatsOptions) int {
	header(w, s)
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	n := 0
	if prev == nil {
		fmt.Fprintln(tw, "COMPONENT\tMETRIC\tLABELS\tKIND\tVALUE")
	} else {
		fmt.Fprintln(tw, "COMPONENT\tMETRIC\tLABELS\tKIND\tVALUE\tCHANGE")
	}
	for _, p := range s.Points {
		if !o.keep(p) {
			continue
		}
		n++
		line := p.Component + "\t" + p.Metric + "\t" + labelText(p.Labels) + "\t" + p.Kind.String() + "\t" + strconv.FormatInt(p.Value, 10)
		if prev != nil {
			was := prev.Value(p.Component, p.Metric, p.Labels)
			line += "\t" + signed(p.Value-was)
		}
		fmt.Fprintln(tw, line)
	}
	_ = tw.Flush()
	return n
}

func signed(v int64) string {
	if v > 0 {
		return "+" + strconv.FormatInt(v, 10)
	}
	return strconv.FormatInt(v, 10)
}

func Stats(e Env, o StatsOptions) int {
	if o.Count < 0 || o.Count > MaxWatch {
		return e.failf(ExitUsage, "stats: -count %d out of range", o.Count)
	}
	samples := 1
	if o.Watch {
		samples = o.Count
		if samples == 0 {
			samples = MaxWatch
		}
	}
	var prev *obs.InstanceSnapshot
	for i := 0; i < samples; i++ {
		if i > 0 && e.Pause != nil {
			e.Pause()
		}
		s, err := e.snapshot()
		if err != nil {
			return e.failf(ExitRuntime, "stats: %v", err)
		}
		if i > 0 {
			fmt.Fprintln(e.Stdout)
		}
		if printStats(e.Stdout, s, prev, o) == 0 && (o.Component != "" || o.Match != "") {
			fmt.Fprintln(e.Stdout, "(no metric matches)")
		}
		cur := s
		prev = &cur
	}
	return ExitOK
}

func Metrics(e Env) int {
	body, code, err := e.get(PathMetrics)
	if err != nil {
		return e.failf(ExitRuntime, "metrics: %v", err)
	}
	if code != statusOK {
		return e.failf(ExitRuntime, "metrics: HTTP %d: %s", code, strings.TrimSpace(string(body)))
	}
	_, _ = e.Stdout.Write(body)
	return ExitOK
}

func Tenants(e Env, configPath string) int {
	s, err := e.snapshot()
	if err != nil {
		return e.failf(ExitRuntime, "tenants: %v", err)
	}
	var configured []string
	if configPath != "" {
		f, err := config.Load(configPath)
		if err != nil {
			return e.failf(ExitUsage, "%s: %v", configPath, err)
		}
		for _, t := range f.Tenants {
			configured = append(configured, t.User)
		}
		slices.Sort(configured)
	}
	header(e.Stdout, s)
	seen := s.Tenants()
	if len(seen) == 0 {
		fmt.Fprintln(e.Stdout, "no per-tenant numbers in this gateway's stats: no component it runs exports a tenant label yet")
	} else {
		tw := tabwriter.NewWriter(e.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "TENANT\tCOMPONENT\tMETRIC\tLABELS\tVALUE")
		for _, ten := range seen {
			for _, p := range s.Points {
				if p.Labels.Tenant == ten {
					l := p.Labels
					l.Tenant = ""
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\n", ten, p.Component, p.Metric, labelText(l), p.Value)
				}
			}
		}
		_ = tw.Flush()
	}
	if configPath != "" {
		fmt.Fprintf(e.Stdout, "configured tenants (%s): %d\n", configPath, len(configured))
		for _, u := range configured {
			mark := "no numbers yet"
			if slices.Contains(seen, u) {
				mark = "has numbers"
			}
			fmt.Fprintf(e.Stdout, "  %s\t%s\n", u, mark)
		}
	}
	return ExitOK
}

var capacityGauges = []struct{ metric, key string }{
	{"database_capacity", "pool.connections"},
	{"queue_capacity", "queue.capacity"},
	{"relay_buffer_capacity", "limits.reply_buffer"},
	{"shell_mailbox_capacity", "(shell mailbox, fixed)"},
}

func ConfigShow(e Env, configPath string) int {
	s, err := e.snapshot()
	if err != nil {
		return e.failf(ExitRuntime, "config show: %v", err)
	}
	fmt.Fprintf(e.Stdout, "running instance %s\nbuild %s\nconfig fingerprint %s\n", s.Instance, s.Build, s.ConfigFingerprint)
	fmt.Fprintln(e.Stdout, "effective settings seen in its stats:")
	for _, c := range capacityGauges {
		if p, ok := s.Point(obs.ComponentEngine, c.metric, obs.Labels{}); ok {
			fmt.Fprintf(e.Stdout, "  %-24s %d  (%s)\n", c.key, p.Value, "engine."+c.metric)
		}
	}
	if configPath == "" {
		fmt.Fprintln(e.Stdout, "(give -config <file> to print that file's effective settings and compare its fingerprint)")
		return ExitOK
	}
	f, err := config.Load(configPath)
	if err != nil {
		return e.failf(ExitUsage, "%s: %v", configPath, err)
	}
	shown := *f
	if shown.Database.Password != "" {
		shown.Database.Password = "(set)"
	}
	b, err := json.MarshalIndent(shown, "", "  ")
	if err != nil {
		return e.failf(ExitRuntime, "config show: %v", err)
	}
	match := "no: the running instance was started from different settings"
	if f.Fingerprint() == s.ConfigFingerprint {
		match = "yes"
	}
	fmt.Fprintf(e.Stdout, "file %s: fingerprint %s; matches the running instance: %s\n%s\n", configPath, f.Fingerprint(), match, b)
	return ExitOK
}

func ConfigCheck(e Env, path string) int {
	f, err := config.Load(path)
	if err != nil {
		fmt.Fprintf(e.Stderr, "mgctl: %s: %v\n", path, err)
		return ExitUsage
	}
	fmt.Fprintf(e.Stdout, "config %s is valid; fingerprint %s\n", path, f.Fingerprint())
	return ExitOK
}

func Verifier(e Env, password string, iterations int) int {
	if password == "-" {
		line, err := bufio.NewReaderSize(io.LimitReader(e.Stdin, maxPassword), maxPassword).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return e.failf(ExitUsage, "verifier: reading the password: %v", err)
		}
		password = strings.TrimRight(line, "\r\n")
	}
	if password == "" {
		return e.failf(ExitUsage, "verifier: needs -password <pw|-> (\"-\" reads the first line of stdin)")
	}
	salt := make([]byte, pgwire.ScramSaltLen)
	if e.Salt == nil {
		return e.failf(ExitRuntime, "verifier: no random source")
	}
	e.Salt(salt)
	v, err := config.DeriveVerifier(password, salt, iterations)
	if err != nil {
		return e.failf(ExitUsage, "verifier: %s", hide(err.Error(), password))
	}
	fmt.Fprintln(e.Stdout, v)
	return ExitOK
}

func Query(e Env, c Credentials, sql string) int {
	if e.Dial == nil {
		return e.failf(ExitUsage, "query: no gateway address")
	}
	conn, err := e.Dial()
	if err != nil {
		return e.failf(ExitRuntime, "query: %v", err)
	}
	defer conn.Close()
	nonce := ""
	if e.Nonce != nil {
		nonce = e.Nonce()
	}
	sess, err := Login(conn, c, e.StartTLS, nonce)
	if err != nil {
		var se *ServerError
		if errors.As(err, &se) {
			fmt.Fprintf(e.Stdout, "%s %s: %s\n%s\n", se.Severity, se.Code, se.Message, ContractLine(se))
			return ExitAnswered
		}
		return e.failf(ExitRuntime, "query: %s", hide(err.Error(), c.Password))
	}
	r, err := sess.Query(sql)
	if err != nil {
		return e.failf(ExitRuntime, "query: %v", err)
	}
	_ = sess.Terminate()
	return PrintReply(e.Stdout, r)
}

func hide(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "(password)")
}

func PrintReply(w io.Writer, r Reply) int {
	if r.Err != nil {
		fmt.Fprintf(w, "%s %s: %s\n%s\n", r.Err.Severity, r.Err.Code, r.Err.Message, ContractLine(r.Err))
		return ExitAnswered
	}
	if len(r.Columns) > 0 {
		tw := tabwriter.NewWriter(w, 0, 4, 1, ' ', 0)
		fmt.Fprintln(tw, strings.Join(r.Columns, "\t|\t"))
		for _, row := range r.Rows {
			fmt.Fprintln(tw, strings.Join(row, "\t|\t"))
		}
		_ = tw.Flush()
		fmt.Fprintf(w, "(%d rows)\n", len(r.Rows))
		if r.Truncated > 0 {
			fmt.Fprintf(w, "(%d more rows not shown; at most %d are kept)\n", r.Truncated, MaxRows)
		}
	}
	fmt.Fprintln(w, r.Tag)
	return ExitOK
}

func Transcript(w io.Writer, args []string, stdout, stderr string, code int) {
	fmt.Fprintf(w, "$ mgctl %s\n", strings.Join(args, " "))
	if stdout != "" {
		fmt.Fprint(w, indent(stdout))
	}
	if stderr != "" {
		fmt.Fprint(w, indent("stderr: "+stderr))
	}
	fmt.Fprintf(w, "[exit %d]\n\n", code)
}

func indent(s string) string {
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return "  " + strings.ReplaceAll(strings.TrimSuffix(s, "\n"), "\n", "\n  ") + "\n"
}
