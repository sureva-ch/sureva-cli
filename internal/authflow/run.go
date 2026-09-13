package authflow

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Config configures one Run of the browser login flow. CognitoDomain is
// expected to already be scheme-normalized by the caller (see
// credentials.CognitoDomainFromPath) — Run does not re-normalize it, and
// relies on the same strings.TrimRight(domain, "/") join exchangeCode uses
// for /oauth2/token so a trailing-slash domain never produces a double
// slash before /oauth2/authorize.
type Config struct {
	// CognitoDomain is the Cognito hosted-UI domain, including scheme.
	CognitoDomain string
	// ClientID is the public Cognito app client ID.
	ClientID string
	// BrowserOpener opens the authorize URL. Defaults to the per-OS opener
	// when nil; a failure here is non-fatal (the URL is already printed).
	BrowserOpener BrowserOpener
	// HTTPClient is used for the Cognito token exchange. Defaults to
	// http.DefaultClient when nil.
	HTTPClient *http.Client
	// Timeout bounds the wait for the loopback callback. Defaults to
	// DefaultTimeout when zero.
	Timeout time.Duration
	// Writer receives the authorize URL message. Defaults to io.Discard
	// when nil. Run writes to it exactly once, before opening the browser,
	// and never writes the id_token, authorization code, or PKCE verifier
	// to it on any path.
	Writer io.Writer
}

// Result is the successful outcome of Run.
type Result struct {
	// IDToken is the Cognito id_token to use as the bearer for minting a PAT.
	IDToken string
}

// Run executes one full browser login attempt: it binds the loopback
// callback server before opening any browser (R3), builds and prints the
// PKCE S256 authorize URL, best-effort opens the browser (a failure here is
// non-fatal, R4), waits for the callback, and exchanges the resulting code
// for an id_token. It returns the flow's typed errors (*PortsBusyError,
// *StateMismatchError, *IdPError, *CallbackError, *TimeoutError,
// *ExchangeError) unwrapped, for the caller to map to exit codes.
func Run(ctx context.Context, cfg Config) (*Result, error) {
	listener, err := Listen(DefaultPorts)
	if err != nil {
		return nil, err
	}
	defer func() { _ = listener.Close() }()

	p, err := newPKCE()
	if err != nil {
		return nil, err
	}
	state, err := newState()
	if err != nil {
		return nil, err
	}

	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", listener.Port())
	authorizeURL := buildAuthorizeURL(cfg.CognitoDomain, cfg.ClientID, redirectURI, p.Challenge, state)

	writer := cfg.Writer
	if writer == nil {
		writer = io.Discard
	}
	_, _ = fmt.Fprintf(writer, "Open this URL to continue logging in:\n%s\n", authorizeURL)

	opener := cfg.BrowserOpener
	if opener == nil {
		opener = defaultBrowserOpener()
	}
	_ = opener(authorizeURL) // best-effort; non-fatal per R4, URL already printed above

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	callback, err := listener.WaitForCallback(waitCtx, state)
	if err != nil {
		return nil, err
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	// waitCtx (not the outer ctx) bounds the exchange too, per exchangeCode's
	// own doc comment: it must share the timeout bounding the callback wait.
	idToken, err := exchangeCode(waitCtx, httpClient, cfg.CognitoDomain, cfg.ClientID, callback.Code, redirectURI, p.Verifier)
	if err != nil {
		return nil, err
	}

	return &Result{IDToken: idToken}, nil
}

// buildAuthorizeURL constructs the Cognito /oauth2/authorize URL for one
// login attempt: fixed scopes (openid email profile), authorization code
// response type, and PKCE S256 challenge. Uses the same
// strings.TrimRight(domain, "/") join as exchangeCode's /oauth2/token URL so
// a trailing-slash domain never produces a double slash.
func buildAuthorizeURL(domain, clientID, redirectURI, codeChallenge, state string) string {
	base := strings.TrimRight(domain, "/") + "/oauth2/authorize"
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {"openid email profile"},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
	}
	return base + "?" + q.Encode()
}
