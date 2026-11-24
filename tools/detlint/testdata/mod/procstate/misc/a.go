package misc

import (
	"reflect"
	"runtime"
	"unsafe"
)

func CPUs() int {
	return runtime.NumCPU()
}

func Addr(v any) uintptr {
	return reflect.ValueOf(v).Pointer()
}

func Size() uintptr {
	return unsafe.Sizeof(0)
}
