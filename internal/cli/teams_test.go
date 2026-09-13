package cli_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/sureva-ch/sureva-cli/internal/output"
)

// ---- T9: teams list RED tests ----

const teamsJSON = `[
	{"id":"team-1","org_id":"org-1","slug":"engineers","name":"Engineers","is_system":false,"is_active":true,"created_at":"2024-01-01T00:00:00Z"},
	{"id":"team-2","org_id":"org-1","slug":"admins","name":"Admins","is_system":true,"is_active":true,"created_at":"2024-01-01T00:00:00Z"}
]`

func teamsHandler(orgSlug, orgID string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/orgs", orgsHandler(orgSlug, orgID))
	mux.HandleFunc("/v1/orgs/"+orgID+"/teams", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(teamsJSON))
	})
	return mux
}

func TestTeamsList_JSONSuccess(t *testing.T) {
	srv := newTestServer(t, teamsHandler("acme", "org-1"))
	outBuf, _, exec := newTestRoot(t, srv)

	err := exec("teams", "list", "--org", "acme")

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("teams list: want exit 0, got %d", got)
	}
	var got []map[string]any
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("teams list: stdout not valid JSON array: %v\noutput: %s", jsonErr, outBuf)
	}
	if len(got) != 2 {
		t.Errorf("teams list: want 2 teams, got %d", len(got))
	}
	if slug, _ := got[0]["slug"].(string); slug != "engineers" {
		t.Errorf("teams list: want first slug 'engineers', got %q", slug)
	}
}

func TestTeamsList_MissingOrg_ExitValidation(t *testing.T) {
	srv := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	_, errBuf, exec := newTestRoot(t, srv)

	err := exec("teams", "list")

	if got := exitCode(err); got != output.ExitValidation {
		t.Errorf("teams list missing --org: want exit %d, got %d; stderr: %s", output.ExitValidation, got, errBuf)
	}
	var env map[string]any
	if jsonErr := json.NewDecoder(errBuf).Decode(&env); jsonErr != nil {
		t.Fatalf("teams list missing --org: stderr not JSON: %v\nstderr: %s", jsonErr, errBuf)
	}
	if code, _ := env["code"].(string); code != "validation_error" {
		t.Errorf("teams list missing --org: want code validation_error, got %q", code)
	}
}

func TestTeamsList_APIError_Passthrough(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/orgs", orgsHandler("acme", "org-1"))
	mux.HandleFunc("/v1/orgs/org-1/teams", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := newTestServer(t, mux)
	_, errBuf, exec := newTestRoot(t, srv)

	err := exec("teams", "list", "--org", "acme")

	if got := exitCode(err); got != output.ExitGeneral {
		t.Errorf("teams list API error: want exit %d, got %d; stderr: %s", output.ExitGeneral, got, errBuf)
	}
}
