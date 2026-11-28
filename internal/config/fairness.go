package config

import "github.com/nickstrad/mini-gateway/internal/admission"

// Fairness holds the JSON "fairness" section configuring per-tenant share weights.
type Fairness struct {
	DefaultWeight int          `json:"default_weight"`
	Weights       []FairWeight `json:"weights"`
}

// FairWeight configures one tenant's relative share weight in the JSON config.
type FairWeight struct {
	Tenant string `json:"tenant"`
	Weight int    `json:"weight"`
}

func (f *File) applyFairnessDefaults() {
	if f.Fairness.DefaultWeight == 0 {
		f.Fairness.DefaultWeight = admission.DefaultWeight
	}
}

func (f *File) fairness() admission.FairConfig {
	c := admission.FairConfig{DefaultWeight: f.Fairness.DefaultWeight}
	for _, w := range f.Fairness.Weights {
		c.Weights = append(c.Weights, admission.TenantWeight{Tenant: w.Tenant, Weight: w.Weight})
	}
	return c
}
