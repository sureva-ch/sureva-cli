package cli

// Note: this file is in package cli (not cli_test) so it can override the
// unexported runAuthFlow seam, mirroring the existing convention in
// wait_test.go/banner_test.go/upgrade_test.go for tests that need internal
// access.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sureva-ch/sureva-cli/internal/authflow"
	"github.com/sureva-ch/sureva-cli/internal/credentials"
	"github.com/sureva-ch/sureva-cli/internal/output"
)

// ---- test helpers (local to this file; cli_test's helpers in auth_test.go
// are not visible from package cli) ----

// newLoginTestRoot returns a root command wired to capture stdout/stderr.
func newLoginTestRoot(t *testing.T) (*bytes.Buffer, *bytes.Buffer, func(args ...string) error) {
	t.Helper()
	outBuf := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	exec := func(args ...string) error {
		root := NewRootCmd()
		root.SetOut(outBuf)
		root.SetErr(errBuf)
		root.SetArgs(args)
		outBuf.Reset()
		errBuf.Reset()
		return root.Execute()
	}
	return outBuf, errBuf, exec
}

// decodeTrailingJSONObject decodes the last JSON object found in s, starting
// at its first "{". Used for stderr buffers that may also contain the
// plain-text authorize-URL message authflow.Run prints before a later
// mint/whoami failure — stdout stays JSON-only (R9), stderr does not.
func decodeTrailingJSONObject(t *testing.T, s string) map[string]any {
	t.Helper()
	idx := strings.Index(s, "{")
	if idx == -1 {
		t.Fatalf("no JSON object found in: %s", s)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(s[idx:]), &env); err != nil {
		t.Fatalf("trailing content not JSON: %v; content=%s", err, s[idx:])
	}
	return env
}

// loginExitCode extracts the exit code from an *ExitError, 0 for nil.
func loginExitCode(err error) int {
	if err == nil {
		return output.ExitOK
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return output.ExitGeneral
}

// withStubAuthFlow overrides runAuthFlow for the duration of the test and
// restores the original afterward.
func withStubAuthFlow(t *testing.T, stub func(ctx context.Context, cfg authflow.Config) (*authflow.Result, error)) {
	t.Helper()
	original := runAuthFlow
	runAuthFlow = stub
	t.Cleanup(func() { runAuthFlow = original })
}

func withStubSaveLoginToken(t *testing.T, stub func(path, token string) error) {
	t.Helper()
	original := saveLoginToken
	saveLoginToken = stub
	t.Cleanup(func() { saveLoginToken = original })
}

// loginFakeCognito simulates Cognito's /oauth2/token endpoint, returning idToken
// on every request (no error).
func loginFakeCognito(t *testing.T, idToken string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		_, _ = fmt.Fprintf(w, `{"id_token":%q}`, idToken)
	}))
}

// loginCallbackOpener simulates the browser completing the OAuth dance by
// firing an async GET at the loopback callback URL parsed out of the
// authorize URL — no real browser or network is ever involved. Mirrors
// authflow's own (unexported) callbackOpener test helper, one layer up.
func loginCallbackOpener(extra url.Values) authflow.BrowserOpener {
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

// stubSuccessfulAuthFlow wraps the REAL authflow.Run, injecting a fake
// BrowserOpener + HTTPClient + short Timeout — this exercises login.go's
// actual wiring against a real (but fully injected) authflow.Run, not a
// hand-rolled fake of Run itself.
func stubSuccessfulAuthFlow(cognitoSrv *httptest.Server, code string) func(context.Context, authflow.Config) (*authflow.Result, error) {
	return func(ctx context.Context, cfg authflow.Config) (*authflow.Result, error) {
		cfg.BrowserOpener = loginCallbackOpener(url.Values{"code": {code}})
		cfg.HTTPClient = cognitoSrv.Client()
		cfg.Timeout = 5 * time.Second
		return authflow.Run(ctx, cfg)
	}
}

// cloudAPIStub returns an httptest server backing POST /v1/auth/tokens
// and GET /v1/auth/me, asserting the bearer token used for each leg.
func cloudAPIStub(t *testing.T, idTokenForMint, patToken string, mintStatus int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/tokens":
			if got := r.Header.Get("Authorization"); got != "Bearer "+idTokenForMint {
				t.Errorf("CreateToken Authorization = %q, want Bearer %s", got, idTokenForMint)
			}
			if mintStatus != 0 && mintStatus != http.StatusCreated {
				w.WriteHeader(mintStatus)
				_, _ = w.Write([]byte(`{"error":"mint failed"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w,
				`{"id":"tok-1","name":"cli-test","token":%q,"last_four":"abcd","status":"active","created_at":"2026-01-01T00:00:00Z"}`,
				patToken,
			)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/auth/me":
			if got := r.Header.Get("Authorization"); got != "Bearer "+patToken {
				t.Errorf("Whoami Authorization = %q, want Bearer %s", got, patToken)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"u-1","email":"ana@example.com","name":"Ana","is_active":true,"created_at":"2026-01-01T00:00:00Z"}`))
		default:
			http.NotFound(w, r)
		}
	}))
}

// setLoginEnv sets the Cognito + API env vars this command reads, restored
// automatically by t.Setenv.
func setLoginEnv(t *testing.T, cognitoDomain, clientID, apiURL string) {
	t.Helper()
	t.Setenv("SUREVA_COGNITO_DOMAIN", cognitoDomain)
	t.Setenv("SUREVA_COGNITO_CLIENT_ID", clientID)
	t.Setenv("SUREVA_API_URL", apiURL)
	t.Setenv("SUREVA_TOKEN", "")
}

// ---- 8.1 RED / 8.2 GREEN: successful login end to end ----

func TestLoginCmd_Success(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	cognitoSrv := loginFakeCognito(t, "id-token-abc")
	defer cognitoSrv.Close()
	cloudSrv := cloudAPIStub(t, "id-token-abc", "sapi_minted_pat_1234567890", 0)
	defer cloudSrv.Close()

	setLoginEnv(t, cognitoSrv.URL, "test-client", cloudSrv.URL)
	withStubAuthFlow(t, stubSuccessfulAuthFlow(cognitoSrv, "good-code"))

	outBuf, errBuf, exec := newLoginTestRoot(t)
	err := exec("--config", cfgPath, "login")

	if got := loginExitCode(err); got != output.ExitOK {
		t.Fatalf("login: want exit 0, got %d; stderr=%s", got, errBuf)
	}

	var got map[string]any
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("login: stdout not JSON: %v; stdout=%s", jsonErr, outBuf)
	}
	if status, _ := got["status"].(string); status != "authenticated" {
		t.Errorf("login: want status authenticated, got %q", status)
	}
	if cfg, _ := got["config_path"].(string); cfg != cfgPath {
		t.Errorf("login: want config_path %q, got %q", cfgPath, cfg)
	}
	user, _ := got["user"].(map[string]any)
	if user == nil || user["email"] != "ana@example.com" {
		t.Errorf("login: want user.email ana@example.com, got %+v", user)
	}

	data, readErr := os.ReadFile(cfgPath)
	if readErr != nil {
		t.Fatalf("read saved config: %v", readErr)
	}
	if !strings.Contains(string(data), "sapi_minted_pat_1234567890") {
		t.Fatalf("saved config missing minted PAT:\n%s", data)
	}

	// Secret hygiene (R11): neither the id_token nor the minted PAT may ever
	// appear in stdout or stderr, on the success path.
	for _, leaked := range []string{"id-token-abc", "sapi_minted_pat_1234567890"} {
		if strings.Contains(outBuf.String(), leaked) {
			t.Errorf("stdout leaked secret %q: %s", leaked, outBuf)
		}
		if strings.Contains(errBuf.String(), leaked) {
			t.Errorf("stderr leaked secret %q: %s", leaked, errBuf)
		}
	}
}

// ---- R1: failed re-login preserves the existing stored token ----

func TestLoginCmd_FailedReLoginPreservesExistingToken(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := credentials.SaveToken(cfgPath, "sapi_existing_token_0000000000"); err != nil {
		t.Fatalf("seed existing token: %v", err)
	}

	setLoginEnv(t, "https://auth.example.com", "test-client", "https://unused.example.com")
	withStubAuthFlow(t, func(ctx context.Context, cfg authflow.Config) (*authflow.Result, error) {
		return nil, &authflow.ExchangeError{Reason: "token endpoint returned status 400"}
	})

	_, errBuf, exec := newLoginTestRoot(t)
	err := exec("--config", cfgPath, "login")

	if got := loginExitCode(err); got != output.ExitAuth {
		t.Fatalf("login exchange failure: want exit %d, got %d; stderr=%s", output.ExitAuth, got, errBuf)
	}

	data, readErr := os.ReadFile(cfgPath)
	if readErr != nil {
		t.Fatalf("read config: %v", readErr)
	}
	if !strings.Contains(string(data), "sapi_existing_token_0000000000") {
		t.Fatalf("existing token was overwritten/lost after a failed re-login:\n%s", data)
	}
}

func TestLoginCmd_WhoamiFailurePreservesExistingToken(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	const existing = "sapi_existing_token_0000000000"
	if err := credentials.SaveToken(cfgPath, existing); err != nil {
		t.Fatalf("seed existing token: %v", err)
	}

	cleanupCalls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/tokens":
			_, _ = w.Write([]byte(`{"id":"tok-1","name":"cli-test","token":"sapi_new_invalid_pat","last_four":"ipat","status":"active","created_at":"2026-01-01T00:00:00Z"}`))
		case "/v1/auth/me":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid token"}`))
		case "/v1/auth/tokens/tok-1":
			cleanupCalls++
			if r.Method != http.MethodDelete {
				t.Errorf("cleanup method = %s, want DELETE", r.Method)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer sapi_new_invalid_pat" {
				t.Errorf("cleanup Authorization = %q", got)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()

	setLoginEnv(t, "https://auth.example.com", "test-client", api.URL)
	withStubAuthFlow(t, func(context.Context, authflow.Config) (*authflow.Result, error) {
		return &authflow.Result{IDToken: "id-token-not-printed"}, nil
	})

	outBuf, errBuf, exec := newLoginTestRoot(t)
	err := exec("--config", cfgPath, "login")
	if got := loginExitCode(err); got != output.ExitAuth {
		t.Fatalf("whoami failure exit = %d, want %d; stderr=%s", got, output.ExitAuth, errBuf)
	}
	data, readErr := os.ReadFile(cfgPath)
	if readErr != nil {
		t.Fatalf("read config: %v", readErr)
	}
	if !strings.Contains(string(data), existing) || strings.Contains(string(data), "sapi_new_invalid_pat") {
		t.Fatalf("failed PAT validation changed saved credential: %s", data)
	}
	if cleanupCalls != 1 {
		t.Fatalf("cleanup calls = %d, want 1", cleanupCalls)
	}
	combined := outBuf.String() + errBuf.String()
	for _, secret := range []string{"id-token-not-printed", "sapi_new_invalid_pat"} {
		if strings.Contains(combined, secret) {
			t.Fatalf("failure output leaked secret %q", secret)
		}
	}
}

func TestLoginCmd_SaveFailureRevokesMintedTokenAndPreservesExistingToken(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	const existing = "sapi_existing_token_0000000000"
	if err := credentials.SaveToken(cfgPath, existing); err != nil {
		t.Fatalf("seed existing token: %v", err)
	}

	cleanupCalls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/tokens":
			_, _ = w.Write([]byte(`{"id":"tok-save","token":"sapi_new_save_failure","created_at":"2026-01-01T00:00:00Z"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/auth/me":
			_, _ = w.Write([]byte(`{"id":"u-1","email":"ana@example.com","is_active":true,"created_at":"2026-01-01T00:00:00Z"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/auth/tokens/tok-save":
			cleanupCalls++
			if got := r.Header.Get("Authorization"); got != "Bearer sapi_new_save_failure" {
				t.Errorf("cleanup Authorization = %q", got)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()

	setLoginEnv(t, "https://auth.example.com", "test-client", api.URL)
	withStubAuthFlow(t, func(context.Context, authflow.Config) (*authflow.Result, error) {
		return &authflow.Result{IDToken: "id-token-not-printed"}, nil
	})
	withStubSaveLoginToken(t, func(path, token string) error {
		return errors.New("injected persistence failure")
	})

	outBuf, errBuf, exec := newLoginTestRoot(t)
	err := exec("--config", cfgPath, "login")
	if got := loginExitCode(err); got != output.ExitGeneral {
		t.Fatalf("save failure exit = %d, want %d; stderr=%s", got, output.ExitGeneral, errBuf)
	}
	if cleanupCalls != 1 {
		t.Fatalf("cleanup calls = %d, want 1", cleanupCalls)
	}
	data, readErr := os.ReadFile(cfgPath)
	if readErr != nil {
		t.Fatalf("read config: %v", readErr)
	}
	if !strings.Contains(string(data), existing) || strings.Contains(string(data), "sapi_new_save_failure") {
		t.Fatalf("save failure changed existing credential: %s", data)
	}
	combined := outBuf.String() + errBuf.String()
	for _, secret := range []string{"id-token-not-printed", "sapi_new_save_failure"} {
		if strings.Contains(combined, secret) {
			t.Fatalf("failure output leaked secret %q", secret)
		}
	}
}

func TestLoginCmd_CleanupFailurePreservesOriginalWhoamiError(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/tokens":
			_, _ = w.Write([]byte(`{"id":"tok-cleanup-fail","token":"sapi_cleanup_failure","created_at":"2026-01-01T00:00:00Z"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/auth/me":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid token"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/auth/tokens/tok-cleanup-fail":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()

	setLoginEnv(t, "https://auth.example.com", "test-client", api.URL)
	withStubAuthFlow(t, func(context.Context, authflow.Config) (*authflow.Result, error) {
		return &authflow.Result{IDToken: "id-token-not-printed"}, nil
	})

	_, errBuf, exec := newLoginTestRoot(t)
	err := exec("--config", cfgPath, "login")
	if got := loginExitCode(err); got != output.ExitAuth {
		t.Fatalf("cleanup failure changed original exit = %d, want %d; stderr=%s", got, output.ExitAuth, errBuf)
	}
	env := decodeTrailingJSONObject(t, errBuf.String())
	if code, _ := env["code"].(string); code != "auth_error" {
		t.Fatalf("cleanup failure changed original code = %q, want auth_error", code)
	}
	if strings.Contains(errBuf.String(), "sapi_cleanup_failure") {
		t.Fatal("cleanup failure leaked minted PAT")
	}
}

// ---- fail-fast on unconfigured Cognito client id ----

func TestLoginCmd_MissingClientID(t *testing.T) {
	if credentials.DefaultCognitoClientID != "" {
		t.Skip("binary built with a Cognito client ID via ldflags; cannot exercise the empty-client_id path")
	}
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	setLoginEnv(t, "https://auth.example.com", "", "https://unused.example.com")

	called := false
	withStubAuthFlow(t, func(ctx context.Context, cfg authflow.Config) (*authflow.Result, error) {
		called = true
		return nil, fmt.Errorf("must not be called")
	})

	_, errBuf, exec := newLoginTestRoot(t)
	err := exec("--config", cfgPath, "login")

	if got := loginExitCode(err); got != output.ExitValidation {
		t.Errorf("login missing client id: want exit %d, got %d; stderr=%s", output.ExitValidation, got, errBuf)
	}
	if called {
		t.Error("runAuthFlow must not be called when the Cognito client id is unconfigured")
	}
	var env map[string]any
	if jsonErr := json.NewDecoder(errBuf).Decode(&env); jsonErr != nil {
		t.Fatalf("stderr not JSON: %v; stderr=%s", jsonErr, errBuf)
	}
	if code, _ := env["code"].(string); code != "validation_error" {
		t.Errorf("want code validation_error, got %q", code)
	}
	if _, statErr := os.Stat(cfgPath); !os.IsNotExist(statErr) {
		t.Fatalf("missing client id should not write config; stat err=%v", statErr)
	}
}

// ---- 8.2/8.4: authflow typed-error to exit-code mapping (errors.As, not
// string matching or ==) ----

func TestLoginCmd_AuthFlowErrorMapping(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantExit int
		wantCode string
	}{
		{"ports busy", &authflow.PortsBusyError{Ports: authflow.DefaultPorts}, output.ExitGeneral, "port_error"},
		{"timeout", &authflow.TimeoutError{}, output.ExitGeneral, "timeout_error"},
		{"idp error", &authflow.IdPError{Code: "access_denied"}, output.ExitAuth, "auth_error"},
		{"exchange error", &authflow.ExchangeError{Reason: "boom"}, output.ExitAuth, "auth_error"},
		{"callback error", &authflow.CallbackError{Reason: "missing authorization code"}, output.ExitAuth, "auth_error"},
		{"state mismatch", &authflow.StateMismatchError{}, output.ExitValidation, "validation_error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfgPath := filepath.Join(t.TempDir(), "config.yaml")
			setLoginEnv(t, "https://auth.example.com", "test-client", "https://unused.example.com")
			withStubAuthFlow(t, func(ctx context.Context, cfg authflow.Config) (*authflow.Result, error) {
				return nil, tt.err
			})

			_, errBuf, exec := newLoginTestRoot(t)
			err := exec("--config", cfgPath, "login")

			if got := loginExitCode(err); got != tt.wantExit {
				t.Errorf("%s: want exit %d, got %d; stderr=%s", tt.name, tt.wantExit, got, errBuf)
			}
			var env map[string]any
			if jsonErr := json.NewDecoder(errBuf).Decode(&env); jsonErr != nil {
				t.Fatalf("%s: stderr not JSON: %v; stderr=%s", tt.name, jsonErr, errBuf)
			}
			if code, _ := env["code"].(string); code != tt.wantCode {
				t.Errorf("%s: want code %q, got %q", tt.name, tt.wantCode, code)
			}
			if _, statErr := os.Stat(cfgPath); !os.IsNotExist(statErr) {
				t.Fatalf("%s: failed login should not write config; stat err=%v", tt.name, statErr)
			}
		})
	}
}

// ---- mint/whoami failures go through the existing handleAPIError path,
// not the authflow auth_error mapping (design's Error Taxonomy + Rules
// Enforced) ----

func TestLoginCmd_MintFailureUsesAPIErrorPath(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	cognitoSrv := loginFakeCognito(t, "id-token-mintfail")
	defer cognitoSrv.Close()
	// 404 on CreateToken maps to "not_found"/exit 3 via the cloud-api client's
	// error parsing — a code/exit pair the authflow mapping never produces,
	// proving this path is handleAPIError, not mapAuthFlowError.
	cloudSrv := cloudAPIStub(t, "id-token-mintfail", "sapi_unused", http.StatusNotFound)
	defer cloudSrv.Close()

	setLoginEnv(t, cognitoSrv.URL, "test-client", cloudSrv.URL)
	withStubAuthFlow(t, stubSuccessfulAuthFlow(cognitoSrv, "good-code-2"))

	outBuf, errBuf, exec := newLoginTestRoot(t)
	err := exec("--config", cfgPath, "login")

	if got := loginExitCode(err); got != output.ExitNotFound {
		t.Fatalf("login mint failure: want exit %d, got %d; stderr=%s", output.ExitNotFound, got, errBuf)
	}
	// stderr also contains the authorize-URL message printed by authflow.Run
	// before the mint call (design: authflow's Writer is cmd.ErrOrStderr()),
	// so the JSON error envelope is not the first thing on the stream —
	// decode from the first "{" instead of the buffer start.
	env := decodeTrailingJSONObject(t, errBuf.String())
	if code, _ := env["code"].(string); code != "not_found" {
		t.Errorf("want code not_found, got %q", code)
	}
	if _, statErr := os.Stat(cfgPath); !os.IsNotExist(statErr) {
		t.Fatalf("mint failure should not write config; stat err=%v", statErr)
	}
	if strings.Contains(outBuf.String(), "id-token-mintfail") || strings.Contains(errBuf.String(), "id-token-mintfail") {
		t.Error("id_token leaked on mint-failure path")
	}
}

// ---- R10: root wiring registers login as a top-level command ----

func TestLoginCmd_RegisteredOnRoot(t *testing.T) {
	root := NewRootCmd()
	cmd, _, err := root.Find([]string{"login"})
	if err != nil {
		t.Fatalf("root.Find(login): %v", err)
	}
	if cmd == nil || cmd.Name() != "login" {
		t.Fatalf("login command not registered on root; got %+v", cmd)
	}
}
