package authflow

import "testing"

// TestS256Challenge_KnownVector uses the RFC 7636 Appendix B test vector to
// prove the challenge derivation is a real base64url(sha256(verifier)), not a
// stub or pass-through.
func TestS256Challenge_KnownVector(t *testing.T) {
	tests := []struct {
		name      string
		verifier  string
		challenge string
	}{
		{
			name:      "RFC 7636 Appendix B vector",
			verifier:  "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk",
			challenge: "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM",
		},
		{
			name:      "different verifier yields different challenge",
			verifier:  "another-example-verifier-value-1234567890",
			challenge: "7EFQylRXdmkImBvC83rzxEMi3DY3YvhuEZMzlzMgPN8",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s256Challenge(tt.verifier)
			if got != tt.challenge {
				t.Errorf("s256Challenge(%q) = %q, want %q", tt.verifier, got, tt.challenge)
			}
		})
	}
}

// TestNewPKCE_VerifierAndChallengeAreLinked proves newPKCE derives Challenge
// from Verifier via the real S256 function, not a hardcoded pair.
func TestNewPKCE_VerifierAndChallengeAreLinked(t *testing.T) {
	p, err := newPKCE()
	if err != nil {
		t.Fatalf("newPKCE() error: %v", err)
	}
	if p.Verifier == "" {
		t.Fatal("Verifier is empty")
	}
	if want := s256Challenge(p.Verifier); p.Challenge != want {
		t.Errorf("Challenge = %q, want %q (derived from Verifier)", p.Challenge, want)
	}
}

// TestNewPKCE_Uniqueness proves crypto/rand is actually used: two calls must
// not produce the same verifier.
func TestNewPKCE_Uniqueness(t *testing.T) {
	a, err := newPKCE()
	if err != nil {
		t.Fatalf("newPKCE() error: %v", err)
	}
	b, err := newPKCE()
	if err != nil {
		t.Fatalf("newPKCE() error: %v", err)
	}
	if a.Verifier == b.Verifier {
		t.Errorf("two newPKCE() calls produced the same verifier %q; want random uniqueness", a.Verifier)
	}
	if a.Challenge == b.Challenge {
		t.Errorf("two newPKCE() calls produced the same challenge %q; want random uniqueness", a.Challenge)
	}
}

// TestNewPKCE_VerifierLength proves the verifier has enough entropy (RFC 7636
// requires 43-128 chars); our 32-byte source yields 43 base64url chars.
func TestNewPKCE_VerifierLength(t *testing.T) {
	p, err := newPKCE()
	if err != nil {
		t.Fatalf("newPKCE() error: %v", err)
	}
	const wantLen = 43 // base64.RawURLEncoding of 32 random bytes
	if len(p.Verifier) != wantLen {
		t.Errorf("len(Verifier) = %d, want %d", len(p.Verifier), wantLen)
	}
}

// TestNewState_Uniqueness proves state values are random per call, which is
// what makes the CSRF check in the callback meaningful.
func TestNewState_Uniqueness(t *testing.T) {
	a, err := newState()
	if err != nil {
		t.Fatalf("newState() error: %v", err)
	}
	b, err := newState()
	if err != nil {
		t.Fatalf("newState() error: %v", err)
	}
	if a == b {
		t.Errorf("two newState() calls produced the same value %q; want random uniqueness", a)
	}
	if a == "" {
		t.Fatal("newState() returned empty string")
	}
}
