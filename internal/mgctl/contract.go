package mgctl

import (
	"strings"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/work"
)

const (
	proxyMessagePrefix = "mini-gateway:"
	fatalSeverity      = "FATAL"
	exactMatchBonus    = 1 << 20
	severityMatchBonus = 1 << 21
)

type ContractMatch struct {
	Row       pgwire.ErrorRow
	Found     bool
	Ambiguous bool
	ByCode    bool
}

func ContractRow(code, message string) ContractMatch {
	return bestMessageRow("", code, message)
}

func bestMessageRow(severity, code, message string) ContractMatch {
	best, bestRank := ContractMatch{}, -1
	for _, r := range pgwire.ErrorRows {
		if !r.HasMessage() || r.Code != code || !matches(message, r.Message) {
			continue
		}
		if rank := messageRank(severity, message, r); rank > bestRank {
			best, bestRank = ContractMatch{Row: r, Found: true, Ambiguous: !strings.HasPrefix(r.Message, proxyMessagePrefix)}, rank
		}
	}
	return best
}

func messageRank(severity, message string, r pgwire.ErrorRow) int {
	rank := len(r.Message)
	if message == r.Message {
		rank += exactMatchBonus
	}
	if severity != "" && r.Severity() == severity {
		rank += severityMatchBonus
	}
	return rank
}

func ContractRowFor(severity, code, message string) ContractMatch {
	if m := bestMessageRow(severity, code, message); m.Found || severity != fatalSeverity {
		return m
	}
	best, bestScore, candidates := ContractMatch{}, -1, 0
	for _, r := range pgwire.ErrorRows {
		if r.Code != code || r.Severity() != fatalSeverity {
			continue
		}
		candidates++
		if score := overlap(message, r.Message); score > bestScore {
			best, bestScore = ContractMatch{Row: r, Found: true, ByCode: true}, score
		}
	}
	if candidates > 1 && bestScore == 0 {
		return ContractMatch{}
	}
	return best
}

func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(c rune) bool { return !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') })
}

func overlap(a, b string) int {
	n := 0
	bw := words(b)
	for _, w := range words(a) {
		for _, x := range bw {
			if w == x && w != "mini" && w != "gateway" {
				n++
				break
			}
		}
	}
	return n
}

func matches(message, row string) bool {
	if message == row || strings.HasPrefix(message, row) {
		return true
	}
	plain := unquoted(message)
	return plain == row || strings.HasPrefix(plain, row)
}

func unquoted(s string) string {
	var b strings.Builder
	in := false
	for _, c := range s {
		switch {
		case c == '"':
			in = !in
		case !in:
			b.WriteRune(c)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
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
	m := ContractRowFor(e.Severity, e.Code, e.Message)
	if !m.Found {
		return "error-contract row: pass-through (an error from Postgres itself, relayed unchanged)"
	}
	r := m.Row
	line := "error-contract row: " + r.ID + " — " + r.Situation + "; reached the database: " + reachedText(r.Reached) +
		"; write applied: " + reachedText(r.WriteApplied) + "; retry: " + r.Retry + "; client connection: " + r.Conn.String()
	if m.Ambiguous {
		line += " (Postgres sends the same text for its own error of this code)"
	}
	if m.ByCode {
		line += " (named by its code: the proxy's FATAL text differs from the row's message)"
	}
	return line
}
