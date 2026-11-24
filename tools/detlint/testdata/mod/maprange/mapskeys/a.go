package mapskeys

import "maps"

func First(m map[string]int) string {
	for k := range maps.Keys(m) {
		return k
	}
	return ""
}
