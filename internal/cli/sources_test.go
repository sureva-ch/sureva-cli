package cli_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/sureva-ch/sureva-cli/internal/output"
)

const sourcesPath = "/v1/orgs/" + testOrgID + "/apps/" + testAppID + "/sources"

const (
	readySourceJSON    = `{"id":"src-1","app_id":"app-1","seq":1,"status":"ready","release_tag":"src-1","size_bytes":2048,"sha256":"abc123","available":true,"created_at":"2026-10-01T10:00:00Z","updated_at":"2026-10-01T10:05:00Z"}`
	rejectedSourceJSON = `{"id":"src-2","app_id":"app-1","seq":2,"status":"rejected","validation_error":"archive contains a symlink","created_at":"2026-10-02T10:00:00Z","updated_at":"2026-10-02T10:00:00Z"}`
)

func TestSourcesList_Success(t *testing.T) {
	mux := deploys_mux()
	mux.HandleFunc(sourcesPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "expected GET", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[` + rejectedSourceJSON + `,` + readySourceJSON + `]`))
	})
	srv := newTestServer(t, mux)
	outBuf, _, exec := newTestRoot(t, srv)

	err := exec("sources", "list", testAppID, "--org", testOrgSlug)

	if got := exitCode(err); got != output.ExitOK {
		t.Fatalf("sources list: want exit 0, got %d", got)
	}
	var got []map[string]any
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("sources list: stdout not valid JSON array: %v\noutput: %s", jsonErr, outBuf)
	}
	if len(got) != 2 {
		t.Fatalf("sources list: want 2 rows, got %d", len(got))
	}
	if got[0]["validation_error"] != "archive contains a symlink" {
		t.Errorf("sources list: row 0 validation_error = %v", got[0]["validation_error"])
	}
	for key, want := range map[string]any{"release_tag": "src-1", "sha256": "abc123", "available": true, "size_bytes": float64(2048)} {
		if got[1][key] != want {
			t.Errorf("sources list: row 1 %s = %v, want %v", key, got[1][key], want)
		}
	}
}

func TestSourcesList_TableOutput(t *testing.T) {
	mux := deploys_mux()
	mux.HandleFunc(sourcesPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[` + readySourceJSON + `]`))
	})
	srv := newTestServer(t, mux)
	outBuf, _, exec := newTestRoot(t, srv)

	err := exec("sources", "list", testAppID, "--org", testOrgSlug, "--output", "table")

	if got := exitCode(err); got != output.ExitOK {
		t.Fatalf("sources list table: want exit 0, got %d", got)
	}
	for _, want := range []string{"status", "release_tag", "src-1", "ready"} {
		if !strings.Contains(outBuf.String(), want) {
			t.Errorf("sources list table: output missing %q:\n%s", want, outBuf)
		}
	}
}

func TestSourcesGet_ShowsValidationError(t *testing.T) {
	mux := deploys_mux()
	mux.HandleFunc(sourcesPath+"/src-2", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(rejectedSourceJSON))
	})
	srv := newTestServer(t, mux)
	outBuf, _, exec := newTestRoot(t, srv)

	err := exec("sources", "get", testAppID, "src-2", "--org", testOrgSlug)

	if got := exitCode(err); got != output.ExitOK {
		t.Fatalf("sources get: want exit 0, got %d", got)
	}
	var got map[string]any
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("sources get: stdout not valid JSON: %v\noutput: %s", jsonErr, outBuf)
	}
	if got["status"] != "rejected" || got["validation_error"] != "archive contains a symlink" {
		t.Errorf("sources get: unexpected body %v", got)
	}
}

func TestSourcesGet_NotFound(t *testing.T) {
	mux := deploys_mux()
	mux.HandleFunc(sourcesPath+"/missing", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"source not found"}`))
	})
	srv := newTestServer(t, mux)
	_, errBuf, exec := newTestRoot(t, srv)

	err := exec("sources", "get", testAppID, "missing", "--org", testOrgSlug)

	if got := exitCode(err); got != output.ExitNotFound {
		t.Errorf("sources get missing: want exit %d, got %d", output.ExitNotFound, got)
	}
	var env map[string]any
	if jsonErr := json.NewDecoder(errBuf).Decode(&env); jsonErr != nil {
		t.Fatalf("sources get missing: stderr not JSON: %v\nstderr: %s", jsonErr, errBuf)
	}
	if env["code"] != "not_found" {
		t.Errorf("sources get missing: code = %v, want not_found", env["code"])
	}
}

func TestSourcesList_MissingOrg_ExitValidation(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	srv := newTestServer(t, deploys_mux())
	_, _, exec := newTestRoot(t, srv)

	err := exec("sources", "list", testAppID)

	if got := exitCode(err); got != output.ExitValidation {
		t.Errorf("sources list without org: want exit %d, got %d", output.ExitValidation, got)
	}
}

func TestSourcesGet_RequiresTwoArgs(t *testing.T) {
	srv := newTestServer(t, deploys_mux())
	_, _, exec := newTestRoot(t, srv)

	if err := exec("sources", "get", testAppID, "--org", testOrgSlug); err == nil {
		t.Error("sources get with one arg: want an error, got nil")
	}
}

func TestHelpJSON_IncludesSources(t *testing.T) {
	outBuf, _, exec := newTestRoot(t, "")
	if err := exec("--help", "--json"); err != nil {
		t.Fatalf("--help --json: %v", err)
	}
	var tree struct {
		Commands []struct {
			Name        string `json:"name"`
			Subcommands []struct {
				Name string `json:"name"`
			} `json:"subcommands"`
		} `json:"commands"`
	}
	if err := json.NewDecoder(outBuf).Decode(&tree); err != nil {
		t.Fatalf("--help --json not valid JSON: %v", err)
	}
	subs := map[string]bool{}
	for _, c := range tree.Commands {
		if c.Name == "sources" {
			for _, s := range c.Subcommands {
				subs[s.Name] = true
			}
		}
	}
	if !subs["list"] || !subs["get"] {
		t.Errorf("command tree sources subcommands = %v, want list and get", subs)
	}
}
