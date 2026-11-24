// Scratch probe (not product code): what does client-facing TLS termination cost in front of Postgres?
// Standard library only. A terminating relay: prelude loop (SSLRequest / GSSENCRequest), crypto/tls on the
// client leg, optional TLS on the backend leg, then a byte relay. Also a handshake CPU-cost measurement.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"syscall"
	"time"
)

const (
	sslRequest    = 80877103
	gssEncRequest = 80877104
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

// makeCert returns a CA pool and a server certificate for localhost/127.0.0.1 signed by it.
func makeCert(useRSA bool) (*x509.CertPool, tls.Certificate, []byte) {
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caT := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "mg-probe-ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true}
	caDER, err := x509.CreateCertificate(rand.Reader, caT, caT, &caKey.PublicKey, caKey)
	must(err)
	caCert, _ := x509.ParseCertificate(caDER)
	var pub, priv any
	if useRSA {
		k, _ := rsa.GenerateKey(rand.Reader, 2048)
		pub, priv = &k.PublicKey, k
	} else {
		k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		pub, priv = &k.PublicKey, k
	}
	t := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, t, caCert, pub, caKey)
	must(err)
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	return pool, tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}, caPEM
}

// --- TLS-SPECIFIC BEGIN (client leg) ---
// prelude reads the fixed 8-byte requests that may precede the startup message. It reads exactly 8 bytes at
// a time straight from the socket, never through a buffer, so nothing sent in plaintext after an SSLRequest
// can be mistaken for data received inside TLS (the CVE-2021-23214 class of bug).
func prelude(c net.Conn, cfg *tls.Config) (net.Conn, []byte, error) {
	secured := false
	for {
		var h [8]byte
		if _, err := io.ReadFull(c, h[:]); err != nil {
			return nil, nil, err
		}
		n, code := binary.BigEndian.Uint32(h[:4]), binary.BigEndian.Uint32(h[4:])
		switch {
		case n == 8 && code == sslRequest && !secured && cfg != nil:
			if _, err := c.Write([]byte{'S'}); err != nil {
				return nil, nil, err
			}
			t := tls.Server(c, cfg)
			start := time.Now()
			if err := t.Handshake(); err != nil {
				return nil, nil, fmt.Errorf("tls handshake: %w", err)
			}
			st := t.ConnectionState()
			fmt.Printf("  client leg: TLS %s %s, handshake %s\n", tls.VersionName(st.Version),
				tls.CipherSuiteName(st.CipherSuite), time.Since(start).Round(100*time.Microsecond))
			c, secured = t, true
		case n == 8 && (code == sslRequest || code == gssEncRequest):
			if _, err := c.Write([]byte{'N'}); err != nil {
				return nil, nil, err
			}
		default: // a startup message or a cancel request: hand the 8 bytes on
			if !secured {
				fmt.Println("  client leg: plaintext")
			}
			return c, h[:], nil
		}
	}
}

// --- TLS-SPECIFIC END (client leg) ---

// --- TLS-SPECIFIC BEGIN (backend leg) ---
func dialBackend(addr string, useTLS bool) (net.Conn, error) {
	b, err := net.Dial("tcp", addr)
	if err != nil || !useTLS {
		return b, err
	}
	var req [8]byte
	binary.BigEndian.PutUint32(req[:4], 8)
	binary.BigEndian.PutUint32(req[4:], sslRequest)
	if _, err := b.Write(req[:]); err != nil {
		return nil, err
	}
	var ans [1]byte
	if _, err := io.ReadFull(b, ans[:]); err != nil {
		return nil, err
	}
	if ans[0] != 'S' {
		return nil, fmt.Errorf("backend refused TLS: %q", ans[0])
	}
	// The probe's Postgres uses the image's self-signed certificate, so verification is off here.
	// verify-full is the same call with RootCAs and ServerName set.
	t := tls.Client(b, &tls.Config{InsecureSkipVerify: true})
	if err := t.Handshake(); err != nil {
		return nil, err
	}
	fmt.Printf("  backend leg: TLS %s\n", tls.VersionName(t.ConnectionState().Version))
	return t, nil
}

// --- TLS-SPECIFIC END (backend leg) ---

// tap prints the GS2 header of the client's first SASL message (how the client says whether it wants channel binding).
func tap(dst io.Writer, src io.Reader) {
	buf := make([]byte, 32*1024)
	seen := false
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if !seen && buf[0] == 'p' && n > 30 {
				seen = true
				m := buf[5:n]
				for i, ch := range m { // mechanism name is NUL-terminated, then int32 length, then the data
					if ch == 0 {
						fmt.Printf("  client SASL mechanism %q, GS2 header %q\n", m[:i], m[i+5:i+8])
						break
					}
				}
			}
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func serve(listen, backend string, cfg *tls.Config, backendTLS bool) {
	ln, err := net.Listen("tcp", listen)
	must(err)
	for {
		c, err := ln.Accept()
		must(err)
		go func() {
			defer c.Close()
			fmt.Println("connection:")
			c.SetDeadline(time.Now().Add(5 * time.Second)) // one deadline covers prelude + handshake
			cc, first, err := prelude(c, cfg)
			if err != nil {
				fmt.Println("  closed in prelude:", err)
				return
			}
			c.SetDeadline(time.Time{})
			b, err := dialBackend(backend, backendTLS)
			if err != nil {
				fmt.Println("  backend:", err)
				return
			}
			defer b.Close()
			b.Write(first)
			go tap(b, cc)
			io.Copy(cc, b)
		}()
	}
}

func cpu() time.Duration {
	var ru syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

// bench measures CPU per full handshake (client and server in this one process, so it is an upper bound for
// the server's half) over loopback TCP.
func bench(name string, useRSA bool, n int) {
	pool, cert, _ := makeCert(useRSA)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	scfg := &tls.Config{Certificates: []tls.Certificate{cert}}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { t := tls.Server(c, scfg); t.Handshake(); io.Copy(io.Discard, t); t.Close() }()
		}
	}()
	ccfg := &tls.Config{RootCAs: pool, ServerName: "localhost"}
	c0, w0 := cpu(), time.Now()
	for i := 0; i < n; i++ {
		c, err := tls.Dial("tcp", ln.Addr().String(), ccfg)
		must(err)
		c.Close()
	}
	fmt.Printf("  %-12s %d full handshakes: %.2f ms CPU each (client+server), %.2f ms wall each\n", name, n,
		float64(cpu()-c0)/float64(n)/1e6, float64(time.Since(w0))/float64(n)/1e6)
	ln.Close()
}

func garbage(addr string) {
	c, err := net.Dial("tcp", addr)
	must(err)
	defer c.Close()
	var req [8]byte
	binary.BigEndian.PutUint32(req[:4], 8)
	binary.BigEndian.PutUint32(req[4:], sslRequest)
	// SSLRequest and a plaintext startup message in ONE write: bytes smuggled in before the handshake.
	startup := []byte{0, 0, 0, 23, 0, 3, 0, 0, 'u', 's', 'e', 'r', 0, 'p', 'o', 's', 't', 'g', 'r', 'e', 's', 0, 0}
	c.Write(append(req[:], startup...))
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 64)
	n, _ := c.Read(buf)
	m, err := c.Read(buf[n:])
	fmt.Printf("  smuggled plaintext after SSLRequest: proxy answered %q then %d more bytes, err=%v\n", buf[:n], m, err)
}

func main() {
	mode := flag.String("mode", "serve", "serve | bench | garbage")
	listen := flag.String("listen", "127.0.0.1:6543", "")
	backend := flag.String("backend", "127.0.0.1:55432", "")
	backendTLS := flag.Bool("backend-tls", false, "")
	noTLS := flag.Bool("no-client-tls", false, "answer N to SSLRequest")
	caOut := flag.String("ca-out", "", "")
	flag.Parse()
	switch *mode {
	case "bench":
		bench("ECDSA P-256", false, 300)
		bench("RSA 2048", true, 300)
	case "garbage":
		garbage(*listen)
	default:
		_, cert, caPEM := makeCert(false)
		if *caOut != "" {
			must(os.WriteFile(*caOut, caPEM, 0o644))
		}
		var cfg *tls.Config
		if !*noTLS {
			cfg = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
		}
		serve(*listen, *backend, cfg, *backendTLS)
	}
}
