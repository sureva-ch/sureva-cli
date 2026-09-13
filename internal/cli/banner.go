package cli

import (
	"fmt"
	"io"
	"os"
)

// Brand presentation constants. brandGreen is the Sureva brand color
// (#15803d) expressed as an ANSI truecolor escape; colorReset clears it.
const (
	brandGreen = "\x1b[38;2;21;128;61m"
	colorReset = "\x1b[0m"
)

// bannerArt is the "sureva" wordmark (figlet "small") shown in interactive
// help. Trailing whitespace is intentionally omitted so the block stays tight.
const bannerArt = ` ___ _   _ ___ _____   ___
/ __| | | | _ \ __\ \ / /_
\__ \ |_| |   / _| \ V / _ \
|___/\___/|_|_\___|  \_/_/ \_\`

// bannerTagline sits under the wordmark.
const bannerTagline = "  the Sureva cloud platform"

// printBanner writes the brand banner to w, but only when w is an interactive
// terminal. It is suppressed for pipes, redirects, and CI so it can never
// contaminate machine-readable output (JSON on stdout). NO_COLOR drops the
// ANSI coloring while keeping the art.
func printBanner(w io.Writer) {
	f, ok := w.(*os.File)
	if !ok || !isTerminal(f) {
		return
	}

	green, reset := brandGreen, colorReset
	if os.Getenv("NO_COLOR") != "" {
		green, reset = "", ""
	}

	_, _ = fmt.Fprintln(w, green+bannerArt+reset)
	_, _ = fmt.Fprintln(w, green+bannerTagline+reset)
	_, _ = fmt.Fprintln(w)
}

// isTerminal reports whether f refers to a character device (a TTY) rather than
// a pipe or regular file. This is the dependency-free stdlib check.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
