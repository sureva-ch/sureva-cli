// Package client_test exercises the internal/client package using httptest servers.
// Tests cover: auth header injection, User-Agent, Content-Type, error envelope parsing,
// 401 guidance message, base URL joining, retry on 5xx, no retry on 4xx, context
// cancellation, network errors, and success decode for each resource method group.
package client_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sureva-ch/sureva-cli/internal/client"
)

const testToken = "sapi_test_token_1234567890abcdef"

// newTestClient creates an httptest.Server backed by handler and a Client targeting it.
// The retry delay is zeroed so tests do not sleep between retries.
func newTestClient(t *testing.T, handler http.HandlerFunc) (*client.Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := client.New(srv.URL, testToken, client.WithRetryDelay(func(int) time.Duration { return 0 }))
	return c, srv
}

// ---- Header injection ----

func TestClientSetsAuthHeader(t *testing.T) {
	t.Parallel()

	var gotAuth string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]client.Org{})
	})

	_, _ = c.ListOrgs(context.Background())

	want := "Bearer " + testToken
	if gotAuth != want {
		t.Errorf("Authorization = %q, want %q", gotAuth, want)
	}
}

func TestClientSetsUserAgent(t *testing.T) {
	t.Parallel()

	var gotUA string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]client.Org{})
	})

	_, _ = c.ListOrgs(context.Background())

	if !strings.HasPrefix(gotUA, "sureva-cli/") {
		t.Errorf("User-Agent = %q, want prefix 'sureva-cli/'", gotUA)
	}
}

func TestClientSetsContentTypeOnPost(t *testing.T) {
	t.Parallel()

	var gotCT string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(client.CreateTokenResponse{ID: "tok-1"})
	})

	_, _ = c.CreateToken(context.Background(), "ci", nil)

	if gotCT != "application/json" {
		t.Errorf("Content-Type on POST = %q, want 'application/json'", gotCT)
	}
}

func TestClientDoesNotSetContentTypeOnGet(t *testing.T) {
	t.Parallel()

	var gotCT string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]client.Org{})
	})

	_, _ = c.ListOrgs(context.Background())

	if gotCT != "" {
		t.Errorf("Content-Type on GET = %q, want empty", gotCT)
	}
}

// ---- Base URL joining (no double slashes) ----

func TestBaseURLJoining_NormalizesOriginAndVersionSuffix(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		baseSuffix string
	}{
		{name: "origin"},
		{name: "trailing slash", baseSuffix: "/"},
		{name: "accidental version suffix", baseSuffix: "/v1"},
		{name: "accidental version suffix with trailing slash", baseSuffix: "/v1/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode([]client.Org{})
			}))
			t.Cleanup(srv.Close)

			c := client.New(srv.URL+tc.baseSuffix, testToken, client.WithRetryDelay(func(int) time.Duration { return 0 }))
			_, _ = c.ListOrgs(context.Background())

			if strings.Contains(gotPath, "//") {
				t.Errorf("URL path contains double slash: %q", gotPath)
			}
			if gotPath != "/v1/orgs" {
				t.Errorf("path = %q, want /v1/orgs", gotPath)
			}
		})
	}
}

// ---- Error envelope parsing ----

// Scenario B-02a: 401 must carry a guidance message that mentions SUREVA_TOKEN.
func TestErrorParsing_401_GivesGuidanceMessage(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
	})

	_, err := c.ListOrgs(context.Background())

	apiErr, ok := err.(*client.APIError)
	if !ok {
		t.Fatalf("error type = %T, want *client.APIError", err)
	}
	if apiErr.HTTPStatus != 401 {
		t.Errorf("HTTPStatus = %d, want 401", apiErr.HTTPStatus)
	}
	if apiErr.Code != "auth_error" {
		t.Errorf("Code = %q, want 'auth_error'", apiErr.Code)
	}
	if !strings.Contains(apiErr.Message, "SUREVA_TOKEN") {
		t.Errorf("401 message does not mention SUREVA_TOKEN: %q", apiErr.Message)
	}
}

// Scenario B-02b: 404 maps to not_found with server message preserved.
func TestErrorParsing_404(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "app not found"})
	})

	_, err := c.GetApp(context.Background(), "org-1", "app-1")

	apiErr, ok := err.(*client.APIError)
	if !ok {
		t.Fatalf("error type = %T, want *client.APIError", err)
	}
	if apiErr.HTTPStatus != 404 {
		t.Errorf("HTTPStatus = %d, want 404", apiErr.HTTPStatus)
	}
	if apiErr.Code != "not_found" {
		t.Errorf("Code = %q, want 'not_found'", apiErr.Code)
	}
	if apiErr.Message != "app not found" {
		t.Errorf("Message = %q, want 'app not found'", apiErr.Message)
	}
}

// 500 after all retries exhausted → APIError with server_error code.
func TestErrorParsing_500_IsServerError(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	_, err := c.ListOrgs(context.Background())

	apiErr, ok := err.(*client.APIError)
	if !ok {
		t.Fatalf("error type = %T, want *client.APIError", err)
	}
	if apiErr.Code != "server_error" {
		t.Errorf("Code = %q, want 'server_error'", apiErr.Code)
	}
	if apiErr.HTTPStatus != 500 {
		t.Errorf("HTTPStatus = %d, want 500", apiErr.HTTPStatus)
	}
}

// ---- Retry behavior ----

func TestRetryOn5xx_SucceedsAfterRetry(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]client.Org{})
	})

	_, err := c.ListOrgs(context.Background())
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	if calls.Load() != 3 {
		t.Errorf("server calls = %d, want 3 (two 5xx + one success)", calls.Load())
	}
}

// TestNoRetryOnPOST proves that non-idempotent requests are never retried:
// a POST that hits a 5xx (or a network failure after the request was sent)
// may already have produced a side effect — retrying it would trigger a
// duplicate deployment or mint a duplicate token.
func TestNoRetryOnPOST(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	})

	_, err := c.TriggerDeployment(context.Background(), "org-1", "app-1", "v1.0.0", "")
	if err == nil {
		t.Fatal("expected error from 5xx POST")
	}
	if calls.Load() != 1 {
		t.Errorf("server calls = %d, want 1 (POST must never be retried)", calls.Load())
	}
}

func TestNoRetryOn4xx(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
	})

	_, _ = c.GetApp(context.Background(), "org-1", "app-1")

	if calls.Load() != 1 {
		t.Errorf("server calls = %d, want 1 (no retry on 4xx)", calls.Load())
	}
}

// ---- Context cancellation ----

func TestContextCancellation(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		// Block until the client's context expires or test cleanup fires.
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := c.ListOrgs(ctx)
	if err == nil {
		t.Fatal("expected context error, got nil")
	}
}

// ---- Network error ----

func TestNetworkError_ReturnsAPIError(t *testing.T) {
	t.Parallel()

	// Close the server before issuing the request so all attempts fail with
	// "connection refused" — a clean network-layer failure with no HTTP response.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srvURL := srv.URL
	srv.Close()

	c := client.New(srvURL, testToken, client.WithRetryDelay(func(int) time.Duration { return 0 }))
	_, err := c.ListOrgs(context.Background())

	if err == nil {
		t.Fatal("expected network error, got nil")
	}
	apiErr, ok := err.(*client.APIError)
	if !ok {
		t.Fatalf("error type = %T, want *client.APIError", err)
	}
	if apiErr.HTTPStatus != 0 {
		t.Errorf("HTTPStatus = %d, want 0 (no HTTP response)", apiErr.HTTPStatus)
	}
	if apiErr.Code != "network_error" {
		t.Errorf("Code = %q, want 'network_error'", apiErr.Code)
	}
}

// ---- Tokens ----

func TestCreateToken_Success(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Second)
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(client.CreateTokenResponse{
			ID:        "tok-abc",
			Name:      "ci",
			Token:     "sapi_" + strings.Repeat("a", 64),
			LastFour:  "aaaa",
			Status:    "active",
			CreatedAt: now,
		})
	})

	resp, err := c.CreateToken(context.Background(), "ci", nil)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if resp.ID != "tok-abc" {
		t.Errorf("ID = %q, want 'tok-abc'", resp.ID)
	}
	if !strings.HasPrefix(resp.Token, "sapi_") {
		t.Errorf("Token = %q, want sapi_ prefix", resp.Token)
	}
}

func TestListTokens_Success(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]client.Token{
			{ID: "t1", Name: "deploy", LastFour: "aaaa", Status: "active"},
			{ID: "t2", Name: "ci", LastFour: "bbbb", Status: "active"},
		})
	})

	tokens, err := c.ListTokens(context.Background())
	if err != nil {
		t.Fatalf("ListTokens: %v", err)
	}
	if len(tokens) != 2 {
		t.Errorf("len(tokens) = %d, want 2", len(tokens))
	}
	if tokens[0].ID != "t1" {
		t.Errorf("tokens[0].ID = %q, want 't1'", tokens[0].ID)
	}
}

func TestRevokeToken_Success(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	})

	err := c.RevokeToken(context.Background(), "tok-1")
	if err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}
}

// ---- Orgs ----

func TestListOrgs_Success(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]client.Org{
			{ID: "org-1", Name: "Acme", Slug: "acme"},
		})
	})

	orgs, err := c.ListOrgs(context.Background())
	if err != nil {
		t.Fatalf("ListOrgs: %v", err)
	}
	if len(orgs) != 1 {
		t.Errorf("len(orgs) = %d, want 1", len(orgs))
	}
	if orgs[0].Slug != "acme" {
		t.Errorf("slug = %q, want 'acme'", orgs[0].Slug)
	}
}

// ---- Apps ----

func TestListApps_Success(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]client.App{
			{ID: "app-1", OrgID: "org-1", Name: "my-api", Type: "api"},
		})
	})

	apps, err := c.ListApps(context.Background(), "org-1")
	if err != nil {
		t.Fatalf("ListApps: %v", err)
	}
	if len(apps) != 1 {
		t.Errorf("len(apps) = %d, want 1", len(apps))
	}
}

func TestListAllApps_Success(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps" {
			t.Errorf("path = %q, want /v1/apps", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]client.App{
			{ID: "app-1", OrgID: "org-1", Name: "api"},
			{ID: "app-2", OrgID: "org-2", Name: "web"},
		})
	})

	apps, err := c.ListAllApps(context.Background())
	if err != nil {
		t.Fatalf("ListAllApps: %v", err)
	}
	if len(apps) != 2 {
		t.Errorf("len(apps) = %d, want 2", len(apps))
	}
}

func TestGetApp_Success(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(client.App{
			ID: "app-1", OrgID: "org-1", Name: "my-api",
		})
	})

	app, err := c.GetApp(context.Background(), "org-1", "app-1")
	if err != nil {
		t.Fatalf("GetApp: %v", err)
	}
	if app.ID != "app-1" {
		t.Errorf("ID = %q, want 'app-1'", app.ID)
	}
}

// ---- Env ----

func TestGetEnv_Success(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"DATABASE_URL": "postgres://...",
			"API_KEY":      "secret",
		})
	})

	env, err := c.GetEnv(context.Background(), "org-1", "app-1")
	if err != nil {
		t.Fatalf("GetEnv: %v", err)
	}
	if env["API_KEY"] != "secret" {
		t.Errorf("API_KEY = %q, want 'secret'", env["API_KEY"])
	}
}

func TestSetEnv_Success(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	})

	err := c.SetEnv(context.Background(), "org-1", "app-1", map[string]string{"KEY": "val"})
	if err != nil {
		t.Fatalf("SetEnv: %v", err)
	}
}

// ---- Deployments ----

func TestListDeployments_Success(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]client.Deployment{
			{ID: "dep-1", AppID: "app-1", Status: "success"},
		})
	})

	deps, err := c.ListDeployments(context.Background(), "org-1", "app-1")
	if err != nil {
		t.Fatalf("ListDeployments: %v", err)
	}
	if len(deps) != 1 {
		t.Errorf("len(deps) = %d, want 1", len(deps))
	}
}

func TestTriggerDeployment_Success(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(client.Deployment{
			ID: "dep-1", AppID: "app-1", Status: "pending",
		})
	})

	dep, err := c.TriggerDeployment(context.Background(), "org-1", "app-1", "v1.0.0", "")
	if err != nil {
		t.Fatalf("TriggerDeployment: %v", err)
	}
	if dep.Status != "pending" {
		t.Errorf("status = %q, want 'pending'", dep.Status)
	}
}

func TestGetDeployment_Success(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(client.Deployment{
			ID: "dep-1", AppID: "app-1", Status: "success",
		})
	})

	dep, err := c.GetDeployment(context.Background(), "org-1", "app-1", "dep-1")
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	if dep.ID != "dep-1" {
		t.Errorf("ID = %q, want 'dep-1'", dep.ID)
	}
}

// ---- Logs ----

func TestGetLogs_Success(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(client.LogsResponse{
			FunctionName: "my-api",
			LogGroup:     "/aws/lambda/my-api",
			Events: []client.LogEvent{
				{Timestamp: 1234567890000, Message: "request received"},
			},
		})
	})

	logs, err := c.GetLogs(context.Background(), "org-1", "app-1", "env-1")
	if err != nil {
		t.Fatalf("GetLogs: %v", err)
	}
	if len(logs.Events) != 1 {
		t.Errorf("len(events) = %d, want 1", len(logs.Events))
	}
	if logs.Events[0].Message != "request received" {
		t.Errorf("message = %q, want 'request received'", logs.Events[0].Message)
	}
}

// 204 No Content means Lambda not yet provisioned — returns empty result, no error.
func TestGetLogs_NoContent_ReturnsEmpty(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	logs, err := c.GetLogs(context.Background(), "org-1", "app-1", "env-1")
	if err != nil {
		t.Fatalf("GetLogs 204: %v", err)
	}
	if logs == nil {
		t.Fatal("expected non-nil LogsResponse for 204")
	}
	if len(logs.Events) != 0 {
		t.Errorf("len(events) = %d, want 0", len(logs.Events))
	}
}

// ---- Whoami decodes the real /auth/me shape ----

func TestWhoamiDecodesAuthMeShape(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth/me" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"u-1","email":"a@b.co","name":"Ana","is_active":true,"created_at":"2026-01-01T00:00:00Z"}`))
	})

	u, err := c.Whoami(context.Background())
	if err != nil {
		t.Fatalf("Whoami: %v", err)
	}
	if u.Email != "a@b.co" || u.Name != "Ana" || !u.IsActive {
		t.Errorf("Whoami decoded wrong fields: %+v", u)
	}
}

// ---- Environments ----

func TestDefaultEnvironmentPicksIsDefault(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"id":"env-a","app_id":"app-1","name":"preview","branch":"dev","is_default":false,"is_active":true},
			{"id":"env-b","app_id":"app-1","name":"production","branch":"main","is_default":true,"is_active":true}
		]`))
	})

	env, err := c.DefaultEnvironment(context.Background(), "org-1", "app-1")
	if err != nil {
		t.Fatalf("DefaultEnvironment: %v", err)
	}
	if env.ID != "env-b" {
		t.Errorf("DefaultEnvironment = %s, want env-b (is_default)", env.ID)
	}
}

func TestDefaultEnvironmentNoEnvironments(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	})

	_, err := c.DefaultEnvironment(context.Background(), "org-1", "app-1")
	if err == nil {
		t.Fatal("expected error when app has no environments")
	}
}
