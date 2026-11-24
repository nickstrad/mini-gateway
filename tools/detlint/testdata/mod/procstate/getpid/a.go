package getpid

import "os"

func ID() int {
	return os.Getpid()
}
