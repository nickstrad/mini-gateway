package e2e

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/config"
	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/enginesrc"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/shell"
)

type cryptoRandom struct{}

func (cryptoRandom) Uint64() uint64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return binary.LittleEndian.Uint64(b[:])
}

func startInProcess(t *testing.T, env *RealEnv, o GatewayOpts, noReset bool) *Gateway {
	t.Helper()
	b, err := json.Marshal(env.gatewayConfig(o, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	f, err := config.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	pc, err := f.Proxy(cryptoRandom{}, pgwire.NewKeyIssuer(cryptoRandom{}))
	if err != nil {
		t.Fatal(err)
	}
	if noReset {
		pc.Plant |= proxy.PlantNoReset
	}
	eng, err := proxy.New(pc)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sh, err := shell.New(eng, shell.Config{Listener: ln, AdminListener: admin, DBAddress: f.Database.Address,
		ClientTLS: f.ClientTLSConfig(), BackendTLS: f.BackendTLSConfig(), Timeouts: f.ProxyTimeouts(),
		PublishInterval: f.PublishInterval(), Obs: shell.ObsConfig{Instance: "gw-inproc", Build: "test", Detail: true},
		Sources:     func(c *obs.Collector, s func() proxy.ShellSnapshot) error { return enginesrc.Register(c, eng, s) },
		HealthRules: enginesrc.HealthRules()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sh.Run(ctx) }()
	g := &Gateway{Addr: ln.Addr().String(), Admin: admin.Addr().String()}
	g.stopFn = func() error {
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(gatewayStop):
			return context.DeadlineExceeded
		}
	}
	t.Cleanup(func() { _ = g.Stop() })
	return g
}
