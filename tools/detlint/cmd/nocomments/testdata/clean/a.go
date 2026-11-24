//go:build linux || darwin

package a

import _ "embed"

//go:embed a.go
var self string

//go:generate echo generate
//go:noinline
func F() int {
	return len(self)
}
