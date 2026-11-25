// Package simtls defines fixed-size 16-byte handshake tokens exchanged over
// simnet to model TLS upgrade outcomes and virtual durations without running
// real cryptography inside the deterministic simulator.
package simtls

import (
	"bytes"
	"time"
)

// Wire layout and protocol constants for simulated TLS handshake tokens.
const (
	// HelloLen is the fixed byte length of both ClientHello and ServerReply tokens.
	HelloLen        = 16
	recordHandshake = 0x16
	recordAlert     = 0x15
	kindOK          = 'O'
	kindBadCert     = 'C'
	kindAlert       = 'A'
	// Version13 encodes a simulated TLS 1.3 handshake reply.
	Version13 = 0x04
	// Version12 encodes a simulated TLS 1.2 handshake reply.
	Version12 = 0x03
	// DefaultHandshake is the default virtual duration of a simulated handshake.
	DefaultHandshake = 2 * time.Millisecond
	replyKindAt      = 10
	replyVersionAt   = 11
	badHelloLastByte = 0xff
)

var helloBytes = [HelloLen]byte{recordHandshake, 0x03, 0x01, 'S', 'I', 'M', 'T', 'L', 'S', '-', 'H', 'E', 'L', 'L', 'O', 0}

var replyPrefix = []byte("SIMTLS-")

// Reply classifies the server's simulated TLS handshake response token.
type Reply uint8

// Simulated server handshake outcomes.
const (
	ReplyOK Reply = iota + 1
	ReplyBadCertificate
	ReplyAlert
)

// AppendClientHello appends a valid 16-byte simulated ClientHello token to dst.
func AppendClientHello(dst []byte) []byte { return append(dst, helloBytes[:]...) }

// AppendBadClientHello appends a 16-byte ClientHello token that servers reject
// with ReplyAlert to exercise failed handshakes.
func AppendBadClientHello(dst []byte) []byte {
	dst = append(dst, helloBytes[:HelloLen-1]...)
	return append(dst, badHelloLastByte)
}

// ValidClientHello reports whether b equals the valid 16-byte ClientHello token.
func ValidClientHello(b []byte) bool { return bytes.Equal(b, helloBytes[:]) }

// AppendServerReply appends a 16-byte simulated server handshake token carrying
// reply kind r and negotiated TLS version byte.
func AppendServerReply(dst []byte, r Reply, version byte) []byte {
	rec, kind := byte(recordHandshake), byte(kindOK)
	switch r {
	case ReplyBadCertificate:
		kind = kindBadCert
	case ReplyAlert:
		rec, kind, version = recordAlert, kindAlert, 0
	}
	out := make([]byte, HelloLen)
	out[0], out[1], out[2] = rec, 0x03, 0x03
	copy(out[3:], replyPrefix)
	out[replyKindAt] = kind
	out[replyVersionAt] = version
	return append(dst, out...)
}

// ParseServerReply decodes a 16-byte simulated server handshake token into its
// Reply outcome and TLS version byte.
func ParseServerReply(b []byte) (Reply, byte, bool) {
	if len(b) != HelloLen || !bytes.Equal(b[3:3+len(replyPrefix)], replyPrefix) {
		return 0, 0, false
	}
	switch {
	case b[0] == recordHandshake && b[replyKindAt] == kindOK:
		return ReplyOK, b[replyVersionAt], true
	case b[0] == recordHandshake && b[replyKindAt] == kindBadCert:
		return ReplyBadCertificate, b[replyVersionAt], true
	case b[0] == recordAlert && b[replyKindAt] == kindAlert:
		return ReplyAlert, 0, true
	}
	return 0, 0, false
}
