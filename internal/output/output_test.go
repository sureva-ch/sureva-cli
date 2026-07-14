package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// --- Exit code tests ---

func TestHTTPStatusToExitCode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		httpStatus int
		want       int
	}{
		{"200 ok", 200, ExitOK},
		{"201 created", 201, ExitOK},
		{"204 no content", 204, ExitOK},
		{"400 bad request", 400, ExitValidation},
		{"401 unauthorized", 401, ExitAuth},
		{"403 forbidden", 403, ExitAuth},
		{"404 not found", 404, ExitNotFound},
		{"422 unprocessable", 422, ExitValidation},
		{"500 server error", 500, ExitGeneral},
		{"503 unavailable", 503, ExitGeneral},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := HTTPStatusToExitCode(tt.httpStatus)
			if got != tt.want {
				t.Errorf("HTTPStatusToExitCode(%d) = %d, want %d", tt.httpStatus, got, tt.want)
			}
		})
	}
}

func TestExitCodeConstants(t *testing.T) {
	t.Parallel()
	// Ensure the constants match the spec table so they cannot silently drift.
	if ExitOK != 0 {
		t.Errorf("ExitOK = %d, want 0", ExitOK)
	}
	if ExitGeneral != 1 {
		t.Errorf("ExitGeneral = %d, want 1", ExitGeneral)
	}
	if ExitAuth != 2 {
		t.Errorf("ExitAuth = %d, want 2", ExitAuth)
	}
	if ExitNotFound != 3 {
		t.Errorf("ExitNotFound = %d, want 3", ExitNotFound)
	}
	if ExitValidation != 4 {
		t.Errorf("ExitValidation = %d, want 4", ExitValidation)
	}
	if ExitNetwork != 5 {
		t.Errorf("ExitNetwork = %d, want 5", ExitNetwork)
	}
}

// --- JSON renderer ---

func TestRenderJSON(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	r := NewRenderer(FormatJSON, &out, &bytes.Buffer{})

	payload := map[string]string{"id": "app-123", "name": "my-app"}
	if err := r.Render(payload); err != nil {
		t.Fatalf("Render: %v", err)
	}

	var got map[string]string
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, out.String())
	}
	if got["id"] != "app-123" {
		t.Errorf("id = %q, want app-123", got["id"])
	}
}

func TestRenderJSON_Array(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	r := NewRenderer(FormatJSON, &out, &bytes.Buffer{})

	items := []map[string]string{{"name": "a"}, {"name": "b"}}
	if err := r.Render(items); err != nil {
		t.Fatalf("Render: %v", err)
	}

	var got []map[string]string
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON array: %v\nraw: %s", err, out.String())
	}
	if len(got) != 2 {
		t.Errorf("len = %d, want 2", len(got))
	}
}

// --- Table renderer ---

func TestRenderTable_ArrayOfMaps(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	r := NewRenderer(FormatTable, &out, &bytes.Buffer{})

	items := []map[string]any{
		{"id": "1", "name": "alpha"},
		{"id": "2", "name": "beta"},
	}
	if err := r.Render(items); err != nil {
		t.Fatalf("Render: %v", err)
	}

	body := out.String()
	if !strings.Contains(body, "id") || !strings.Contains(body, "name") {
		t.Errorf("table header missing: %q", body)
	}
	if !strings.Contains(body, "alpha") || !strings.Contains(body, "beta") {
		t.Errorf("table rows missing: %q", body)
	}
}

func TestRenderTable_FallsBackToJSON_ForNonArray(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	r := NewRenderer(FormatTable, &out, &bytes.Buffer{})

	payload := map[string]string{"key": "val"}
	if err := r.Render(payload); err != nil {
		t.Fatalf("Render: %v", err)
	}

	// Must still be valid JSON.
	var got map[string]string
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("fallback output is not valid JSON: %v\nraw: %s", err, out.String())
	}
}

// --- Error envelope (stderr) ---

// Scenario B-02a: auth failure error shape
func TestRenderError_AuthFailure(t *testing.T) {
	t.Parallel()

	var errBuf bytes.Buffer
	r := NewRenderer(FormatJSON, &bytes.Buffer{}, &errBuf)

	exitCode := r.RenderError("token expired", "auth_error", 401)
	if exitCode != ExitAuth {
		t.Errorf("exitCode = %d, want %d (ExitAuth)", exitCode, ExitAuth)
	}

	var env errorEnvelope
	if err := json.Unmarshal(errBuf.Bytes(), &env); err != nil {
		t.Fatalf("stderr is not valid JSON: %v\nraw: %s", err, errBuf.String())
	}
	if env.Error != "token expired" {
		t.Errorf("error = %q, want 'token expired'", env.Error)
	}
	if env.Code != "auth_error" {
		t.Errorf("code = %q, want 'auth_error'", env.Code)
	}
	if env.HTTPStatus != 401 {
		t.Errorf("http_status = %d, want 401", env.HTTPStatus)
	}
}

// Scenario B-02b: not-found error shape
func TestRenderError_NotFound(t *testing.T) {
	t.Parallel()

	var errBuf bytes.Buffer
	r := NewRenderer(FormatJSON, &bytes.Buffer{}, &errBuf)

	exitCode := r.RenderError("app not found", "not_found", 404)
	if exitCode != ExitNotFound {
		t.Errorf("exitCode = %d, want %d (ExitNotFound)", exitCode, ExitNotFound)
	}

	var env errorEnvelope
	if err := json.Unmarshal(errBuf.Bytes(), &env); err != nil {
		t.Fatalf("stderr is not valid JSON: %v", err)
	}
	if env.Code != "not_found" {
		t.Errorf("code = %q, want 'not_found'", env.Code)
	}
}

// Scenario B-02a/network: no HTTP response → exit 5
func TestRenderError_NetworkError(t *testing.T) {
	t.Parallel()

	var errBuf bytes.Buffer
	r := NewRenderer(FormatJSON, &bytes.Buffer{}, &errBuf)

	exitCode := r.RenderError("connection refused", "network_error", 0)
	if exitCode != ExitNetwork {
		t.Errorf("exitCode = %d, want %d (ExitNetwork)", exitCode, ExitNetwork)
	}
}

// Error output must be JSON regardless of --output flag.
func TestRenderError_AlwaysJSONEvenInTableMode(t *testing.T) {
	t.Parallel()

	var errBuf bytes.Buffer
	r := NewRenderer(FormatTable, &bytes.Buffer{}, &errBuf)
	r.RenderError("something broke", "general_error", 500)

	var env errorEnvelope
	if err := json.Unmarshal(errBuf.Bytes(), &env); err != nil {
		t.Fatalf("error output is not JSON in table mode: %v\nraw: %s", err, errBuf.String())
	}
}
