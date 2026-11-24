package atomic

import "sync/atomic"

var n atomic.Int64

func Inc() int64 {
	return n.Add(1)
}
