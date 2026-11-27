package main

import (
	"net"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/config"
	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/catalog"
	"github.com/nickstrad/mini-gateway/internal/obs/csvflush"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/shell"
)

// TestEveryMetricTheBinaryExportsIsInTheCatalog proves every point and histogram
// emitted along the real binary's shellConfig registration path is declared in
// internal/obs/catalog so docs/csv-schema.md stays complete.
func TestEveryMetricTheBinaryExportsIsInTheCatalog(t *testing.T) {
	f, err := config.Load(exampleConfig(t, ephemeral))
	if err != nil {
		t.Fatal(err)
	}
	f.Obs.CSV.Dir = t.TempDir()
	r := cryptoRandom{}
	pcfg, err := f.Proxy(r, pgwire.NewKeyIssuer(r))
	if err != nil {
		t.Fatal(err)
	}
	engine, err := proxy.New(pcfg)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", f.Listen)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := net.Listen("tcp", f.AdminListen)
	if err != nil {
		t.Fatal(err)
	}
	sh, err := shell.New(engine, shellConfig(f, engine, ln, admin))
	if err != nil {
		t.Fatal(err)
	}
	if err := sh.Start(); err != nil {
		t.Fatal(err)
	}
	if err := sh.Close(); err != nil {
		t.Fatal(err)
	}
	run, err := csvflush.Read(sh.CSVRunDir())
	if err != nil {
		t.Fatal(err)
	}
	seqs := run.FlushSeqs(f.Instance)
	if len(seqs) == 0 {
		t.Fatal("no flush")
	}
	reg, err := catalog.Registry()
	if err != nil {
		t.Fatal(err)
	}
	components := map[string]int{}
	for _, seq := range seqs {
		s := run.Snapshot(f.Instance, seq)
		for _, p := range s.Points {
			if _, ok := reg.Lookup(p.Component, p.Metric); !ok {
				t.Errorf("%s.%s is exported by the binary but not in the catalog (docs/csv-schema.md)", p.Component, p.Metric)
			}
			components[p.Component]++
		}
		for _, h := range s.Hists {
			if _, ok := reg.Lookup(h.Component, h.Metric); !ok {
				t.Errorf("histogram %s.%s is exported by the binary but not in the catalog", h.Component, h.Metric)
			}
		}
	}
	for _, c := range []string{obs.ComponentEngine, obs.ComponentObs} {
		if components[c] == 0 {
			t.Errorf("component %s exported nothing: %v", c, components)
		}
	}
	t.Logf("points by component over %d flushes: %v", len(seqs), components)
}
