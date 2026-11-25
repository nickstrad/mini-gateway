// Package logline implements the structured one-line publish log adapter.
// Each publish formats one key=value line capped at MaxLineBytes containing
// snapshot metadata and every untenanted Headline metric in fixed order.
package logline

import (
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/nickstrad/mini-gateway/internal/obs"
)

const (
	// AdapterName is the name under which Adapter attaches to obs.Collector.
	AdapterName = "log"
	// MaxLineBytes bounds every rendered line including its trailing newline.
	MaxLineBytes = 1024
	// TruncatedMark is appended before the newline when headline metrics exceed MaxLineBytes.
	TruncatedMark = " truncated=1"
	maxValueBytes = 32
)

// Adapter implements obs.Adapter, rendering one bounded log line per publish.
type Adapter struct {
	reg  *obs.Registry
	w    io.Writer
	last []byte
}

// New creates a logline Adapter consulting reg to select Headline metrics and
// writing each rendered line to w when w is non-nil.
func New(reg *obs.Registry, w io.Writer) *Adapter { return &Adapter{reg: reg, w: w} }

// Name returns AdapterName ("log").
func (a *Adapter) Name() string { return AdapterName }

// Export renders one log line for s, saves it for Last, and writes it to w if set.
func (a *Adapter) Export(s obs.InstanceSnapshot) error {
	a.last = Render(a.reg, s)
	if a.w == nil {
		return nil
	}
	_, err := a.w.Write(a.last)
	return err
}

// Last returns the most recently rendered log line.
func (a *Adapter) Last() []byte { return a.last }

// Key formats a point's log key as component.metric[.outcome][.reason][.mode],
// replacing characters outside [A-Za-z0-9._-] with underscores.
func Key(p obs.Point) string {
	k := p.Component + "." + p.Metric
	for _, v := range []string{p.Labels.Outcome, p.Labels.Reason, p.Labels.Mode} {
		if v != "" {
			k += "." + v
		}
	}
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, k)
}

// Render formats s as a single newline-terminated log line of at most
// MaxLineBytes bytes, starting with fixed metadata fields followed by every
// untenanted Headline point in snapshot order.
func Render(reg *obs.Registry, s obs.InstanceSnapshot) []byte {
	b := make([]byte, 0, MaxLineBytes+1)
	b = append(b, "obs.publish"...)
	b = appendKV(b, "seq", strconv.FormatInt(s.Seq, 10))
	b = appendKV(b, "instance", s.Instance)
	b = appendKV(b, "build", s.Build)
	b = appendKV(b, "config", s.ConfigFingerprint)
	b = appendKV(b, "points", strconv.Itoa(len(s.Points)))
	b = appendKV(b, "histograms", strconv.Itoa(len(s.Hists)))
	for _, p := range s.Points {
		d, ok := reg.Lookup(p.Component, p.Metric)
		if !ok || !d.Headline || p.Labels.Tenant != "" {
			continue
		}
		next := appendKV(nil, Key(p), strconv.FormatInt(p.Value, 10))
		if len(b)+len(next)+len(TruncatedMark)+1 > MaxLineBytes {
			b = append(b, TruncatedMark...)
			break
		}
		b = append(b, next...)
	}
	return append(b, '\n')
}

func appendKV(b []byte, k, v string) []byte {
	b = append(b, ' ')
	b = append(b, k...)
	b = append(b, '=')
	if len(v) > maxValueBytes {
		v = v[:maxValueBytes]
	}
	if v == "" || strings.ContainsFunc(v, func(r rune) bool { return r <= ' ' || r == '"' || r == '=' || r == '\\' || r >= 0x7f }) {
		return strconv.AppendQuoteToASCII(b, v)
	}
	return append(b, v...)
}

// ErrBadLine is returned by Parse when a line does not match the obs.publish grammar.
var ErrBadLine = errors.New("logline: bad line")

// Parse splits a rendered obs.publish line back into ordered [key, value] pairs.
func Parse(line []byte) ([][2]string, error) {
	s := strings.TrimSuffix(string(line), "\n")
	if !strings.HasPrefix(s, "obs.publish") || strings.ContainsAny(s, "\n") {
		return nil, ErrBadLine
	}
	s = s[len("obs.publish"):]
	var out [][2]string
	for s != "" {
		if s[0] != ' ' {
			return nil, ErrBadLine
		}
		s = s[1:]
		eq := strings.IndexByte(s, '=')
		if eq <= 0 {
			return nil, ErrBadLine
		}
		k := s[:eq]
		s = s[eq+1:]
		var v string
		if strings.HasPrefix(s, "\"") {
			q, err := strconv.QuotedPrefix(s)
			if err != nil {
				return nil, ErrBadLine
			}
			v, _ = strconv.Unquote(q)
			s = s[len(q):]
		} else {
			end := strings.IndexByte(s, ' ')
			if end < 0 {
				end = len(s)
			}
			v, s = s[:end], s[end:]
		}
		out = append(out, [2]string{k, v})
	}
	return out, nil
}
