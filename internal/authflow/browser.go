package authflow

import (
	"fmt"
	"os/exec"
	"runtime"
)

// BrowserOpener opens the given URL in the user's default browser. It is
// injectable so tests never launch a real browser, and so the CLI can
// gracefully fall back to printing the URL when opening fails (e.g. a
// headless or SSH session with no local display).
type BrowserOpener func(url string) error

// defaultBrowserOpener returns the per-OS BrowserOpener used when the caller
// does not inject one. A non-nil error from the returned func means the OS
// call itself failed to start (missing binary, no display, etc.); it is NOT
// fatal to the login flow — callers print the authorize URL instead.
func defaultBrowserOpener() BrowserOpener {
	return func(url string) error {
		cmd, args := openCommand(runtime.GOOS, url)
		// #nosec G204 -- cmd/args come from openCommand's fixed per-OS table;
		// url is the Cognito authorize URL we generated, not external input.
		if err := exec.Command(cmd, args...).Start(); err != nil {
			return fmt.Errorf("open browser: %w", err)
		}
		return nil
	}
}

// openCommand returns the OS-specific command and arguments used to open url
// in the default browser. Kept as a pure function so the OS-selection logic
// is unit-testable without spawning any process.
func openCommand(goos, url string) (string, []string) {
	switch goos {
	case "darwin":
		return "open", []string{url}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default: // linux and other unix-likes
		return "xdg-open", []string{url}
	}
}
