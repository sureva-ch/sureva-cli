// Package authflow implements the OAuth 2.0 Authorization Code + PKCE (S256)
// browser login flow used by `sureva login` against Amazon Cognito.
//
// This is a PUBLIC client flow: no client_secret, no Basic auth, no cookies.
// The PKCE code_verifier lives only in process memory for the duration of one
// login attempt; only the derived code_challenge ever leaves the process.
package authflow

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// verifierBytes is the amount of crypto/rand entropy used to build the PKCE
// code_verifier. 32 bytes base64url-encodes to 43 characters, within the
// RFC 7636 required range of 43-128 characters.
const verifierBytes = 32

// stateBytes is the amount of crypto/rand entropy used to build the CSRF
// state value returned by newState.
const stateBytes = 16

// pkce holds one login attempt's PKCE code verifier and its derived S256
// code challenge.
type pkce struct {
	// Verifier is the secret proof kept in process memory and sent only in
	// the final token exchange request (never in the authorize URL).
	Verifier string
	// Challenge is derived from Verifier and sent in the /oauth2/authorize
	// request; it is safe to expose.
	Challenge string
}

// newPKCE generates a new PKCE verifier/challenge pair for one login attempt.
func newPKCE() (*pkce, error) {
	verifier, err := randomURLSafeString(verifierBytes)
	if err != nil {
		return nil, fmt.Errorf("generate PKCE code verifier: %w", err)
	}
	return &pkce{
		Verifier:  verifier,
		Challenge: s256Challenge(verifier),
	}, nil
}

// s256Challenge derives the PKCE S256 code_challenge from a code_verifier per
// RFC 7636: base64url(sha256(verifier)), unpadded.
func s256Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// newState generates a random CSRF state value for one login attempt. The
// callback handler MUST reject any request whose state does not match this
// exact value.
func newState() (string, error) {
	state, err := randomURLSafeString(stateBytes)
	if err != nil {
		return "", fmt.Errorf("generate CSRF state: %w", err)
	}
	return state, nil
}

// randomURLSafeString returns an unpadded base64url string derived from n
// bytes of crypto/rand.
func randomURLSafeString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
