package e2e

import (
	"strings"
	"time"

	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
)

type Vanish uint8

const (
	VanishNone Vanish = iota
	VanishAfterSend
	VanishAfterReply
)

type Step struct {
	SQL         string
	Raw         []byte
	Delay       time.Duration
	Vanish      Vanish
	StopReading bool
	StallFor    time.Duration
}

type Script struct {
	Tenant int
	Start  time.Duration
	Steps  []Step
}

type Spec struct {
	Name      string
	Tenants   int
	Conns     int
	QueueCap  int
	Timeouts  proxy.Timeouts
	ReplyCap  int
	Clients   []Script
	Rows      []fakepg.Row
	Service   fakepg.Service
	Faults    []sim.Fault
	Fragment  bool
	Plant     proxy.Plant
	Duration  time.Duration
	SetupSQL  []string
	ClientTLS bool
}

type ClientResult struct {
	Startup []string
	Steps   [][]string
	Elapsed []time.Duration
	Closed  bool
}

func (c ClientResult) String() string {
	var b strings.Builder
	b.WriteString("startup[" + strings.Join(c.Startup, " ") + "]")
	for i, s := range c.Steps {
		b.WriteString(" ")
		b.WriteString(itoa(i))
		b.WriteString("[" + strings.Join(s, " ") + "]")
	}
	if c.Closed {
		b.WriteString(" closed")
	}
	return b.String()
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var d []byte
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		d = append([]byte{byte('0' + i%10)}, d...)
		i /= 10
	}
	if neg {
		d = append([]byte{'-'}, d...)
	}
	return string(d)
}

func Tag(client int, sql string) string { return "/* c" + itoa(client) + " */ " + sql }

func (r ClientResult) Last() []string {
	if len(r.Steps) == 0 {
		return nil
	}
	return r.Steps[len(r.Steps)-1]
}

func HasToken(tokens []string, prefix string) bool {
	for _, t := range tokens {
		if strings.HasPrefix(t, prefix) {
			return true
		}
	}
	return false
}
