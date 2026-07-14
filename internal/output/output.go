// Package output provides the CLI rendering layer.
//
// Stdout receives structured data (JSON by default, table with --output table).
// Stderr always receives JSON error envelopes, regardless of --output.
//
// All commands should call Render or RenderError rather than writing directly
// to os.Stdout/os.Stderr so the format flag is respected globally.
package output

import (
	"io"
	"os"
)

// Format controls how successful results are rendered.
type Format string

const (
	// FormatJSON renders results as indented JSON on stdout. This is the default.
	// Agents and scripts consume this format directly.
	FormatJSON Format = "json"
	// FormatTable renders results as a human-readable ASCII table on stdout.
	FormatTable Format = "table"
)

// Renderer writes structured data to the configured output streams.
// The zero value is not valid; use NewRenderer.
type Renderer struct {
	format Format
	out    io.Writer
	err    io.Writer
}

// NewRenderer returns a Renderer that writes to out (data) and errw (errors).
// format must be FormatJSON or FormatTable; an unrecognised value falls back to JSON.
func NewRenderer(format Format, out, errw io.Writer) *Renderer {
	if format != FormatJSON && format != FormatTable {
		format = FormatJSON
	}
	return &Renderer{format: format, out: out, err: errw}
}

// Default returns a Renderer using the given format writing to os.Stdout / os.Stderr.
func Default(format Format) *Renderer {
	return NewRenderer(format, os.Stdout, os.Stderr)
}

// Render writes v to stdout in the configured format.
// It returns a non-nil error only when the underlying write fails.
func (r *Renderer) Render(v any) error {
	switch r.format {
	case FormatTable:
		return writeTable(r.out, v)
	default:
		return writeJSON(r.out, v)
	}
}

// RenderError writes an error envelope to stderr as JSON and returns the
// canonical exit code for the given HTTP status (0 when httpStatus is 0,
// which means no HTTP response was available).
//
// httpStatus == 0 maps to ExitNetwork when there is no HTTP response.
// Pass -1 to suppress the http_status field (client-side validation errors).
func (r *Renderer) RenderError(message, code string, httpStatus int) int {
	var exitCode int
	switch {
	case httpStatus == 0:
		exitCode = ExitNetwork
	case httpStatus < 0:
		exitCode = ExitValidation
	default:
		exitCode = HTTPStatusToExitCode(httpStatus)
	}
	_ = writeError(r.err, message, code, max(httpStatus, 0))
	return exitCode
}
