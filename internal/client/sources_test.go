package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/sureva-ch/sureva-cli/internal/client"
)

// The payload mirrors cloud-api's appSourceView (models.AppSource plus
// available), including the storage internals the CLI deliberately drops, so a
// renamed or retyped field fails here.
const sourceRowJSON = `{
  "id": "src-id-1", "org_id": "org-1", "app_id": "app-1", "seq": 3,
  "status": "ready", "aws_region": "eu-central-1", "upload_key": "q/k",
  "s3_key": "s/k", "s3_version_id": "v1", "release_tag": "src-3",
  "size_bytes": 2048, "sha256": "abc123", "stripped_prefix": "my-app/",
  "created_by": "11111111-1111-1111-1111-111111111111",
  "created_at": "2026-10-01T10:00:00Z", "updated_at": "2026-10-01T10:05:00Z",
  "available": true
}`

func TestListSources_Success(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/v1/orgs/org-1/apps/app-1/sources" {
			t.Errorf("path = %q, want /v1/orgs/org-1/apps/app-1/sources", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[` + sourceRowJSON + `,{"id":"src-id-2","app_id":"app-1","seq":4,"status":"rejected","validation_error":"archive contains a symlink","created_at":"2026-10-02T10:00:00Z","updated_at":"2026-10-02T10:00:00Z"}]`))
	})

	got, err := c.ListSources(context.Background(), "org-1", "app-1")
	if err != nil {
		t.Fatalf("ListSources: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	ready := got[0]
	if ready.ID != "src-id-1" || ready.AppID != "app-1" || ready.Seq != 3 || ready.Status != "ready" {
		t.Errorf("row 0 identity = %+v", ready)
	}
	if ready.ReleaseTag == nil || *ready.ReleaseTag != "src-3" {
		t.Errorf("ReleaseTag = %v, want src-3", ready.ReleaseTag)
	}
	if ready.SizeBytes == nil || *ready.SizeBytes != 2048 {
		t.Errorf("SizeBytes = %v, want 2048", ready.SizeBytes)
	}
	if ready.SHA256 == nil || *ready.SHA256 != "abc123" {
		t.Errorf("SHA256 = %v, want abc123", ready.SHA256)
	}
	if ready.StrippedPrefix == nil || *ready.StrippedPrefix != "my-app/" {
		t.Errorf("StrippedPrefix = %v, want my-app/", ready.StrippedPrefix)
	}
	if ready.Available == nil || !*ready.Available {
		t.Errorf("Available = %v, want true", ready.Available)
	}
	if ready.CreatedAt.IsZero() || ready.UpdatedAt.IsZero() {
		t.Errorf("timestamps not decoded: %+v", ready)
	}
	rejected := got[1]
	if rejected.ValidationError == nil || *rejected.ValidationError != "archive contains a symlink" {
		t.Errorf("ValidationError = %v", rejected.ValidationError)
	}
	// "Not applicable" must stay distinct from "gone": no available key, no value.
	if rejected.Available != nil {
		t.Errorf("Available = %v, want nil when the API omits it", *rejected.Available)
	}
}

func TestGetSource_Success(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/orgs/org-1/apps/app-1/sources/src-id-1" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sourceRowJSON))
	})

	got, err := c.GetSource(context.Background(), "org-1", "app-1", "src-id-1")
	if err != nil {
		t.Fatalf("GetSource: %v", err)
	}
	if got.ID != "src-id-1" || got.Seq != 3 {
		t.Errorf("source = %+v", got)
	}
}

func TestSource_OmitsUnsetOptionalFields(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"s","app_id":"a","seq":1,"status":"pending"}`))
	})
	got, err := c.GetSource(context.Background(), "o", "a", "s")
	if err != nil {
		t.Fatalf("GetSource: %v", err)
	}
	out, _ := json.Marshal(got)
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	for _, k := range []string{"release_tag", "size_bytes", "sha256", "validation_error", "available"} {
		if _, present := m[k]; present {
			t.Errorf("key %q present in output for an unset field: %s", k, out)
		}
	}
}

func TestGetSource_NotFound(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"source not found"}`))
	})
	_, err := c.GetSource(context.Background(), "o", "a", "missing")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestSourceIDsAreEscapedAsOnePathSegment(t *testing.T) {
	t.Parallel()
	var gotRaw string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotRaw = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	ctx := context.Background()

	if _, err := c.DownloadSource(ctx, "org-1", "app-1", "../../x?y=1#z"); err != nil {
		t.Fatal(err)
	}
	if want := "/v1/orgs/org-1/apps/app-1/sources/..%2F..%2Fx%3Fy=1%23z/download"; gotRaw != want {
		t.Errorf("path = %q, want %q", gotRaw, want)
	}
	if _, err := c.GetSource(ctx, "org/1", "app 1", "a/b"); err != nil {
		t.Fatal(err)
	}
	if want := "/v1/orgs/org%2F1/apps/app%201/sources/a%2Fb"; gotRaw != want {
		t.Errorf("path = %q, want %q", gotRaw, want)
	}
	if _, err := c.DownloadSource(ctx, "org-1", "app-1", "latest"); err != nil {
		t.Fatal(err)
	}
	if want := "/v1/orgs/org-1/apps/app-1/sources/latest/download"; gotRaw != want {
		t.Errorf("latest path = %q, want %q", gotRaw, want)
	}
}

func TestEmptySourceIDIsRejectedWithoutARequest(t *testing.T) {
	t.Parallel()
	hit := false
	c, _ := newTestClient(t, func(http.ResponseWriter, *http.Request) { hit = true })
	ctx := context.Background()

	if _, err := c.GetSource(ctx, "o", "a", ""); err == nil {
		t.Error("GetSource: want an error")
	}
	if _, err := c.DownloadSource(ctx, "o", "a", ""); err == nil {
		t.Error("DownloadSource: want an error")
	}
	if _, err := c.CompleteSource(ctx, "o", "a", ""); err == nil {
		t.Error("CompleteSource: want an error")
	}
	if hit {
		t.Error("a request was sent for an empty id")
	}
}

// A rejected row as the API writes it: validation_code on the row and
// retryable added by the view, next to the base the upload declared.
func TestGetSource_RejectedRowKeepsCodeRetryableAndBase(t *testing.T) {
	t.Parallel()
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"s1","app_id":"app-1","seq":4,"status":"rejected","aws_region":"us-east-2","upload_key":"k",` +
			`"validation_error":"prose","validation_code":"promote_failed","base_source_id":"3c1b",` +
			`"created_at":"2026-10-02T10:00:00Z","updated_at":"2026-10-02T10:00:00Z","retryable":true}`))
	})

	got, err := c.GetSource(context.Background(), "org-1", "app-1", "s1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ValidationCode == nil || *got.ValidationCode != "promote_failed" || !got.IsRetryable() ||
		got.BaseSourceID == nil || *got.BaseSourceID != "3c1b" {
		t.Errorf("source = %+v", got)
	}
}

func TestAppSource_IsRetryableNeedsTheFlagOnARejectedRow(t *testing.T) {
	t.Parallel()
	yes, no := true, false
	for _, tc := range []struct {
		name string
		s    *client.AppSource
		want bool
	}{
		{"nil", nil, false},
		{"rejected, retryable", &client.AppSource{Status: "rejected", Retryable: &yes}, true},
		{"rejected, not retryable", &client.AppSource{Status: "rejected", Retryable: &no}, false},
		{"rejected, flag absent (older API)", &client.AppSource{Status: "rejected"}, false},
		{"ready", &client.AppSource{Status: "ready", Retryable: &yes}, false},
	} {
		if got := tc.s.IsRetryable(); got != tc.want {
			t.Errorf("%s: IsRetryable = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The error body's code and detail fields reach the *APIError, and a body
// without them leaves them empty.
func TestAPIError_KeepsCodeAndDetailFields(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body                         string
		status                             int
		code, sourceStatus, validationCode string
		envs                               []string
	}{
		{"source_not_ready", `{"code":"source_not_ready","error":"m","source_status":"rejected","validation_code":"archive_empty"}`, 409, "source_not_ready", "rejected", "archive_empty", nil},
		{"source_not_completable", `{"code":"source_not_completable","error":"m","source_status":"ready"}`, 409, "source_not_completable", "ready", "", nil},
		{"source_is_live", `{"code":"source_is_live","error":"m","environments":["production","staging"]}`, 409, "source_is_live", "", "", []string{"production", "staging"}},
		{"no code (older API)", `{"error":"m"}`, 409, "", "", "", nil},
	} {
		c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		})
		_, err := c.GetSource(context.Background(), "org-1", "app-1", "s1")
		var apiErr *client.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("%s: err = %v", tc.name, err)
		}
		if apiErr.ServerCode != tc.code || apiErr.SourceStatus != tc.sourceStatus || apiErr.ValidationCode != tc.validationCode ||
			strings.Join(apiErr.Environments, ",") != strings.Join(tc.envs, ",") {
			t.Errorf("%s: %+v", tc.name, apiErr)
		}
		if apiErr.Message != "m" || apiErr.HTTPStatus != tc.status {
			t.Errorf("%s: message/status changed: %+v", tc.name, apiErr)
		}
	}
}
