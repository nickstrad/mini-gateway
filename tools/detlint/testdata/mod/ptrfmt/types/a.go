package types

import (
	"fmt"
	"strings"
)

type node struct {
	next *node
	val  int
}

type wrap struct{ inner []map[string]node }

func A(p *int) string { return fmt.Sprint(p) }

func B(n node) string { return fmt.Sprintf("%v", n) }

func C(w wrap) string { return fmt.Sprintln(w) }

func D(f func()) string { return fmt.Sprint(f) }

func E(u uintptr) string { return fmt.Sprint(u) }

func F(b *strings.Builder, xs []*int) {
	fmt.Fprint(b,
		xs)
}

func G() func(string, ...any) string { return fmt.Sprintf }

var H = fmt.Errorf

func I[T ~*int](p T) string { return fmt.Sprint(p) }

func pair() (int, *int) { return 0, nil }

func J() string { return fmt.Sprint(pair()) }
