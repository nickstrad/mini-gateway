package obs

import (
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"
)

// State is the three-state health verdict for one check or the whole instance.
// Values are ordered by severity so max(a, b) computes the worst state.
type State uint8

const (
	// StateOK means the check or instance is operating normally.
	StateOK State = iota
	// StateDegraded means the instance is still answering, with reduced health
	// (for example shedding, open breaker, draining, or export errors).
	StateDegraded
	// StateFailed means the instance cannot work at all.
	StateFailed
)

var stateNames = [...]string{"ok", "degraded", "failed"}

// String returns "ok", "degraded", or "failed".
func (s State) String() string {
	if int(s) < len(stateNames) {
		return stateNames[s]
	}
	return "unknown"
}

// CheckResult is the evaluated outcome of one named health rule.
type CheckResult struct {
	Name   string
	State  State
	Reason string
}

// Rule is a named health check evaluated purely over an InstanceSnapshot.
type Rule struct {
	Name  string
	Check func(s InstanceSnapshot) (State, string)
}

// Health is the overall health verdict (worst across Checks) and each check's detail.
type Health struct {
	State  State
	Checks []CheckResult
}

// MaxHealthRules caps the number of rules a HealthTable accepts.
const MaxHealthRules = 64

// ErrBadRule is returned by HealthTable.Add for invalid, duplicate, or excess rules.
var ErrBadRule = errors.New("obs: bad health rule")

// HealthTable holds an ordered table of named health rules that later chunks
// extend as new proxy components land.
type HealthTable struct {
	rules []Rule
}

// Add registers r in t, rejecting invalid names, nil callbacks, duplicates, or
// rules past MaxHealthRules.
func (t *HealthTable) Add(r Rule) error {
	if !validName(r.Name) || r.Check == nil {
		return ErrBadRule
	}
	if len(t.rules) >= MaxHealthRules {
		return ErrBadRule
	}
	for _, x := range t.rules {
		if x.Name == r.Name {
			return ErrBadRule
		}
	}
	t.rules = append(t.rules, r)
	return nil
}

// Names returns the registered rule names in evaluation order.
func (t *HealthTable) Names() []string {
	out := make([]string, len(t.rules))
	for i, r := range t.rules {
		out[i] = r.Name
	}
	return out
}

// Evaluate runs every rule against s and returns a Health whose overall State
// is the worst state returned by any rule.
func (t *HealthTable) Evaluate(s InstanceSnapshot) Health {
	h := Health{Checks: make([]CheckResult, 0, len(t.rules))}
	for _, r := range t.rules {
		st, reason := r.Check(s)
		if st > StateFailed {
			st, reason = StateFailed, "rule returned an unknown state"
		}
		h.Checks = append(h.Checks, CheckResult{Name: r.Name, State: st, Reason: reason})
		h.State = max(h.State, st)
	}
	return h
}

// CheckCollector is the rule name of the collector's built-in self-check.
const CheckCollector = "collector"

// CollectorRule returns the collector's self-check rule: degraded before the
// first publish or while the latest publish had emit or adapter export errors,
// recovering to ok once they clear. It never reports StateFailed because an
// export failure does not stop the instance from serving traffic.
func CollectorRule() Rule {
	return Rule{Name: CheckCollector, Check: func(s InstanceSnapshot) (State, string) {
		if s.Value(ComponentObs, MetricPublishes, Labels{}) == 0 {
			return StateDegraded, "nothing published yet"
		}
		if s.Value(ComponentObs, MetricEmitFailing, Labels{}) > 0 {
			n := s.Value(ComponentObs, MetricEmitErrors, Labels{})
			return StateDegraded, "the latest publish had metric errors (" + strconv.FormatInt(n, 10) + " publishes in all)"
		}
		failing := []string(nil)
		for _, p := range s.Points {
			if p.Component == ComponentObs && p.Metric == MetricAdapterFailing && p.Value > 0 {
				failing = append(failing, p.Labels.Reason)
			}
		}
		if len(failing) > 0 {
			return StateDegraded, "adapter export failing: " + strings.Join(failing, ",")
		}
		return StateOK, "publishing"
	}}
}

// Health evaluates t against the last published snapshot with the collector's
// own obs metrics refreshed to reflect the latest publish immediately.
func (c *Collector) Health(t *HealthTable) Health {
	if !c.published {
		var empty InstanceSnapshot
		return t.Evaluate(empty)
	}
	return t.Evaluate(c.selfView())
}

// AppendJSON appends the JSON health object to b without importing encoding/json
// so internal/obs stays within the determinism lint's allowed import set.
func (h Health) AppendJSON(b []byte) []byte {
	b = append(b, `{"state":`...)
	b = AppendJSONString(b, h.State.String())
	b = append(b, `,"checks":[`...)
	for i, c := range h.Checks {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, `{"name":`...)
		b = AppendJSONString(b, c.Name)
		b = append(b, `,"state":`...)
		b = AppendJSONString(b, c.State.String())
		b = append(b, `,"reason":`...)
		b = AppendJSONString(b, c.Reason)
		b = append(b, '}')
	}
	return append(b, "]}"...)
}

const hexDigits = "0123456789abcdef"

// AppendJSONString appends s as a JSON string literal, matching encoding/json's
// escaping (including HTML-safe <, >, &, U+2028/U+2029, and invalid UTF-8 bytes)
// without importing encoding/json.
func AppendJSONString(b []byte, s string) []byte {
	b = append(b, '"')
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			switch {
			case c == '"' || c == '\\':
				b = append(b, '\\', c)
			case c == '\n':
				b = append(b, '\\', 'n')
			case c == '\r':
				b = append(b, '\\', 'r')
			case c == '\t':
				b = append(b, '\\', 't')
			case c == '\b':
				b = append(b, '\\', 'b')
			case c == '\f':
				b = append(b, '\\', 'f')
			case c < 0x20 || c == '<' || c == '>' || c == '&':
				b = append(b, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xf])
			default:
				b = append(b, c)
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b = append(b, '\\', 'u', 'f', 'f', 'f', 'd')
		} else if r == 0x2028 || r == 0x2029 {
			b = append(b, '\\', 'u', '2', '0', '2', hexDigits[r&0xf])
		} else {
			b = append(b, s[i:i+size]...)
		}
		i += size
	}
	return append(b, '"')
}
