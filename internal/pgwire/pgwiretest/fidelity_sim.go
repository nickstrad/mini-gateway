package pgwiretest

import (
	"errors"
	"fmt"
	"io"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
	"github.com/nickstrad/mini-gateway/internal/sim/simtls"
)

const (
	// FidelityUser is the role used by the startup fidelity script.
	FidelityUser = "postgres"
	// FidelityDatabase is the database used by the startup fidelity script.
	FidelityDatabase = "postgres"
	// FidelityPassword is the SCRAM password configured in the tier R container and fake.
	FidelityPassword = "mg-tier-r-scram"
	maxSimSteps      = 1 << 20
)

// SimDialer adapts a simulated fakepg.Server to the Dialer interface by
// stepping the simulator kernel synchronously inside Read and Write.
type SimDialer struct {
	S   *sim.Sim
	Net *simnet.Net
	PG  *fakepg.Server
}

// NewSimFidelity creates an isolated simulator and SCRAM-enabled fakepg server
// matching the tier R startup fidelity configuration.
func NewSimFidelity(seed int64) (*SimDialer, error) {
	s := sim.New(sim.Config{Seed: seed, Scenario: "startup-fidelity"})
	n := simnet.New(s)
	v, err := pgwire.NewVerifier(FidelityPassword, []byte("fidelity-salt-01"), pgwire.DefaultScramIterations)
	if err != nil {
		return nil, err
	}
	pg, err := fakepg.New(n, fakepg.Config{Auth: fakepg.AuthSCRAM, Users: []string{FidelityUser}, Databases: []string{FidelityDatabase},
		Verifiers: []fakepg.UserVerifier{{User: FidelityUser, Verifier: v}}})
	if err != nil {
		return nil, err
	}
	return &SimDialer{S: s, Net: n, PG: pg}, nil
}

type simConn struct {
	d         *SimDialer
	end       *simnet.End
	connected bool
	refused   bool
}

func (d *SimDialer) step() bool {
	ok, err := d.S.Step()
	return ok && err == nil
}

// Dial configures the fake's TLS answer for target and dials it over simnet.
func (d *SimDialer) Dial(target string) (Transport, error) {
	switch target {
	case TargetSSLOff:
		d.PG.SetTLS(fakepg.TLSConfig{Answer: pgwire.AnswerTLSNo})
	case TargetSSLOn:
		d.PG.SetTLS(fakepg.TLSConfig{Answer: pgwire.AnswerTLSYes})
	default:
		return nil, fmt.Errorf("pgwiretest: no target %q", target)
	}
	addr := d.PG.Addr()
	c := &simConn{d: d}
	c.end = d.Net.Dial(addr, simnet.DefaultOptions(), simnet.HandlerFunc(func(_ *simnet.End, ev simnet.Event) {
		switch ev {
		case simnet.EvConnected:
			c.connected = true
		case simnet.EvRefused:
			c.refused = true
		}
	}))
	for i := 0; !c.connected && !c.refused; i++ {
		if i >= maxSimSteps || !d.step() {
			return nil, ErrTimeout
		}
	}
	if c.refused {
		return nil, errors.New("pgwiretest: refused")
	}
	return c, nil
}

func (c *simConn) Write(b []byte) error {
	for i := 0; len(b) > 0; i++ {
		n, err := c.end.Write(b)
		if err != nil {
			return err
		}
		b = b[n:]
		if len(b) > 0 && (i >= maxSimSteps || !c.d.step()) {
			return ErrTimeout
		}
	}
	return nil
}

func (c *simConn) Read(buf []byte) (int, error) {
	for i := 0; ; i++ {
		n, err := c.end.Read(buf)
		if n > 0 {
			return n, nil
		}
		if err != nil {
			return 0, io.EOF
		}
		if i >= maxSimSteps || !c.d.step() {
			return 0, ErrTimeout
		}
	}
}

func (c *simConn) StartTLS() error {
	if err := c.Write(simtls.AppendClientHello(nil)); err != nil {
		return err
	}
	var reply []byte
	buf := make([]byte, simtls.HelloLen)
	for len(reply) < simtls.HelloLen {
		n, err := c.Read(buf[:simtls.HelloLen-len(reply)])
		if err != nil {
			return err
		}
		reply = append(reply, buf[:n]...)
	}
	if r, _, ok := simtls.ParseServerReply(reply); !ok || r == simtls.ReplyAlert {
		return errors.New("pgwiretest: simulated handshake failed")
	}
	return nil
}

func (c *simConn) Close() {
	_ = c.end.Close()
	for i := 0; i < maxSimSteps && c.d.step(); i++ {
	}
}
