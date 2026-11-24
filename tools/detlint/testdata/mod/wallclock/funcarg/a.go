package funcarg

import "time"

func call(g func() time.Time) time.Time {
	return g()
}

func F() time.Time {
	return call(time.Now)
}
