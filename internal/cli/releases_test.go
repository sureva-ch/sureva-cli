package cli_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sureva-ch/sureva-cli/internal/output"
)

const (
	releasesPath = "/v1/orgs/" + testOrgID + "/apps/" + testAppID + "/releases"
	appPath      = "/v1/orgs/" + testOrgID + "/apps/" + testAppID

	publishedReleaseJSON  = `{"id":101,"tag_name":"v1.1.0","name":"One point one","body":"notes","draft":false,"prerelease":false,"published_at":"2026-10-02T09:30:00Z","html_url":"https://github.com/acme/web/releases/tag/v1.1.0","author":{"login":"octo","avatar_url":"https://avatars.example/octo.png"}}`
	prereleaseJSON        = `{"id":102,"tag_name":"v1.2.0-rc.1","name":"","body":"","draft":false,"prerelease":true,"published_at":"2026-10-03T09:30:00Z","html_url":"","author":{"login":"octo","avatar_url":""}}`
	draftReleaseJSON      = `{"id":103,"tag_name":"v1.3.0","name":"Next","body":"wip","draft":true,"prerelease":false,"published_at":"","html_url":"","author":{"login":"octo","avatar_url":""}}`
	apiAppNotGitHubBacked = `{"code":"app_not_github_backed","error":"reworded: no repository linked"}`
)

// releasesMux serves the app (with the given body) and the releases (with the
// given status and body). hits counts the requests the releases path got.
func releasesMux(appBody string, status int, body string, hits *atomic.Int32) *http.ServeMux {
	mux := deploys_mux()
	mux.HandleFunc(appPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(appBody))
	})
	mux.HandleFunc(releasesPath, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Method != http.MethodGet {
			http.Error(w, "expected GET", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	return mux
}

func TestReleasesList_HidesDraftsKeepsPrereleasesAndOrder(t *testing.T) {
	var hits atomic.Int32
	mux := releasesMux(`{"id":"app-1","source_type":"github"}`, http.StatusOK,
		`[`+draftReleaseJSON+`,`+prereleaseJSON+`,`+publishedReleaseJSON+`]`, &hits)
	outBuf, _, exec := newTestRoot(t, newTestServer(t, mux))

	err := exec("releases", "list", testAppID, "--org", testOrgSlug)

	if got := exitCode(err); got != output.ExitOK {
		t.Fatalf("releases list: want exit 0, got %d", got)
	}
	var got []map[string]any
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("releases list: stdout not a JSON array: %v\noutput: %s", jsonErr, outBuf)
	}
	if len(got) != 2 {
		t.Fatalf("releases list: want 2 rows (draft hidden), got %d: %v", len(got), got)
	}
	if got[0]["tag_name"] != "v1.2.0-rc.1" || got[0]["prerelease"] != true {
		t.Errorf("row 0 = %v, want the prerelease first (API order preserved)", got[0])
	}
	if got[1]["tag_name"] != "v1.1.0" || got[1]["published_at"] != "2026-10-02T09:30:00Z" {
		t.Errorf("row 1 = %v", got[1])
	}
	author, _ := got[1]["author"].(map[string]any)
	if got[1]["id"] != float64(101) || got[1]["html_url"] == "" || author["login"] != "octo" {
		t.Errorf("row 1 lost fields: %v", got[1])
	}
}

func TestReleasesList_AppWithoutSourceTypeIsLetThrough(t *testing.T) {
	var hits atomic.Int32
	mux := releasesMux(`{"id":"app-1"}`, http.StatusOK, `[`+publishedReleaseJSON+`]`, &hits)
	outBuf, _, exec := newTestRoot(t, newTestServer(t, mux))

	err := exec("releases", "list", testAppID, "--org", testOrgSlug)

	if got := exitCode(err); got != output.ExitOK {
		t.Fatalf("releases list: want exit 0, got %d", got)
	}
	if !strings.Contains(outBuf.String(), "v1.1.0") {
		t.Errorf("releases list: missing release in %s", outBuf)
	}
}

func TestReleasesList_EmptyPrintsArray(t *testing.T) {
	for name, body := range map[string]string{
		"no releases":   `[]`,
		"only drafts":   `[` + draftReleaseJSON + `]`,
		"null from API": `null`,
	} {
		t.Run(name, func(t *testing.T) {
			var hits atomic.Int32
			mux := releasesMux(`{"id":"app-1","source_type":"github"}`, http.StatusOK, body, &hits)
			outBuf, _, exec := newTestRoot(t, newTestServer(t, mux))

			err := exec("releases", "list", testAppID, "--org", testOrgSlug)

			if got := exitCode(err); got != output.ExitOK {
				t.Fatalf("releases list: want exit 0, got %d", got)
			}
			if got := strings.TrimSpace(outBuf.String()); got != "[]" {
				t.Errorf("stdout = %q, want []", got)
			}
		})
	}
}

func TestReleasesList_TableOutput(t *testing.T) {
	var hits atomic.Int32
	mux := releasesMux(`{"id":"app-1","source_type":"github"}`, http.StatusOK, `[`+publishedReleaseJSON+`]`, &hits)
	outBuf, _, exec := newTestRoot(t, newTestServer(t, mux))

	err := exec("releases", "list", testAppID, "--org", testOrgSlug, "--output", "table")

	if got := exitCode(err); got != output.ExitOK {
		t.Fatalf("releases list table: want exit 0, got %d", got)
	}
	for _, want := range []string{"tag_name", "published_at", "v1.1.0", "2026-10-02T09:30:00Z"} {
		if !strings.Contains(outBuf.String(), want) {
			t.Errorf("releases list table: output missing %q:\n%s", want, outBuf)
		}
	}
}

func TestReleasesList_UploadBackedAppMakesNoReleasesRequest(t *testing.T) {
	var hits atomic.Int32
	// What the API would answer for this app: source releases, not GitHub ones.
	mux := releasesMux(`{"id":"app-1","source_type":"upload"}`, http.StatusOK,
		`[{"id":3,"tag_name":"src-3","name":"src-3","published_at":"2026-10-01T10:00:00Z","source_id":"s-3"}]`, &hits)
	outBuf, errBuf, exec := newTestRoot(t, newTestServer(t, mux))

	err := exec("releases", "list", testAppID, "--org", testOrgSlug)

	stderr := errBuf.String()
	assertEnvelope(t, errBuf, exitCode(err), output.ExitValidation, "upload_backed_app", nil)
	if outBuf.Len() != 0 {
		t.Errorf("stdout = %q, want nothing", outBuf)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("releases endpoint got %d request(s), want none", n)
	}
	if !strings.Contains(stderr, "sureva sources list "+testAppID) {
		t.Errorf("message does not point to 'sources list': %s", stderr)
	}
}

func TestReleasesList_AppNotFound(t *testing.T) {
	mux := deploys_mux()
	mux.HandleFunc(appPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"app not found"}`))
	})
	outBuf, errBuf, exec := newTestRoot(t, newTestServer(t, mux))

	err := exec("releases", "list", testAppID, "--org", testOrgSlug)

	assertEnvelope(t, errBuf, exitCode(err), output.ExitNotFound, "not_found", nil)
	if outBuf.Len() != 0 {
		t.Errorf("stdout = %q, want nothing", outBuf)
	}
}

func TestReleasesList_NotGitHubBackedIsClassifiedByAPICode(t *testing.T) {
	var hits atomic.Int32
	mux := releasesMux(`{"id":"app-1","source_type":"github"}`, http.StatusUnprocessableEntity, apiAppNotGitHubBacked, &hits)
	_, errBuf, exec := newTestRoot(t, newTestServer(t, mux))

	err := exec("releases", "list", testAppID, "--org", testOrgSlug)

	assertEnvelope(t, errBuf, exitCode(err), output.ExitValidation, "validation_error",
		map[string]string{"api_code": "app_not_github_backed"})
}

func TestReleasesList_MissingOrg_ExitValidation(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, _, exec := newTestRoot(t, newTestServer(t, deploys_mux()))

	err := exec("releases", "list", testAppID)

	if got := exitCode(err); got != output.ExitValidation {
		t.Errorf("releases list without org: want exit %d, got %d", output.ExitValidation, got)
	}
}

func TestReleasesList_RequiresOneArg(t *testing.T) {
	_, _, exec := newTestRoot(t, newTestServer(t, deploys_mux()))

	if err := exec("releases", "list", "--org", testOrgSlug); err == nil {
		t.Error("releases list without an app id: want an error, got nil")
	}
}

func TestHelpJSON_IncludesReleases(t *testing.T) {
	outBuf, _, exec := newTestRoot(t, "")
	if err := exec("--help", "--json"); err != nil {
		t.Fatalf("--help --json: %v", err)
	}
	var tree struct {
		Commands []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Subcommands []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"subcommands"`
		} `json:"commands"`
	}
	if err := json.NewDecoder(outBuf).Decode(&tree); err != nil {
		t.Fatalf("--help --json not valid JSON: %v", err)
	}
	found := false
	for _, c := range tree.Commands {
		if c.Name != "releases" {
			continue
		}
		found = true
		if !strings.Contains(c.Description, "releases of a GitHub-backed app") {
			t.Errorf("releases description = %q, want it to say it lists the releases of a GitHub-backed app", c.Description)
		}
		if len(c.Subcommands) != 1 || c.Subcommands[0].Name != "list" {
			t.Fatalf("releases subcommands = %+v, want exactly list", c.Subcommands)
		}
		if !strings.Contains(c.Subcommands[0].Description, "releases of a GitHub-backed app") {
			t.Errorf("releases list description = %q", c.Subcommands[0].Description)
		}
	}
	if !found {
		t.Error("command tree has no releases command")
	}
}
