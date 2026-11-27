package mgctl

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
)

// ClientTLS builds a *tls.Config for client TLS modes "off", "require", and
// "verify-full" (requiring at least TLS 1.2). It returns (nil, nil) for "off".
func ClientTLS(mode, caFile, serverName, addr string) (*tls.Config, error) {
	switch mode {
	case "off", "":
		return nil, nil
	case "require":
		return &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}, nil
	case "verify-full":
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("-ca-file: %v", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("-ca-file %s: no certificate", caFile)
		}
		if serverName == "" {
			serverName, _, _ = net.SplitHostPort(addr)
		}
		return &tls.Config{RootCAs: pool, ServerName: serverName, MinVersion: tls.VersionTLS12}, nil
	}
	return nil, fmt.Errorf("-tls %q: want off, require or verify-full", mode)
}
