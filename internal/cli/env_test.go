package cli_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sureva-ch/sureva-cli/internal/cli"
	"github.com/sureva-ch/sureva-cli/internal/output"
)

// orgsHandler is a minimal handler that returns a single org for org resolution.
func orgsHandler(slug, orgID string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"` + orgID + `","name":"Acme","slug":"` + slug + `","created_at":"2024-01-01T00:00:00Z"}]`))
	}
}

// ---- spec B-06a: env get masks values by default ----

func TestEnvGet_Masked(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/orgs", orgsHandler("acme", "org-1"))
	mux.HandleFunc("/v1/orgs/org-1/apps/app-1/env", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"API_KEY":"supersecret","DB_URL":"postgres://..."}`))
	})
	srv := newTestServer(t, mux)
	outBuf, _, exec := newTestRoot(t, srv)

	err := exec("env", "get", "app-1", "--org", "acme")

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("env get: want exit 0, got %d", got)
	}

	var got []map[string]any
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("env get: stdout not valid JSON array: %v\noutput: %s", jsonErr, outBuf)
	}
	if len(got) == 0 {
		t.Fatalf("env get: expected at least one entry")
	}
	for _, entry := range got {
		if val, _ := entry["value"].(string); val != "***" {
			t.Errorf("env get: expected value to be masked '***', got %q (key %v)", val, entry["key"])
		}
	}
}

// ---- env get --reveal shows plaintext values ----

func TestEnvGet_Reveal(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/orgs", orgsHandler("acme", "org-1"))
	mux.HandleFunc("/v1/orgs/org-1/apps/app-1/env", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"API_KEY":"supersecret"}`))
	})
	srv := newTestServer(t, mux)
	outBuf, _, exec := newTestRoot(t, srv)

	err := exec("env", "get", "app-1", "--org", "acme", "--reveal")

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("env get --reveal: want exit 0, got %d", got)
	}
	var got []map[string]any
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("env get --reveal: stdout not valid JSON: %v\noutput: %s", jsonErr, outBuf)
	}
	if len(got) == 0 {
		t.Fatalf("env get --reveal: expected at least one entry")
	}
	if val, _ := got[0]["value"].(string); val != "supersecret" {
		t.Errorf("env get --reveal: expected plaintext value, got %q", val)
	}
}

// ---- env set: success ----

func TestEnvSet_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/orgs", orgsHandler("acme", "org-1"))
	mux.HandleFunc("/v1/orgs/org-1/apps/app-1/env", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.Error(w, "expected PUT", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	srv := newTestServer(t, mux)
	outBuf, _, exec := newTestRoot(t, srv)

	err := exec("env", "set", "app-1", "--org", "acme", "KEY=value", "OTHER=value2")

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("env set: want exit 0, got %d", got)
	}
	var got map[string]any
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("env set: stdout not valid JSON: %v\noutput: %s", jsonErr, outBuf)
	}
}

func TestEnvSet_FromStdin(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/orgs", orgsHandler("acme", "org-1"))
	mux.HandleFunc("/v1/orgs/org-1/apps/app-1/env", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.Error(w, "expected PUT", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if !strings.Contains(string(body), `"API_KEY":"supersecret"`) {
			t.Fatalf("request body missing API_KEY: %s", body)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	srv := newTestServer(t, mux)
	t.Setenv("SUREVA_TOKEN", "sapi_test_deadbeef1234567890123456789012")
	t.Setenv("SUREVA_API_URL", srv)

	outBuf := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	root := cli.NewRootCmd()
	root.SetIn(strings.NewReader("API_KEY=supersecret\n# ignored\nOTHER=value2\n"))
	root.SetOut(outBuf)
	root.SetErr(errBuf)
	root.SetArgs([]string{"env", "set", "app-1", "--org", "acme", "--from-stdin"})

	err := root.Execute()

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("env set --from-stdin: want exit 0, got %d; stderr: %s", got, errBuf)
	}
}

func TestEnvSet_FromFile(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/orgs", orgsHandler("acme", "org-1"))
	mux.HandleFunc("/v1/orgs/org-1/apps/app-1/env", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.Error(w, "expected PUT", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if !strings.Contains(string(body), `"DB_URL":"postgres://example"`) {
			t.Fatalf("request body missing DB_URL: %s", body)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	srv := newTestServer(t, mux)
	outBuf, _, exec := newTestRoot(t, srv)
	envPath := filepath.Join(t.TempDir(), "env.txt")
	t.Setenv("SUREVA_TOKEN", "sapi_test_deadbeef1234567890123456789012")
	if writeErr := os.WriteFile(envPath, []byte("DB_URL=postgres://example\n"), 0600); writeErr != nil {
		t.Fatalf("write env file: %v", writeErr)
	}

	err := exec("env", "set", "app-1", "--org", "acme", "--from-file", envPath)

	if got := exitCode(err); got != output.ExitOK {
		t.Errorf("env set --from-file: want exit 0, got %d", got)
	}
	var got map[string]any
	if jsonErr := json.NewDecoder(outBuf).Decode(&got); jsonErr != nil {
		t.Fatalf("env set --from-file: stdout not valid JSON: %v\noutput: %s", jsonErr, outBuf)
	}
}

// ---- env set: invalid KEY=VALUE format → exit 4 ----

func TestEnvSet_InvalidFormat(t *testing.T) {
	srv := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	_, errBuf, exec := newTestRoot(t, srv)

	// "NOEQUALS" is not KEY=VALUE
	err := exec("env", "set", "app-1", "NOEQUALS")

	if got := exitCode(err); got != output.ExitValidation {
		t.Errorf("invalid format: want exit %d (validation), got %d; stderr: %s", output.ExitValidation, got, errBuf)
	}
}
