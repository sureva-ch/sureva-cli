package authflow

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestExchangeCode covers success plus the two failure classes that must
// both collapse to the same *ExchangeError (auth_error/exit 2): non-2xx and
// a missing id_token in an otherwise-2xx response.
func TestExchangeCode(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantTok string
	}{
		{
			name: "success returns id_token, asserts public-client form and no Basic auth",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if _, _, ok := r.BasicAuth(); ok {
					t.Error("must not use HTTP Basic auth (public client)")
				}
				if err := r.ParseForm(); err != nil {
					t.Fatalf("parse form: %v", err)
				}
				want := map[string]string{
					"grant_type":    "authorization_code",
					"client_id":     "client-abc",
					"code":          "auth-code-xyz",
					"redirect_uri":  "http://127.0.0.1:8976/callback",
					"code_verifier": "verifier-123",
				}
				for k, v := range want {
					if got := r.Form.Get(k); got != v {
						t.Errorf("form[%q] = %q, want %q", k, got, v)
					}
				}
				if r.Form.Get("client_secret") != "" {
					t.Error("must not send client_secret (public client)")
				}
				_, _ = w.Write([]byte(`{"id_token":"test-id-token"}`))
			},
			wantTok: "test-id-token",
		},
		{
			name: "non-2xx status is an ExchangeError",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			},
		},
		{
			name: "missing id_token is an ExchangeError",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"access_token":"no-id-token-here"}`))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()

			idToken, err := exchangeCode(context.Background(), srv.Client(), srv.URL,
				"client-abc", "auth-code-xyz", "http://127.0.0.1:8976/callback", "verifier-123")

			if tt.wantTok == "" {
				if err == nil {
					t.Fatal("exchangeCode() error = nil, want error")
				}
				if _, ok := err.(*ExchangeError); !ok {
					t.Fatalf("err type = %T, want *ExchangeError", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("exchangeCode() error = %v, want nil", err)
			}
			if idToken != tt.wantTok {
				t.Errorf("idToken = %q, want %q", idToken, tt.wantTok)
			}
		})
	}
}

// TestExchangeCode_TransportFailure proves a network-level failure maps to
// the same *ExchangeError as any other exchange failure — there is no
// separate network error type; the flow layer treats both identically.
func TestExchangeCode_TransportFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := srv.URL
	srv.Close() // closed listener -> connection refused

	_, err := exchangeCode(context.Background(), http.DefaultClient, deadURL, "client-abc", "code", "redirect", "verifier")
	if _, ok := err.(*ExchangeError); !ok {
		t.Fatalf("err type = %T, want *ExchangeError (transport failure must not become a separate network error)", err)
	}
}

// TestExchangeCode_SecretHygiene proves the returned error never embeds the
// authorization code or PKCE verifier on a failure path.
func TestExchangeCode_SecretHygiene(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()

	_, err := exchangeCode(context.Background(), srv.Client(), srv.URL, "client-abc", "super-secret-code", "redirect", "super-secret-verifier")
	if err == nil {
		t.Fatal("want error")
	}
	if msg := err.Error(); strings.Contains(msg, "super-secret-code") || strings.Contains(msg, "super-secret-verifier") {
		t.Errorf("error message leaked a secret value: %q", msg)
	}
}
