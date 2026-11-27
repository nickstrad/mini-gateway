// Property and regression tests for integer histogram bucket boundaries,
// merge associativity, clamp accounting, and zero-allocation recording.
package obs_test

import (
	"math"
	"math/big"
	"reflect"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/sim"
)

const c01Seed int64 = 731921

func c01Mark(t *testing.T) {
	t.Helper()
	t.Logf("seed=%d; REPLAY: go test -count=1 ./internal/obs -run '^%s$' -v", c01Seed, t.Name())
}

func c01Must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func c01Histogram(t *testing.T) *obs.Histogram {
	t.Helper()
	h, e := obs.NewHistogram(0, obs.MaxValue)
	c01Must(t, e)
	return h
}

func TestAdvC01_8_PowerBoundaryError(t *testing.T) {
	c01Mark(t)
	h := c01Histogram(t)
	for k := 8; k <= 62; k++ {
		for d := int64(-2); d <= 2; d++ {
			v := (int64(1) << k) + d
			if v > obs.MaxValue {
				continue
			}
			h.Reset()
			h.Record(v)
			q := h.Quantile(9999)
			delta := new(big.Int).Mul(big.NewInt(q-v), big.NewInt(128))
			if q < v || delta.Cmp(big.NewInt(v)) > 0 {
				t.Fatalf("v=%d q=%d exceeds 1/128", v, q)
			}
		}
	}
}

func TestAdvC01_8_MergeTreeAndClamps(t *testing.T) {
	c01Mark(t)
	a, b, c, ref := c01Histogram(t), c01Histogram(t), c01Histogram(t), c01Histogram(t)
	r := sim.NewStream(c01Seed, "hist")
	for i := 0; i < 10000; i++ {
		v := r.Int64()
		if i%5 == 0 {
			v = -v
		}
		n := int64(i%9 + 1)
		[]*obs.Histogram{a, b, c}[i%3].RecordN(v, n)
		ref.RecordN(v, n)
	}
	c01Must(t, b.Merge(c))
	c01Must(t, a.Merge(b))
	if !reflect.DeepEqual(a.Snapshot(), ref.Snapshot()) {
		t.Fatal("merge tree disagrees with direct records")
	}
	if a.ClampedLow() == 0 || a.ClampedHigh() == 0 {
		t.Fatal("clamps did not move")
	}
}

func TestAdvC01_8_AllocationAndHugeRank(t *testing.T) {
	c01Mark(t)
	h := c01Histogram(t)
	if a := testing.AllocsPerRun(1000, func() { h.Record(123456789) }); a != 0 {
		t.Fatalf("record allocations=%g", a)
	}
	h.Reset()
	h.RecordN(0, math.MaxInt64/2)
	h.RecordN(obs.MaxValue, math.MaxInt64/2)
	if h.Quantile(5000) != 0 || h.Quantile(5001) != obs.MaxValue {
		t.Fatalf("large rank wrong: median=%d upper=%d", h.Quantile(5000), h.Quantile(5001))
	}
}
