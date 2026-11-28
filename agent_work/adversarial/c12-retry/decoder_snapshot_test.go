// Decoder under test extracted verbatim from internal/retry/retry_test.go.
// Its body is checked against the current source before adversarial inputs run.
package adv_test

import (
	. "github.com/nickstrad/mini-gateway/internal/retry"
	"github.com/nickstrad/mini-gateway/internal/work"
)

const (
	fuzzOpBytes = 3
	fuzzMaxOps  = 256
	Millisecond = Time(1000000)
	opFail      = 1
)

type op struct {
	kind int
	dt   Time
	f    Failure
	ttl  Time
}

func decodeOps(b []byte) []op {
	n := min(len(b)/fuzzOpBytes, fuzzMaxOps)
	ops := make([]op, n)
	for i := range ops {
		x := b[i*fuzzOpBytes : (i+1)*fuzzOpBytes]
		o := op{dt: Time(x[1]) * Millisecond}
		if x[0]&1 == 1 {
			o.kind = opFail
			o.f = Failure{Reached: work.Tri(x[0] >> 1 & 3), InTx: x[0]&8 != 0, Attempt: int(x[0] >> 4 & 1),
				BreakerOpen: x[0]&32 != 0, Draining: x[0]&64 != 0}
			o.ttl = Time(x[2]) * Millisecond / 2
		}
		ops[i] = o
	}
	return ops
}
