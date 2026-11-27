package live

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/nickstrad/mini-gateway/internal/loadgen"
	"github.com/nickstrad/mini-gateway/internal/mgctl"
	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/csvflush"
	"github.com/nickstrad/mini-gateway/internal/testclient"
)

const (
	DefaultDrain       = 5 * time.Second
	DefaultStatusEvery = time.Second
	DefaultDialTimeout = 10 * time.Second
	eventsPerConn      = 4
	nonceBytes         = 18
	instanceName       = "loadgen"
)

type Options struct {
	Addr        string
	Database    string
	UserPrefix  string
	Password    string
	TLS         *tls.Config
	Plan        loadgen.Plan
	Tracker     loadgen.TrackerConfig
	Drain       time.Duration
	StatusEvery time.Duration
	DialTimeout time.Duration
	CSVDir      string
	Build       string
	Status      io.Writer
}

type Result struct {
	Snapshot  loadgen.Snapshot
	RunDir    string
	Stopped   bool
	Elapsed   time.Duration
	FlushErrs int64
}

type evKind uint8

const (
	evSent evKind = iota
	evReply
	evDown
)

type event struct {
	kind evKind
	conn int
	at   int64
	res  loadgen.Result
}

type conn struct {
	idx  int
	nc   net.Conn
	sess *mgctl.Session
	jobs chan string
}

type runner struct {
	o      Options
	tr     *loadgen.Tracker
	start  time.Time
	events chan event
	quit   chan struct{}
	conns  []*conn
	wg     sync.WaitGroup
	col    *obs.Collector
	csv    *csvflush.Writer
}

var ErrSetup = errors.New("loadgen: setup failed")

func (o *Options) defaults() {
	if o.Drain == 0 {
		o.Drain = DefaultDrain
	}
	if o.StatusEvery == 0 {
		o.StatusEvery = DefaultStatusEvery
	}
	if o.DialTimeout == 0 {
		o.DialTimeout = DefaultDialTimeout
	}
	if o.Status == nil {
		o.Status = io.Discard
	}
}

func Run(ctx context.Context, o Options) (Result, error) {
	o.defaults()
	tr, err := loadgen.NewTracker(o.Plan, o.Tracker)
	if err != nil {
		return Result{}, err
	}
	r := &runner{o: o, tr: tr, quit: make(chan struct{})}
	cfg := tr.Config()
	r.events = make(chan event, cfg.Conns*cfg.MaxOutstanding*eventsPerConn)
	if err := r.observe(); err != nil {
		return Result{}, err
	}
	r.connect(ctx)
	if tr.Snapshot().ConnsOpen == 0 {
		r.publish()
		res := r.finish()
		return res, fmt.Errorf("%w: no connection to %s could log in", ErrSetup, o.Addr)
	}
	r.start = time.Now()
	for _, c := range r.conns {
		if c != nil {
			r.spawn(c)
		}
	}
	stopped := r.loop(ctx)
	res := r.finish()
	res.Stopped = stopped
	return res, nil
}

func (r *runner) now() int64 { return int64(time.Since(r.start)) }

func (r *runner) observe() error {
	r.col = obs.NewCollector(obs.Config{Instance: instanceName, Build: r.o.Build, ConfigFingerprint: r.o.Plan.Fingerprint(), Detail: true})
	if err := r.col.Register(loadgen.Source(r.tr.Snapshot)); err != nil {
		return err
	}
	if r.o.CSVDir == "" {
		return nil
	}
	p := r.o.Plan
	scenario := "load-" + p.Profile.String()
	runID := scenario + "/" + strconv.FormatInt(p.Seed, 10) + "/" + strconv.FormatInt(time.Now().UnixNano(), 10)
	base := time.Now()
	w, err := csvflush.New(csvflush.Config{Dir: r.o.CSVDir, Async: true, Clock: func() int64 { return int64(time.Since(base)) },
		Run: csvflush.RunInfo{RunID: runID, Scenario: scenario, Tier: csvflush.TierReal, Seed: p.Seed,
			Arm: "rate-" + strconv.FormatInt(p.Rate, 10), Instances: 1, Build: r.o.Build, ConfigFingerprint: p.Fingerprint()}})
	if err != nil {
		return err
	}
	r.csv = w
	return r.col.Attach(w)
}

func (r *runner) connect(ctx context.Context) {
	cfg := r.tr.Config()
	r.conns = make([]*conn, cfg.Conns)
	var mu sync.Mutex
	var wg sync.WaitGroup
	results := make([]*conn, cfg.Conns)
	for i := range r.conns {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, err := r.dial(ctx, i)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				fmt.Fprintf(r.o.Status, "loadgen: connection %d (%s): %v\n", i, r.user(i), err)
				return
			}
			results[i] = c
		}(i)
	}
	wg.Wait()
	for i, c := range results {
		if c == nil {
			r.tr.ConnectFailed(i)
			continue
		}
		r.conns[i] = c
		r.tr.ConnUp(i)
	}
}

func (r *runner) user(i int) string {
	return loadgen.TenantUser(r.o.UserPrefix, r.tr.ConnTenant(i))
}

func (r *runner) dial(ctx context.Context, i int) (*conn, error) {
	d := net.Dialer{Timeout: r.o.DialTimeout}
	nc, err := d.DialContext(ctx, "tcp", r.o.Addr)
	if err != nil {
		return nil, err
	}
	_ = nc.SetDeadline(time.Now().Add(r.o.DialTimeout))
	var start mgctl.StartTLS
	if r.o.TLS != nil {
		start = func(rw io.ReadWriter) (io.ReadWriter, error) {
			tc := tls.Client(nc, r.o.TLS)
			if err := tc.HandshakeContext(ctx); err != nil {
				return nil, err
			}
			nc = tc
			return tc, nil
		}
	}
	sess, err := mgctl.Login(nc, mgctl.Credentials{User: r.user(i), Password: r.o.Password, Database: r.o.Database}, start, nonce())
	if err != nil {
		_ = nc.Close()
		return nil, err
	}
	_ = nc.SetDeadline(time.Time{})
	return &conn{idx: i, nc: nc, sess: sess, jobs: make(chan string, r.tr.Config().MaxOutstanding)}, nil
}

func nonce() string {
	b := make([]byte, nonceBytes)
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

func (r *runner) post(ev event) {
	select {
	case r.events <- ev:
	case <-r.quit:
	}
}

func (r *runner) spawn(c *conn) {
	r.wg.Add(2)
	go func() {
		defer r.wg.Done()
		r.write(c)
	}()
	go func() {
		defer r.wg.Done()
		r.read(c)
	}()
}

func (r *runner) write(c *conn) {
	for {
		select {
		case sql := <-c.jobs:
			r.post(event{kind: evSent, conn: c.idx, at: r.now()})
			if err := c.sess.Write(testclient.EncodeQuery(nil, sql)); err != nil {
				r.post(event{kind: evDown, conn: c.idx})
				return
			}
		case <-r.quit:
			return
		}
	}
}

func (r *runner) read(c *conn) {
	for {
		reply, err := c.sess.Receive()
		if err != nil {
			r.post(event{kind: evDown, conn: c.idx})
			return
		}
		r.post(event{kind: evReply, conn: c.idx, at: r.now(), res: classify(reply)})
		if reply.Err != nil && (reply.Err.Severity == "FATAL" || reply.Err.Severity == "PANIC") {
			r.post(event{kind: evDown, conn: c.idx})
			return
		}
	}
}

func classify(reply mgctl.Reply) loadgen.Result {
	if reply.Err == nil {
		return loadgen.Result{Outcome: loadgen.OutcomeOK}
	}
	if mgctl.ProxyMade(reply.Err.Code, reply.Err.Message) {
		return loadgen.Result{Outcome: loadgen.OutcomeRefused, Code: reply.Err.Code}
	}
	return loadgen.Result{Outcome: loadgen.OutcomeDBError, Code: reply.Err.Code}
}

func (r *runner) apply(ev event) {
	switch ev.kind {
	case evSent:
		r.tr.Sent(ev.conn, ev.at)
	case evReply:
		r.tr.Completed(ev.conn, ev.at, ev.res)
	case evDown:
		r.tr.ConnDown(ev.conn)
	}
}

func (r *runner) dispatch(out []loadgen.Dispatch) []loadgen.Dispatch {
	out = r.tr.Tick(r.now(), out[:0])
	for _, d := range out {
		select {
		case r.conns[d.Conn].jobs <- d.Op.SQL:
		default:
			r.tr.ConnDown(d.Conn)
		}
	}
	return out
}

func (r *runner) loop(ctx context.Context) bool {
	timer := time.NewTimer(0)
	defer timer.Stop()
	ticker := time.NewTicker(r.o.StatusEvery)
	defer ticker.Stop()
	var out []loadgen.Dispatch
	var drainUntil time.Time
	stopped := false
	done := ctx.Done()
	for {
		select {
		case ev := <-r.events:
			r.apply(ev)
		case <-timer.C:
			out = r.dispatch(out)
			if at, ok := r.tr.NextAt(); ok {
				timer.Reset(max(time.Duration(at-r.now()), 0))
			}
		case <-ticker.C:
			r.status()
		case <-done:
			r.tr.Stop()
			stopped, done = true, nil
		}
		if !r.tr.Done() {
			continue
		}
		if drainUntil.IsZero() {
			drainUntil = time.Now().Add(r.o.Drain)
		}
		s := r.tr.Snapshot()
		if s.Queued+s.InFlight == 0 || !time.Now().Before(drainUntil) {
			return stopped
		}
		if wait := time.Until(drainUntil); wait > 0 {
			timer.Reset(wait)
		}
	}
}

func (r *runner) status() {
	s := r.tr.Snapshot()
	fmt.Fprintln(r.o.Status, loadgen.StatusLine(r.now(), s, loadgen.SnapshotLatencies(s.Latency)))
	r.publish()
}

func (r *runner) publish() {
	if _, err := r.col.Publish(); err != nil {
		fmt.Fprintf(r.o.Status, "loadgen: publish: %v\n", err)
	}
}

func (r *runner) drainEvents() {
	for {
		select {
		case ev := <-r.events:
			r.apply(ev)
		default:
			return
		}
	}
}

func (r *runner) finish() Result {
	r.drainEvents()
	r.tr.Finish()
	close(r.quit)
	for _, c := range r.conns {
		if c != nil {
			_ = c.nc.Close()
		}
	}
	r.wg.Wait()
	r.publish()
	res := Result{Snapshot: r.tr.Snapshot()}
	if !r.start.IsZero() {
		res.Elapsed = time.Since(r.start)
	}
	if r.csv != nil {
		res.RunDir = r.csv.RunDir()
		if err := r.csv.Close(); err != nil {
			fmt.Fprintf(r.o.Status, "loadgen: CSV flush: %v\n", err)
		}
		res.FlushErrs = r.csv.FlushStats().FlushErrors
	}
	return res
}
