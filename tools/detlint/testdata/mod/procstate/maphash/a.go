package maphash

import "hash/maphash"

func Sum(b []byte) uint64 {
	return maphash.Bytes(maphash.MakeSeed(), b)
}
