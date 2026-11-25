package jsonstats

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/nickstrad/mini-gateway/internal/obs"
)

const AdapterName = "json"

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

type Bucket struct {
	Lo    int64 `json:"lo"`
	Hi    int64 `json:"hi"`
	Count int64 `json:"count"`
}

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

type Document struct {
	Instance          string      `json:"instance"`
	Build             string      `json:"build"`
	ConfigFingerprint string      `json:"config_fingerprint"`
	Seq               int64       `json:"seq"`
	Metrics           []Metric    `json:"metrics"`
	Histograms        []Histogram `json:"histograms"`
}

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

func Render(s obs.InstanceSnapshot) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetIndent("", " ")
	if err := enc.Encode(FromSnapshot(s)); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func Decode(b []byte) (obs.InstanceSnapshot, error) {
	var d Document
	if err := json.Unmarshal(b, &d); err != nil {
		return obs.InstanceSnapshot{}, err
	}
	return d.Snapshot(), nil
}

type Adapter struct {
	w    io.Writer
	last []byte
}

func New(w io.Writer) *Adapter { return &Adapter{w: w} }

func (a *Adapter) Name() string { return AdapterName }

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

func (a *Adapter) Last() []byte { return a.last }
