package client

import (
	"net/http"
	"net/url"
	"testing"
)

func req(t *testing.T, raw string) *http.Request {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Request{URL: u, Header: http.Header{"Authorization": {"Bearer x"}}}
}

func TestCheckDownloadRedirect(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		from    string
		to      string
		hops    int
		wantErr bool
	}{
		{"https to https", "https://a.example/x", "https://b.example/y", 1, false},
		{"https to http", "https://a.example/x", "http://b.example/y", 1, true},
		{"http to https", "http://a.example/x", "https://b.example/y", 1, false},
		{"http to http", "http://a.example/x", "http://b.example/y", 1, false},
		{"too many", "https://a.example/x", "https://b.example/y", maxRedirects, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			via := make([]*http.Request, tc.hops)
			for i := range via {
				via[i] = req(t, tc.from)
			}
			r := req(t, tc.to)
			err := checkDownloadRedirect(r, via)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if err == nil && r.Header.Get("Authorization") != "" {
				t.Error("Authorization must be stripped from a followed redirect")
			}
		})
	}
}
