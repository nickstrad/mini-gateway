package admsrc_test

import (
	"fmt"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/admission"
)

// TestARealTenantNamedOtherPublishesOnceAfterTheFold proves a real "other" tenant shares the
// fold-over bucket past MaxTenantStats without triggering duplicate points in obs.Collector.
func TestARealTenantNamedOtherPublishesOnceAfterTheFold(t *testing.T) {
	q, err := admission.New(admission.Config{Rule: admission.RuleTimeout, Capacity: 64, TenantCapacity: 1, MaxTenants: 8, Detail: true})
	if err != nil {
		t.Fatal(err)
	}
	now := admission.Time(1)
	q.Push(now, admission.OtherTenant, 1)
	q.Pop(now)
	for i := range admission.MaxTenantStats + 2 {
		now++
		q.Push(now, fmt.Sprintf("churn-%d", i), int64(10+i))
		if i%2 == 0 {
			q.Pop(now)
		}
	}
	if err := q.Check(); err != nil {
		t.Fatal(err)
	}
	collect(t, q, true)
}
