package client_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
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
