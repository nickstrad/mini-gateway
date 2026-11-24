package obstest

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
)

func PlainProblems(v any) []string {
	var out []string
	walkType(reflect.TypeOf(v), "", map[reflect.Type]bool{}, &out)
	return out
}

func walkType(t reflect.Type, path string, seen map[reflect.Type]bool, out *[]string) {
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Bool, reflect.String:
	case reflect.Array, reflect.Slice:
		walkType(t.Elem(), path+"[]", seen, out)
	case reflect.Struct:
		if t.PkgPath() == "time" {
			*out = append(*out, path+": time value")
			return
		}
		if seen[t] {
			return
		}
		seen[t] = true
		for i := 0; i < t.NumField(); i++ {
			walkType(t.Field(i).Type, join(path, t.Field(i).Name), seen, out)
		}
	default:
		*out = append(*out, fmt.Sprintf("%s: %s is not a plain snapshot type", path, t.Kind()))
	}
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

func RequirePlain(t testing.TB, v any) {
	t.Helper()
	if p := PlainProblems(v); len(p) > 0 {
		t.Fatalf("snapshot %T is not plain: %v", v, p)
	}
}

func ZeroCounters(v any) []string {
	var out []string
	walkValue(reflect.ValueOf(v), "", &out)
	slices.Sort(out)
	return out
}

func walkValue(v reflect.Value, path string, out *[]string) {
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if v.Int() == 0 {
			*out = append(*out, path)
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if v.Uint() == 0 {
			*out = append(*out, path)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			walkValue(v.Field(i), join(path, v.Type().Field(i).Name), out)
		}
	}
}

func MergeMoved(dst map[string]bool, v any) {
	all := map[string]bool{}
	collect(reflect.ValueOf(v), "", all)
	zero := map[string]bool{}
	for _, z := range ZeroCounters(v) {
		zero[z] = true
	}
	for name := range all {
		if !zero[name] {
			dst[name] = true
		} else if _, ok := dst[name]; !ok {
			dst[name] = false
		}
	}
}

func collect(v reflect.Value, path string, out map[string]bool) {
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		out[path] = true
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			collect(v.Field(i), join(path, v.Type().Field(i).Name), out)
		}
	}
}

func NeverMoved(moved map[string]bool) []string {
	var out []string
	for name, ok := range moved {
		if !ok {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}
