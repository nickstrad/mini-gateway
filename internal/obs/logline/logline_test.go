package logline_test

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/logline"
	"github.com/nickstrad/mini-gateway/internal/obs/obstest"
)

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("log full") }

func decode(t testing.TB, reg *obs.Registry, line []byte) obs.InstanceSnapshot {
	t.Helper()
	kvs, err := logline.Parse(line)
	if err != nil {
		t.Fatalf("%v: %q", err, line)
	}
	var s obs.InstanceSnapshot
	for _, kv := range kvs {
		parts := strings.Split(kv[0], ".")
		if len(parts) < 2 {
			continue
		}
		d, ok := reg.Lookup(parts[0], parts[1])
		if !ok {
			t.Fatalf("key %s names no declared metric", kv[0])
		}
		var l obs.Labels
		rest := parts[2:]
		for _, x := range []struct {
			set obs.LabelSet
			dst *string
		}{{obs.LabelOutcome, &l.Outcome}, {obs.LabelReason, &l.Reason}, {obs.LabelMode, &l.Mode}} {
			if d.Labels.Has(x.set) && len(rest) > 0 {
				*x.dst, rest = rest[0], rest[1:]
			}
		}
		v, err := strconv.ParseInt(kv[1], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		s.Points = append(s.Points, obs.Point{Component: d.Component, Metric: d.Name, Kind: d.Kind, Labels: l, Value: v})
	}
	return s
}

func subject() obstest.Subject {
	return obstest.Subject{
		Name:     logline.AdapterName,
		Fidelity: obstest.Headline,
		New: func(t testing.TB, reg *obs.Registry) obstest.Instance {
			var buf bytes.Buffer
			a := logline.New(reg, &buf)
			return obstest.Instance{
				Adapter: a,
				Output:  func(testing.TB) []byte { return bytes.Clone(buf.Bytes()) },
				Decoded: func(t testing.TB) obs.InstanceSnapshot { return decode(t, reg, a.Last()) },
			}
		},
		NewFailing: func(_ testing.TB, reg *obs.Registry) obs.Adapter { return logline.New(reg, failWriter{}) },
	}
}

func TestConformance(t *testing.T) { obstest.Conformance(t, subject()) }

func TestGolden(t *testing.T) {
	s, reg := obstest.FixtureSnapshot(t)
	obstest.Golden(t, "fixture.log", logline.Render(reg, s))
}

func TestOneBoundedLineInFixedOrder(t *testing.T) {
	var reg obs.Registry
	var descs []obs.MetricDesc
	s := obs.InstanceSnapshot{Instance: strings.Repeat("i\"\n ", 100), Build: "b", Seq: 3}
	for i := 0; i < 500; i++ {
		name := "headline_" + strconv.Itoa(1000+i)
		descs = append(descs, obs.CounterDesc(name, 0, "H.").AsHeadline())
		s.Points = append(s.Points, obs.Point{Component: obs.ComponentPool, Metric: name, Kind: obs.KindCounter, Value: int64(i) << 40})
	}
	if err := reg.Declare(obs.ComponentPool, descs...); err != nil {
		t.Fatal(err)
	}
	line := logline.Render(&reg, s)
	if len(line) > logline.MaxLineBytes || bytes.Count(line, []byte("\n")) != 1 || !bytes.HasSuffix(line, []byte(logline.TruncatedMark+"\n")) {
		t.Fatalf("%d bytes: %q", len(line), line)
	}
	kvs, err := logline.Parse(line)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"seq", "instance", "build", "config", "points", "histograms", "pool.headline_1000"}
	for i, k := range want {
		if kvs[i][0] != k {
			t.Fatalf("key %d = %s, want %s", i, kvs[i][0], k)
		}
	}
	if kvs[1][1] != strings.Repeat("i\"\n ", 100)[:32] {
		t.Fatalf("instance %q", kvs[1][1])
	}
}
