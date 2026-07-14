package authflow

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeCognito simulates Cognito's /oauth2/token endpoint. When tokenErr is
// true it always returns a non-2xx error response; otherwise it asserts the
// received code (when wantCode is non-empty) and returns idToken.
func fakeCognito(t *testing.T, wantCode, idToken string, tokenErr bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tokenErr {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if wantCode != "" && r.Form.Get("code") != wantCode {
			t.Errorf("code = %q, want %q", r.Form.Get("code"), wantCode)
		}
		_, _ = fmt.Fprintf(w, `{"id_token":%q}`, idToken)
	}))
}

// callbackOpener simulates the browser completing the OAuth dance: it parses
// redirect_uri and state out of the constructed authorize URL, then fires an
// async GET at the loopback callback with those values overridden by extra
// (e.g. a tampered state, a different code, or an error param) — no real
// browser or network is ever involved.
func callbackOpener(extra url.Values) BrowserOpener {
	return func(authorizeURL string) error {
		u, err := url.Parse(authorizeURL)
		if err != nil {
			return err
		}
		q := u.Query()

		cbURL, err := url.Parse(q.Get("redirect_uri"))
		if err != nil {
			return err
		}
		cbQuery := url.Values{}
		cbQuery.Set("state", q.Get("state"))
		for k, v := range extra {
			cbQuery[k] = v
		}
		cbURL.RawQuery = cbQuery.Encode()

		go func() {
			_, _ = http.Get(cbURL.String())
		}()
		return nil
	}
}

// TestRun covers the five end-to-end outcomes: success, state mismatch, IdP
// denial, exchange failure, and callback timeout. Every case asserts the
// writer captured the authorize URL and never captured the id_token or any
// authorization code value used in that subtest.
func TestRun(t *testing.T) {
	tests := []struct {
		name      string
		wantCode  string
		idToken   string
		tokenErr  bool
		extra     url.Values
		wantErr   any // nil means success
		wantToken string
	}{
		{
			name:      "success",
			wantCode:  "good-code",
			idToken:   "id-token-abc",
			extra:     url.Values{"code": {"good-code"}},
			wantToken: "id-token-abc",
		},
		{
			name:    "state mismatch",
			extra:   url.Values{"code": {"good-code"}, "state": {"tampered-state"}},
			wantErr: &StateMismatchError{},
		},
		{
			name:    "IdP denies consent",
			extra:   url.Values{"error": {"access_denied"}},
			wantErr: &IdPError{},
		},
		{
			name:     "exchange failure",
			wantCode: "good-code",
			tokenErr: true,
			extra:    url.Values{"code": {"good-code"}},
			wantErr:  &ExchangeError{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := fakeCognito(t, tt.wantCode, tt.idToken, tt.tokenErr)
			defer srv.Close()

			var buf bytes.Buffer
			cfg := Config{
				CognitoDomain: srv.URL,
				ClientID:      "test-client",
				BrowserOpener: callbackOpener(tt.extra),
				HTTPClient:    srv.Client(),
				Timeout:       5 * time.Second,
				Writer:        &buf,
			}

			result, err := Run(context.Background(), cfg)

			out := buf.String()
			if !strings.Contains(out, "/oauth2/authorize") {
				t.Errorf("writer output = %q, want it to contain the authorize URL", out)
			}
			if tt.idToken != "" && strings.Contains(out, tt.idToken) {
				t.Errorf("writer output leaked the id_token: %q", out)
			}
			// Check the code value actually used in this subtest's callback
			// (tt.extra), not tt.wantCode — some rows (e.g. state mismatch)
			// send a code to the loopback callback that never reaches
			// Cognito, so tt.wantCode is empty even though a code value was
			// used. Running this on every row (not just where wantCode is
			// set) is the gate-required fix.
			if codeUsed := tt.extra.Get("code"); codeUsed != "" && strings.Contains(out, codeUsed) {
				t.Errorf("writer output leaked the authorization code: %q", out)
			}
			// The PKCE verifier is generated internally by Run and never
			// exposed to the test, so it cannot be checked by value. Instead
			// assert the authorize URL construction never includes the
			// code_verifier parameter name at all — that value belongs only
			// in the token exchange POST body (exchange.go), never in any
			// writer output.
			if strings.Contains(out, "code_verifier") {
				t.Errorf("writer output leaked the PKCE verifier parameter: %q", out)
			}

			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("Run() error = %v, want nil", err)
				}
				if result == nil || result.IDToken != tt.wantToken {
					t.Fatalf("Run() result = %+v, want IDToken %q", result, tt.wantToken)
				}
				return
			}

			if err == nil {
				t.Fatal("Run() error = nil, want error")
			}
			if fmt.Sprintf("%T", err) != fmt.Sprintf("%T", tt.wantErr) {
				t.Fatalf("Run() error type = %T, want %T", err, tt.wantErr)
			}
		})
	}
}

// TestRun_CallbackTimeout proves Run stops waiting and returns a
// *TimeoutError when no callback ever arrives, without leaking anything to
// the writer.
func TestRun_CallbackTimeout(t *testing.T) {
	var buf bytes.Buffer
	cfg := Config{
		CognitoDomain: "https://auth.example.com",
		ClientID:      "test-client",
		BrowserOpener: func(string) error { return nil }, // never hits the callback
		Timeout:       100 * time.Millisecond,
		Writer:        &buf,
	}

	_, err := Run(context.Background(), cfg)
	if _, ok := err.(*TimeoutError); !ok {
		t.Fatalf("Run() error type = %T, want *TimeoutError", err)
	}
	if !strings.Contains(buf.String(), "/oauth2/authorize") {
		t.Errorf("writer output = %q, want it to contain the authorize URL", buf.String())
	}
	if strings.Contains(buf.String(), "code_verifier") {
		t.Errorf("writer output leaked the PKCE verifier parameter: %q", buf.String())
	}
}

// TestRun_PortsBusyFailsBeforeBrowserOpen proves Run fails before ever
// invoking the browser opener when all loopback ports are busy, and never
// writes anything (R3: fail before any browser call).
func TestRun_PortsBusyFailsBeforeBrowserOpen(t *testing.T) {
	for _, port := range DefaultPorts {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			t.Skipf("port %d unavailable in this environment, cannot simulate busy state: %v", port, err)
		}
		t.Cleanup(func() { _ = ln.Close() })
	}

	var opened atomic.Bool
	var buf bytes.Buffer
	cfg := Config{
		CognitoDomain: "https://auth.example.com",
		ClientID:      "test-client",
		BrowserOpener: func(string) error { opened.Store(true); return nil },
		Writer:        &buf,
	}

	_, err := Run(context.Background(), cfg)
	if _, ok := err.(*PortsBusyError); !ok {
		t.Fatalf("Run() error type = %T, want *PortsBusyError", err)
	}
	if opened.Load() {
		t.Error("BrowserOpener was invoked despite all loopback ports being busy")
	}
	if buf.Len() != 0 {
		t.Errorf("writer output = %q, want empty (nothing printed before the ports-busy failure)", buf.String())
	}
}

// TestRun_BrowserOpenFailureIsNonFatal proves a failing BrowserOpener does
// not fail the flow: the URL is already printed, and the callback can still
// arrive (e.g. the user copy-pasted it manually).
func TestRun_BrowserOpenFailureIsNonFatal(t *testing.T) {
	srv := fakeCognito(t, "good-code", "id-token-xyz", false)
	defer srv.Close()

	opener := callbackOpener(url.Values{"code": {"good-code"}})
	failingOpener := func(u string) error {
		_ = opener(u) // still simulates the browser completing the dance
		return fmt.Errorf("simulated: no display available")
	}

	var buf bytes.Buffer
	cfg := Config{
		CognitoDomain: srv.URL,
		ClientID:      "test-client",
		BrowserOpener: failingOpener,
		HTTPClient:    srv.Client(),
		Timeout:       5 * time.Second,
		Writer:        &buf,
	}

	result, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error = %v, want nil (browser-open failure must be non-fatal)", err)
	}
	if result == nil || result.IDToken != "id-token-xyz" {
		t.Fatalf("Run() result = %+v, want IDToken %q", result, "id-token-xyz")
	}
	if strings.Contains(buf.String(), "code_verifier") {
		t.Errorf("writer output leaked the PKCE verifier parameter: %q", buf.String())
	}
}

// TestBuildAuthorizeURL_TrailingSlashDomain proves a trailing slash on
// CognitoDomain never produces a double slash before /oauth2/authorize,
// mirroring exchangeCode's own strings.TrimRight(domain, "/") handling for
// /oauth2/token.
func TestBuildAuthorizeURL_TrailingSlashDomain(t *testing.T) {
	got := buildAuthorizeURL("https://auth.example.com/", "client-abc", "http://127.0.0.1:8976/callback", "challenge", "state")
	if strings.Contains(got, "//oauth2/authorize") {
		t.Errorf("buildAuthorizeURL() = %q, want no double slash before /oauth2/authorize", got)
	}
	if !strings.HasPrefix(got, "https://auth.example.com/oauth2/authorize?") {
		t.Errorf("buildAuthorizeURL() = %q, want prefix %q", got, "https://auth.example.com/oauth2/authorize?")
	}
	if !strings.Contains(got, "code_challenge_method=S256") {
		t.Errorf("buildAuthorizeURL() = %q, want code_challenge_method=S256 parameter", got)
	}
}
