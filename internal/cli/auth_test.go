package cli_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sureva-ch/sureva-cli/internal/cli"
	"github.com/sureva-ch/sureva-cli/internal/output"
)

// ---- shared test helpers ----

// newTestServer starts a local httptest server backed by handler and returns
// its URL and a cleanup function.
func newTestServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv.URL
}

// newTestRoot returns a root cobra command wired to a test server.
// SUREVA_TOKEN and SUREVA_API_URL are set via t.Setenv so they are
// restored after the test. stdout and stderr are captured in the returned buffers.
func newTestRoot(t *testing.T, serverURL string) (*bytes.Buffer, *bytes.Buffer, func(args ...string) error) {
	t.Helper()
	t.Setenv("SUREVA_TOKEN", "sapi_test_deadbeef1234567890123456789012")
	if serverURL != "" {
		t.Setenv("SUREVA_API_URL", serverURL)
	}

	outBuf := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)

	exec := func(args ...string) error {
		root := cli.NewRootCmd()
		root.SetOut(outBuf)
		root.SetErr(errBuf)
		root.SetArgs(args)
		outBuf.Reset()
		errBuf.Reset()
		return root.Execute()
	}
	return outBuf, errBuf, exec
}

// exitCode extracts the exit code from an *ExitError, returns 0 for nil.
func exitCode(err error) int {
	if err == nil {
		return output.ExitOK
	}
	var ee *cli.ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return output.ExitGeneral
}

func TestRootExposesBrowserAndPATImportLogin(t *testing.T) {
	root := cli.NewRootCmd()
	foundBrowserLogin := false
	for _, command := range root.Commands() {
		if command.Name() == "login" {
			foundBrowserLogin = true
		}
	}
	if !foundBrowserLogin {
		t.Fatal("top-level browser login command is missing")
	}
	auth, _, err := root.Find([]string{"auth"})
	if err != nil {
		t.Fatalf("find auth command: %v", err)
	}
	foundPATLogin := false
	for _, command := range auth.Commands() {
		if command.Name() == "login" {
			foundPATLogin = true
		}
	}
	if !foundPATLogin {
		t.Fatal("auth login PAT import command is missing")
	}
}

// ---- auth login requires a token source ----

func TestAuthLogin_MissingToken(t *testing.T) {
	_, errBuf, exec := newTestRoot(t, "")
	t.Setenv("SUREVA_TOKEN", "")
	err := exec("auth", "login")

	if got := exitCode(err); got != output.ExitValidation {
		t.Errorf("auth login missing token: want exit %d, got %d", output.ExitValidation, got)
	}
	var env map[string]any
	if jsonErr := json.NewDecoder(errBuf).Decode(&env); jsonErr != nil {
		t.Fatalf("auth login: stderr is not JSON: %v\nstderr: %s", jsonErr, errBuf)
	}
	if code, _ := env["code"].(string); code != "validation_error" {
		t.Errorf("auth login missing token: want code validation_error, got %q", code)
	}
}

func TestAuthLogin_TokenFlagSavesVerifiedToken(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth/me" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sapi_login_token" {
			t.Errorf("Authorization = %q, want Bearer sapi_login_token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"u-1","email":"ana@example.com","name":"Ana","is_active":true,"created_at":"2026-01-01T00:00:00Z"}`))
	})
	srv := newTestServer(t, handler)
	outBuf, _, exec := newTestRoot(t, srv)
	t.Setenv("SUREVA_TOKEN", "")

	err := exec("--config", cfgPath, "auth", "login", "--token", "sapi_login_token")

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("auth login: want exit 0, got %d", got)
	}
	var got map[string]any
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("auth login: stdout not JSON: %v\noutput: %s", jsonErr, outBuf)
	}
	if status, _ := got["status"].(string); status != "authenticated" {
		t.Errorf("auth login: want status authenticated, got %q", status)
	}
	data, readErr := os.ReadFile(cfgPath)
	if readErr != nil {
		t.Fatalf("read saved config: %v", readErr)
	}
	if !strings.Contains(string(data), "sapi_login_token") {
		t.Fatalf("saved config missing token:\n%s", data)
	}
	info, statErr := os.Stat(cfgPath)
	if statErr != nil {
		t.Fatalf("stat saved config: %v", statErr)
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Errorf("config mode = %04o, want 0600", mode)
	}
}

func TestAuthLogin_TokenStdinSavesVerifiedToken(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth/me" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sapi_stdin_token" {
			t.Errorf("Authorization = %q, want Bearer sapi_stdin_token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"u-1","email":"ana@example.com","name":"Ana","is_active":true,"created_at":"2026-01-01T00:00:00Z"}`))
	})
	srv := newTestServer(t, handler)
	t.Setenv("SUREVA_API_URL", srv)
	t.Setenv("SUREVA_TOKEN", "")

	outBuf := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	root := cli.NewRootCmd()
	root.SetIn(strings.NewReader("sapi_stdin_token\n"))
	root.SetOut(outBuf)
	root.SetErr(errBuf)
	root.SetArgs([]string{"--config", cfgPath, "auth", "login", "--token-stdin"})

	err := root.Execute()

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("auth login --token-stdin: want exit 0, got %d; stderr: %s", got, errBuf)
	}
	data, readErr := os.ReadFile(cfgPath)
	if readErr != nil {
		t.Fatalf("read saved config: %v", readErr)
	}
	if !strings.Contains(string(data), "sapi_stdin_token") {
		t.Fatalf("saved config missing stdin token:\n%s", data)
	}
}

func TestAuthLogin_InvalidTokenDoesNotSave(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth/me" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid token"}`))
	})
	srv := newTestServer(t, handler)
	_, errBuf, exec := newTestRoot(t, srv)
	t.Setenv("SUREVA_TOKEN", "")

	err := exec("--config", cfgPath, "auth", "login", "--token", "sapi_bad_token")

	if got := exitCode(err); got != output.ExitAuth {
		t.Errorf("auth login invalid token: want exit %d, got %d; stderr: %s", output.ExitAuth, got, errBuf)
	}
	var env map[string]any
	if jsonErr := json.NewDecoder(errBuf).Decode(&env); jsonErr != nil {
		t.Fatalf("auth login invalid token: stderr not JSON: %v\nstderr: %s", jsonErr, errBuf)
	}
	if code, _ := env["code"].(string); code != "auth_error" {
		t.Errorf("auth login invalid token: want code auth_error, got %q", code)
	}
	if _, statErr := os.Stat(cfgPath); !os.IsNotExist(statErr) {
		t.Fatalf("invalid login should not write config; stat err=%v", statErr)
	}
}

// ---- spec B-03b: no credentials → exit 2 ----

func TestNoCredentials_ExitAuth(t *testing.T) {
	t.Setenv("SUREVA_TOKEN", "")
	t.Setenv("SUREVA_ORG", "")

	_, errBuf, _ := newTestRoot(t, "")
	// Override the token env again because newTestRoot sets it.
	t.Setenv("SUREVA_TOKEN", "")

	root := cli.NewRootCmd()
	outBuf := new(bytes.Buffer)
	eb := new(bytes.Buffer)
	root.SetOut(outBuf)
	root.SetErr(eb)
	root.SetArgs([]string{"--config", "/nonexistent/sureva.yaml", "orgs", "list"})
	err := root.Execute()

	_ = errBuf // not used for this assertion path

	if got := exitCode(err); got != output.ExitAuth {
		t.Errorf("no credentials: want exit %d (auth), got %d; stderr: %s", output.ExitAuth, got, eb)
	}

	var env map[string]any
	if jsonErr := json.NewDecoder(eb).Decode(&env); jsonErr != nil {
		t.Fatalf("no credentials: stderr not JSON: %v\nstderr: %s", jsonErr, eb)
	}
	if code, _ := env["code"].(string); code != "auth_error" {
		t.Errorf("no credentials: want code auth_error, got %q", code)
	}
	message, _ := env["error"].(string)
	if strings.Contains(message, "auth token create") {
		t.Errorf("no credentials error must not recommend authenticated token creation as bootstrap: %q", message)
	}
	if !strings.Contains(message, "sureva login") {
		t.Errorf("no credentials error should point to browser login: %q", message)
	}
}

// ---- spec B-09a: --help --json parseable ----

func TestHelpJSON_Parseable(t *testing.T) {
	outBuf, _, exec := newTestRoot(t, "")
	err := exec("--help", "--json")

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("--help --json: want exit 0, got %d", got)
	}
	var got map[string]any
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("--help --json output not valid JSON: %v\noutput: %s", jsonErr, outBuf)
	}
	if _, ok := got["commands"]; !ok {
		t.Errorf("--help --json missing 'commands' key; got %v", got)
	}
}

func TestHelpJSON_HidesAuthLoginTokenFlag(t *testing.T) {
	outBuf, _, exec := newTestRoot(t, "")
	err := exec("--help", "--json")

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("--help --json: want exit 0, got %d", got)
	}
	if strings.Contains(outBuf.String(), "Personal access token to verify and save") {
		t.Fatalf("--help --json should not advertise auth login --token; output:\n%s", outBuf)
	}
	if !strings.Contains(outBuf.String(), `"name": "token-stdin"`) {
		t.Fatalf("--help --json should advertise auth login --token-stdin; output:\n%s", outBuf)
	}
}

func TestRootHelp_ExplainsLoginPaths(t *testing.T) {
	help := cli.NewRootCmd().Long
	if strings.Contains(help, "--description") {
		t.Fatalf("root long help should not reference --description; output:\n%s", help)
	}
	if !strings.Contains(help, "Run sureva login for interactive browser authentication") {
		t.Fatalf("root long help should show primary browser login; output:\n%s", help)
	}
	if !strings.Contains(help, "advanced fallback") {
		t.Fatalf("root long help should distinguish PAT import fallback; output:\n%s", help)
	}
}

// ---- spec B-10a: --version emits JSON ----

func TestVersion_JSON(t *testing.T) {
	outBuf, _, exec := newTestRoot(t, "")
	err := exec("--version")

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("--version: want exit 0, got %d", got)
	}
	var got map[string]string
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("--version output not valid JSON: %v\noutput: %s", jsonErr, outBuf)
	}
	for _, field := range []string{"version", "commit", "built_at"} {
		if _, ok := got[field]; !ok {
			t.Errorf("--version missing field %q; keys: %v", field, got)
		}
	}
}

// ---- spec B-02a: auth failure → exit 2 ----

func TestAuthFailure_ExitCode(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid token"}`))
	})
	srv := newTestServer(t, handler)
	_, errBuf, exec := newTestRoot(t, srv)

	err := exec("orgs", "list")

	if got := exitCode(err); got != output.ExitAuth {
		t.Errorf("401 response: want exit %d (auth), got %d", output.ExitAuth, got)
	}
	var env map[string]any
	if jsonErr := json.NewDecoder(errBuf).Decode(&env); jsonErr != nil {
		t.Fatalf("401 response: stderr not JSON: %v\nstderr: %s", jsonErr, errBuf)
	}
	if code, _ := env["code"].(string); code != "auth_error" {
		t.Errorf("401 response: want code auth_error, got %q", code)
	}
}

// ---- spec B-04a: auth token create shows raw token with warning field ----

func TestAuthTokenCreate_ShowsTokenOnce(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{
			"id": "tok-1",
			"name": "ci",
			"token": "sapi_abc123def456",
			"last_four": "f456",
			"status": "active",
			"created_at": "2024-01-01T00:00:00Z"
		}`))
	})
	srv := newTestServer(t, handler)
	outBuf, _, exec := newTestRoot(t, srv)

	err := exec("auth", "token", "create", "--name", "ci")

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("token create: want exit 0, got %d", got)
	}

	var got map[string]any
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("token create: stdout not JSON: %v\noutput: %s", jsonErr, outBuf)
	}
	if token, _ := got["token"].(string); token != "sapi_abc123def456" {
		t.Errorf("token create: want token sapi_abc123def456, got %q", token)
	}
	if warning, _ := got["warning"].(string); warning == "" {
		t.Errorf("token create: missing 'warning' field in output")
	}
}

// ---- spec B-01a: default output is JSON ----

func TestOrgsListJSON_DefaultOutput(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"org-1","name":"Acme","slug":"acme","created_at":"2024-01-01T00:00:00Z"}]`))
	})
	srv := newTestServer(t, handler)
	outBuf, _, exec := newTestRoot(t, srv)

	err := exec("orgs", "list")

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("orgs list: want exit 0, got %d", got)
	}
	var got []map[string]any
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("orgs list: stdout not valid JSON array: %v\noutput: %s", jsonErr, outBuf)
	}
	if len(got) == 0 {
		t.Errorf("orgs list: expected at least one org in output")
	}
}
