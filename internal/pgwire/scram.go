package pgwire

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// SCRAM-SHA-256 (RFC 7677 / RFC 5802) mechanism names, bounds, and GS2 headers.
const (
	ScramMechanism         = "SCRAM-SHA-256"
	ScramPlusMechanism     = "SCRAM-SHA-256-PLUS"
	DefaultScramIterations = 4096
	MinScramIterations     = 1
	MaxScramIterations     = 1 << 20
	ScramSaltLen           = 16
	ScramKeyLen            = sha256.Size
	ScramNonceBytes        = 18
	// MaxScramMessage bounds every SCRAM payload accepted by ScramServer and ScramClient.
	MaxScramMessage     = 2048
	GS2NoBinding        = "n,,"
	GS2ClientSupports   = "y,,"
	scramVerifierPrefix = "SCRAM-SHA-256$"
	mockSaltLabel       = "pgwire mock salt"
	maxMockSaltLen      = 4 * ScramKeyLen
)

var b64 = base64.StdEncoding

// ErrVerifier is wrapped when a SCRAM verifier or iteration count is invalid.
var ErrVerifier = errors.New("pgwire: bad SCRAM verifier")

// Random supplies deterministic or cryptographic randomness for SCRAM nonces
// and proxy-issued BackendKey values.
type Random interface {
	Uint64() uint64
}

// Verifier holds Postgres's SCRAM-SHA-256 stored credential (salt, iterations,
// StoredKey, ServerKey) and never stores the plaintext password.
type Verifier struct {
	Salt       []byte
	Iterations int
	StoredKey  [ScramKeyLen]byte
	ServerKey  [ScramKeyLen]byte
}

// AuthCause classifies why an authentication exchange failed on either leg.
type AuthCause uint8

// Authentication failure causes indexed into AuthFailures arrays.
const (
	AuthCauseNone AuthCause = iota
	AuthCauseWrongPassword
	AuthCauseUnknownUser
	AuthCauseBadMechanism
	AuthCauseMalformed
	AuthCauseChannelBindingRefused
	AuthCauseChannelBindingMismatch
	AuthCauseNonceMismatch
	AuthCauseServerSignature
	AuthCauseUnsupportedMethod
	AuthCauseServerRejected
	AuthCauseNoScramOffered
	NumAuthCauses
)

var authCauseNames = [NumAuthCauses]string{"none", "wrong-password", "unknown-user", "bad-mechanism", "malformed",
	"channel-binding-refused", "channel-binding-mismatch", "nonce-mismatch", "server-signature",
	"unsupported-method", "server-rejected", "no-scram-offered"}

// String returns the canonical hyphenated name of c.
func (c AuthCause) String() string {
	if c < NumAuthCauses {
		return authCauseNames[c]
	}
	return "unknown"
}

// SaltedPassword derives the PBKDF2-HMAC-SHA256 salted password.
// SASLprep is not implemented: password bytes are used verbatim (ASCII matches
// Postgres; non-ASCII strings that SASLprep would normalize do not).
func SaltedPassword(password string, salt []byte, iterations int) ([]byte, error) {
	if iterations < MinScramIterations || iterations > MaxScramIterations {
		return nil, fmt.Errorf("%w: %d iterations", ErrVerifier, iterations)
	}
	return pbkdf2.Key(sha256.New, password, salt, iterations, ScramKeyLen)
}

func scramHMAC(key []byte, parts ...string) [ScramKeyLen]byte {
	h := hmac.New(sha256.New, key)
	for i, p := range parts {
		if i > 0 {
			h.Write([]byte{','})
		}
		h.Write([]byte(p))
	}
	var out [ScramKeyLen]byte
	h.Sum(out[:0])
	return out
}

// NewVerifier derives a SCRAM-SHA-256 Verifier from password, salt, and
// iterations using PBKDF2-HMAC-SHA256 without retaining the password.
func NewVerifier(password string, salt []byte, iterations int) (Verifier, error) {
	salted, err := SaltedPassword(password, salt, iterations)
	if err != nil {
		return Verifier{}, err
	}
	clientKey := scramHMAC(salted, "Client Key")
	return Verifier{
		Salt:       append([]byte(nil), salt...),
		Iterations: iterations,
		StoredKey:  sha256.Sum256(clientKey[:]),
		ServerKey:  scramHMAC(salted, "Server Key"),
	}, nil
}

// String formats v in Postgres's pg_authid text form
// ("SCRAM-SHA-256$<iterations>:<salt>$<storedKey>:<serverKey>").
func (v Verifier) String() string {
	return scramVerifierPrefix + strconv.Itoa(v.Iterations) + ":" + b64.EncodeToString(v.Salt) + "$" +
		b64.EncodeToString(v.StoredKey[:]) + ":" + b64.EncodeToString(v.ServerKey[:])
}

// ParseVerifier parses Postgres's SCRAM-SHA-256 verifier string format.
func ParseVerifier(s string) (Verifier, error) {
	rest, ok := strings.CutPrefix(s, scramVerifierPrefix)
	if !ok {
		return Verifier{}, fmt.Errorf("%w: no %q prefix", ErrVerifier, scramVerifierPrefix)
	}
	params, keys, ok := strings.Cut(rest, "$")
	iterText, saltText, ok1 := strings.Cut(params, ":")
	storedText, serverText, ok2 := strings.Cut(keys, ":")
	if !ok || !ok1 || !ok2 {
		return Verifier{}, fmt.Errorf("%w: layout", ErrVerifier)
	}
	iters, err := strconv.Atoi(iterText)
	if err != nil || iters < MinScramIterations || iters > MaxScramIterations {
		return Verifier{}, fmt.Errorf("%w: iterations %q", ErrVerifier, iterText)
	}
	salt, err := b64.DecodeString(saltText)
	if err != nil || len(salt) == 0 {
		return Verifier{}, fmt.Errorf("%w: salt", ErrVerifier)
	}
	v := Verifier{Salt: salt, Iterations: iters}
	if !decodeKey(storedText, &v.StoredKey) || !decodeKey(serverText, &v.ServerKey) {
		return Verifier{}, fmt.Errorf("%w: keys", ErrVerifier)
	}
	return v, nil
}

func decodeKey(s string, dst *[ScramKeyLen]byte) bool {
	b, err := b64.DecodeString(s)
	if err != nil || len(b) != ScramKeyLen {
		return false
	}
	copy(dst[:], b)
	return true
}

// MockVerifier deterministically derives a plausible SCRAM verifier for an
// unknown user from secret and user so the server can run a full challenge
// without revealing user existence.
func MockVerifier(secret []byte, user string, iterations int) Verifier {
	return mockVerifier(secret, user, iterations, ScramSaltLen)
}

func mockVerifier(secret []byte, user string, iterations, saltLen int) Verifier {
	if iterations < MinScramIterations || iterations > MaxScramIterations {
		iterations = DefaultScramIterations
	}
	if saltLen < 1 || saltLen > maxMockSaltLen {
		saltLen = ScramSaltLen
	}
	first := scramHMAC(secret, mockSaltLabel, user)
	salt := first[:]
	for block := 1; len(salt) < saltLen; block++ {
		next := scramHMAC(secret, mockSaltLabel, user, strconv.Itoa(block))
		salt = append(salt, next[:]...)
	}
	return Verifier{Salt: salt[:saltLen], Iterations: iterations, StoredKey: scramHMAC(secret, "stored", user),
		ServerKey: scramHMAC(secret, "server", user)}
}

// NewNonce encodes 18 random bytes from r as a 24-character printable base64
// SCRAM nonce (never containing commas).
func NewNonce(r Random) string {
	var raw [ScramNonceBytes]byte
	for i := 0; i < len(raw); i += 8 {
		v := r.Uint64()
		for j := 0; j < 8 && i+j < len(raw); j++ {
			raw[i+j] = byte(v >> (8 * j))
		}
	}
	return b64.EncodeToString(raw[:])
}

// GS2Header returns the RFC 5802 GS2 header a non-channel-binding client sends:
// "y,," over TLS only when the server did not offer SCRAM-SHA-256-PLUS, and
// "n,," otherwise (real Postgres 17 refuses "y,," with FATAL 28000 when it
// offered -PLUS).
func GS2Header(overTLS, plusOffered bool) string {
	if overTLS && !plusOffered {
		return GS2ClientSupports
	}
	return GS2NoBinding
}

// ChannelBindingValue returns the base64-encoded c= attribute expected for gs2
// ("biws" for "n,,", "eSws" for "y,,").
func ChannelBindingValue(gs2 string) string { return b64.EncodeToString([]byte(gs2)) }

func authMessage(clientFirstBare, serverFirst, clientFinalNoProof string) string {
	return clientFirstBare + "," + serverFirst + "," + clientFinalNoProof
}

func xorKey(a, b [ScramKeyLen]byte) [ScramKeyLen]byte {
	var out [ScramKeyLen]byte
	for i := range out {
		out[i] = a[i] ^ b[i]
	}
	return out
}

func validNonce(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x21 || c > 0x7e || c == ',' {
			return false
		}
	}
	return true
}

type scramAttr struct {
	name  byte
	value string
}

func parseAttrs(s string) ([]scramAttr, bool) {
	parts := strings.Split(s, ",")
	if len(parts) > maxScramAttrs {
		return nil, false
	}
	out := make([]scramAttr, 0, len(parts))
	for _, p := range parts {
		if len(p) < 2 || p[1] != '=' || !isAttrName(p[0]) {
			return nil, false
		}
		out = append(out, scramAttr{name: p[0], value: p[2:]})
	}
	return out, true
}

const maxScramAttrs = 16

func isAttrName(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

func scramTooLong(msg []byte) bool { return len(msg) > MaxScramMessage }

func equalKeys(a, b []byte) bool { return subtle.ConstantTimeCompare(a, b) == 1 }

func sha256Sum(b []byte) [ScramKeyLen]byte { return sha256.Sum256(b) }
