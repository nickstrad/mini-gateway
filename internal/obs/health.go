package obs

import (
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"
)

type State uint8

const (
	StateOK State = iota
	StateDegraded
	StateFailed
)

var stateNames = [...]string{"ok", "degraded", "failed"}

func (s State) String() string {
	if int(s) < len(stateNames) {
		return stateNames[s]
	}
	return "unknown"
}

type CheckResult struct {
	Name   string
	State  State
	Reason string
}

type Rule struct {
	Name  string
	Check func(s InstanceSnapshot) (State, string)
}

type Health struct {
	State  State
	Checks []CheckResult
}

const MaxHealthRules = 64

var ErrBadRule = errors.New("obs: bad health rule")

type HealthTable struct {
	rules []Rule
}

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

func (t *HealthTable) Names() []string {
	out := make([]string, len(t.rules))
	for i, r := range t.rules {
		out[i] = r.Name
	}
	return out
}

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

const CheckCollector = "collector"

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

func (c *Collector) Health(t *HealthTable) Health {
	if !c.published {
		var empty InstanceSnapshot
		return t.Evaluate(empty)
	}
	return t.Evaluate(c.selfView())
}

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
