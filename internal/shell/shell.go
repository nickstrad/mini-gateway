package shell

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/csvflush"
	"github.com/nickstrad/mini-gateway/internal/obs/jsonstats"
	"github.com/nickstrad/mini-gateway/internal/obs/promtext"
	"github.com/nickstrad/mini-gateway/internal/proxy"
)

const (
	mailboxCap      = 4096
	maxMailboxCap   = 1 << 16
	bellCap         = 1
	writeQueueSlack = 2 << 20
	writeQueueCap   = proxy.MaxReplyBufferCap + writeQueueSlack
	maxQueueItems   = 1 << 16
	closeSlack      = 1
	maxClientConns  = proxy.MaxClients
	maxDBConns      = proxy.MaxConnections
	maxLiveConns    = maxClientConns + maxDBConns
	maxDeferred     = maxDBConns

	minAcceptBackoff     = 5 * time.Millisecond
	maxAcceptBackoff     = time.Second
	adminHeaderTimeout   = 5 * time.Second
	adminWriteTimeout    = 5 * time.Second
	adminIdleTimeout     = 30 * time.Second
	adminShutdownTimeout = 2 * time.Second
	adminMaxHeaderBytes  = 8 << 10
)

var (
	ErrListenerDown = errors.New("shell: the client listener stopped")
	ErrConfig       = errors.New("shell: bad configuration")
	ErrStarted      = errors.New("shell: already started")
)

type Core interface {
	Start(now proxy.Instant, out []proxy.Action) []proxy.Action
	Step(now proxy.Instant, ev proxy.Event, out []proxy.Action) []proxy.Action
}

type DialFunc func(ctx context.Context, address string) (net.Conn, error)

type ObsConfig struct {
	Instance          string
	Build             string
	ConfigFingerprint string
	Detail            bool
	CSVDir            string
	CSVMaxBytes       int64
}

type Config struct {
	Listener        net.Listener
	AdminListener   net.Listener
	DBAddress       string
	Dial            DialFunc
	ClientTLS       *tls.Config
	BackendTLS      *tls.Config
	Timeouts        proxy.Timeouts
	PublishInterval time.Duration
	MailboxCap      int
	Obs             ObsConfig
	Sources         func(c *obs.Collector, shell func() proxy.ShellSnapshot) error
	HealthRules     []obs.Rule
}

type envelope struct {
	ev   proxy.Event
	conn *conn
}

type published struct {
	seq     int64
	health  obs.Health
	stats   []byte
	metrics []byte
}

type Shell struct {
	cfg     Config
	core    Core
	base    time.Time
	mailbox chan envelope
	stop    chan struct{}
	lost    chan struct{}

	collector *obs.Collector
	health    obs.HealthTable
	json      *jsonstats.Adapter
	prom      *promtext.Adapter
	csv       *csvflush.Writer
	pub       atomic.Pointer[published]
	admin     *http.Server

	wg        sync.WaitGroup
	mu        sync.Mutex
	live      map[*conn]struct{}
	closing   bool
	started   bool
	closeOnce sync.Once
	closeErr  error

	clients  map[proxy.ConnID]*conn
	dbs      map[proxy.ConnID]*conn
	out      []proxy.Action
	deferred []proxy.Event
	wake     *time.Timer
	wakeSet  bool

	listenerUp atomic.Bool
	nextClient atomic.Int64
	stats      counters
}

func New(core Core, cfg Config) (*Shell, error) {
	if core == nil || cfg.Listener == nil {
		return nil, fmt.Errorf("%w: a core and a client listener are required", ErrConfig)
	}
	if cfg.MailboxCap == 0 {
		cfg.MailboxCap = mailboxCap
	}
	if cfg.MailboxCap < 1 || cfg.MailboxCap > maxMailboxCap {
		return nil, fmt.Errorf("%w: mailbox capacity %d is not 1 to %d", ErrConfig, cfg.MailboxCap, maxMailboxCap)
	}
	if cfg.PublishInterval <= 0 {
		return nil, fmt.Errorf("%w: the publish interval must be above 0", ErrConfig)
	}
	if cfg.Dial == nil {
		cfg.Dial = dialTCP
	}
	cfg.Timeouts = proxy.Config{Timeouts: cfg.Timeouts}.WithDefaults().Timeouts
	s := &Shell{cfg: cfg, core: core, base: time.Now(),
		mailbox: make(chan envelope, cfg.MailboxCap), stop: make(chan struct{}, bellCap), lost: make(chan struct{}, bellCap),
		live: make(map[*conn]struct{}), clients: make(map[proxy.ConnID]*conn), dbs: make(map[proxy.ConnID]*conn)}
	if err := s.wireObs(); err != nil {
		return nil, err
	}
	return s, nil
}

func dialTCP(ctx context.Context, address string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "tcp", address)
}

func (s *Shell) Addr() net.Addr { return s.cfg.Listener.Addr() }

func (s *Shell) AdminAddr() net.Addr {
	if s.cfg.AdminListener == nil {
		return nil
	}
	return s.cfg.AdminListener.Addr()
}

func (s *Shell) now() proxy.Instant { return proxy.Instant(time.Since(s.base)) }

func (s *Shell) Start() error {
	s.mu.Lock()
	if s.started || s.closing {
		s.mu.Unlock()
		return ErrStarted
	}
	s.started = true
	s.mu.Unlock()
	s.wake = time.NewTimer(time.Hour)
	s.wake.Stop()
	s.listenerUp.Store(true)
	s.spawn(s.engineLoop, nil)
	s.spawn(s.acceptLoop, nil)
	if s.cfg.AdminListener != nil {
		s.startAdmin()
	}
	return nil
}

func (s *Shell) Run(ctx context.Context) error {
	if err := s.Start(); err != nil {
		return err
	}
	var lost bool
	select {
	case <-ctx.Done():
	case <-s.lost:
		lost = true
	}
	err := s.Close()
	if lost {
		return errors.Join(ErrListenerDown, err)
	}
	return err
}

func (s *Shell) ListenerLost() <-chan struct{} { return s.lost }

func (s *Shell) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closing = true
		started := s.started
		live := make([]*conn, 0, len(s.live))
		for c := range s.live {
			live = append(live, c)
		}
		s.mu.Unlock()
		close(s.stop)
		_ = s.cfg.Listener.Close()
		s.stopAdmin()
		for _, c := range live {
			c.abort()
		}
		s.wg.Wait()
		if !started {
			s.closeErr = s.closeObs()
		}
	})
	return s.closeErr
}

func (s *Shell) spawn(fn func(), g *gauge) {
	s.wg.Add(1)
	s.stats.goroutines.add(1)
	if g != nil {
		g.add(1)
	}
	go func() {
		defer s.wg.Done()
		defer s.stats.goroutines.add(-1)
		if g != nil {
			defer g.add(-1)
		}
		fn()
	}()
}

func (s *Shell) register(c *conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return false
	}
	if c.side == sideClient && len(s.live) >= maxClientConns {
		return false
	}
	if len(s.live) >= maxLiveConns {
		return false
	}
	s.live[c] = struct{}{}
	return true
}

func (s *Shell) unregister(c *conn) {
	s.mu.Lock()
	delete(s.live, c)
	s.mu.Unlock()
}

func (s *Shell) stopping() bool {
	select {
	case <-s.stop:
		return true
	default:
		return false
	}
}

func (s *Shell) post(env envelope) bool {
	select {
	case s.mailbox <- env:
	default:
		s.stats.mailboxFull.Add(1)
		select {
		case s.mailbox <- env:
		case <-s.stop:
			return false
		}
	}
	s.stats.mailbox.peakAt(int64(len(s.mailbox)))
	return true
}

func (s *Shell) acceptLoop() {
	defer s.listenerGone()
	backoff := minAcceptBackoff
	for {
		nc, err := s.cfg.Listener.Accept()
		if err != nil {
			if s.stopping() || errors.Is(err, net.ErrClosed) {
				return
			}
			s.stats.acceptErrors.Add(1)
			if !s.pause(backoff) {
				return
			}
			backoff = min(2*backoff, maxAcceptBackoff)
			continue
		}
		backoff = minAcceptBackoff
		s.admit(nc)
	}
}

func (s *Shell) pause(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-s.stop:
		return false
	}
}

func (s *Shell) listenerGone() {
	s.listenerUp.Store(false)
	if !s.stopping() {
		close(s.lost)
	}
}

func (s *Shell) admit(nc net.Conn) {
	id := proxy.ConnID(s.nextClient.Add(1))
	c := newConn(sideClient, id, time.Now(), proxy.FirstClientRead)
	c.nc = nc
	if !s.register(c) {
		s.stats.refused.Add(1)
		_ = nc.Close()
		return
	}
	s.stats.accepted.Add(1)
	if !s.post(envelope{ev: proxy.Event{Kind: proxy.EvClientAccepted, Conn: id}, conn: c}) {
		c.abort()
		s.unregister(c)
		return
	}
	s.spawn(func() { s.readLoop(c) }, &s.stats.clientReaders)
	s.spawn(func() { s.writeLoop(c) }, &s.stats.clientWriters)
}
