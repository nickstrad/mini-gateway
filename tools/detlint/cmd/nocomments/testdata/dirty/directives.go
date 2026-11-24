package dirty

//go:nosplit
func D() {}

//go:linkname x runtime.x
//go:generate echo hi
//go:embedx
//line foo.go:1
var x int
