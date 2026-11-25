// Package jsonstats implements the JSON stats adapter, serializing the full
// obs.InstanceSnapshot—every tenant and every full-resolution histogram bucket—
// in deterministic struct-field order.
package jsonstats

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/nickstrad/mini-gateway/internal/obs"
)

// AdapterName is the name under which Adapter attaches to obs.Collector.
const AdapterName = "json"

// Metric is the JSON wire representation of one scalar obs.Point.
type Metric struct {
	Component string `json:"component"`
	Metric    string `json:"metric"`
	Kind      string `json:"kind"`
	Tenant    string `json:"tenant"`
	Outcome   string `json:"outcome"`
	Reason    string `json:"reason"`
	Mode      string `json:"mode"`
	Value     int64  `json:"value"`
}

// Bucket is the JSON wire representation of one inclusive obs.Bucket.
type Bucket struct {
	Lo    int64 `json:"lo"`
	Hi    int64 `json:"hi"`
	Count int64 `json:"count"`
}

// Histogram is the JSON wire representation of one full-resolution obs.HistPoint.
type Histogram struct {
	Component string   `json:"component"`
	Metric    string   `json:"metric"`
	Tenant    string   `json:"tenant"`
	Outcome   string   `json:"outcome"`
	Reason    string   `json:"reason"`
	Mode      string   `json:"mode"`
	Unit      string   `json:"unit"`
	Buckets   []Bucket `json:"buckets"`
}

// Document is the top-level JSON payload for one published snapshot. Using
// structs and slices rather than maps keeps field and row order deterministic.
type Document struct {
	Instance          string      `json:"instance"`
	Build             string      `json:"build"`
	ConfigFingerprint string      `json:"config_fingerprint"`
	Seq               int64       `json:"seq"`
	Metrics           []Metric    `json:"metrics"`
	Histograms        []Histogram `json:"histograms"`
}

// FromSnapshot copies s into a JSON-ready Document without retaining any slice
// backing storage from s.
func FromSnapshot(s obs.InstanceSnapshot) Document {
	d := Document{Instance: s.Instance, Build: s.Build, ConfigFingerprint: s.ConfigFingerprint, Seq: s.Seq,
		Metrics: make([]Metric, 0, len(s.Points)), Histograms: make([]Histogram, 0, len(s.Hists))}
	for _, p := range s.Points {
		d.Metrics = append(d.Metrics, Metric{Component: p.Component, Metric: p.Metric, Kind: p.Kind.String(),
			Tenant: p.Labels.Tenant, Outcome: p.Labels.Outcome, Reason: p.Labels.Reason, Mode: p.Labels.Mode, Value: p.Value})
	}
	for _, h := range s.Hists {
		bs := make([]Bucket, 0, len(h.Buckets))
		for _, b := range h.Buckets {
			bs = append(bs, Bucket{Lo: b.Lo, Hi: b.Hi, Count: b.Count})
		}
		d.Histograms = append(d.Histograms, Histogram{Component: h.Component, Metric: h.Metric, Tenant: h.Labels.Tenant,
			Outcome: h.Labels.Outcome, Reason: h.Labels.Reason, Mode: h.Labels.Mode, Unit: string(h.Unit), Buckets: bs})
	}
	return d
}

// Snapshot converts d back into an obs.InstanceSnapshot.
func (d Document) Snapshot() obs.InstanceSnapshot {
	s := obs.InstanceSnapshot{Instance: d.Instance, Build: d.Build, ConfigFingerprint: d.ConfigFingerprint, Seq: d.Seq}
	for _, m := range d.Metrics {
		k := obs.KindGauge
		if m.Kind == "counter" {
			k = obs.KindCounter
		}
		s.Points = append(s.Points, obs.Point{Component: m.Component, Metric: m.Metric, Kind: k,
			Labels: obs.Labels{Tenant: m.Tenant, Outcome: m.Outcome, Reason: m.Reason, Mode: m.Mode}, Value: m.Value})
	}
	for _, h := range d.Histograms {
		hp := obs.HistPoint{Component: h.Component, Metric: h.Metric, Unit: obs.Unit(h.Unit),
			Labels: obs.Labels{Tenant: h.Tenant, Outcome: h.Outcome, Reason: h.Reason, Mode: h.Mode}}
		for _, b := range h.Buckets {
			hp.Buckets = append(hp.Buckets, obs.Bucket{Lo: b.Lo, Hi: b.Hi, Count: b.Count})
		}
		s.Hists = append(s.Hists, hp)
	}
	return s
}

// Render encodes s as indented JSON bytes with deterministic field order.
func Render(s obs.InstanceSnapshot) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetIndent("", " ")
	if err := enc.Encode(FromSnapshot(s)); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Decode parses JSON bytes produced by Render back into an obs.InstanceSnapshot.
func Decode(b []byte) (obs.InstanceSnapshot, error) {
	var d Document
	if err := json.Unmarshal(b, &d); err != nil {
		return obs.InstanceSnapshot{}, err
	}
	return d.Snapshot(), nil
}

// Adapter implements obs.Adapter, caching the latest rendered JSON and
// optionally writing each publish to w.
type Adapter struct {
	w    io.Writer
	last []byte
}

// New creates a JSON stats Adapter that writes rendered snapshots to w when w is non-nil.
func New(w io.Writer) *Adapter { return &Adapter{w: w} }

// Name returns AdapterName ("json").
func (a *Adapter) Name() string { return AdapterName }

// Export renders s, retains the bytes for Last, and writes them to w if set.
func (a *Adapter) Export(s obs.InstanceSnapshot) error {
	b, err := Render(s)
	if err != nil {
		return err
	}
	a.last = b
	if a.w == nil {
		return nil
	}
	_, err = a.w.Write(b)
	return err
}

// Last returns the most recently rendered JSON payload.
func (a *Adapter) Last() []byte { return a.last }
