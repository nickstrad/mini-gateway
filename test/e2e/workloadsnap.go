package e2e

import (
	"slices"
	"strings"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/simsrc"
	"github.com/nickstrad/mini-gateway/internal/proxy/proxysim"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
)

const maxCodes = 64

// WorkloadSnapshot summarizes r's scripted client outcomes into component
// "workload" metrics so SQL assertions can cross-check proxy vs client totals.
func (r *SimRun) WorkloadSnapshot() workload.Snapshot {
	tenants := make([]workload.TenantSnapshot, max(r.Spec.Tenants, 1))
	lat := make([]*obs.Histogram, len(tenants))
	for i := range tenants {
		tenants[i].Name = proxysim.TenantName(i)
		lat[i] = obs.NewDurationHistogram()
	}
	out := workload.Snapshot{Clients: int64(len(r.Spec.Clients))}
	for c, sc := range r.Spec.Clients {
		res := r.Clients[c]
		t := &tenants[sc.Tenant%len(tenants)]
		h := lat[sc.Tenant%len(tenants)]
		if res.Closed || len(res.Steps) == len(sc.Steps) {
			out.Finished++
		}
		t.Connects++
		if !slices.ContainsFunc(res.Startup, func(s string) bool { return strings.HasPrefix(s, "closed:") || strings.HasPrefix(s, "E:") }) && (len(res.Steps) > 0 || !res.Closed) {
			t.ConnectsOK++
		}
		for i, st := range res.Steps {
			t.Intended++
			t.Sent++
			if i < len(res.Elapsed) {
				h.Record(int64(res.Elapsed[i]))
			}
			outcome(t, st)
		}
	}
	for i := range tenants {
		tenants[i].Latency = lat[i].Snapshot()
		tenants[i].LatencyFromSend = tenants[i].Latency
	}
	out.Tenants = tenants
	return out
}

func outcome(t *workload.TenantSnapshot, st []string) {
	switch {
	case slices.Contains(st, "vanished"):
		t.ClientLeft++
	case len(st) == 0 || !strings.HasPrefix(st[len(st)-1], "Z"):
		t.Lost++
	default:
		code := ""
		for _, tok := range st {
			if rest, ok := strings.CutPrefix(tok, "E:"); ok {
				code = rest[strings.LastIndex(rest, ":")+1:]
				break
			}
		}
		if code == "" {
			t.OK++
			return
		}
		t.Errors++
		addCode(t, code)
	}
}

func addCode(t *workload.TenantSnapshot, code string) {
	for i := range t.ErrorsByCode {
		if t.ErrorsByCode[i].Code == code {
			t.ErrorsByCode[i].Count++
			return
		}
	}
	if len(t.ErrorsByCode) >= maxCodes {
		t.ErrorsOtherCodes++
		return
	}
	t.ErrorsByCode = append(t.ErrorsByCode, workload.CodeCount{Code: code, Count: 1})
	slices.SortFunc(t.ErrorsByCode, func(a, b workload.CodeCount) int { return strings.Compare(a.Code, b.Code) })
}

func (r *SimRun) workloadSource() obs.Source {
	return obs.NewSource(obs.ComponentWorkload, simsrc.WorkloadMetrics(), r.WorkloadSnapshot, simsrc.MapWorkload)
}
