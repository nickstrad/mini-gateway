package osfile

import "os"

func Load(p string) ([]byte, error) {
	return os.ReadFile(p)
}
