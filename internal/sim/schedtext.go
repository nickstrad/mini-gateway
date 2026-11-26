package sim

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type KindName struct {
	Kind FaultKind
	Name string
}

var ErrScheduleText = errors.New("sim: malformed fault schedule text")

const (
	scheduleHeader  = "faults"
	unnamedPrefix   = "fault-"
	maxKindNameLen  = 64
	faultLineFields = 5
)

var faultLineKeys = [faultLineFields]string{"at", "kind", "target", "arg1", "arg2"}

func FormatSchedule(sched []Fault, names []KindName) (string, error) {
	if err := checkKindNames(names); err != nil {
		return "", err
	}
	if len(sched) > MaxScheduleLen {
		return "", fmt.Errorf("%w: %d faults, at most %d", ErrScheduleText, len(sched), MaxScheduleLen)
	}
	var b strings.Builder
	b.WriteString(scheduleHeader + " " + strconv.Itoa(len(sched)) + "\n")
	for _, f := range sched {
		fmt.Fprintf(&b, "at=%s kind=%s target=%d arg1=%d arg2=%d\n", f.At, kindToken(f.Kind, names), f.Target, f.Arg1, f.Arg2)
	}
	return b.String(), nil
}

func ParseSchedule(text string, names []KindName) ([]Fault, error) {
	if err := checkKindNames(names); err != nil {
		return nil, err
	}
	if !strings.HasSuffix(text, "\n") {
		return nil, fmt.Errorf("%w: the text does not end with a newline", ErrScheduleText)
	}
	if strings.Count(text, "\n") > MaxScheduleLen+1 {
		return nil, fmt.Errorf("%w: more than %d lines", ErrScheduleText, MaxScheduleLen+1)
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	n, err := parseHeader(lines[0])
	if err != nil {
		return nil, err
	}
	if n != len(lines)-1 {
		return nil, fmt.Errorf("%w: the header says %d faults, the text has %d lines after it", ErrScheduleText, n, len(lines)-1)
	}
	out := make([]Fault, 0, n)
	for i, line := range lines[1:] {
		f, err := parseFaultLine(line, names)
		if err != nil {
			return nil, fmt.Errorf("%w: line %d: %v", ErrScheduleText, i+2, err)
		}
		out = append(out, f)
	}
	return out, nil
}

func parseHeader(line string) (int, error) {
	word, count, ok := strings.Cut(line, " ")
	if !ok || word != scheduleHeader {
		return 0, fmt.Errorf("%w: line 1: want %q followed by the fault count", ErrScheduleText, scheduleHeader)
	}
	n, err := canonicalInt(count)
	if err != nil || n < 0 || n > MaxScheduleLen {
		return 0, fmt.Errorf("%w: line 1: the fault count %q is not a whole number from 0 to %d", ErrScheduleText, count, MaxScheduleLen)
	}
	return int(n), nil
}

func parseFaultLine(line string, names []KindName) (Fault, error) {
	fields := strings.Split(line, " ")
	if len(fields) != faultLineFields {
		return Fault{}, fmt.Errorf("want %d space-separated fields, got %d", faultLineFields, len(fields))
	}
	var vals [faultLineFields]string
	for i, field := range fields {
		key, val, ok := strings.Cut(field, "=")
		if !ok || key != faultLineKeys[i] {
			return Fault{}, fmt.Errorf("field %d: want %s=<value>, got %q", i+1, faultLineKeys[i], field)
		}
		vals[i] = val
	}
	var f Fault
	var err error
	if f.Kind, err = parseKind(vals[1], names); err != nil {
		return Fault{}, err
	}
	nanos, ok := strings.CutSuffix(vals[0], "ns")
	at, err := canonicalInt(nanos)
	if !ok || err != nil {
		return Fault{}, fmt.Errorf("at: %q is not a whole number of nanoseconds written as <n>ns", vals[0])
	}
	f.At = Time(at)
	ints := [3]*int64{&f.Target, &f.Arg1, &f.Arg2}
	for i, p := range ints {
		if *p, err = canonicalInt(vals[2+i]); err != nil {
			return Fault{}, fmt.Errorf("%s: %v", faultLineKeys[2+i], err)
		}
	}
	return f, nil
}

func canonicalInt(s string) (int64, error) {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || strconv.FormatInt(v, 10) != s {
		return 0, fmt.Errorf("%q is not a canonical decimal int64", s)
	}
	return v, nil
}

func parseKind(tok string, names []KindName) (FaultKind, error) {
	for _, kn := range names {
		if kn.Name == tok {
			return kn.Kind, nil
		}
	}
	id, ok := strings.CutPrefix(tok, unnamedPrefix)
	v, err := canonicalInt(id)
	if !ok || err != nil || v < 0 || v > int64(^FaultKind(0)) {
		return 0, fmt.Errorf("kind: %q is neither a known name nor %s<id>", tok, unnamedPrefix)
	}
	if _, named := kindName(FaultKind(v), names); named {
		return 0, fmt.Errorf("kind: %q has a name, write the name", tok)
	}
	return FaultKind(v), nil
}

func kindName(k FaultKind, names []KindName) (string, bool) {
	for _, kn := range names {
		if kn.Kind == k {
			return kn.Name, true
		}
	}
	return "", false
}

func kindToken(k FaultKind, names []KindName) string {
	if name, ok := kindName(k, names); ok {
		return name
	}
	return unnamedPrefix + strconv.Itoa(int(k))
}

func checkKindNames(names []KindName) error {
	for i, kn := range names {
		if !validKindName(kn.Name) {
			return fmt.Errorf("%w: kind %d: name %q must be 1 to %d of a-z 0-9 . _ -, start with a letter or digit, and not start with %q",
				ErrScheduleText, kn.Kind, kn.Name, maxKindNameLen, unnamedPrefix)
		}
		for _, prev := range names[:i] {
			if prev.Kind == kn.Kind || prev.Name == kn.Name {
				return fmt.Errorf("%w: kind %d %q is named twice", ErrScheduleText, kn.Kind, kn.Name)
			}
		}
	}
	return nil
}

func validKindName(s string) bool {
	if s == "" || len(s) > maxKindNameLen || strings.HasPrefix(s, unnamedPrefix) {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		letter := c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
		if !letter && (i == 0 || c != '.' && c != '_' && c != '-') {
			return false
		}
	}
	return true
}
