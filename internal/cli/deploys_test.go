package cli_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/sureva-ch/sureva-cli/internal/output"
)

const (
	testOrgSlug  = "acme"
	testOrgID    = "org-1"
	testAppID    = "app-1"
	testDeployID = "deploy-1"
)

func deploys_mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/orgs", orgsHandler(testOrgSlug, testOrgID))
	return mux
}

// ---- deploys trigger: success ----

func TestDeploysTrigger_Success(t *testing.T) {
	mux := deploys_mux()
	mux.HandleFunc("/v1/orgs/"+testOrgID+"/apps/"+testAppID+"/deployments", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "expected POST", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"` + testDeployID + `","app_id":"` + testAppID + `","status":"pending","release_tag":"v1.0.0","environment_id":"","created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z"}`))
	})
	srv := newTestServer(t, mux)
	outBuf, _, exec := newTestRoot(t, srv)

	err := exec("deploys", "trigger", testAppID, "--org", testOrgSlug, "--tag", "v1.0.0")

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("deploys trigger: want exit 0, got %d", got)
	}
	var got map[string]any
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("deploys trigger: stdout not valid JSON: %v\noutput: %s", jsonErr, outBuf)
	}
	if id, _ := got["id"].(string); id != testDeployID {
		t.Errorf("deploys trigger: want id %q, got %q", testDeployID, id)
	}
	if status, _ := got["status"].(string); status != "pending" {
		t.Errorf("deploys trigger: want status pending, got %q", status)
	}
}

// ---- deploys list: success ----

func TestDeploysList_Success(t *testing.T) {
	mux := deploys_mux()
	mux.HandleFunc("/v1/orgs/"+testOrgID+"/apps/"+testAppID+"/deployments", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "expected GET", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"` + testDeployID + `","app_id":"` + testAppID + `","status":"success","release_tag":"v1.0.0","environment_id":"","created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z"}]`))
	})
	srv := newTestServer(t, mux)
	outBuf, _, exec := newTestRoot(t, srv)

	err := exec("deploys", "list", testAppID, "--org", testOrgSlug)

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("deploys list: want exit 0, got %d", got)
	}
	var got []map[string]any
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("deploys list: stdout not valid JSON array: %v\noutput: %s", jsonErr, outBuf)
	}
	if len(got) == 0 {
		t.Errorf("deploys list: expected at least one deployment")
	}
}

// ---- deploys status: success ----

func TestDeploysStatus_Success(t *testing.T) {
	mux := deploys_mux()
	mux.HandleFunc("/v1/orgs/"+testOrgID+"/apps/"+testAppID+"/deployments/"+testDeployID, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + testDeployID + `","app_id":"` + testAppID + `","status":"success","release_tag":"v1.0.0","environment_id":"","created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z"}`))
	})
	srv := newTestServer(t, mux)
	outBuf, _, exec := newTestRoot(t, srv)

	err := exec("deploys", "status", testAppID, testDeployID, "--org", testOrgSlug)

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("deploys status: want exit 0, got %d", got)
	}
	var got map[string]any
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("deploys status: stdout not valid JSON: %v\noutput: %s", jsonErr, outBuf)
	}
	if status, _ := got["status"].(string); status != "success" {
		t.Errorf("deploys status: want status success, got %q", status)
	}
}

func TestDeploysCancel_Success(t *testing.T) {
	mux := deploys_mux()
	mux.HandleFunc("/v1/orgs/"+testOrgID+"/apps/"+testAppID+"/deployments/"+testDeployID, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "expected DELETE", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	srv := newTestServer(t, mux)
	outBuf, _, exec := newTestRoot(t, srv)

	err := exec("deploys", "cancel", testAppID, testDeployID, "--org", testOrgSlug)

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("deploys cancel: want exit 0, got %d", got)
	}
	var got map[string]any
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("deploys cancel: stdout not valid JSON: %v\noutput: %s", jsonErr, outBuf)
	}
	if id, _ := got["id"].(string); id != testDeployID {
		t.Errorf("deploys cancel: want id %q, got %q", testDeployID, id)
	}
	if status, _ := got["status"].(string); status != "cancelled" {
		t.Errorf("deploys cancel: want status cancelled, got %q", status)
	}
}

func TestDeploysCancel_NotFound(t *testing.T) {
	mux := deploys_mux()
	mux.HandleFunc("/v1/orgs/"+testOrgID+"/apps/"+testAppID+"/deployments/"+testDeployID, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "expected DELETE", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"deployment not found"}`))
	})
	srv := newTestServer(t, mux)
	_, errBuf, exec := newTestRoot(t, srv)

	err := exec("deploys", "cancel", testAppID, testDeployID, "--org", testOrgSlug)

	if got := exitCode(err); got != output.ExitNotFound {
		t.Errorf("deploys cancel not found: want exit %d, got %d; stderr: %s", output.ExitNotFound, got, errBuf)
	}
	var env map[string]any
	if jsonErr := json.NewDecoder(errBuf).Decode(&env); jsonErr != nil {
		t.Fatalf("deploys cancel not found: stderr not JSON: %v\nstderr: %s", jsonErr, errBuf)
	}
	if code, _ := env["code"].(string); code != "not_found" {
		t.Errorf("deploys cancel not found: want code not_found, got %q", code)
	}
}

// ---- T18: deploys trigger --wait RED tests ----

func TestDeploysTrigger_Wait_Success(t *testing.T) {
	mux := deploys_mux()
	// POST → create deployment (pending).
	mux.HandleFunc("/v1/orgs/"+testOrgID+"/apps/"+testAppID+"/deployments", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "expected POST", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"` + testDeployID + `","app_id":"` + testAppID + `","status":"pending","release_tag":"v1.0.0","environment_id":"","created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z"}`))
	})
	// GET → poll; return pending once, then success.
	var polls int32
	mux.HandleFunc("/v1/orgs/"+testOrgID+"/apps/"+testAppID+"/deployments/"+testDeployID, func(w http.ResponseWriter, _ *http.Request) {
		n := int(polls)
		polls++
		status := "pending"
		if n >= 1 {
			status = "success"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + testDeployID + `","app_id":"` + testAppID + `","status":"` + status + `","release_tag":"v1.0.0","environment_id":"","created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z"}`))
	})
	srv := newTestServer(t, mux)
	outBuf, _, exec := newTestRoot(t, srv)

	err := exec("deploys", "trigger", testAppID, "--org", testOrgSlug, "--tag", "v1.0.0",
		"--wait", "--wait-interval", "1ms", "--wait-timeout", "5s")

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("deploys trigger --wait success: want exit 0, got %d", got)
	}
	var got map[string]any
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("deploys trigger --wait success: stdout not JSON: %v\noutput: %s", jsonErr, outBuf)
	}
	if status, _ := got["status"].(string); status != "success" {
		t.Errorf("deploys trigger --wait success: want status success, got %q", status)
	}
}

func TestDeploysTrigger_Wait_Failed_ExitGeneral(t *testing.T) {
	mux := deploys_mux()
	mux.HandleFunc("/v1/orgs/"+testOrgID+"/apps/"+testAppID+"/deployments", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "expected POST", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"` + testDeployID + `","app_id":"` + testAppID + `","status":"pending","release_tag":"v1.0.0","environment_id":"","created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z"}`))
	})
	mux.HandleFunc("/v1/orgs/"+testOrgID+"/apps/"+testAppID+"/deployments/"+testDeployID, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + testDeployID + `","app_id":"` + testAppID + `","status":"failed","release_tag":"v1.0.0","environment_id":"","created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z"}`))
	})
	srv := newTestServer(t, mux)
	_, errBuf, exec := newTestRoot(t, srv)

	err := exec("deploys", "trigger", testAppID, "--org", testOrgSlug, "--tag", "v1.0.0",
		"--wait", "--wait-interval", "1ms", "--wait-timeout", "5s")

	if got := exitCode(err); got != output.ExitGeneral {
		t.Errorf("deploys trigger --wait failed: want exit %d, got %d; stderr: %s", output.ExitGeneral, got, errBuf)
	}
	var env map[string]any
	if jsonErr := json.NewDecoder(errBuf).Decode(&env); jsonErr != nil {
		t.Fatalf("deploys trigger --wait failed: stderr not JSON: %v\nstderr: %s", jsonErr, errBuf)
	}
	if code, _ := env["code"].(string); code != "deploy_failed" {
		t.Errorf("deploys trigger --wait failed: want code deploy_failed, got %q", code)
	}
}

func TestDeploysTrigger_Wait_Timeout_ExitGeneral(t *testing.T) {
	mux := deploys_mux()
	mux.HandleFunc("/v1/orgs/"+testOrgID+"/apps/"+testAppID+"/deployments", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "expected POST", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"` + testDeployID + `","app_id":"` + testAppID + `","status":"pending","release_tag":"v1.0.0","environment_id":"","created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z"}`))
	})
	// Poll always returns pending — forces timeout.
	mux.HandleFunc("/v1/orgs/"+testOrgID+"/apps/"+testAppID+"/deployments/"+testDeployID, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + testDeployID + `","app_id":"` + testAppID + `","status":"pending","release_tag":"v1.0.0","environment_id":"","created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z"}`))
	})
	srv := newTestServer(t, mux)
	_, errBuf, exec := newTestRoot(t, srv)

	err := exec("deploys", "trigger", testAppID, "--org", testOrgSlug, "--tag", "v1.0.0",
		"--wait", "--wait-interval", "1ms", "--wait-timeout", "10ms")

	if got := exitCode(err); got != output.ExitGeneral {
		t.Errorf("deploys trigger --wait timeout: want exit %d, got %d; stderr: %s", output.ExitGeneral, got, errBuf)
	}
	var env map[string]any
	if jsonErr := json.NewDecoder(errBuf).Decode(&env); jsonErr != nil {
		t.Fatalf("deploys trigger --wait timeout: stderr not JSON: %v\nstderr: %s", jsonErr, errBuf)
	}
	if code, _ := env["code"].(string); code != "wait_timeout" {
		t.Errorf("deploys trigger --wait timeout: want code wait_timeout, got %q", code)
	}
}

func TestDeploysTrigger_NoWait_Unchanged(t *testing.T) {
	mux := deploys_mux()
	mux.HandleFunc("/v1/orgs/"+testOrgID+"/apps/"+testAppID+"/deployments", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "expected POST", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"` + testDeployID + `","app_id":"` + testAppID + `","status":"pending","release_tag":"v1.0.0","environment_id":"","created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z"}`))
	})
	srv := newTestServer(t, mux)
	outBuf, _, exec := newTestRoot(t, srv)

	// Without --wait the command returns immediately with pending status.
	err := exec("deploys", "trigger", testAppID, "--org", testOrgSlug, "--tag", "v1.0.0")

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("deploys trigger no --wait: want exit 0, got %d", got)
	}
	var got map[string]any
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("deploys trigger no --wait: stdout not JSON: %v\noutput: %s", jsonErr, outBuf)
	}
	if status, _ := got["status"].(string); status != "pending" {
		t.Errorf("deploys trigger no --wait: want status pending, got %q", status)
	}
}

// ---- logs: success (non-streaming snapshot) ----
// When --env-id is omitted the CLI resolves the app's default environment via
// GET /environments first, then fetches logs at the env-scoped path — the
// exact route shape chi serves in cloud-api (no empty path segments).

const testEnvID = "env-1"

func registerEnvironmentsHandler(mux *http.ServeMux) {
	mux.HandleFunc("/v1/orgs/"+testOrgID+"/apps/"+testAppID+"/environments", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"` + testEnvID + `","app_id":"` + testAppID + `","name":"production","branch":"main","is_default":true,"is_active":true}]`))
	})
}

func TestLogs_SnapshotResolvesDefaultEnv(t *testing.T) {
	mux := deploys_mux()
	registerEnvironmentsHandler(mux)
	mux.HandleFunc("/v1/orgs/"+testOrgID+"/apps/"+testAppID+"/environments/"+testEnvID+"/logs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"function_name":"my-fn","log_group":"/aws/lambda/my-fn","events":[{"timestamp":1700000000000,"message":"hello"}]}`))
	})
	srv := newTestServer(t, mux)
	outBuf, _, exec := newTestRoot(t, srv)

	err := exec("logs", testAppID, "--org", testOrgSlug)

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("logs: want exit 0, got %d", got)
	}
	var got map[string]any
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("logs: stdout not valid JSON: %v\noutput: %s", jsonErr, outBuf)
	}
}

// ---- logs: explicit --env-id skips environment resolution ----

func TestLogs_ExplicitEnvID(t *testing.T) {
	mux := deploys_mux()
	mux.HandleFunc("/v1/orgs/"+testOrgID+"/apps/"+testAppID+"/environments/"+testEnvID+"/logs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"function_name":"my-fn","log_group":"/aws/lambda/my-fn","events":[]}`))
	})
	srv := newTestServer(t, mux)
	_, _, exec := newTestRoot(t, srv)

	err := exec("logs", testAppID, "--org", testOrgSlug, "--env-id", testEnvID)

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("logs --env-id: want exit 0, got %d", got)
	}
}

// ---- logs: 204 No Content returns empty (not an error) ----

func TestLogs_EmptyOn204(t *testing.T) {
	mux := deploys_mux()
	registerEnvironmentsHandler(mux)
	mux.HandleFunc("/v1/orgs/"+testOrgID+"/apps/"+testAppID+"/environments/"+testEnvID+"/logs", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	srv := newTestServer(t, mux)
	_, _, exec := newTestRoot(t, srv)

	err := exec("logs", testAppID, "--org", testOrgSlug)

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("logs 204: want exit 0, got %d", got)
	}
}

// ---- deploys trigger: upload-backed apps ----

const deploymentsPath = "/v1/orgs/" + testOrgID + "/apps/" + testAppID + "/deployments"

func triggerStub(t *testing.T, status int, body string, gotBody *map[string]any, calls *int) *http.ServeMux {
	t.Helper()
	mux := deploys_mux()
	mux.HandleFunc(deploymentsPath, func(w http.ResponseWriter, r *http.Request) {
		*calls++
		_ = json.NewDecoder(r.Body).Decode(gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	return mux
}

func TestDeploysTrigger_SourceID_SendsSourceID(t *testing.T) {
	var sent map[string]any
	var calls int
	mux := triggerStub(t, http.StatusCreated, `{"id":"`+testDeployID+`","app_id":"`+testAppID+`","status":"pending","release_tag":"src-3"}`, &sent, &calls)
	srv := newTestServer(t, mux)
	outBuf, _, exec := newTestRoot(t, srv)

	err := exec("deploys", "trigger", testAppID, "--org", testOrgSlug, "--source-id", "src-id-3")

	if got := exitCode(err); got != output.ExitOK {
		t.Fatalf("want exit 0, got %d", got)
	}
	if sent["source_id"] != "src-id-3" {
		t.Errorf("source_id sent = %v, want src-id-3 (body: %v)", sent["source_id"], sent)
	}
	if _, present := sent["release_tag"]; present {
		t.Errorf("release_tag must not be sent with --source-id (body: %v)", sent)
	}
	if !strings.Contains(outBuf.String(), "src-3") {
		t.Errorf("stdout missing the deployment: %s", outBuf)
	}
}

func TestDeploysTrigger_NeitherFlag_SendsNeither(t *testing.T) {
	var sent map[string]any
	var calls int
	mux := triggerStub(t, http.StatusCreated, `{"id":"`+testDeployID+`","status":"pending"}`, &sent, &calls)
	srv := newTestServer(t, mux)
	_, _, exec := newTestRoot(t, srv)

	if got := exitCode(exec("deploys", "trigger", testAppID, "--org", testOrgSlug)); got != output.ExitOK {
		t.Fatalf("want exit 0, got %d", got)
	}
	if _, present := sent["source_id"]; present {
		t.Errorf("source_id sent without --source-id: %v", sent)
	}
	if _, present := sent["release_tag"]; present {
		t.Errorf("release_tag sent without --tag: %v", sent)
	}
}

func TestDeploysTrigger_TagAndSourceID_MutuallyExclusive(t *testing.T) {
	var sent map[string]any
	var calls int
	mux := triggerStub(t, http.StatusCreated, `{}`, &sent, &calls)
	srv := newTestServer(t, mux)
	_, errBuf, exec := newTestRoot(t, srv)

	err := exec("deploys", "trigger", testAppID, "--org", testOrgSlug, "--tag", "v1.0.0", "--source-id", "src-id-3")

	if got := exitCode(err); got != output.ExitValidation {
		t.Errorf("want exit %d, got %d", output.ExitValidation, got)
	}
	if calls != 0 {
		t.Errorf("a request was sent (%d calls); the conflict must be caught before any request", calls)
	}
	var env map[string]any
	if jsonErr := json.NewDecoder(errBuf).Decode(&env); jsonErr != nil {
		t.Fatalf("stderr not JSON: %v\nstderr: %s", jsonErr, errBuf)
	}
	if env["code"] != "validation_error" {
		t.Errorf("code = %v, want validation_error", env["code"])
	}
	if msg, _ := env["error"].(string); !strings.Contains(msg, "--tag") || !strings.Contains(msg, "--source-id") {
		t.Errorf("message should name both flags: %q", msg)
	}
}

func TestDeploysTrigger_SourceErrors_AreDistinguishable(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		message  string
		wantExit int
		wantCode string
	}{
		{"expired", http.StatusGone, "this source archive has expired and is no longer stored; upload the source again to deploy it", output.ExitGeneral, "source_expired"},
		{"not ready", http.StatusConflict, "this source archive is rejected and cannot be deployed; only a ready archive can", output.ExitGeneral, "source_not_ready"},
		{"deploy in progress", http.StatusConflict, "a deployment is already in progress for this app and environment", output.ExitGeneral, "api_error"},
		{"unknown release", http.StatusNotFound, "source not found", output.ExitNotFound, "not_found"},
		{"tag on upload app", http.StatusBadRequest, "release_tag is not accepted for an upload-backed app", output.ExitValidation, "validation_error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var sent map[string]any
			var calls int
			body, _ := json.Marshal(map[string]string{"error": tc.message})
			srv := newTestServer(t, triggerStub(t, tc.status, string(body), &sent, &calls))
			_, errBuf, exec := newTestRoot(t, srv)

			err := exec("deploys", "trigger", testAppID, "--org", testOrgSlug, "--source-id", "src-id-3")

			if got := exitCode(err); got != tc.wantExit {
				t.Errorf("exit = %d, want %d", got, tc.wantExit)
			}
			var env map[string]any
			if jsonErr := json.NewDecoder(errBuf).Decode(&env); jsonErr != nil {
				t.Fatalf("stderr not JSON: %v\nstderr: %s", jsonErr, errBuf)
			}
			if env["code"] != tc.wantCode {
				t.Errorf("code = %v, want %s", env["code"], tc.wantCode)
			}
			if env["http_status"] != float64(tc.status) {
				t.Errorf("http_status = %v, want %d", env["http_status"], tc.status)
			}
			if env["error"] != tc.message {
				t.Errorf("error = %v, want the API message unchanged", env["error"])
			}
		})
	}
}
