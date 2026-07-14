package cli_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/sureva-ch/sureva-cli/internal/output"
)

// orgsAndAppsHandler returns an http.ServeMux that simulates an org + apps pair.
func orgsAndAppsHandler(orgSlug, orgID string, appsJSON, appJSON string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/orgs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"` + orgID + `","name":"Acme","slug":"` + orgSlug + `","created_at":"2024-01-01T00:00:00Z"}]`))
	})
	if appsJSON != "" {
		mux.HandleFunc("/v1/orgs/"+orgID+"/apps", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(appsJSON))
		})
	}
	if appJSON != "" {
		mux.HandleFunc("/v1/orgs/"+orgID+"/apps/app-1", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(appJSON))
		})
	}
	return mux
}

// ---- spec B-01a: apps list default JSON output ----

func TestAppsList_JSONOutput(t *testing.T) {
	const appsJSON = `[{"id":"app-1","org_id":"org-1","name":"my-app","label":"my-app","type":"api","aws_region":"us-east-1","github_repo_full":"acme/repo","github_branch":"main","subdomain":"my-app","domain_status":"active","visibility":"private","is_active":true,"created_at":"2024-01-01T00:00:00Z"}]`
	handler := orgsAndAppsHandler("acme", "org-1", appsJSON, "")
	srv := newTestServer(t, handler)
	outBuf, _, exec := newTestRoot(t, srv)

	err := exec("apps", "list", "--org", "acme")

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("apps list: want exit 0, got %d", got)
	}
	var got []map[string]any
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("apps list: stdout not valid JSON array: %v\noutput: %s", jsonErr, outBuf)
	}
	if len(got) == 0 {
		t.Errorf("apps list: expected at least one app in output")
	}
}

// ---- spec B-01a: apps list without --org uses flat endpoint ----

func TestAppsList_NoOrg_UsesFlatEndpoint(t *testing.T) {
	const appsJSON = `[{"id":"app-1","org_id":"org-1","name":"my-app","label":"my-app","type":"api","aws_region":"us-east-1","github_repo_full":"acme/repo","github_branch":"main","subdomain":"my-app","domain_status":"active","visibility":"private","is_active":true,"created_at":"2024-01-01T00:00:00Z"}]`
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/apps", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(appsJSON))
	})
	srv := newTestServer(t, mux)
	outBuf, _, exec := newTestRoot(t, srv)

	err := exec("apps", "list")

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("apps list (no org): want exit 0, got %d", got)
	}
	var got []map[string]any
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("apps list (no org): stdout not valid JSON: %v\noutput: %s", jsonErr, outBuf)
	}
}

// ---- spec B-02b: apps get not-found → exit 3 ----

func TestAppsGet_NotFound_ExitCode(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/orgs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"org-1","name":"Acme","slug":"acme","created_at":"2024-01-01T00:00:00Z"}]`))
	})
	mux.HandleFunc("/v1/orgs/org-1/apps/nonexistent", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"app not found"}`))
	})
	srv := newTestServer(t, mux)
	_, errBuf, exec := newTestRoot(t, srv)

	err := exec("apps", "get", "nonexistent", "--org", "acme")

	if got := exitCode(err); got != output.ExitNotFound {
		t.Errorf("404 response: want exit %d (not_found), got %d; stderr: %s", output.ExitNotFound, got, errBuf)
	}
}

// ---- apps get: missing --org → exit 4 (validation) ----

func TestAppsGet_MissingOrg_ExitValidation(t *testing.T) {
	srv := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Serve orgs as empty list so org resolution returns not-found.
		// But actually with missing slug the validation error fires before the API call.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	_, errBuf, exec := newTestRoot(t, srv)

	err := exec("apps", "get", "app-1")

	if got := exitCode(err); got != output.ExitValidation {
		t.Errorf("missing --org: want exit %d (validation), got %d; stderr: %s", output.ExitValidation, got, errBuf)
	}
	var env map[string]any
	if jsonErr := json.NewDecoder(errBuf).Decode(&env); jsonErr != nil {
		t.Fatalf("missing --org: stderr not JSON: %v\nstderr: %s", jsonErr, errBuf)
	}
	if code, _ := env["code"].(string); code != "validation_error" {
		t.Errorf("missing --org: want code validation_error, got %q", code)
	}
}

// ---- apps get: success ----

func TestAppsGet_Success(t *testing.T) {
	const appJSON = `{"id":"app-1","org_id":"org-1","name":"my-app","label":"my-app","type":"api","aws_region":"us-east-1","github_repo_full":"acme/repo","github_branch":"main","subdomain":"my-app","domain_status":"active","visibility":"private","is_active":true,"created_at":"2024-01-01T00:00:00Z"}`
	handler := orgsAndAppsHandler("acme", "org-1", "", appJSON)
	srv := newTestServer(t, handler)
	outBuf, _, exec := newTestRoot(t, srv)

	err := exec("apps", "get", "app-1", "--org", "acme")

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("apps get: want exit 0, got %d", got)
	}
	var got map[string]any
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("apps get: stdout not valid JSON: %v\noutput: %s", jsonErr, outBuf)
	}
	if id, _ := got["id"].(string); id != "app-1" {
		t.Errorf("apps get: want id app-1, got %q", id)
	}
}

// ---- T16: appView url composition RED tests ----

func TestAppsGet_URL_OmittedWithoutConfiguredSuffix(t *testing.T) {
	const appJSON = `{"id":"app-1","org_id":"org-1","name":"my-app","label":"my-app","type":"web","aws_region":"us-east-1","github_repo_full":"","github_branch":"main","subdomain":"my-app","domain_status":"active","visibility":"private","is_active":true,"created_at":"2024-01-01T00:00:00Z"}`
	handler := orgsAndAppsHandler("acme", "org-1", "", appJSON)
	srv := newTestServer(t, handler)
	outBuf, _, exec := newTestRoot(t, srv)

	t.Setenv("SUREVA_DOMAIN_SUFFIX", "")
	err := exec("apps", "get", "app-1", "--org", "acme")

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("apps get url default: want exit 0, got %d", got)
	}
	var got map[string]any
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("apps get url default: stdout not JSON: %v\noutput: %s", jsonErr, outBuf)
	}
	if _, present := got["url"]; present {
		t.Errorf("apps get url default: url must be omitted without an explicitly configured suffix, got %q", got["url"])
	}
}

func TestAppsGet_URL_EnvOverride(t *testing.T) {
	const appJSON = `{"id":"app-1","org_id":"org-1","name":"my-app","label":"my-app","type":"web","aws_region":"us-east-1","github_repo_full":"","github_branch":"main","subdomain":"my-app","domain_status":"active","visibility":"private","is_active":true,"created_at":"2024-01-01T00:00:00Z"}`
	handler := orgsAndAppsHandler("acme", "org-1", "", appJSON)
	srv := newTestServer(t, handler)
	outBuf, _, exec := newTestRoot(t, srv)

	t.Setenv("SUREVA_DOMAIN_SUFFIX", "example.com")
	err := exec("apps", "get", "app-1", "--org", "acme")

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("apps get url env: want exit 0, got %d", got)
	}
	var got map[string]any
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("apps get url env: stdout not JSON: %v\noutput: %s", jsonErr, outBuf)
	}
	wantURL := "https://my-app.example.com"
	if u, _ := got["url"].(string); u != wantURL {
		t.Errorf("apps get url env: want url %q, got %q", wantURL, u)
	}
}

func TestAppsGet_URL_EmptySubdomain_Omitted(t *testing.T) {
	// App with empty subdomain → url field must be absent from output.
	const appJSON = `{"id":"app-1","org_id":"org-1","name":"my-app","label":"my-app","type":"web","aws_region":"us-east-1","github_repo_full":"","github_branch":"main","subdomain":"","domain_status":"pending","visibility":"private","is_active":true,"created_at":"2024-01-01T00:00:00Z"}`
	handler := orgsAndAppsHandler("acme", "org-1", "", appJSON)
	srv := newTestServer(t, handler)
	outBuf, _, exec := newTestRoot(t, srv)

	t.Setenv("SUREVA_DOMAIN_SUFFIX", "")
	err := exec("apps", "get", "app-1", "--org", "acme")

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("apps get url empty subdomain: want exit 0, got %d", got)
	}
	var got map[string]any
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("apps get url empty subdomain: stdout not JSON: %v\noutput: %s", jsonErr, outBuf)
	}
	if _, present := got["url"]; present {
		t.Errorf("apps get url empty subdomain: url field must be absent when subdomain is empty, got %q", got["url"])
	}
}

// ---- T14: apps delete RED tests ----

func TestAppsDelete_WithYes_202Deleting(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/orgs", orgsHandler("acme", "org-1"))
	mux.HandleFunc("/v1/orgs/org-1/apps/app-1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "expected DELETE", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"deleting"}`))
	})
	srv := newTestServer(t, mux)
	outBuf, _, exec := newTestRoot(t, srv)

	err := exec("apps", "delete", "app-1", "--org", "acme", "--yes")

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("delete --yes: want exit 0, got %d", got)
	}
	var got map[string]any
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("delete --yes: stdout not JSON: %v\noutput: %s", jsonErr, outBuf)
	}
	if status, _ := got["status"].(string); status != "deleting" {
		t.Errorf("delete --yes: want status deleting, got %q", status)
	}
}

func TestAppsDelete_MissingYes_ExitValidation(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/orgs", orgsHandler("acme", "org-1"))
	mux.HandleFunc("/v1/orgs/org-1/apps/app-1", func(w http.ResponseWriter, _ *http.Request) {
		t.Error("DeleteApp must not be called when --yes is absent")
	})
	srv := newTestServer(t, mux)
	_, errBuf, exec := newTestRoot(t, srv)

	err := exec("apps", "delete", "app-1", "--org", "acme")

	if got := exitCode(err); got != output.ExitValidation {
		t.Errorf("delete missing --yes: want exit %d, got %d; stderr: %s", output.ExitValidation, got, errBuf)
	}
	var env map[string]any
	if jsonErr := json.NewDecoder(errBuf).Decode(&env); jsonErr != nil {
		t.Fatalf("delete missing --yes: stderr not JSON: %v\nstderr: %s", jsonErr, errBuf)
	}
	if code, _ := env["code"].(string); code != "validation_error" {
		t.Errorf("delete missing --yes: want code validation_error, got %q", code)
	}
}

func TestAppsDelete_NotFound_ExitNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/orgs", orgsHandler("acme", "org-1"))
	mux.HandleFunc("/v1/orgs/org-1/apps/nonexistent", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"app not found"}`))
	})
	srv := newTestServer(t, mux)
	_, errBuf, exec := newTestRoot(t, srv)

	err := exec("apps", "delete", "nonexistent", "--org", "acme", "--yes")

	if got := exitCode(err); got != output.ExitNotFound {
		t.Errorf("delete not found: want exit %d, got %d; stderr: %s", output.ExitNotFound, got, errBuf)
	}
}

// ---- T11: apps create — resolveTeamID and --wait RED tests ----

// appsCreateMux returns a base mux for apps create tests.
// The teams endpoint returns teamsJSON; additional handlers are registered by callers.
func appsCreateMux(orgSlug, orgID, teamsJSON string) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/orgs", orgsHandler(orgSlug, orgID))
	mux.HandleFunc("/v1/orgs/"+orgID+"/teams", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(teamsJSON))
	})
	return mux
}

func TestAppsCreate_ZeroTeams_ExitValidation(t *testing.T) {
	mux := appsCreateMux("acme", "org-1", "[]")
	mux.HandleFunc("/v1/orgs/org-1/apps", func(w http.ResponseWriter, _ *http.Request) {
		t.Error("CreateApp must not be called when team resolution fails")
	})
	srv := newTestServer(t, mux)
	_, errBuf, exec := newTestRoot(t, srv)

	err := exec("apps", "create", "--name", "my-app", "--type", "web", "--region", "us-east-1", "--org", "acme")

	if got := exitCode(err); got != output.ExitValidation {
		t.Errorf("zero teams: want exit %d, got %d; stderr: %s", output.ExitValidation, got, errBuf)
	}
	var env map[string]any
	if jsonErr := json.NewDecoder(errBuf).Decode(&env); jsonErr != nil {
		t.Fatalf("zero teams: stderr not JSON: %v\nstderr: %s", jsonErr, errBuf)
	}
	if code, _ := env["code"].(string); code != "validation_error" {
		t.Errorf("zero teams: want code validation_error, got %q", code)
	}
}

func TestAppsCreate_SingleTeam_AutoSelect_Success(t *testing.T) {
	const oneTeam = `[{"id":"team-1","org_id":"org-1","slug":"engineers","name":"Engineers","is_system":false,"is_active":true,"created_at":"2024-01-01T00:00:00Z"}]`
	const createdApp = `{"id":"app-new","org_id":"org-1","name":"my-app","label":"my-app","type":"web","aws_region":"us-east-1","github_repo_full":"","github_branch":"main","subdomain":"my-app","domain_status":"pending","visibility":"private","is_active":true,"created_at":"2024-01-01T00:00:00Z"}`
	mux := appsCreateMux("acme", "org-1", oneTeam)
	mux.HandleFunc("/v1/orgs/org-1/apps", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "expected POST", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(createdApp))
	})
	srv := newTestServer(t, mux)
	outBuf, _, exec := newTestRoot(t, srv)

	err := exec("apps", "create", "--name", "my-app", "--type", "web", "--region", "us-east-1", "--org", "acme")

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("single team auto-select: want exit 0, got %d", got)
	}
	var got map[string]any
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("single team auto-select: stdout not JSON: %v\noutput: %s", jsonErr, outBuf)
	}
	if id, _ := got["id"].(string); id != "app-new" {
		t.Errorf("single team auto-select: want id app-new, got %q", id)
	}
}

func TestAppsCreate_MultipleTeams_ExitValidation(t *testing.T) {
	const twoTeams = `[{"id":"team-1","org_id":"org-1","slug":"engineers","name":"Engineers","is_system":false,"is_active":true,"created_at":"2024-01-01T00:00:00Z"},{"id":"team-2","org_id":"org-1","slug":"admins","name":"Admins","is_system":false,"is_active":true,"created_at":"2024-01-01T00:00:00Z"}]`
	mux := appsCreateMux("acme", "org-1", twoTeams)
	srv := newTestServer(t, mux)
	_, errBuf, exec := newTestRoot(t, srv)

	err := exec("apps", "create", "--name", "my-app", "--type", "web", "--region", "us-east-1", "--org", "acme")

	if got := exitCode(err); got != output.ExitValidation {
		t.Errorf("multiple teams: want exit %d, got %d; stderr: %s", output.ExitValidation, got, errBuf)
	}
	var env map[string]any
	if jsonErr := json.NewDecoder(errBuf).Decode(&env); jsonErr != nil {
		t.Fatalf("multiple teams: stderr not JSON: %v\nstderr: %s", jsonErr, errBuf)
	}
	if code, _ := env["code"].(string); code != "validation_error" {
		t.Errorf("multiple teams: want code validation_error, got %q", code)
	}
}

func TestAppsCreate_ExplicitTeam_BySlug(t *testing.T) {
	const twoTeams = `[{"id":"team-1","org_id":"org-1","slug":"engineers","name":"Engineers","is_system":false,"is_active":true,"created_at":"2024-01-01T00:00:00Z"},{"id":"team-2","org_id":"org-1","slug":"admins","name":"Admins","is_system":false,"is_active":true,"created_at":"2024-01-01T00:00:00Z"}]`
	mux := appsCreateMux("acme", "org-1", twoTeams)
	mux.HandleFunc("/v1/orgs/org-1/apps", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"app-new","org_id":"org-1","name":"my-app","type":"web","subdomain":"my-app","domain_status":"pending","is_active":true,"created_at":"2024-01-01T00:00:00Z"}`))
	})
	srv := newTestServer(t, mux)
	outBuf, _, exec := newTestRoot(t, srv)

	err := exec("apps", "create", "--name", "my-app", "--type", "web", "--region", "us-east-1", "--team", "admins", "--org", "acme")

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("explicit team by slug: want exit 0, got %d", got)
	}
	var got map[string]any
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("explicit team by slug: stdout not JSON: %v\noutput: %s", jsonErr, outBuf)
	}
	if id, _ := got["id"].(string); id != "app-new" {
		t.Errorf("explicit team by slug: want id app-new, got %q", id)
	}
}

func TestAppsCreate_MissingRuntime_ExitValidation(t *testing.T) {
	const oneTeam = `[{"id":"team-1","org_id":"org-1","slug":"engineers","name":"Engineers","is_system":false,"is_active":true,"created_at":"2024-01-01T00:00:00Z"}]`
	mux := appsCreateMux("acme", "org-1", oneTeam)
	mux.HandleFunc("/v1/orgs/org-1/apps", func(w http.ResponseWriter, _ *http.Request) {
		t.Error("CreateApp must not be called when runtime validation fails")
	})
	srv := newTestServer(t, mux)
	_, errBuf, exec := newTestRoot(t, srv)

	// --type api requires --runtime; omit it to trigger client-side validation.
	err := exec("apps", "create", "--name", "my-api", "--type", "api", "--region", "us-east-1", "--org", "acme")

	if got := exitCode(err); got != output.ExitValidation {
		t.Errorf("missing runtime: want exit %d, got %d; stderr: %s", output.ExitValidation, got, errBuf)
	}
	var env map[string]any
	if jsonErr := json.NewDecoder(errBuf).Decode(&env); jsonErr != nil {
		t.Fatalf("missing runtime: stderr not JSON: %v\nstderr: %s", jsonErr, errBuf)
	}
	if code, _ := env["code"].(string); code != "validation_error" {
		t.Errorf("missing runtime: want code validation_error, got %q", code)
	}
}

func TestAppsCreate_Wait_DomainActive_ExitOK(t *testing.T) {
	const oneTeam = `[{"id":"team-1","org_id":"org-1","slug":"engineers","name":"Engineers","is_system":false,"is_active":true,"created_at":"2024-01-01T00:00:00Z"}]`
	mux := appsCreateMux("acme", "org-1", oneTeam)
	mux.HandleFunc("/v1/orgs/org-1/apps", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "expected POST", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"app-new","org_id":"org-1","name":"my-app","type":"web","subdomain":"my-app","domain_status":"pending","is_active":true,"created_at":"2024-01-01T00:00:00Z"}`))
	})
	// Poll endpoint: return pending once, then active.
	var polls int32
	mux.HandleFunc("/v1/orgs/org-1/apps/app-new", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		n := int(polls)
		polls++
		domainStatus := "pending"
		if n >= 1 {
			domainStatus = "active"
		}
		_, _ = w.Write([]byte(`{"id":"app-new","org_id":"org-1","name":"my-app","type":"web","subdomain":"my-app","domain_status":"` + domainStatus + `","is_active":true,"created_at":"2024-01-01T00:00:00Z"}`))
	})
	srv := newTestServer(t, mux)
	outBuf, _, exec := newTestRoot(t, srv)

	err := exec("apps", "create", "--name", "my-app", "--type", "web", "--region", "us-east-1", "--org", "acme",
		"--wait", "--wait-interval", "1ms", "--wait-timeout", "5s")

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("wait domain active: want exit 0, got %d", got)
	}
	var got map[string]any
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("wait domain active: stdout not JSON: %v\noutput: %s", jsonErr, outBuf)
	}
	if ds, _ := got["domain_status"].(string); ds != "active" {
		t.Errorf("wait domain active: want domain_status active, got %q", ds)
	}
}

func TestAppsCreate_Wait_DomainFailed_ExitGeneral(t *testing.T) {
	const oneTeam = `[{"id":"team-1","org_id":"org-1","slug":"engineers","name":"Engineers","is_system":false,"is_active":true,"created_at":"2024-01-01T00:00:00Z"}]`
	mux := appsCreateMux("acme", "org-1", oneTeam)
	mux.HandleFunc("/v1/orgs/org-1/apps", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"app-new","org_id":"org-1","name":"my-app","type":"web","subdomain":"my-app","domain_status":"pending","is_active":true,"created_at":"2024-01-01T00:00:00Z"}`))
	})
	mux.HandleFunc("/v1/orgs/org-1/apps/app-new", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"app-new","org_id":"org-1","name":"my-app","type":"web","subdomain":"my-app","domain_status":"failed","is_active":false,"created_at":"2024-01-01T00:00:00Z"}`))
	})
	srv := newTestServer(t, mux)
	_, errBuf, exec := newTestRoot(t, srv)

	err := exec("apps", "create", "--name", "my-app", "--type", "web", "--region", "us-east-1", "--org", "acme",
		"--wait", "--wait-interval", "1ms", "--wait-timeout", "5s")

	if got := exitCode(err); got != output.ExitGeneral {
		t.Errorf("wait domain failed: want exit %d, got %d; stderr: %s", output.ExitGeneral, got, errBuf)
	}
	var env map[string]any
	if jsonErr := json.NewDecoder(errBuf).Decode(&env); jsonErr != nil {
		t.Fatalf("wait domain failed: stderr not JSON: %v\nstderr: %s", jsonErr, errBuf)
	}
	if code, _ := env["code"].(string); code != "domain_failed" {
		t.Errorf("wait domain failed: want code domain_failed, got %q", code)
	}
}

func TestAppsCreate_Wait_Timeout_ExitGeneral(t *testing.T) {
	const oneTeam = `[{"id":"team-1","org_id":"org-1","slug":"engineers","name":"Engineers","is_system":false,"is_active":true,"created_at":"2024-01-01T00:00:00Z"}]`
	mux := appsCreateMux("acme", "org-1", oneTeam)
	mux.HandleFunc("/v1/orgs/org-1/apps", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"app-new","org_id":"org-1","name":"my-app","type":"web","subdomain":"my-app","domain_status":"pending","is_active":true,"created_at":"2024-01-01T00:00:00Z"}`))
	})
	// Poll always returns pending — forces timeout.
	mux.HandleFunc("/v1/orgs/org-1/apps/app-new", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"app-new","org_id":"org-1","name":"my-app","type":"web","subdomain":"my-app","domain_status":"pending","is_active":true,"created_at":"2024-01-01T00:00:00Z"}`))
	})
	srv := newTestServer(t, mux)
	_, errBuf, exec := newTestRoot(t, srv)

	// 1ms interval, 10ms timeout → forces timeout quickly.
	err := exec("apps", "create", "--name", "my-app", "--type", "web", "--region", "us-east-1", "--org", "acme",
		"--wait", "--wait-interval", "1ms", "--wait-timeout", "10ms")

	if got := exitCode(err); got != output.ExitGeneral {
		t.Errorf("wait timeout: want exit %d, got %d; stderr: %s", output.ExitGeneral, got, errBuf)
	}
	var env map[string]any
	if jsonErr := json.NewDecoder(errBuf).Decode(&env); jsonErr != nil {
		t.Fatalf("wait timeout: stderr not JSON: %v\nstderr: %s", jsonErr, errBuf)
	}
	if code, _ := env["code"].(string); code != "wait_timeout" {
		t.Errorf("wait timeout: want code wait_timeout, got %q", code)
	}
}

// ---- W3: apps create — explicit team by ID ----

func TestAppsCreate_ExplicitTeam_ByID(t *testing.T) {
	const twoTeams = `[{"id":"team-uuid-1","org_id":"org-1","slug":"engineers","name":"Engineers","is_system":false,"is_active":true,"created_at":"2024-01-01T00:00:00Z"},{"id":"team-uuid-2","org_id":"org-1","slug":"admins","name":"Admins","is_system":false,"is_active":true,"created_at":"2024-01-01T00:00:00Z"}]`
	mux := appsCreateMux("acme", "org-1", twoTeams)

	var capturedTeamID string
	mux.HandleFunc("/v1/orgs/org-1/apps", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "expected POST", http.StatusMethodNotAllowed)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			capturedTeamID, _ = body["team_id"].(string)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"app-new","org_id":"org-1","name":"my-app","type":"web","subdomain":"my-app","domain_status":"pending","is_active":true,"created_at":"2024-01-01T00:00:00Z"}`))
	})
	srv := newTestServer(t, mux)
	outBuf, _, exec := newTestRoot(t, srv)

	// Pass the raw UUID (not slug) of the second team.
	err := exec("apps", "create", "--name", "my-app", "--type", "web", "--region", "us-east-1", "--team", "team-uuid-2", "--org", "acme")

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("explicit team by id: want exit 0, got %d", got)
	}
	var got map[string]any
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("explicit team by id: stdout not JSON: %v\noutput: %s", jsonErr, outBuf)
	}
	if capturedTeamID != "team-uuid-2" {
		t.Errorf("explicit team by id: want team_id %q in create payload, got %q", "team-uuid-2", capturedTeamID)
	}
}

// ---- W2: apps delete — dispatch_failed exits 1 ----

func TestAppsDelete_DispatchFailed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/orgs", orgsHandler("acme", "org-1"))
	mux.HandleFunc("/v1/orgs/org-1/apps/app-1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "expected DELETE", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"dispatch_failed"}`))
	})
	srv := newTestServer(t, mux)
	_, errBuf, exec := newTestRoot(t, srv)

	err := exec("apps", "delete", "app-1", "--org", "acme", "--yes")

	if got := exitCode(err); got != output.ExitGeneral {
		t.Errorf("dispatch_failed: want exit %d, got %d; stderr: %s", output.ExitGeneral, got, errBuf)
	}
	var env map[string]any
	if jsonErr := json.NewDecoder(errBuf).Decode(&env); jsonErr != nil {
		t.Fatalf("dispatch_failed: stderr not JSON: %v\nstderr: %s", jsonErr, errBuf)
	}
	if code, _ := env["code"].(string); code != "teardown_dispatch_failed" {
		t.Errorf("dispatch_failed: want code teardown_dispatch_failed, got %q", code)
	}
}

// ---- W1: apps list — url field composed ----

func TestAppsList_URL_ComposedField(t *testing.T) {
	// App with subdomain "my-app" → url must appear in apps list JSON output.
	const appsJSON = `[{"id":"app-1","org_id":"org-1","name":"my-app","label":"my-app","type":"web","aws_region":"us-east-1","github_repo_full":"","github_branch":"main","subdomain":"my-app","domain_status":"active","visibility":"private","is_active":true,"created_at":"2024-01-01T00:00:00Z"}]`
	handler := orgsAndAppsHandler("acme", "org-1", appsJSON, "")
	srv := newTestServer(t, handler)
	outBuf, _, exec := newTestRoot(t, srv)

	t.Setenv("SUREVA_DOMAIN_SUFFIX", "example.com")
	err := exec("apps", "list", "--org", "acme")

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("apps list url: want exit 0, got %d", got)
	}
	var got []map[string]any
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("apps list url: stdout not JSON: %v\noutput: %s", jsonErr, outBuf)
	}
	if len(got) == 0 {
		t.Fatal("apps list url: expected at least one app")
	}
	wantURL := "https://my-app.example.com"
	if u, _ := got[0]["url"].(string); u != wantURL {
		t.Errorf("apps list url: want url %q, got %q", wantURL, u)
	}
}

func TestAppsCreateHelpShowsValidation(t *testing.T) {
	outBuf, _, exec := newTestRoot(t, "")

	err := exec("apps", "create", "--help")

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("apps create --help: want exit 0, got %d", got)
	}
	help := outBuf.String()
	for _, want := range []string{
		"VALIDATION / INPUTS",
		"--name: slug, 1-50 chars",
		"lowercase letters, digits, and hyphens",
		"--runtime: required when --type is not web",
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("apps create help missing %q; output:\n%s", want, help)
		}
	}
}

func TestHelpJSON_AppsCreateNameFlagShowsSlugValidation(t *testing.T) {
	outBuf, _, exec := newTestRoot(t, "")

	err := exec("--help", "--json")

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("--help --json: want exit 0, got %d", got)
	}
	if !strings.Contains(outBuf.String(), "Application slug: 1-50 lowercase letters, digits, hyphens; starts/ends alphanumeric") {
		t.Fatalf("help json should expose apps create --name slug validation; output:\n%s", outBuf)
	}
}
