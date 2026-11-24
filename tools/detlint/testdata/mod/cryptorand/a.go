package cryptorand

import "crypto/rand"

func Fill(b []byte) {
	rand.Read(b)
}
