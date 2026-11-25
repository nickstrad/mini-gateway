package pgwire

import (
	"crypto/sha256"
	"strconv"
	"strings"
)

type scramStep uint8

const (
	scramAwaitFirst scramStep = iota
	scramAwaitFinal
	scramDone
)

type ScramServer struct {
	v               Verifier
	mock            bool
	serverNonce     string
	step            scramStep
	gs2             string
	nonce           string
	clientFirstBare string
	serverFirst     string
}

func NewScramServer(v Verifier, serverNonce string, unknownUser bool) *ScramServer {
	return &ScramServer{v: v, serverNonce: serverNonce, mock: unknownUser}
}

func (s *ScramServer) GS2() string { return s.gs2 }

func (s *ScramServer) First(mechanism string, data []byte) ([]byte, AuthCause) {
	if s.step != scramAwaitFirst {
		return nil, AuthCauseMalformed
	}
	s.step = scramDone
	if mechanism != ScramMechanism {
		return nil, AuthCauseBadMechanism
	}
	msg := string(data)
	if strings.HasPrefix(msg, "p=") {
		return nil, AuthCauseChannelBindingRefused
	}
	gs2, bare, ok := splitGS2(msg)
	if !ok {
		return nil, AuthCauseMalformed
	}
	attrs, ok := parseAttrs(bare)
	if !ok || len(attrs) < 2 || attrs[0].name != 'n' || attrs[1].name != 'r' || !validNonce(attrs[1].value) {
		return nil, AuthCauseMalformed
	}
	s.gs2 = gs2
	s.clientFirstBare = bare
	s.nonce = attrs[1].value + s.serverNonce
	s.serverFirst = "r=" + s.nonce + ",s=" + b64.EncodeToString(s.v.Salt) + ",i=" + strconv.Itoa(s.v.Iterations)
	s.step = scramAwaitFinal
	return []byte(s.serverFirst), AuthCauseNone
}

func splitGS2(msg string) (string, string, bool) {
	if len(msg) < 3 || (msg[0] != 'n' && msg[0] != 'y') || msg[1] != ',' {
		return "", "", false
	}
	authz, bare, ok := strings.Cut(msg[2:], ",")
	if !ok || authz != "" {
		return "", "", false
	}
	return msg[:3], bare, true
}

func (s *ScramServer) Final(data []byte) ([]byte, AuthCause) {
	if s.step != scramAwaitFinal {
		return nil, AuthCauseMalformed
	}
	s.step = scramDone
	msg := string(data)
	cut := strings.LastIndex(msg, ",p=")
	if cut < 0 {
		return nil, AuthCauseMalformed
	}
	withoutProof := msg[:cut]
	attrs, ok := parseAttrs(withoutProof)
	if !ok || len(attrs) < 2 || attrs[0].name != 'c' || attrs[1].name != 'r' {
		return nil, AuthCauseMalformed
	}
	proof, err := b64.DecodeString(msg[cut+3:])
	if err != nil || len(proof) != ScramKeyLen {
		return nil, AuthCauseMalformed
	}
	if attrs[0].value != ChannelBindingValue(s.gs2) {
		return nil, AuthCauseChannelBindingMismatch
	}
	if attrs[1].value != s.nonce {
		return nil, AuthCauseNonceMismatch
	}
	am := authMessage(s.clientFirstBare, s.serverFirst, withoutProof)
	signature := scramHMAC(s.v.StoredKey[:], am)
	var given [ScramKeyLen]byte
	copy(given[:], proof)
	clientKey := xorKey(given, signature)
	stored := sha256.Sum256(clientKey[:])
	match := equalKeys(stored[:], s.v.StoredKey[:])
	if s.mock {
		return nil, AuthCauseUnknownUser
	}
	if !match {
		return nil, AuthCauseWrongPassword
	}
	serverSig := scramHMAC(s.v.ServerKey[:], am)
	return []byte("v=" + b64.EncodeToString(serverSig[:])), AuthCauseNone
}
