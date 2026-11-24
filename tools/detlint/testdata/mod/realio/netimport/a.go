package netimport

import "net"

func Parse(s string) net.IP {
	return net.ParseIP(s)
}
