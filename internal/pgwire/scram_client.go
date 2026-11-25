package pgwire

import (
	"strconv"
	"strings"
)

type ScramClient struct {
	user            string
	password        string
	clientNonce     string
	gs2             string
	step            scramStep
	clientFirstBare string
	serverSig       [ScramKeyLen]byte
}

func NewScramClient(user, password, clientNonce, gs2 string) *ScramClient {
	return &ScramClient{user: user, password: password, clientNonce: clientNonce, gs2: gs2}
}

func (c *ScramClient) First() []byte {
	c.clientFirstBare = "n=" + c.user + ",r=" + c.clientNonce
	return []byte(c.gs2 + c.clientFirstBare)
}

func (c *ScramClient) Final(serverFirst []byte) ([]byte, AuthCause) {
	if c.step != scramAwaitFirst {
		return nil, AuthCauseMalformed
	}
	c.step = scramDone
	sf := string(serverFirst)
	attrs, ok := parseAttrs(sf)
	if !ok || len(attrs) < 3 || attrs[0].name != 'r' || attrs[1].name != 's' || attrs[2].name != 'i' {
		return nil, AuthCauseMalformed
	}
	nonce := attrs[0].value
	if !validNonce(nonce) || len(nonce) <= len(c.clientNonce) || !strings.HasPrefix(nonce, c.clientNonce) {
		return nil, AuthCauseNonceMismatch
	}
	salt, err := b64.DecodeString(attrs[1].value)
	if err != nil || len(salt) == 0 {
		return nil, AuthCauseMalformed
	}
	iters, err := strconv.Atoi(attrs[2].value)
	if err != nil || iters < MinScramIterations || iters > MaxScramIterations {
		return nil, AuthCauseMalformed
	}
	salted, err := SaltedPassword(c.password, salt, iters)
	if err != nil {
		return nil, AuthCauseMalformed
	}
	clientKey := scramHMAC(salted, "Client Key")
	serverKey := scramHMAC(salted, "Server Key")
	storedKey := sha256Sum(clientKey[:])
	withoutProof := "c=" + ChannelBindingValue(c.gs2) + ",r=" + nonce
	am := authMessage(c.clientFirstBare, sf, withoutProof)
	signature := scramHMAC(storedKey[:], am)
	proof := xorKey(clientKey, signature)
	c.serverSig = scramHMAC(serverKey[:], am)
	c.step = scramAwaitFinal
	return []byte(withoutProof + ",p=" + b64.EncodeToString(proof[:])), AuthCauseNone
}

func (c *ScramClient) Verify(serverFinal []byte) AuthCause {
	if c.step != scramAwaitFinal {
		return AuthCauseMalformed
	}
	c.step = scramDone
	sf := string(serverFinal)
	if strings.HasPrefix(sf, "e=") {
		return AuthCauseServerRejected
	}
	attrs, ok := parseAttrs(sf)
	if !ok || len(attrs) < 1 || attrs[0].name != 'v' {
		return AuthCauseMalformed
	}
	got, err := b64.DecodeString(attrs[0].value)
	if err != nil || !equalKeys(got, c.serverSig[:]) {
		return AuthCauseServerSignature
	}
	return AuthCauseNone
}
