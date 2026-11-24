package methodvalue

import "time"

func F() int64 {
	f := time.Now
	return f().Unix()
}
