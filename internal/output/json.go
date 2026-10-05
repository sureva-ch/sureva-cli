package output

import (
	"encoding/json"
	"fmt"
	"io"
)

// writeJSON encodes v as indented JSON and writes it to w.
// It returns an error only if encoding or writing fails.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// errorEnvelope is the JSON shape written to stderr on failure.
// It is always JSON, regardless of the --output flag.
type errorEnvelope struct {
	Error      string `json:"error"`
	Code       string `json:"code"`
	HTTPStatus int    `json:"http_status,omitempty"`
	// Details carries machine-readable facts that come with the failure (for
	// example the API's own error code or a source's validation_code). It is
	// omitted when there are none, so an envelope without details is unchanged.
	Details map[string]any `json:"details,omitempty"`
}

// writeError writes an error envelope to w as JSON.
func writeError(w io.Writer, message, code string, httpStatus int, details map[string]any) error {
	env := errorEnvelope{
		Error:      message,
		Code:       code,
		HTTPStatus: httpStatus,
		Details:    details,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(env); err != nil {
		// Last-resort plain text; json.Encoder failure is extremely unlikely.
		_, werr := fmt.Fprintf(w, `{"error":%q,"code":%q}%s`, message, code, "\n")
		return werr
	}
	return nil
}
