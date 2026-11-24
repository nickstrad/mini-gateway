package clean

import (
	"fmt"
	"strings"
)

type tree struct{ kids []tree }

func A(x *int) string {
	return fmt.Sprintf("%v %d %%p %5.2s", *x, *x, "ab")
}

func B(b *strings.Builder, t tree, err error, v any, s fmt.Stringer) {
	fmt.Fprintf(b, "%d %v %v %v %v", 1, t, err, v, s)
}

func C(s string) (int, error) {
	var n int
	_, err := fmt.Sscan(s, &n)
	return n, err
}

func D[T any](x T) string { return fmt.Sprint(x) }

func E(b []byte, xs []string) []byte { return fmt.Append(b, xs) }
