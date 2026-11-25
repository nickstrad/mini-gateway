package workload

import (
	"math/rand/v2"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/sim/simtls"
	"github.com/nickstrad/mini-gateway/internal/testclient"
)

// TLSRequest configures whether a simulated tenant sends SSLRequest before
// StartupMessage, matching libpq's disable, prefer, and require behaviors.
type TLSRequest uint8

// Client-side TLS negotiation modes (zero keeps chunk 2's plaintext default).
const (
	TLSOff TLSRequest = iota
	TLSPrefer
	TLSRequire
)

// TLSFault injects one of the client-side TLS startup faults of PLAN.md 10.4.
type TLSFault uint8

// Client-side TLS fault kinds indexed into StartupCounters.TLSFaults.
const (
	TLSFaultNone TLSFault = iota
	TLSFaultStall
	TLSFaultBadHello
	TLSFaultSmuggle
	TLSFaultSecondRequest
	NumTLSFaults
)

var tlsFaultNames = [NumTLSFaults]string{"none", "stall-after-request", "bad-hello", "plaintext-after-request", "second-request"}

// String returns the canonical hyphenated name of f.
func (f TLSFault) String() string {
	if f < NumTLSFaults {
		return tlsFaultNames[f]
	}
	return "unknown"
}

// StartupCounters tracks per-tenant TLS negotiation, handshake, fault, and
// SCRAM counts inside TenantSnapshot.
type StartupCounters struct {
	TLSRequests      int64
	TLSAnsweredYes   int64
	TLSAnsweredNo    int64
	HandshakesOK     int64
	HandshakesFailed int64
	TLSFaults        [NumTLSFaults]int64
	SCRAMStarted     int64
	SCRAMOK          int64
	SCRAMFailed      int64
}

func (s *StartupCounters) add(o StartupCounters) {
	s.TLSRequests += o.TLSRequests
	s.TLSAnsweredYes += o.TLSAnsweredYes
	s.TLSAnsweredNo += o.TLSAnsweredNo
	s.HandshakesOK += o.HandshakesOK
	s.HandshakesFailed += o.HandshakesFailed
	for i := range s.TLSFaults {
		s.TLSFaults[i] += o.TLSFaults[i]
	}
	s.SCRAMStarted += o.SCRAMStarted
	s.SCRAMOK += o.SCRAMOK
	s.SCRAMFailed += o.SCRAMFailed
}

type startupState struct {
	tlsWait  bool
	hello    []byte
	overTLS  bool
	smuggled bool
	scram    *pgwire.ScramClient
	nonces   *rand.Rand
}

func (c *client) startupParams() []testclient.Param {
	return []testclient.Param{{Name: "user", Value: c.cfg().Name}, {Name: "database", Value: c.cfg().Database}}
}

func (c *client) beginStartup() {
	t := c.cfg()
	c.ss = startupState{nonces: c.ss.nonces}
	if t.TLS == TLSOff {
		c.send(c.sess.Startup(nil, c.startupParams()))
		return
	}
	st := &c.t.st.startup
	st.TLSRequests++
	b := c.sess.PreStartup(nil, true, false)
	if t.TLSFault == TLSFaultSmuggle {
		st.TLSFaults[TLSFaultSmuggle]++
		c.ss.smuggled = true
		b = c.sess.Startup(b, c.startupParams())
	}
	c.send(b)
}

func (c *client) onTLSAnswer(answer byte) {
	st := &c.t.st.startup
	t := c.cfg()
	if answer == pgwire.AnswerTLSNo {
		st.TLSAnsweredNo++
		switch {
		case c.ss.smuggled:
		case t.TLS == TLSRequire:
			c.connectFailed("", false)
		default:
			c.send(c.sess.Startup(nil, c.startupParams()))
		}
		return
	}
	st.TLSAnsweredYes++
	switch {
	case c.ss.smuggled:
	case t.TLSFault == TLSFaultStall:
		st.TLSFaults[TLSFaultStall]++
	case t.TLSFault == TLSFaultBadHello:
		st.TLSFaults[TLSFaultBadHello]++
		c.ss.tlsWait = true
		c.send(simtls.AppendBadClientHello(nil))
	default:
		c.ss.tlsWait = true
		c.send(simtls.AppendClientHello(nil))
	}
}

func (c *client) takeHello(data []byte) []byte {
	need := simtls.HelloLen - len(c.ss.hello)
	k := min(need, len(data))
	c.ss.hello = append(c.ss.hello, data[:k]...)
	if len(c.ss.hello) < simtls.HelloLen {
		return nil
	}
	c.ss.tlsWait = false
	reply, _, ok := simtls.ParseServerReply(c.ss.hello)
	st := &c.t.st.startup
	if !ok || reply == simtls.ReplyAlert {
		st.HandshakesFailed++
		c.connectFailed("", false)
		return nil
	}
	st.HandshakesOK++
	c.ss.overTLS = true
	if c.cfg().TLSFault == TLSFaultSecondRequest {
		st.TLSFaults[TLSFaultSecondRequest]++
		c.send(c.sess.PreStartup(nil, true, false))
		return data[k:]
	}
	c.send(c.sess.Startup(nil, c.startupParams()))
	return data[k:]
}

func (c *client) onAuth(m testclient.Msg) {
	st := &c.t.st.startup
	switch m.Auth {
	case pgwire.AuthOK:
		return
	case pgwire.AuthSASL:
		scram, plus := false, false
		for _, mech := range splitCStrings(m.Data) {
			scram = scram || mech == pgwire.ScramMechanism
			plus = plus || mech == pgwire.ScramPlusMechanism
		}
		if !scram {
			c.connectFailed("", false)
			return
		}
		if c.ss.nonces == nil {
			c.ss.nonces = c.w.s.Stream(label(c.t.cfg.Name, itoa(c.local)) + "/scram")
		}
		st.SCRAMStarted++
		c.ss.scram = pgwire.NewScramClient("", c.cfg().Password, pgwire.NewNonce(c.ss.nonces), pgwire.GS2Header(c.ss.overTLS, plus))
		c.send(pgwire.AppendSASLInitialResponse(nil, pgwire.ScramMechanism, c.ss.scram.First()))
		return
	case pgwire.AuthSASLContinue:
		if c.ss.scram != nil {
			if final, cause := c.ss.scram.Final(m.Data); cause == pgwire.AuthCauseNone {
				c.send(pgwire.AppendSASLResponse(nil, final))
				return
			}
		}
	case pgwire.AuthSASLFinal:
		if c.ss.scram != nil && c.ss.scram.Verify(m.Data) == pgwire.AuthCauseNone {
			c.ss.scram = nil
			st.SCRAMOK++
			return
		}
	}
	st.SCRAMFailed++
	c.connectFailed("", false)
}

func splitCStrings(b []byte) []string {
	var out []string
	start := 0
	for i, x := range b {
		if x != 0 {
			continue
		}
		if i == start {
			break
		}
		out = append(out, string(b[start:i]))
		start = i + 1
	}
	return out
}
