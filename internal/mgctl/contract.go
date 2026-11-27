package mgctl

import (
	"strings"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/work"
)

const proxyMessagePrefix = "mini-gateway:"

type ContractMatch struct {
	Row       pgwire.ErrorRow
	Found     bool
	Ambiguous bool
}

func ContractRow(code, message string) ContractMatch {
	for _, r := range pgwire.ErrorRows {
		if !r.HasMessage() || r.Code != code {
			continue
		}
		if message == r.Message || strings.HasPrefix(message, r.Message) {
			return ContractMatch{Row: r, Found: true, Ambiguous: !strings.HasPrefix(r.Message, proxyMessagePrefix)}
		}
	}
	return ContractMatch{}
}

func ProxyMade(code, message string) bool {
	m := ContractRow(code, message)
	return m.Found && !m.Ambiguous
}

func reachedText(t work.Tri) string {
	switch t {
	case work.Yes:
		return "yes"
	case work.No:
		return "no"
	}
	return "unknown"
}

func ContractLine(e *ServerError) string {
	m := ContractRow(e.Code, e.Message)
	if !m.Found {
		return "error-contract row: pass-through (an error from Postgres itself, relayed unchanged)"
	}
	r := m.Row
	line := "error-contract row: " + r.ID + " — " + r.Situation + "; reached the database: " + reachedText(r.Reached) +
		"; write applied: " + reachedText(r.WriteApplied) + "; retry: " + r.Retry + "; client connection: " + r.Conn.String()
	if m.Ambiguous {
		line += " (Postgres sends the same text for its own error of this code)"
	}
	return line
}
