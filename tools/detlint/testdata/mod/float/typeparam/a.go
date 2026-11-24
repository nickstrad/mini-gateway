package typeparam

import "cmp"

func Less[T ~float64](a, b T) bool { return a < b }

func Max[T cmp.Ordered](a, b T) T { return max(a, b) }

type Number interface{ ~int | ~float64 }

func Sum[T Number](xs []T) T {
	var s T
	for _, x := range xs {
		s += x
	}
	return s
}

func Nested[T interface{ Number }](a T) T { return a }
