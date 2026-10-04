package client_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sureva-ch/sureva-cli/internal/client"
)

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestDownloadSource_PathAndFields(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/orgs/org-1/apps/app-1/sources/latest/download" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"https://s3.example/x?sig=1","expires_at":"2026-10-04T12:15:00Z","source_id":"s1","release_tag":"src-3","size_bytes":48213,"sha256":"9b74"}`))
	})

	got, err := c.DownloadSource(context.Background(), "org-1", "app-1", client.LatestSource)
	if err != nil {
		t.Fatal(err)
	}
	if got.URL == "" || got.SourceID != "s1" || got.ReleaseTag != "src-3" || got.SizeBytes == nil || *got.SizeBytes != 48213 || got.SHA256 == nil || *got.SHA256 != "9b74" {
		t.Errorf("decoded = %+v", got)
	}
}

func TestDownloadSource_CarriesTheServerCode(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":"clone the repository","code":"app_not_upload_backed"}`))
	})

	_, err := c.DownloadSource(context.Background(), "org-1", "app-1", "latest")

	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.ServerCode != "app_not_upload_backed" {
		t.Fatalf("err = %v, want the server code", err)
	}
}

func newStorage(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s
}

func TestDownloadSourceArchive_StreamsAndHashesWithoutCredentials(t *testing.T) {
	t.Parallel()
	payload := bytes.Repeat([]byte("zip"), 1000)
	var gotAuth []string
	s := newStorage(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = append(gotAuth, r.Header.Get("Authorization"))
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		_, _ = w.Write(payload)
	})
	c := client.New("https://api.invalid", "sapi_secret")

	var dst bytes.Buffer
	gotSum, n, err := c.DownloadSourceArchive(context.Background(), s.URL+"/start", int64(len(payload)), &dst)

	if err != nil {
		t.Fatal(err)
	}
	if n != int64(len(payload)) || gotSum != sum(payload) || !bytes.Equal(dst.Bytes(), payload) {
		t.Errorf("n=%d sum=%s", n, gotSum)
	}
	if len(gotAuth) != 2 {
		t.Fatalf("requests = %d, want the start and the redirect target", len(gotAuth))
	}
	for i, a := range gotAuth {
		if a != "" {
			t.Errorf("request %d carried Authorization %q", i, a)
		}
	}
}

func TestDownloadSourceArchive_SizeIsAnUpperBound(t *testing.T) {
	t.Parallel()
	for name, chunked := range map[string]bool{"content-length": false, "chunked": true} {
		t.Run(name, func(t *testing.T) {
			s := newStorage(t, func(w http.ResponseWriter, _ *http.Request) {
				if chunked {
					w.(http.Flusher).Flush() // no Content-Length: the cap must hold while reading
				}
				_, _ = w.Write(bytes.Repeat([]byte("A"), 5000))
			})
			c := client.New("https://api.invalid", "t")

			var dst bytes.Buffer
			_, _, err := c.DownloadSourceArchive(context.Background(), s.URL, 1000, &dst)

			if !errors.Is(err, client.ErrDownloadTooLarge) {
				t.Fatalf("err = %v, want ErrDownloadTooLarge", err)
			}
			if dst.Len() > 1001 {
				t.Errorf("read %d bytes past the cap", dst.Len())
			}
		})
	}
}

func TestDownloadSourceArchive_StorageRefusal(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		status  int
		body    string
		expired bool
	}{
		"expired link":    {http.StatusForbidden, `<Error><Code>AccessDenied</Code><Message>Request has expired</Message></Error>`, true},
		"expired token":   {http.StatusBadRequest, `<Error><Code>ExpiredToken</Code><Message>token</Message></Error>`, true},
		"other refusal":   {http.StatusForbidden, `<Error><Code>AccessDenied</Code><Message>Access Denied</Message></Error>`, false},
		"no body":         {http.StatusInternalServerError, ``, false},
		"gone from store": {http.StatusNotFound, `<Error><Code>NoSuchKey</Code><Message>missing</Message></Error>`, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := newStorage(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			c := client.New("https://api.invalid", "t")

			_, _, err := c.DownloadSourceArchive(context.Background(), s.URL, 100, &bytes.Buffer{})

			var de *client.DownloadError
			if !errors.As(err, &de) {
				t.Fatalf("err = %v, want a DownloadError", err)
			}
			if de.HTTPStatus != tc.status || de.Expired() != tc.expired {
				t.Errorf("status=%d expired=%v, want %d %v", de.HTTPStatus, de.Expired(), tc.status, tc.expired)
			}
		})
	}
}

func TestDownloadSourceArchive_ErrorsNeverCarryTheURL(t *testing.T) {
	t.Parallel()
	s := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := s.URL + "/secret/path?X-Amz-Signature=topsecret"
	s.Close() // nothing listens: a dial failure whose net/http error text embeds the URL
	c := client.New("https://api.invalid", "t")

	_, _, err := c.DownloadSourceArchive(context.Background(), url, 100, &bytes.Buffer{})

	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.HTTPStatus != 0 {
		t.Fatalf("err = %v, want a network error", err)
	}
	if strings.Contains(err.Error(), "topsecret") || strings.Contains(err.Error(), "/secret/path") {
		t.Errorf("the error leaks the URL: %v", err)
	}
}
