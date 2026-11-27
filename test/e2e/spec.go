// Package e2e provides the end-to-end scenario harness that runs scripted
// clients against both tier S (the deterministic simulator via proxysim) and
// tier R (the compiled mini-gateway binary against Postgres 17 in Docker),
// plus the engine operation fuzz world and reset-reply tamper relay.
package e2e

import (
	"strings"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pool"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
)

// Vanish selects when a scripted client abruptly disconnects without Terminate.
type Vanish uint8

const (
	// VanishNone runs the client through a clean Terminate close.
	VanishNone Vanish = iota
	// VanishAfterSend resets the connection shortly after sending the step's query.
	VanishAfterSend
	// VanishAfterReply resets the connection immediately after receiving ReadyForQuery.
	VanishAfterReply
)

// Step is one query or raw wire-byte exchange executed by a scripted client.
type Step struct {
	SQL         string
	Raw         []byte
	Delay       time.Duration
	Vanish      Vanish
	StopReading bool
	StallFor    time.Duration
	Cancel      time.Duration
}

// Script defines one client's tenant, start delay, and sequence of Steps.
type Script struct {
	Tenant int
	Start  time.Duration
	Steps  []Step
}

// Spec describes an end-to-end scenario runnable in tier S (RunSim) and tier R (RunReal).
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
	Pool      *pool.Config
	Reset     proxy.ResetPolicy
	Reseed    bool
	FakeConns int
	Path      *PathPlan
}

// ClientResult records the tokenized startup messages, per-step replies, elapsed
// durations, and close state observed by one scripted client.
type ClientResult struct {
	Startup []string
	Steps   [][]string
	Elapsed []time.Duration
	Closed  bool
}

// String formats c into a compact single-line summary for assertion failure messages.
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

// Tag prefixes sql with a client identifier comment ("/* cN */ ") recorded in
// fakepg's statement log for transaction- and session-crossing analysis.
func Tag(client int, sql string) string { return "/* c" + itoa(client) + " */ " + sql }

// Last returns the tokens of the client's final executed step, or nil if none.
func (r ClientResult) Last() []string {
	if len(r.Steps) == 0 {
		return nil
	}
	return r.Steps[len(r.Steps)-1]
}

// HasToken reports whether any token in tokens starts with prefix.
func HasToken(tokens []string, prefix string) bool {
	for _, t := range tokens {
		if strings.HasPrefix(t, prefix) {
			return true
		}
	}
	return false
}
