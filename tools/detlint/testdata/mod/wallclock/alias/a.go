package alias

import t "time"

func F() int64 {
	return t.Now().UnixNano()
}
