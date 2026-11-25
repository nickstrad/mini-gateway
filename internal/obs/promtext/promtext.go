package promtext

import (
	"bytes"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/nickstrad/mini-gateway/internal/obs"
)

const (
	AdapterName       = "prom"
	Prefix            = "mg_"
	OtherTenant       = "other"
	DefaultTenantMax  = 32
	MaxTenantMax      = 1024
	DefaultEvictAfter = 60
	MaxTenantLabelLen = 64
	MaxExportBuckets  = 32
)

var DefaultExportBuckets = []int64{
	100_000, 250_000, 500_000, 1_000_000, 2_500_000, 5_000_000, 10_000_000, 25_000_000,
	50_000_000, 100_000_000, 250_000_000, 500_000_000, 1_000_000_000, 2_500_000_000, 10_000_000_000, 60_000_000_000,
}

var SizeBuckets = []int64{
	1, 4, 16, 64, 256, 1024, 4096, 16384, 65536, 262144, 1048576, 4194304, 16777216, 67108864, 268435456, 1073741824,
}

type Config struct {
	TenantMax     int
	Tenants       []string
	EvictAfter    int64
	ExportBuckets []int64
}

type slot struct {
	tenant     string
	signature  int64
	lastActive int64
}

type Adapter struct {
	cfg        Config
	reg        *obs.Registry
	w          io.Writer
	last       []byte
	slots      []slot
	publishes  int64
	configured []string
	prevTen    []string
	prevSig    []int64
}

func New(reg *obs.Registry, w io.Writer, cfg Config) *Adapter {
	if cfg.TenantMax <= 0 {
		cfg.TenantMax = DefaultTenantMax
	}
	cfg.TenantMax = min(cfg.TenantMax, MaxTenantMax)
	if cfg.EvictAfter <= 0 {
		cfg.EvictAfter = DefaultEvictAfter
	}
	if len(cfg.ExportBuckets) == 0 || len(cfg.ExportBuckets) > MaxExportBuckets || !strictlyIncreasing(cfg.ExportBuckets) {
		cfg.ExportBuckets = DefaultExportBuckets
	}
	a := &Adapter{cfg: cfg, reg: reg, w: w}
	for _, t := range cfg.Tenants {
		if len(a.configured) < cfg.TenantMax && Labelable(t) && !slices.Contains(a.configured, t) {
			a.configured = append(a.configured, t)
		}
	}
	slices.Sort(a.configured)
	return a
}

func strictlyIncreasing(b []int64) bool {
	for i := range b {
		if b[i] <= 0 || i > 0 && b[i] <= b[i-1] {
			return false
		}
	}
	return true
}

func Labelable(t string) bool {
	return t != "" && t != OtherTenant && len(t) <= MaxTenantLabelLen && utf8.ValidString(t)
}

func (a *Adapter) Name() string { return AdapterName }

func (a *Adapter) TenantsTracked() int64 { return int64(len(a.slots)) }

func (a *Adapter) Last() []byte { return a.last }

func (a *Adapter) Export(s obs.InstanceSnapshot) error {
	a.publishes++
	a.updateSlots(s)
	a.last = a.render(s)
	if a.w == nil {
		return nil
	}
	_, err := a.w.Write(a.last)
	return err
}

func (a *Adapter) slotOf(t string) int {
	for i, s := range a.slots {
		if s.tenant == t {
			return i
		}
	}
	return -1
}

func signatures(s obs.InstanceSnapshot) ([]string, []int64) {
	tenants := s.Tenants()
	sig := make([]int64, len(tenants))
	for _, p := range s.Points {
		if p.Labels.Tenant != "" && p.Kind == obs.KindCounter {
			i, _ := slices.BinarySearch(tenants, p.Labels.Tenant)
			sig[i] += p.Value
		}
	}
	for _, h := range s.Hists {
		if h.Labels.Tenant != "" {
			i, _ := slices.BinarySearch(tenants, h.Labels.Tenant)
			sig[i] += h.Count()
		}
	}
	return tenants, sig
}

func (a *Adapter) updateSlots(s obs.InstanceSnapshot) {
	tenants, sig := signatures(s)
	for i := range a.slots {
		sl := &a.slots[i]
		j, found := slices.BinarySearch(tenants, sl.tenant)
		if found && sig[j] != sl.signature {
			sl.signature, sl.lastActive = sig[j], a.publishes
		}
	}
	a.slots = slices.DeleteFunc(a.slots, func(sl slot) bool { return a.publishes-sl.lastActive >= a.cfg.EvictAfter })
	for i, t := range tenants {
		if len(a.slots) >= a.cfg.TenantMax {
			break
		}
		if !a.eligible(t) || a.slotOf(t) >= 0 || !a.active(t, sig[i]) {
			continue
		}
		a.slots = append(a.slots, slot{tenant: t, signature: sig[i], lastActive: a.publishes})
	}
	a.prevTen, a.prevSig = tenants, sig
}

func (a *Adapter) active(t string, sig int64) bool {
	j, found := slices.BinarySearch(a.prevTen, t)
	if !found {
		return sig != 0
	}
	return a.prevSig[j] != sig
}

func (a *Adapter) eligible(t string) bool {
	if !Labelable(t) {
		return false
	}
	if len(a.cfg.Tenants) == 0 {
		return true
	}
	_, found := slices.BinarySearch(a.configured, t)
	return found
}

func (a *Adapter) labelTenant(t string) string {
	if t == "" || a.slotOf(t) >= 0 {
		return t
	}
	return OtherTenant
}

type series struct {
	family string
	kind   obs.Kind
	help   string
	labels obs.Labels
	comp   string
	value  int64
	merge  obs.MergeRule
	hist   []int64
	bounds []int64
	count  int64
}

func FamilyName(component, metric string, k obs.Kind) string {
	n := Prefix + component + "_" + metric
	if k == obs.KindCounter {
		n += "_total"
	}
	return n
}

func (a *Adapter) boundsFor(u obs.Unit) []int64 {
	if u == obs.UnitNs {
		return a.cfg.ExportBuckets
	}
	return SizeBuckets
}

func Rebucket(bounds []int64, buckets []obs.Bucket) ([]int64, int64) {
	cum := make([]int64, len(bounds))
	total := int64(0)
	for _, b := range buckets {
		total += b.Count
		i, _ := slices.BinarySearch(bounds, b.Hi)
		if i < len(bounds) {
			cum[i] += b.Count
		}
	}
	for i := 1; i < len(cum); i++ {
		cum[i] += cum[i-1]
	}
	return cum, total
}

func (a *Adapter) collect(s obs.InstanceSnapshot) []series {
	var out []series
	add := func(x series) {
		i, found := slices.BinarySearchFunc(out, x, compareSeries)
		if !found {
			out = slices.Insert(out, i, x)
			return
		}
		y := &out[i]
		switch {
		case x.kind == obs.KindHistogram:
			for k := range y.hist {
				y.hist[k] += x.hist[k]
			}
			y.count += x.count
		case y.merge == obs.MergeMax:
			y.value = max(y.value, x.value)
		default:
			y.value += x.value
		}
	}
	for _, p := range s.Points {
		d, ok := a.reg.Lookup(p.Component, p.Metric)
		if !ok || d.Kind != p.Kind {
			continue
		}
		l := p.Labels
		l.Tenant = a.labelTenant(l.Tenant)
		add(series{family: FamilyName(p.Component, p.Metric, p.Kind), kind: p.Kind, help: d.Help, labels: l, comp: p.Component, value: p.Value, merge: d.Merge})
	}
	for _, h := range s.Hists {
		d, ok := a.reg.Lookup(h.Component, h.Metric)
		if !ok || d.Kind != obs.KindHistogram {
			continue
		}
		l := h.Labels
		l.Tenant = a.labelTenant(l.Tenant)
		bounds := a.boundsFor(h.Unit)
		cum, total := Rebucket(bounds, h.Buckets)
		add(series{family: FamilyName(h.Component, h.Metric, obs.KindHistogram), kind: obs.KindHistogram, help: d.Help, labels: l, comp: h.Component, hist: cum, bounds: bounds, count: total})
	}
	return out
}

func compareSeries(x, y series) int {
	if c := strings.Compare(x.family, y.family); c != 0 {
		return c
	}
	return obs.CompareLabels(x.labels, y.labels)
}

func (a *Adapter) render(s obs.InstanceSnapshot) []byte {
	var b bytes.Buffer
	prev := ""
	for _, x := range a.collect(s) {
		if x.family != prev {
			prev = x.family
			b.WriteString("# HELP " + x.family + " " + escapeHelp(x.help) + "\n")
			b.WriteString("# TYPE " + x.family + " " + x.kind.String() + "\n")
		}
		if x.kind != obs.KindHistogram {
			b.WriteString(x.family + labelText(x.comp, x.labels, "") + " " + strconv.FormatInt(x.value, 10) + "\n")
			continue
		}
		for i, bound := range x.bounds {
			b.WriteString(x.family + "_bucket" + labelText(x.comp, x.labels, strconv.FormatInt(bound, 10)) + " " + strconv.FormatInt(x.hist[i], 10) + "\n")
		}
		b.WriteString(x.family + "_bucket" + labelText(x.comp, x.labels, "+Inf") + " " + strconv.FormatInt(x.count, 10) + "\n")
		b.WriteString(x.family + "_count" + labelText(x.comp, x.labels, "") + " " + strconv.FormatInt(x.count, 10) + "\n")
	}
	return b.Bytes()
}

func labelText(comp string, l obs.Labels, le string) string {
	var b strings.Builder
	b.WriteString(`{component="` + escapeLabel(comp) + `"`)
	for _, kv := range [][2]string{{"tenant", l.Tenant}, {"outcome", l.Outcome}, {"reason", l.Reason}, {"mode", l.Mode}, {"le", le}} {
		if kv[1] != "" {
			b.WriteString(`,` + kv[0] + `="` + escapeLabel(kv[1]) + `"`)
		}
	}
	b.WriteString("}")
	return b.String()
}

func escapeLabel(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s)
}

func escapeHelp(s string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`).Replace(s)
}
