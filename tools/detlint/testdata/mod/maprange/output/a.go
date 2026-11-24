package output

import "fmt"

func Dump(m map[string]int) string {
	out := ""
	for k, v := range m {
		out += fmt.Sprint(k, v)
	}
	return out
}

func Generic[M ~map[string]int](m M) int {
	n := 0
	for _, v := range m {
		n = n*31 + v
	}
	return n
}
