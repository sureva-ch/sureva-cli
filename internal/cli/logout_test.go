package cli

// Note: this file is in package cli so it can override the unexported
// clearLogoutToken seam, like login_test.go does for its own seams.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sureva-ch/sureva-cli/internal/credentials"
	"github.com/sureva-ch/sureva-cli/internal/output"
)

const (
	logoutSavedToken = "sapi_saved_token_0123456789abcd"
	logoutEnvToken   = "sapi_env_token_should_never_be_used"
)

// logoutAPI is a fake token API recording every call it receives.
type logoutAPI struct {
	mu           sync.Mutex
	listStatus   int
	revokeStatus int
	tokens       string
	auths        []string
	revoked      []string
	listCalls    int
}

func newLogoutAPI(t *testing.T, tokensJSON string) (*logoutAPI, *httptest.Server) {
	t.Helper()
	api := &logoutAPI{tokens: tokensJSON}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.mu.Lock()
		defer api.mu.Unlock()
		api.auths = append(api.auths, r.Header.Get("Authorization"))
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/auth/tokens":
			api.listCalls++
			if api.listStatus != 0 {
				w.WriteHeader(api.listStatus)
				_, _ = w.Write([]byte(`{"error":"leaky remote body sapi_remote_secret"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(api.tokens))
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v1/auth/tokens/"):
			api.revoked = append(api.revoked, strings.TrimPrefix(r.URL.Path, "/v1/auth/tokens/"))
			if api.revokeStatus != 0 {
				w.WriteHeader(api.revokeStatus)
				_, _ = w.Write([]byte(`{"error":"leaky remote body sapi_remote_secret"}`))
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return api, srv
}

// logoutConfig writes a config holding the saved token plus other keys and
// points the API URL at apiURL through the environment.
func logoutConfig(t *testing.T, apiURL, extra string) string {
	t.Helper()
	t.Setenv("SUREVA_API_URL", apiURL)
	t.Setenv("SUREVA_TOKEN", "")
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := "org: my-org\n" + extra
	content += "token: " + logoutSavedToken + "\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	return path
}

func decodeLogoutOut(t *testing.T, raw string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("stdout not JSON: %v; %s", err, raw)
	}
	return out
}

func assertTokenCleared(t *testing.T, cfgPath string) {
	t.Helper()
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(data), "token") {
		t.Errorf("config still has a token key:\n%s", data)
	}
	if !strings.Contains(string(data), "org: my-org") {
		t.Errorf("config lost other keys:\n%s", data)
	}
}

func assertNoLeaks(t *testing.T, bufs ...string) {
	t.Helper()
	for _, b := range bufs {
		if strings.Contains(b, logoutSavedToken) || strings.Contains(b, "sapi_remote_secret") {
			t.Errorf("output leaks a secret:\n%s", b)
		}
	}
}

func TestLogoutCmd_RevokesMatchedTokenAndClears(t *testing.T) {
	for _, args := range [][]string{{"logout"}, {"auth", "logout"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			api, srv := newLogoutAPI(t, `[{"id":"other","last_four":"zzzz"},{"id":"tok-1","last_four":"abcd"}]`)
			cfg := logoutConfig(t, srv.URL, "")
			outBuf, errBuf, exec := newLoginTestRoot(t)

			err := exec(append(args, "--config", cfg)...)

			if code := loginExitCode(err); code != output.ExitOK {
				t.Fatalf("exit = %d, want 0; stderr=%s", code, errBuf)
			}
			out := decodeLogoutOut(t, outBuf.String())
			if out["status"] != "logged_out" || out["token_revoked"] != true || out["config_path"] != cfg {
				t.Errorf("unexpected output: %v", out)
			}
			if _, ok := out["warning"]; ok {
				t.Errorf("unexpected warning: %v", out["warning"])
			}
			if len(api.revoked) != 1 || api.revoked[0] != "tok-1" {
				t.Errorf("revoked = %v, want [tok-1]", api.revoked)
			}
			for _, a := range api.auths {
				if a != "Bearer "+logoutSavedToken {
					t.Errorf("Authorization = %q, want the saved token", a)
				}
			}
			assertTokenCleared(t, cfg)
			assertNoLeaks(t, outBuf.String(), errBuf.String())
		})
	}
}

func TestLogoutCmd_NoSavedToken(t *testing.T) {
	api, srv := newLogoutAPI(t, `[]`)
	t.Setenv("SUREVA_API_URL", srv.URL)
	t.Setenv("SUREVA_TOKEN", "")
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	original := "org: my-org\n"
	if err := os.WriteFile(cfg, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	outBuf, errBuf, exec := newLoginTestRoot(t)

	err := exec("logout", "--config", cfg)

	if code := loginExitCode(err); code != output.ExitOK {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errBuf)
	}
	out := decodeLogoutOut(t, outBuf.String())
	if out["status"] != "not_logged_in" || out["token_revoked"] != false {
		t.Errorf("unexpected output: %v", out)
	}
	if len(api.auths) != 0 {
		t.Errorf("made %d network calls, want none", len(api.auths))
	}
	if got, _ := os.ReadFile(cfg); string(got) != original {
		t.Errorf("config changed: %q", got)
	}
}

func TestLogoutCmd_MissingConfigFileIsNotLoggedIn(t *testing.T) {
	t.Setenv("SUREVA_TOKEN", "")
	cfg := filepath.Join(t.TempDir(), "absent", "config.yaml")
	outBuf, _, exec := newLoginTestRoot(t)

	if code := loginExitCode(exec("logout", "--config", cfg)); code != output.ExitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if out := decodeLogoutOut(t, outBuf.String()); out["status"] != "not_logged_in" {
		t.Errorf("status = %v", out["status"])
	}
	if _, err := os.Stat(cfg); !os.IsNotExist(err) {
		t.Errorf("config file was created: %v", err)
	}
}

func TestLogoutCmd_RevokeSkippedStillClears(t *testing.T) {
	cases := []struct {
		name         string
		tokens       string
		listStatus   int
		revokeStatus int
		wantRevokes  int
	}{
		{"zero matches", `[{"id":"other","last_four":"zzzz"}]`, 0, 0, 0},
		{"multiple matches", `[{"id":"a","last_four":"abcd"},{"id":"b","last_four":"abcd"}]`, 0, 0, 0},
		{"list unauthorized", `[]`, http.StatusUnauthorized, 0, 0},
		{"list forbidden", `[]`, http.StatusForbidden, 0, 0},
		{"revoke error", `[{"id":"tok-1","last_four":"abcd"}]`, 0, http.StatusForbidden, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api, srv := newLogoutAPI(t, tc.tokens)
			api.listStatus, api.revokeStatus = tc.listStatus, tc.revokeStatus
			cfg := logoutConfig(t, srv.URL, "")
			outBuf, errBuf, exec := newLoginTestRoot(t)

			err := exec("logout", "--config", cfg)

			if code := loginExitCode(err); code != output.ExitOK {
				t.Fatalf("exit = %d, want 0; stderr=%s", code, errBuf)
			}
			out := decodeLogoutOut(t, outBuf.String())
			if out["status"] != "logged_out" || out["token_revoked"] != false {
				t.Errorf("unexpected output: %v", out)
			}
			warning, _ := out["warning"].(string)
			if !strings.Contains(warning, "sureva auth token list") {
				t.Errorf("warning = %q, want manual revoke guidance", warning)
			}
			if len(api.revoked) != tc.wantRevokes {
				t.Errorf("revoke calls = %d, want %d", len(api.revoked), tc.wantRevokes)
			}
			assertTokenCleared(t, cfg)
			assertNoLeaks(t, outBuf.String(), errBuf.String())
		})
	}
}

func TestLogoutCmd_EnvTokenWarnedAndNeverUsed(t *testing.T) {
	api, srv := newLogoutAPI(t, `[{"id":"tok-1","last_four":"abcd"}]`)
	cfg := logoutConfig(t, srv.URL, "")
	t.Setenv("SUREVA_TOKEN", logoutEnvToken)
	outBuf, errBuf, exec := newLoginTestRoot(t)

	err := exec("logout", "--config", cfg)

	if code := loginExitCode(err); code != output.ExitOK {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errBuf)
	}
	out := decodeLogoutOut(t, outBuf.String())
	if out["token_revoked"] != true {
		t.Errorf("token_revoked = %v, want true", out["token_revoked"])
	}
	if warning, _ := out["warning"].(string); !strings.Contains(warning, "SUREVA_TOKEN") {
		t.Errorf("warning = %q, want SUREVA_TOKEN notice", warning)
	}
	for _, a := range api.auths {
		if strings.Contains(a, logoutEnvToken) {
			t.Errorf("env token used for a request: %q", a)
		}
	}
	assertTokenCleared(t, cfg)
}

func TestLogoutCmd_EnvTokenWarnedWhenNotLoggedIn(t *testing.T) {
	t.Setenv("SUREVA_TOKEN", logoutEnvToken)
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	outBuf, _, exec := newLoginTestRoot(t)

	if code := loginExitCode(exec("logout", "--config", cfg)); code != output.ExitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	out := decodeLogoutOut(t, outBuf.String())
	if out["status"] != "not_logged_in" {
		t.Errorf("status = %v", out["status"])
	}
	if warning, _ := out["warning"].(string); !strings.Contains(warning, "SUREVA_TOKEN") {
		t.Errorf("warning = %q, want SUREVA_TOKEN notice", warning)
	}
}

func TestLogoutCmd_ClearFailureExitsNonZero(t *testing.T) {
	_, srv := newLogoutAPI(t, `[{"id":"tok-1","last_four":"abcd"}]`)
	cfg := logoutConfig(t, srv.URL, "")
	original := clearLogoutToken
	clearLogoutToken = func(string) error { return errors.New("disk full") }
	t.Cleanup(func() { clearLogoutToken = original })
	outBuf, errBuf, exec := newLoginTestRoot(t)

	err := exec("logout", "--config", cfg)

	if code := loginExitCode(err); code != output.ExitGeneral {
		t.Fatalf("exit = %d, want %d", code, output.ExitGeneral)
	}
	if outBuf.Len() != 0 {
		t.Errorf("stdout = %q, want empty on failure", outBuf)
	}
	env := decodeTrailingJSONObject(t, errBuf.String())
	if env["code"] != "config_error" {
		t.Errorf("envelope = %v, want config_error", env)
	}
	assertNoLeaks(t, errBuf.String())
}

func TestLogoutCmd_InvalidConfigFailsWithoutChangingIt(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	original := []byte("token: " + logoutSavedToken + "\ninvalid: [unterminated\n")
	if err := os.WriteFile(cfg, original, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUREVA_TOKEN", "")
	_, errBuf, exec := newLoginTestRoot(t)

	err := exec("logout", "--config", cfg)

	if code := loginExitCode(err); code != output.ExitGeneral {
		t.Fatalf("exit = %d, want %d", code, output.ExitGeneral)
	}
	if got, _ := os.ReadFile(cfg); string(got) != string(original) {
		t.Errorf("config changed: %q", got)
	}
	assertNoLeaks(t, errBuf.String())
}

func TestLogoutCmd_UsesConfigAPIURL(t *testing.T) {
	api, srv := newLogoutAPI(t, `[{"id":"tok-1","last_four":"abcd"}]`)
	cfg := logoutConfig(t, "", "api_url: "+srv.URL+"\n")
	t.Setenv("SUREVA_API_URL", "")
	_, _, exec := newLoginTestRoot(t)

	if code := loginExitCode(exec("logout", "--config", cfg)); code != output.ExitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if len(api.revoked) != 1 {
		t.Errorf("revoked = %v, want one revoke through the config api_url", api.revoked)
	}
	if _, err := credentials.SavedTokenFromPath(cfg); err != nil {
		t.Errorf("config unreadable after logout: %v", err)
	}
}

func TestHelpJSON_ListsLogoutCommands(t *testing.T) {
	outBuf, _, exec := newLoginTestRoot(t)
	if code := loginExitCode(exec("--help", "--json")); code != output.ExitOK {
		t.Fatalf("exit = %d", code)
	}
	var tree struct {
		Commands []struct {
			Name        string `json:"name"`
			Subcommands []struct {
				Name string `json:"name"`
			} `json:"subcommands"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(outBuf.Bytes(), &tree); err != nil {
		t.Fatalf("help json: %v", err)
	}
	var top, nested bool
	for _, c := range tree.Commands {
		top = top || c.Name == "logout"
		if c.Name == "auth" {
			for _, s := range c.Subcommands {
				nested = nested || s.Name == "logout"
			}
		}
	}
	if !top || !nested {
		t.Errorf("logout in help tree: top-level=%v auth=%v", top, nested)
	}
}
