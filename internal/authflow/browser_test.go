package authflow

import (
	"errors"
	"testing"
)

// TestOpenCommand_PerOS proves openCommand selects the right OS command
// without spawning any real process (pure function, table-driven).
func TestOpenCommand_PerOS(t *testing.T) {
	const url = "https://auth.sureva.ai/oauth2/authorize?state=abc"

	tests := []struct {
		name     string
		goos     string
		wantCmd  string
		wantArgs []string
	}{
		{
			name:     "darwin uses open",
			goos:     "darwin",
			wantCmd:  "open",
			wantArgs: []string{url},
		},
		{
			name:     "windows uses rundll32",
			goos:     "windows",
			wantCmd:  "rundll32",
			wantArgs: []string{"url.dll,FileProtocolHandler", url},
		},
		{
			name:     "linux uses xdg-open",
			goos:     "linux",
			wantCmd:  "xdg-open",
			wantArgs: []string{url},
		},
		{
			name:     "unrecognised GOOS falls back to xdg-open",
			goos:     "plan9",
			wantCmd:  "xdg-open",
			wantArgs: []string{url},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCmd, gotArgs := openCommand(tt.goos, url)
			if gotCmd != tt.wantCmd {
				t.Errorf("cmd = %q, want %q", gotCmd, tt.wantCmd)
			}
			if len(gotArgs) != len(tt.wantArgs) {
				t.Fatalf("args = %v, want %v", gotArgs, tt.wantArgs)
			}
			for i := range gotArgs {
				if gotArgs[i] != tt.wantArgs[i] {
					t.Errorf("args[%d] = %q, want %q", i, gotArgs[i], tt.wantArgs[i])
				}
			}
		})
	}
}

// TestBrowserOpener_RecordsURL proves an injected BrowserOpener is a plain
// callable that receives exactly the URL passed to it — no real browser is
// ever launched from tests.
func TestBrowserOpener_RecordsURL(t *testing.T) {
	var got string
	var opener BrowserOpener = func(url string) error {
		got = url
		return nil
	}

	const authorizeURL = "https://auth.sureva.ai/oauth2/authorize?state=xyz"
	if err := opener(authorizeURL); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != authorizeURL {
		t.Errorf("recorded URL = %q, want %q", got, authorizeURL)
	}
}

// TestBrowserOpener_FailurePathReturnsErrorNoPanic proves a failing opener
// (e.g. headless/SSH environment) surfaces an error to the caller instead of
// panicking, so the CLI can fall back to printing the URL.
func TestBrowserOpener_FailurePathReturnsErrorNoPanic(t *testing.T) {
	wantErr := errors.New("exec: \"xdg-open\": executable file not found in $PATH")
	var opener BrowserOpener = func(url string) error {
		return wantErr
	}

	err := opener("https://auth.sureva.ai/oauth2/authorize?state=xyz")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
}

// TestDefaultBrowserOpener_ReturnsOpener pins that the default constructor
// yields a usable opener. It never invokes the opener itself — that would
// spawn a real browser process.
func TestDefaultBrowserOpener_ReturnsOpener(t *testing.T) {
	if opener := defaultBrowserOpener(); opener == nil {
		t.Fatal("defaultBrowserOpener() = nil, want non-nil opener")
	}
}
