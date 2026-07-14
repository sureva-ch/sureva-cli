package cli_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/sureva-ch/sureva-cli/internal/output"
)

func TestServicesKVSTablesCreate_JSONOutput(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/orgs", orgsHandler("acme", "org-1"))
	mux.HandleFunc("/v1/orgs/org-1/apps/app-1/services/kvs/tables", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body["name"] != "sessions" {
			t.Errorf("name = %v, want sessions", body["name"])
		}
		if body["minute_limit"] != float64(300) {
			t.Errorf("minute_limit = %v, want 300", body["minute_limit"])
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"table":{"id":"table-1","name":"sessions","status":"active","kvs_url":"https://kvs.sureva.com","env_var_name":"SUREVA_KVS_TOKEN_SESSIONS","token_last_four":"cret","minute_limit":300,"created_at":"2026-06-20T00:00:00Z","updated_at":"2026-06-20T00:00:00Z"},"token":"skvs_table_secret"}`))
	})
	srv := newTestServer(t, mux)
	outBuf, _, exec := newTestRoot(t, srv)

	err := exec("services", "kvs", "tables", "create", "app-1", "--name", "sessions", "--minute-limit", "300", "--org", "acme")

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("services kvs tables create: want exit 0, got %d", got)
	}
	var got map[string]any
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("services kvs tables create: stdout not valid JSON: %v\noutput: %s", jsonErr, outBuf)
	}
	if token, _ := got["token"].(string); token != "skvs_table_secret" {
		t.Errorf("token = %q, want skvs_table_secret", token)
	}
}

func TestServicesKVSTablesDelete_RequiresYes(t *testing.T) {
	outBuf, errBuf, exec := newTestRoot(t, "")

	err := exec("services", "kvs", "tables", "delete", "app-1", "sessions", "--org", "acme")

	if got := exitCode(err); got != output.ExitValidation {
		t.Errorf("services kvs tables delete without --yes: want exit %d, got %d; stdout: %s stderr: %s", output.ExitValidation, got, outBuf, errBuf)
	}
	var env map[string]any
	if jsonErr := json.NewDecoder(errBuf).Decode(&env); jsonErr != nil {
		t.Fatalf("services kvs tables delete: stderr not valid JSON: %v\nstderr: %s", jsonErr, errBuf)
	}
	if code, _ := env["code"].(string); code != "validation_error" {
		t.Errorf("code = %q, want validation_error", code)
	}
}
