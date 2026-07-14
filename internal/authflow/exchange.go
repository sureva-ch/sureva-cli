package authflow

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ExchangeError means the Cognito token exchange failed: a non-2xx
// response, a malformed/missing id_token, or a transport-level failure. All
// three collapse to this one type (auth_error/exit 2) — there is no
// separate network error. The reason is a static category string; it never
// embeds the authorization code, verifier, or any response body content.
type ExchangeError struct {
	Reason string
}

func (e *ExchangeError) Error() string {
	return fmt.Sprintf("token exchange failed: %s", e.Reason)
}

// tokenResponse is the subset of the Cognito /oauth2/token JSON body this
// flow needs. Only id_token PRESENCE is checked here — full signature,
// issuer, audience, and expiry validation happens server-side when the
// id_token is later used as the bearer for POST /v1/auth/tokens. This
// is an intentional design decision, not an omission: see design doc
// "id_token handling" row.
type tokenResponse struct {
	IDToken string `json:"id_token"`
}

// exchangeCode exchanges an authorization code for an id_token at the given
// Cognito domain's /oauth2/token endpoint. This is a PUBLIC client request:
// no client_secret and no HTTP Basic auth — only client_id, code,
// redirect_uri, and the PKCE code_verifier authenticate the request. ctx
// must be the same context bounding the loopback callback wait, so a single
// timeout covers both.
func exchangeCode(ctx context.Context, httpClient *http.Client, cognitoDomain, clientID, code, redirectURI, codeVerifier string) (string, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {codeVerifier},
	}

	tokenURL := strings.TrimRight(cognitoDomain, "/") + "/oauth2/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", &ExchangeError{Reason: "build token request"}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", &ExchangeError{Reason: "token request transport failure"}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &ExchangeError{Reason: fmt.Sprintf("token endpoint returned status %d", resp.StatusCode)}
	}

	var tok tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", &ExchangeError{Reason: "parse token response"}
	}
	if tok.IDToken == "" {
		return "", &ExchangeError{Reason: "token response missing id_token"}
	}

	return tok.IDToken, nil
}
