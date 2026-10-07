package client_test

import (
	"context"
	"net/http"
	"testing"
)

// The payload mirrors cloud-api's github.Release as GitHub fills it: a
// published release, a prerelease, and a draft whose published_at is empty.
const releasesJSON = `[
  {"id": 101, "tag_name": "v1.1.0", "name": "One point one", "body": "notes",
   "draft": false, "prerelease": false, "published_at": "2026-10-02T09:30:00Z",
   "html_url": "https://github.com/acme/web/releases/tag/v1.1.0",
   "author": {"login": "octo", "avatar_url": "https://avatars.example/octo.png"}},
  {"id": 102, "tag_name": "v1.2.0-rc.1", "name": "", "body": "",
   "draft": false, "prerelease": true, "published_at": "2026-10-03T09:30:00Z",
   "html_url": "", "author": {"login": "octo", "avatar_url": ""}},
  {"id": 103, "tag_name": "v1.3.0", "name": "Next", "body": "wip",
   "draft": true, "prerelease": false, "published_at": "",
   "html_url": "", "author": {"login": "octo", "avatar_url": ""}}
]`

func TestListReleases_Success(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/v1/orgs/org-1/apps/app-1/releases" {
			t.Errorf("path = %q, want /v1/orgs/org-1/apps/app-1/releases", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(releasesJSON))
	})

	got, err := c.ListReleases(context.Background(), "org-1", "app-1")
	if err != nil {
		t.Fatalf("ListReleases: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (the client does not filter)", len(got))
	}
	first := got[0]
	if first.ID != 101 || first.TagName != "v1.1.0" || first.Name != "One point one" || first.Body != "notes" {
		t.Errorf("row 0 identity = %+v", first)
	}
	if first.PublishedAt != "2026-10-02T09:30:00Z" || first.HTMLURL != "https://github.com/acme/web/releases/tag/v1.1.0" {
		t.Errorf("row 0 links = %+v", first)
	}
	if first.Author.Login != "octo" || first.Author.AvatarURL != "https://avatars.example/octo.png" {
		t.Errorf("row 0 author = %+v", first.Author)
	}
	if !got[1].Prerelease || got[1].Draft {
		t.Errorf("row 1 flags = draft %v prerelease %v, want prerelease only", got[1].Draft, got[1].Prerelease)
	}
	if !got[2].Draft || got[2].PublishedAt != "" {
		t.Errorf("row 2 = draft %v published_at %q, want a draft with empty published_at", got[2].Draft, got[2].PublishedAt)
	}
}

func TestListReleases_EscapesPathSegments(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if want := "/v1/orgs/org%2F1/apps/app%3F1/releases"; r.URL.EscapedPath() != want {
			t.Errorf("escaped path = %q, want %q", r.URL.EscapedPath(), want)
		}
		_, _ = w.Write([]byte(`[]`))
	})

	got, err := c.ListReleases(context.Background(), "org/1", "app?1")
	if err != nil {
		t.Fatalf("ListReleases: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}
