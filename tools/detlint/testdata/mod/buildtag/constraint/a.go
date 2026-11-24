//go:build never

package constraint

import "time"

func F() int64 { return time.Now().Unix() }
