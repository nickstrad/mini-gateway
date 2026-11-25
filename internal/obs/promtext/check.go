package promtext

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/nickstrad/mini-gateway/internal/obs"
)

var ErrGrammar = errors.New("promtext: exposition grammar")

type Sample struct {
	Name   string
	Labels [][2]string
	Value  float64
}

func (s Sample) Label(name string) string {
	for _, kv := range s.Labels {
		if kv[0] == name {
			return kv[1]
		}
	}
	return ""
}

type Family struct {
	Name    string
	Type    string
	Help    string
	Samples []Sample
}

func gerr(line int, format string, a ...any) error {
	return fmt.Errorf("%w: line %d: %s", ErrGrammar, line, fmt.Sprintf(format, a...))
}

func validMetricName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c == ':' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func validLabelName(s string) bool {
	return validMetricName(s) && !strings.Contains(s, ":") && !strings.HasPrefix(s, "__")
}

func Parse(text []byte) ([]Family, error) {
	if len(text) > 0 && text[len(text)-1] != '\n' {
		return nil, gerr(0, "the text does not end with a newline")
	}
	var fams []Family
	seen := map[string]bool{}
	series := map[string]bool{}
	for i, line := range strings.Split(strings.TrimSuffix(string(text), "\n"), "\n") {
		n := i + 1
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "# HELP ") || strings.HasPrefix(line, "# TYPE "):
			f := strings.SplitN(line[7:], " ", 2)
			if len(f) != 2 || !validMetricName(f[0]) {
				return nil, gerr(n, "bad %s line", line[2:6])
			}
			if len(fams) == 0 || fams[len(fams)-1].Name != f[0] {
				if seen[f[0]] {
					return nil, gerr(n, "family %s appears twice or is not contiguous", f[0])
				}
				seen[f[0]] = true
				fams = append(fams, Family{Name: f[0]})
			}
			fam := &fams[len(fams)-1]
			if len(fam.Samples) > 0 {
				return nil, gerr(n, "%s after samples of %s", line[2:6], f[0])
			}
			if line[2:6] == "HELP" {
				h, err := unescape(f[1], false)
				if err != nil || fam.Help != "" {
					return nil, gerr(n, "bad or repeated HELP")
				}
				fam.Help = h
			} else {
				if fam.Type != "" || !slices.Contains([]string{"counter", "gauge", "histogram", "summary", "untyped"}, f[1]) {
					return nil, gerr(n, "bad or repeated TYPE %q", f[1])
				}
				fam.Type = f[1]
			}
		case strings.HasPrefix(line, "#"):
			continue
		default:
			s, err := parseSample(line)
			if err != nil {
				return nil, gerr(n, "%v", err)
			}
			if len(fams) == 0 || !belongs(fams[len(fams)-1], s.Name) {
				return nil, gerr(n, "sample %s outside its family's block (no TYPE before it)", s.Name)
			}
			key := s.Name + seriesKey(s.Labels)
			if series[key] {
				return nil, gerr(n, "duplicate series %s", key)
			}
			series[key] = true
			fams[len(fams)-1].Samples = append(fams[len(fams)-1].Samples, s)
		}
	}
	return fams, nil
}

func seriesKey(ls [][2]string) string {
	c := slices.Clone(ls)
	slices.SortFunc(c, func(a, b [2]string) int { return strings.Compare(a[0], b[0]) })
	var b strings.Builder
	for _, kv := range c {
		b.WriteString("\x00" + kv[0] + "\x00" + kv[1])
	}
	return b.String()
}

func belongs(f Family, name string) bool {
	switch f.Type {
	case "histogram":
		return name == f.Name+"_bucket" || name == f.Name+"_count" || name == f.Name+"_sum"
	case "summary":
		return name == f.Name || name == f.Name+"_count" || name == f.Name+"_sum"
	case "":
		return false
	}
	return name == f.Name
}

func parseSample(line string) (Sample, error) {
	var s Sample
	end := strings.IndexAny(line, "{ ")
	if end <= 0 {
		return s, errors.New("no metric name")
	}
	s.Name = line[:end]
	if !validMetricName(s.Name) {
		return s, fmt.Errorf("bad metric name %q", s.Name)
	}
	rest := line[end:]
	if rest[0] == '{' {
		ls, after, err := parseLabels(rest[1:])
		if err != nil {
			return s, err
		}
		s.Labels, rest = ls, after
	}
	if !strings.HasPrefix(rest, " ") {
		return s, errors.New("no space before the value")
	}
	fields := strings.Split(rest[1:], " ")
	if len(fields) < 1 || len(fields) > 2 || fields[0] == "" {
		return s, errors.New("bad value or timestamp")
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return s, fmt.Errorf("value %q", fields[0])
	}
	if len(fields) == 2 {
		if _, err := strconv.ParseInt(fields[1], 10, 64); err != nil {
			return s, fmt.Errorf("timestamp %q", fields[1])
		}
	}
	s.Value = v
	return s, nil
}

func parseLabels(s string) ([][2]string, string, error) {
	var out [][2]string
	names := map[string]bool{}
	for {
		if strings.HasPrefix(s, "}") {
			return out, s[1:], nil
		}
		eq := strings.IndexByte(s, '=')
		if eq <= 0 {
			return nil, "", errors.New("label without =")
		}
		name := s[:eq]
		if !validLabelName(name) || names[name] {
			return nil, "", fmt.Errorf("bad or repeated label name %q", name)
		}
		names[name] = true
		s = s[eq+1:]
		if !strings.HasPrefix(s, `"`) {
			return nil, "", errors.New("label value not quoted")
		}
		i := 1
		for ; i < len(s); i++ {
			if s[i] == '\\' {
				i++
				continue
			}
			if s[i] == '"' {
				break
			}
		}
		if i >= len(s) {
			return nil, "", errors.New("unterminated label value")
		}
		v, err := unescape(s[1:i], true)
		if err != nil {
			return nil, "", err
		}
		out = append(out, [2]string{name, v})
		s = s[i+1:]
		if strings.HasPrefix(s, ",") {
			s = s[1:]
			if strings.HasPrefix(s, "}") {
				return nil, "", errors.New("trailing comma")
			}
		} else if !strings.HasPrefix(s, "}") {
			return nil, "", errors.New("labels not separated by a comma")
		}
	}
}

func unescape(s string, quote bool) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\n' || quote && c == '"' {
			return "", errors.New("raw newline or quote")
		}
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		i++
		if i >= len(s) {
			return "", errors.New("dangling backslash")
		}
		switch s[i] {
		case '\\':
			b.WriteByte('\\')
		case 'n':
			b.WriteByte('\n')
		case '"':
			if !quote {
				return "", errors.New(`\" in HELP`)
			}
			b.WriteByte('"')
		default:
			return "", fmt.Errorf(`bad escape \%c`, s[i])
		}
	}
	return b.String(), nil
}

func Check(text []byte) error {
	fams, err := Parse(text)
	if err != nil {
		return err
	}
	for _, f := range fams {
		if f.Type == "" {
			return fmt.Errorf("%w: family %s has no TYPE", ErrGrammar, f.Name)
		}
		if f.Type == "counter" && !strings.HasSuffix(f.Name, "_total") {
			return fmt.Errorf("%w: counter %s does not end in _total", ErrGrammar, f.Name)
		}
		if f.Type == "counter" {
			for _, s := range f.Samples {
				if s.Value < 0 || math.IsNaN(s.Value) {
					return fmt.Errorf("%w: counter %s is negative", ErrGrammar, f.Name)
				}
			}
		}
		if f.Type == "histogram" {
			if err := checkHistogram(f); err != nil {
				return err
			}
		}
	}
	return nil
}

func withoutLe(ls [][2]string) string {
	var keep [][2]string
	for _, kv := range ls {
		if kv[0] != "le" {
			keep = append(keep, kv)
		}
	}
	return seriesKey(keep)
}

func checkHistogram(f Family) error {
	type hseries struct {
		les    []float64
		counts []float64
		inf    float64
		hasInf bool
		count  float64
		hasCnt bool
	}
	var keys []string
	all := map[string]*hseries{}
	get := func(k string) *hseries {
		if all[k] == nil {
			all[k] = &hseries{}
			keys = append(keys, k)
		}
		return all[k]
	}
	for _, s := range f.Samples {
		k := withoutLe(s.Labels)
		h := get(k)
		switch s.Name {
		case f.Name + "_bucket":
			le := s.Label("le")
			if le == "" {
				return fmt.Errorf("%w: %s_bucket without le", ErrGrammar, f.Name)
			}
			if le == "+Inf" {
				h.inf, h.hasInf = s.Value, true
				continue
			}
			v, err := strconv.ParseFloat(le, 64)
			if err != nil {
				return fmt.Errorf("%w: %s: le %q", ErrGrammar, f.Name, le)
			}
			h.les = append(h.les, v)
			h.counts = append(h.counts, s.Value)
		case f.Name + "_count":
			h.count, h.hasCnt = s.Value, true
		}
	}
	for _, k := range keys {
		h := all[k]
		if !h.hasInf || !h.hasCnt || h.inf != h.count {
			return fmt.Errorf("%w: histogram %s: +Inf bucket and _count missing or unequal", ErrGrammar, f.Name)
		}
		for i := 1; i < len(h.les); i++ {
			if h.les[i] <= h.les[i-1] || h.counts[i] < h.counts[i-1] {
				return fmt.Errorf("%w: histogram %s: buckets not increasing or not cumulative", ErrGrammar, f.Name)
			}
		}
		if n := len(h.counts); n > 0 && h.counts[n-1] > h.inf {
			return fmt.Errorf("%w: histogram %s: a bucket exceeds +Inf", ErrGrammar, f.Name)
		}
	}
	return nil
}

func Decode(reg *obs.Registry, text []byte) (obs.InstanceSnapshot, error) {
	fams, err := Parse(text)
	if err != nil {
		return obs.InstanceSnapshot{}, err
	}
	var s obs.InstanceSnapshot
	for _, f := range fams {
		d, ok := lookupFamily(reg, f.Name)
		if !ok {
			return s, fmt.Errorf("%w: family %s is not declared", ErrGrammar, f.Name)
		}
		if d.Kind != obs.KindHistogram {
			for _, x := range f.Samples {
				s.Points = append(s.Points, obs.Point{Component: d.Component, Metric: d.Name, Kind: d.Kind, Labels: labelsOf(x), Value: int64(x.Value)})
			}
			continue
		}
		hists := map[string]*obs.HistPoint{}
		var order []string
		prev := map[string]int64{}
		lastLe := map[string]int64{}
		for _, x := range f.Samples {
			if x.Name != f.Name+"_bucket" {
				continue
			}
			k := withoutLe(x.Labels)
			h := hists[k]
			if h == nil {
				h = &obs.HistPoint{Component: d.Component, Metric: d.Name, Labels: labelsOf(x), Unit: d.Unit}
				hists[k] = h
				order = append(order, k)
			}
			c := int64(x.Value) - prev[k]
			prev[k] = int64(x.Value)
			lo := lastLe[k] + 1
			hi := obs.MaxValue
			if le := x.Label("le"); le != "+Inf" {
				hi, _ = strconv.ParseInt(le, 10, 64)
				lastLe[k] = hi
			}
			if c > 0 {
				h.Buckets = append(h.Buckets, obs.Bucket{Lo: lo, Hi: hi, Count: c})
			}
		}
		for _, k := range order {
			s.Hists = append(s.Hists, *hists[k])
		}
	}
	s.Sort()
	return s, nil
}

func labelsOf(x Sample) obs.Labels {
	return obs.Labels{Tenant: x.Label("tenant"), Outcome: x.Label("outcome"), Reason: x.Label("reason"), Mode: x.Label("mode")}
}

func lookupFamily(reg *obs.Registry, name string) (obs.MetricDesc, bool) {
	for _, d := range reg.All() {
		if FamilyName(d.Component, d.Name, d.Kind) == name {
			return d, true
		}
	}
	return obs.MetricDesc{}, false
}
