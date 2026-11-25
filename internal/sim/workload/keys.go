package workload

import (
	"math/rand/v2"
	"strconv"

	"github.com/nickstrad/mini-gateway/internal/randdist"
)

type KeyChooser struct {
	cfg  Keys
	r    *rand.Rand
	zipf *rand.Zipf
}

func NewKeyChooser(r *rand.Rand, k Keys) *KeyChooser {
	kc := &KeyChooser{cfg: k, r: r}
	if k.Kind == KeysZipf && k.N > 0 && k.Zipf > 1000 {
		kc.zipf = randdist.NewZipf(r, k.Zipf, k.N)
	}
	return kc
}

func (kc *KeyChooser) Index() int {
	switch {
	case kc.cfg.Kind == KeysFixed || kc.cfg.N <= 0:
		return 0
	case kc.zipf != nil:
		return int(kc.zipf.Uint64())
	}
	return kc.r.IntN(kc.cfg.N)
}

func (kc *KeyChooser) Next() string {
	if kc.cfg.Kind == KeysFixed {
		return kc.cfg.Fixed
	}
	return KeyName(kc.Index())
}

func KeyName(i int) string { return DefaultKeyPrefix + strconv.Itoa(i) }
