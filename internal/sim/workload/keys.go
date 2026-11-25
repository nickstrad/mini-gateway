package workload

import (
	"math/rand/v2"
	"strconv"

	"github.com/nickstrad/mini-gateway/internal/randdist"
)

// KeyChooser draws kv table keys according to a Keys configuration (uniform,
// Zipf, or fixed) from an isolated random stream.
type KeyChooser struct {
	cfg  Keys
	r    *rand.Rand
	zipf *rand.Zipf
}

// NewKeyChooser constructs a KeyChooser backed by r.
func NewKeyChooser(r *rand.Rand, k Keys) *KeyChooser {
	kc := &KeyChooser{cfg: k, r: r}
	if k.Kind == KeysZipf && k.N > 0 && k.Zipf > 1000 {
		kc.zipf = randdist.NewZipf(r, k.Zipf, k.N)
	}
	return kc
}

// Index returns the next integer key index in [0, N), or 0 for fixed/empty configs.
func (kc *KeyChooser) Index() int {
	switch {
	case kc.cfg.Kind == KeysFixed || kc.cfg.N <= 0:
		return 0
	case kc.zipf != nil:
		return int(kc.zipf.Uint64())
	}
	return kc.r.IntN(kc.cfg.N)
}

// Next returns the next key name string.
func (kc *KeyChooser) Next() string {
	if kc.cfg.Kind == KeysFixed {
		return kc.cfg.Fixed
	}
	return KeyName(kc.Index())
}

// KeyName formats integer key index i as "<DefaultKeyPrefix><i>".
func KeyName(i int) string { return DefaultKeyPrefix + strconv.Itoa(i) }
