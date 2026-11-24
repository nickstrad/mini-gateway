package aliasv2

import r "math/rand/v2"

func Pick(n int) int {
	return r.IntN(n)
}

func Dur() int64 {
	return r.N[int64](10)
}
