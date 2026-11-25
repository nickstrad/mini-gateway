package simtls

import (
	"bytes"
	"time"
)

const (
	HelloLen         = 16
	recordHandshake  = 0x16
	recordAlert      = 0x15
	kindOK           = 'O'
	kindBadCert      = 'C'
	kindAlert        = 'A'
	Version13        = 0x04
	Version12        = 0x03
	DefaultHandshake = 2 * time.Millisecond
	replyKindAt      = 10
	replyVersionAt   = 11
	badHelloLastByte = 0xff
)

var helloBytes = [HelloLen]byte{recordHandshake, 0x03, 0x01, 'S', 'I', 'M', 'T', 'L', 'S', '-', 'H', 'E', 'L', 'L', 'O', 0}

var replyPrefix = []byte("SIMTLS-")

type Reply uint8

const (
	ReplyOK Reply = iota + 1
	ReplyBadCertificate
	ReplyAlert
)

func AppendClientHello(dst []byte) []byte { return append(dst, helloBytes[:]...) }

func AppendBadClientHello(dst []byte) []byte {
	dst = append(dst, helloBytes[:HelloLen-1]...)
	return append(dst, badHelloLastByte)
}

func ValidClientHello(b []byte) bool { return bytes.Equal(b, helloBytes[:]) }

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
