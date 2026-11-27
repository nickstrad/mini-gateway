// Package certtest generates ephemeral ECDSA test CAs and server certificates
// using only the Go standard library.
package certtest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// CA holds an ephemeral self-signed test certificate authority.
type CA struct {
	Cert *x509.Certificate
	Key  *ecdsa.PrivateKey
	PEM  []byte
}

func nextSerial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), serialBits))
	if err != nil {
		return big.NewInt(time.Now().UnixNano())
	}
	return n.Add(n, big.NewInt(1))
}

const serialBits = 62

// validity starts in 1990 so certificates remain valid inside testing/synctest's
// synthetic clock epoch (2000-01-01) as well as real wall-clock runs.
func validity() (time.Time, time.Time) {
	return time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC), time.Now().Add(7 * 24 * time.Hour)
}

// NewCA creates a fresh self-signed CA with CommonName name.
func NewCA(t testing.TB, name string) *CA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	from, until := validity()
	tmpl := &x509.Certificate{SerialNumber: nextSerial(), Subject: pkix.Name{CommonName: name}, NotBefore: from,
		NotAfter: until, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &CA{Cert: cert, Key: key, PEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// Pool returns an x509.CertPool trusting only ca.
func (ca *CA) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca.Cert)
	return p
}

// Leaf holds a signed server certificate and private key in both tls and PEM form.
type Leaf struct {
	TLS     tls.Certificate
	CertPEM []byte
	KeyPEM  []byte
}

// Issue signs a server certificate for the given DNS names or IP addresses.
func (ca *CA) Issue(t testing.TB, names ...string) Leaf {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	from, until := validity()
	tmpl := &x509.Certificate{SerialNumber: nextSerial(), Subject: pkix.Name{CommonName: names[0]}, NotBefore: from,
		NotAfter: until, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	for _, n := range names {
		if ip := net.ParseIP(n); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, n)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, &key.PublicKey, ca.Key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	l := Leaf{CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		KeyPEM: pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})}
	l.TLS, err = tls.X509KeyPair(l.CertPEM, l.KeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// WriteFiles writes the leaf certificate and private key PEM files into dir.
func (l Leaf) WriteFiles(t testing.TB, dir, certName, keyName string) (string, string) {
	t.Helper()
	certPath, keyPath := filepath.Join(dir, certName), filepath.Join(dir, keyName)
	WriteFile(t, certPath, l.CertPEM)
	WriteFile(t, keyPath, l.KeyPEM)
	return certPath, keyPath
}

// WriteFile writes b to path with mode 0600, failing the test on error.
func WriteFile(t testing.TB, path string, b []byte) {
	t.Helper()
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}
