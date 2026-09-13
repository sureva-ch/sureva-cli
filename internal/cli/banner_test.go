package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// TestPrintBannerSuppressedForNonTTY is the contract guard: when output is not
// an interactive terminal (a buffer, pipe, or redirect), the banner must emit
// nothing so JSON stdout is never contaminated.
func TestPrintBannerSuppressedForNonTTY(t *testing.T) {
	var buf bytes.Buffer
	printBanner(&buf)
	if buf.Len() != 0 {
		t.Fatalf("banner wrote %d bytes to a non-terminal writer; want 0", buf.Len())
	}
}

// TestPrintBannerSuppressedForPipe ensures an *os.File that is a pipe (not a
// char device) is also treated as non-interactive.
func TestPrintBannerSuppressedForPipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()

	if isTerminal(w) {
		t.Fatal("os.Pipe write end reported as a terminal")
	}
	printBanner(w) // must be a no-op; nothing reads r, so a write would block on full buffer only — but it must not write at all
}

// TestBannerArtMatchesWordmark sanity-checks the embedded art so an accidental
// edit that empties or truncates it is caught.
func TestBannerArtMatchesWordmark(t *testing.T) {
	lines := strings.Split(bannerArt, "\n")
	if len(lines) != 4 {
		t.Fatalf("banner art has %d lines, want 4", len(lines))
	}
	if !strings.Contains(bannerTagline, "Sureva") {
		t.Fatalf("tagline missing brand name: %q", bannerTagline)
	}
}
