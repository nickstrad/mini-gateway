package generic

type Integer interface{ ~int | ~int64 | ~uint8 }

func Min[T ~int | ~string](a, b T) T { return min(a, b) }

func Sum[T Integer](xs []T) T {
	var s T
	for _, x := range xs {
		s += x
	}
	return s
}

func Eq[T comparable](a, b T) bool { return a == b }

func Id[T any](x T) T { return x }
