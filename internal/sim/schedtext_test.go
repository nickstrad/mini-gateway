package sim

import (
	"errors"
	"math"
	"slices"
	"strings"
	"testing"
)

var textNames = []KindName{{1, "net.reset"}, {5, "net.chunk"}, {9, "net.clear"}, {300, "pg.slow_reply"}, {65535, "z-9"}}

func randomTextSchedule(seed int64) []Fault {
	r := NewStream(seed, "schedtext")
	extremes := []int64{0, 1, -1, math.MaxInt64, math.MinInt64, 1 << 40}
	pick := func() int64 {
		if r.IntN(3) == 0 {
			return extremes[r.IntN(len(extremes))]
		}
		return int64(r.Uint64())
	}
	kinds := []FaultKind{0, 1, 2, 5, 9, 300, 301, 65535, 65534}
	n := r.IntN(12)
	if seed == 7 {
		n = MaxScheduleLen
	}
	out := make([]Fault, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, Fault{At: Time(pick()), Kind: kinds[r.IntN(len(kinds))], Target: pick(), Arg1: pick(), Arg2: pick()})
	}
	return out
}

func TestScheduleTextRoundTrips(t *testing.T) {
	for seed := int64(0); seed < 500; seed++ {
		sched := randomTextSchedule(seed)
		for _, names := range [][]KindName{textNames, nil} {
			text, err := FormatSchedule(sched, names)
			if err != nil {
				t.Fatalf("seed %d: %v", seed, err)
			}
			back, err := ParseSchedule(text, names)
			if err != nil {
				t.Fatalf("seed %d: %v\n%s", seed, err, text)
			}
			if !slices.Equal(back, sched) || len(back) != len(sched) {
				t.Fatalf("seed %d: round trip changed the schedule", seed)
			}
			again, _ := FormatSchedule(back, names)
			if again != text {
				t.Fatalf("seed %d: the text is not canonical", seed)
			}
		}
	}
}

func TestScheduleTextReadsAsDocumented(t *testing.T) {
	sched := []Fault{{At: 2500000, Kind: 5, Target: 1, Arg1: 3, Arg2: 1}, {At: -4, Kind: 2, Target: 0, Arg1: -1, Arg2: 0}}
	want := "faults 2\nat=2500000ns kind=net.chunk target=1 arg1=3 arg2=1\nat=-4ns kind=fault-2 target=0 arg1=-1 arg2=0\n"
	got, err := FormatSchedule(sched, textNames)
	if err != nil || got != want {
		t.Fatalf("got %q, %v", got, err)
	}
	if got, err := FormatSchedule(nil, nil); err != nil || got != "faults 0\n" {
		t.Fatalf("empty: %q %v", got, err)
	}
}

func TestScheduleTextRejectsMalformed(t *testing.T) {
	good := "faults 2\nat=10ns kind=net.chunk target=1 arg1=3 arg2=1\nat=20ns kind=fault-2 target=0 arg1=0 arg2=0\n"
	if _, err := ParseSchedule(good, textNames); err != nil {
		t.Fatal(err)
	}
	line := func(s string) string { return "faults 1\n" + s + "\n" }
	bad := map[string]string{
		"empty":                "",
		"no final newline":     strings.TrimSuffix(good, "\n"),
		"truncated":            "faults 2\nat=10ns kind=net.chunk target=1 arg1=3 arg2=1\n",
		"extra line":           good + "at=30ns kind=net.reset target=0 arg1=0 arg2=0\n",
		"blank line":           good + "\n",
		"blank inside":         "faults 2\nat=10ns kind=net.chunk target=1 arg1=3 arg2=1\n\n",
		"no header":            "at=10ns kind=net.chunk target=1 arg1=3 arg2=1\n",
		"bad header word":      "fault 0\n",
		"header count sign":    "faults +0\n",
		"header count zeros":   "faults 00\n",
		"header count big":     "faults 1025\n",
		"header negative":      "faults -1\n",
		"crlf":                 strings.ReplaceAll(good, "\n", "\r\n"),
		"tab":                  line("at=10ns\tkind=net.chunk target=1 arg1=3 arg2=1"),
		"double space":         line("at=10ns  kind=net.chunk target=1 arg1=3 arg2=1"),
		"trailing space":       line("at=10ns kind=net.chunk target=1 arg1=3 arg2=1 "),
		"missing field":        line("at=10ns kind=net.chunk target=1 arg1=3"),
		"extra field":          line("at=10ns kind=net.chunk target=1 arg1=3 arg2=1 arg3=0"),
		"fields reordered":     line("kind=net.chunk at=10ns target=1 arg1=3 arg2=1"),
		"no unit":              line("at=10 kind=net.chunk target=1 arg1=3 arg2=1"),
		"other unit":           line("at=10ms kind=net.chunk target=1 arg1=3 arg2=1"),
		"leading zero":         line("at=010ns kind=net.chunk target=1 arg1=3 arg2=1"),
		"plus sign":            line("at=10ns kind=net.chunk target=+1 arg1=3 arg2=1"),
		"minus zero":           line("at=10ns kind=net.chunk target=-0 arg1=3 arg2=1"),
		"overflow":             line("at=10ns kind=net.chunk target=9223372036854775808 arg1=3 arg2=1"),
		"hex":                  line("at=10ns kind=net.chunk target=0x1 arg1=3 arg2=1"),
		"unknown name":         line("at=10ns kind=net.nothing target=1 arg1=3 arg2=1"),
		"named kind by number": line("at=10ns kind=fault-5 target=1 arg1=3 arg2=1"),
		"kind too big":         line("at=10ns kind=fault-65536 target=1 arg1=3 arg2=1"),
		"kind negative":        line("at=10ns kind=fault--1 target=1 arg1=3 arg2=1"),
		"empty value":          line("at=10ns kind=net.chunk target= arg1=3 arg2=1"),
		"no equals":            line("at=10ns kind=net.chunk target arg1=3 arg2=1"),
		"comment":              "faults 0\n# a comment\n",
	}
	for name, text := range bad {
		got, err := ParseSchedule(text, textNames)
		if err == nil || !errors.Is(err, ErrScheduleText) || got != nil {
			t.Errorf("%s: got %v, %v", name, got, err)
		}
	}
}

func TestScheduleTextRejectsBadNameTables(t *testing.T) {
	for _, names := range [][]KindName{
		{{1, ""}}, {{1, "Net"}}, {{1, "a b"}}, {{1, ".a"}}, {{1, "fault-3"}}, {{1, strings.Repeat("a", 65)}},
		{{1, "a"}, {2, "a"}}, {{1, "a"}, {1, "b"}}, {{1, "a=b"}},
	} {
		if _, err := FormatSchedule(nil, names); !errors.Is(err, ErrScheduleText) {
			t.Errorf("format accepted %v", names)
		}
		if _, err := ParseSchedule("faults 0\n", names); !errors.Is(err, ErrScheduleText) {
			t.Errorf("parse accepted %v", names)
		}
	}
	if _, err := FormatSchedule(make([]Fault, MaxScheduleLen+1), nil); !errors.Is(err, ErrScheduleText) {
		t.Error("format accepted an over-long schedule")
	}
}

func FuzzParseSchedule(f *testing.F) {
	f.Add("faults 0\n")
	f.Add("faults 1\nat=10ns kind=net.chunk target=1 arg1=3 arg2=1\n")
	f.Add("faults 1\nat=-9223372036854775808ns kind=fault-2 target=0 arg1=0 arg2=0\n")
	f.Fuzz(func(t *testing.T, text string) {
		sched, err := ParseSchedule(text, textNames)
		if err != nil {
			if sched != nil || !errors.Is(err, ErrScheduleText) {
				t.Fatalf("an error with a result or of the wrong kind: %v", err)
			}
			return
		}
		back, err := FormatSchedule(sched, textNames)
		if err != nil || back != text {
			t.Fatalf("accepted text is not canonical: %q -> %q (%v)", text, back, err)
		}
	})
}
