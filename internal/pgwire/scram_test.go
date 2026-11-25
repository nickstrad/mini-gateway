package pgwire

import (
	"math/rand/v2"
	"strings"
	"testing"
)

const (
	rfcUser        = "user"
	rfcPassword    = "pencil"
	rfcClientNonce = "rOprNGfwEbeRWgbNEkqO"
	rfcServerNonce = "%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0"
	rfcSalt        = "W22ZaJ0SNY7soEsUEjb6gQ=="
	rfcClientFirst = "n,,n=user,r=rOprNGfwEbeRWgbNEkqO"
	rfcServerFirst = "r=rOprNGfwEbeRWgbNEkqO%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0,s=W22ZaJ0SNY7soEsUEjb6gQ==,i=4096"
	rfcClientFinal = "c=biws,r=rOprNGfwEbeRWgbNEkqO%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0,p=dHzbZapWIk4jUhN+Ute9ytag9zjfMHgsqmmiz7AndVQ="
	rfcServerFinal = "v=6rriTRBi23WpRR/wtup+mMhUZUn/dB5nLTJRsjl95G4="
)

func rfcVerifier(t *testing.T) Verifier {
	t.Helper()
	salt, err := b64.DecodeString(rfcSalt)
	if err != nil {
		t.Fatal(err)
	}
	v, err := NewVerifier(rfcPassword, salt, 4096)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestScramRFC7677ClientSide proves ScramClient matches the RFC 7677 test vector.
func TestScramRFC7677ClientSide(t *testing.T) {
	c := NewScramClient(rfcUser, rfcPassword, rfcClientNonce, GS2NoBinding)
	if got := string(c.First()); got != rfcClientFirst {
		t.Fatalf("client first %q", got)
	}
	final, cause := c.Final([]byte(rfcServerFirst))
	if cause != AuthCauseNone || string(final) != rfcClientFinal {
		t.Fatalf("client final %q cause %v", final, cause)
	}
	if cause := c.Verify([]byte(rfcServerFinal)); cause != AuthCauseNone {
		t.Fatalf("server signature refused: %v", cause)
	}
}

// TestScramRFC7677ServerSide proves ScramServer matches the RFC 7677 test vector.
func TestScramRFC7677ServerSide(t *testing.T) {
	s := NewScramServer(rfcVerifier(t), rfcServerNonce, false)
	first, cause := s.First(ScramMechanism, []byte(rfcClientFirst))
	if cause != AuthCauseNone || string(first) != rfcServerFirst {
		t.Fatalf("server first %q cause %v", first, cause)
	}
	final, cause := s.Final([]byte(rfcClientFinal))
	if cause != AuthCauseNone || string(final) != rfcServerFinal {
		t.Fatalf("server final %q cause %v", final, cause)
	}
}

// TestVerifierRoundTripsThroughItsText proves Verifier.String and ParseVerifier
// round-trip cleanly and reject malformed verifier strings.
func TestVerifierRoundTripsThroughItsText(t *testing.T) {
	v := rfcVerifier(t)
	got, err := ParseVerifier(v.String())
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != v.String() || got.StoredKey != v.StoredKey || got.ServerKey != v.ServerKey {
		t.Fatalf("round trip changed the verifier: %s vs %s", got, v)
	}
	for _, bad := range []string{"", "md5abc", "SCRAM-SHA-256$4096:", "SCRAM-SHA-256$x:c2FsdA==$a:b",
		"SCRAM-SHA-256$0:c2FsdA==$" + b64.EncodeToString(v.StoredKey[:]) + ":" + b64.EncodeToString(v.ServerKey[:]),
		"SCRAM-SHA-256$4096:c2FsdA==$YQ==:YQ=="} {
		if _, err := ParseVerifier(bad); err == nil {
			t.Errorf("ParseVerifier(%q) accepted", bad)
		}
	}
}

// TestVerifierHoldsNoPassword proves the derived Verifier contains no plaintext password.
func TestVerifierHoldsNoPassword(t *testing.T) {
	v := rfcVerifier(t)
	if strings.Contains(v.String(), rfcPassword) || strings.Contains(string(v.Salt), rfcPassword) ||
		strings.Contains(string(v.StoredKey[:]), rfcPassword) || strings.Contains(string(v.ServerKey[:]), rfcPassword) {
		t.Fatal("the verifier contains the password")
	}
}

type scramRun struct {
	clientGS2   string
	serverGS2   string
	finalEdit   func(string) string
	firstEdit   func(string) string
	serverFirst func(string) string
	serverFinal func(string) string
	password    string
	unknownUser bool
}

func runScram(t *testing.T, v Verifier, r scramRun) (AuthCause, AuthCause) {
	t.Helper()
	rnd := rand.New(rand.NewPCG(1, 2))
	if r.clientGS2 == "" {
		r.clientGS2 = GS2NoBinding
	}
	if r.password == "" {
		r.password = "secret"
	}
	c := NewScramClient("", r.password, NewNonce(rnd), r.clientGS2)
	s := NewScramServer(v, NewNonce(rnd), r.unknownUser)
	first := string(c.First())
	if r.firstEdit != nil {
		first = r.firstEdit(first)
	}
	sf, cause := s.First(ScramMechanism, []byte(first))
	if cause != AuthCauseNone {
		return cause, AuthCauseNone
	}
	sfs := string(sf)
	if r.serverFirst != nil {
		sfs = r.serverFirst(sfs)
	}
	cf, ccause := c.Final([]byte(sfs))
	if ccause != AuthCauseNone {
		return AuthCauseNone, ccause
	}
	cfs := string(cf)
	if r.finalEdit != nil {
		cfs = r.finalEdit(cfs)
	}
	fin, cause := s.Final([]byte(cfs))
	if cause != AuthCauseNone {
		return cause, AuthCauseNone
	}
	fs := string(fin)
	if r.serverFinal != nil {
		fs = r.serverFinal(fs)
	}
	return AuthCauseNone, c.Verify([]byte(fs))
}

func secretVerifier(t *testing.T) Verifier {
	t.Helper()
	v, err := NewVerifier("secret", []byte("0123456789abcdef"), 64)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestScramClientAgainstServer cross-tests ScramClient against ScramServer
// across valid GS2 headers, mismatched c= values, p= refusal, tampered proofs,
// nonces, and tampered server signatures.
func TestScramClientAgainstServer(t *testing.T) {
	v := secretVerifier(t)
	swapC := func(to string) func(string) string {
		return func(s string) string { return "c=" + to + s[strings.Index(s, ","):] }
	}
	cases := []struct {
		name       string
		run        scramRun
		wantServer AuthCause
		wantClient AuthCause
	}{
		{"n-header", scramRun{clientGS2: GS2NoBinding}, AuthCauseNone, AuthCauseNone},
		{"y-header", scramRun{clientGS2: GS2ClientSupports}, AuthCauseNone, AuthCauseNone},
		{"n-header-with-y-value", scramRun{clientGS2: GS2NoBinding, finalEdit: swapC("eSws")}, AuthCauseChannelBindingMismatch, AuthCauseNone},
		{"y-header-with-n-value", scramRun{clientGS2: GS2ClientSupports, finalEdit: swapC("biws")}, AuthCauseChannelBindingMismatch, AuthCauseNone},
		{"p-header-refused", scramRun{firstEdit: func(s string) string { return "p=tls-server-end-point" + s[1:] }}, AuthCauseChannelBindingRefused, AuthCauseNone},
		{"authzid-refused", scramRun{firstEdit: func(s string) string { return "n,a=bob" + s[2:] }}, AuthCauseMalformed, AuthCauseNone},
		{"wrong-password", scramRun{password: "wrong"}, AuthCauseWrongPassword, AuthCauseNone},
		{"unknown-user-even-with-the-right-proof", scramRun{unknownUser: true}, AuthCauseUnknownUser, AuthCauseNone},
		{"tampered-nonce-in-final", scramRun{finalEdit: func(s string) string { return strings.Replace(s, ",r=", ",r=X", 1) }}, AuthCauseNonceMismatch, AuthCauseNone},
		{"tampered-proof", scramRun{finalEdit: func(s string) string {
			i := strings.LastIndex(s, ",p=") + 3
			p, _ := b64.DecodeString(s[i:])
			p[0] ^= 1
			return s[:i] + b64.EncodeToString(p)
		}}, AuthCauseWrongPassword, AuthCauseNone},
		{"server-nonce-not-extending-the-client-nonce", scramRun{serverFirst: func(s string) string { return "r=Z" + s[2:] }}, AuthCauseNone, AuthCauseNonceMismatch},
		{"server-nonce-equal-to-client-nonce", scramRun{serverFirst: func(s string) string {
			i := strings.Index(s, ",s=")
			return s[:2+24] + s[i:]
		}}, AuthCauseNone, AuthCauseNonceMismatch},
		{"tampered-server-signature", scramRun{serverFinal: func(s string) string {
			p, _ := b64.DecodeString(s[2:])
			p[5] ^= 0x40
			return "v=" + b64.EncodeToString(p)
		}}, AuthCauseNone, AuthCauseServerSignature},
		{"server-error-attribute", scramRun{serverFinal: func(string) string { return "e=invalid-proof" }}, AuthCauseNone, AuthCauseServerRejected},
		{"iterations-out-of-range", scramRun{serverFirst: func(s string) string { return s[:strings.LastIndex(s, ",i=")] + ",i=99999999" }}, AuthCauseNone, AuthCauseMalformed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotS, gotC := runScram(t, v, tc.run)
			if gotS != tc.wantServer || gotC != tc.wantClient {
				t.Fatalf("server %v client %v, want server %v client %v", gotS, gotC, tc.wantServer, tc.wantClient)
			}
		})
	}
}

// TestScramServerRejectsAnotherMechanism proves ScramServer rejects -PLUS and
// malformed client-first payloads.
func TestScramServerRejectsAnotherMechanism(t *testing.T) {
	s := NewScramServer(secretVerifier(t), "abc", false)
	if _, cause := s.First(ScramPlusMechanism, []byte("p=tls-server-end-point,,n=,r=abc")); cause != AuthCauseBadMechanism {
		t.Fatalf("cause %v", cause)
	}
	s = NewScramServer(secretVerifier(t), "abc", false)
	for _, bad := range []string{"", "n,,", "n,,r=abc,n=", "x,,n=,r=abc", "n,,n=,r=a b", "n,,n=,r=", "n,n=,r=abc"} {
		s = NewScramServer(secretVerifier(t), "abc", false)
		if _, cause := s.First(ScramMechanism, []byte(bad)); cause != AuthCauseMalformed {
			t.Errorf("%q: cause %v", bad, cause)
		}
	}
}

// TestScramServerStepsOnlyOnce proves out-of-order Final and Verify calls fail.
func TestScramServerStepsOnlyOnce(t *testing.T) {
	v := secretVerifier(t)
	s := NewScramServer(v, "abc", false)
	if _, cause := s.Final([]byte("c=biws,r=x,p=AA==")); cause != AuthCauseMalformed {
		t.Fatalf("final before first: %v", cause)
	}
	c := NewScramClient("", "secret", "cn", GS2NoBinding)
	if c.Verify([]byte("v=AA==")) != AuthCauseMalformed {
		t.Fatal("verify before final accepted")
	}
}

// TestGS2HeaderRule proves GS2Header selects y,, only over TLS without -PLUS
// and verifies the corresponding c= base64 encodings.
func TestGS2HeaderRule(t *testing.T) {
	if GS2Header(false, false) != GS2NoBinding || GS2Header(false, true) != GS2NoBinding ||
		GS2Header(true, true) != GS2NoBinding || GS2Header(true, false) != GS2ClientSupports {
		t.Fatal("GS2 rule")
	}
	if ChannelBindingValue(GS2NoBinding) != "biws" || ChannelBindingValue(GS2ClientSupports) != "eSws" {
		t.Fatal("c= values")
	}
}

// TestMockVerifierIsStablePerUserAndDiffersBetweenUsers proves MockVerifier is
// deterministic per user and distinct across users.
func TestMockVerifierIsStablePerUserAndDiffersBetweenUsers(t *testing.T) {
	a, b := MockVerifier([]byte("k"), "alice", 0), MockVerifier([]byte("k"), "alice", 0)
	c := MockVerifier([]byte("k"), "bob", 0)
	if a.String() != b.String() || a.String() == c.String() || len(a.Salt) != ScramSaltLen || a.Iterations != DefaultScramIterations {
		t.Fatalf("mock verifiers: %s %s %s", a, b, c)
	}
}

// TestNonceIsPrintableAndFromTheSource proves NewNonce is deterministic per
// seed and emits valid 24-character printable nonces.
func TestNonceIsPrintableAndFromTheSource(t *testing.T) {
	n1 := NewNonce(rand.New(rand.NewPCG(7, 7)))
	n2 := NewNonce(rand.New(rand.NewPCG(7, 7)))
	n3 := NewNonce(rand.New(rand.NewPCG(8, 7)))
	if n1 != n2 || n1 == n3 || len(n1) != 24 || !validNonce(n1) {
		t.Fatalf("nonces %q %q %q", n1, n2, n3)
	}
}
