package jsonstats_test

import (
	"bytes"
	"errors"
	"flag"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/jsonstats"
	"github.com/nickstrad/mini-gateway/internal/obs/obstest"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func subject() obstest.Subject {
	return obstest.Subject{
		Name:     jsonstats.AdapterName,
		Fidelity: obstest.Exact,
		New: func(t testing.TB, _ *obs.Registry) obstest.Instance {
			var buf bytes.Buffer
			a := jsonstats.New(&buf)
			return obstest.Instance{
				Adapter: a,
				Output:  func(testing.TB) []byte { return bytes.Clone(buf.Bytes()) },
				Decoded: func(t testing.TB) obs.InstanceSnapshot {
					s, err := jsonstats.Decode(a.Last())
					if err != nil {
						t.Fatal(err)
					}
					return s
				},
			}
		},
		NewFailing: func(testing.TB, *obs.Registry) obs.Adapter { return jsonstats.New(failWriter{}) },
	}
}

// TestConformance proves jsonstats passes all five obstest.Conformance checks at Exact fidelity.
func TestConformance(t *testing.T) { obstest.Conformance(t, subject()) }

// TestGolden proves Render matches the committed testdata/fixture.json golden file.
func TestGolden(t *testing.T) {
	s, _ := obstest.FixtureSnapshot(t)
	b, err := jsonstats.Render(s)
	if err != nil {
		t.Fatal(err)
	}
	obstest.Golden(t, *update, "fixture.json", b)
}

// TestEveryTenantAndStableOrder proves JSON output keeps every hostile tenant
// unbounded and preserves deterministic top-level field order.
func TestEveryTenantAndStableOrder(t *testing.T) {
	s, _ := obstest.FixtureSnapshot(t)
	d := jsonstats.FromSnapshot(s)
	seen := map[string]bool{}
	for _, m := range d.Metrics {
		seen[m.Tenant] = true
	}
	for _, tn := range obstest.HostileTenants {
		if !seen[tn] {
			t.Fatalf("tenant %q missing", tn)
		}
	}
	b, _ := jsonstats.Render(s)
	if !bytes.HasPrefix(b, []byte("{\n \"instance\": \"inst-1\",\n \"build\": \"build-x\",\n \"config_fingerprint\": \"cfg-1\",\n \"seq\": 0,\n \"metrics\": [")) {
		t.Fatalf("field order changed:\n%s", b[:120])
	}
}
