// Package output handles rendering of CLI results and errors.
// This file is the single source of truth for exit codes.
package output

// Exit code constants. Every command must exit with one of these.
// Consumers (scripts, agents) rely on these codes being stable.
const (
	// ExitOK is returned on success (2xx API response or no error).
	ExitOK = 0
	// ExitGeneral is returned for unmapped API errors or unexpected failures.
	ExitGeneral = 1
	// ExitAuth is returned for authentication or authorization failures (401, 403, missing token).
	ExitAuth = 2
	// ExitNotFound is returned when a requested resource does not exist (404).
	ExitNotFound = 3
	// ExitValidation is returned for client-side argument errors or API 400/422 responses.
	ExitValidation = 4
	// ExitNetwork is returned when no HTTP response is received (dial, timeout, DNS failure).
	ExitNetwork = 5
)

// HTTPStatusToExitCode maps an HTTP status code to the canonical CLI exit code.
// It returns ExitGeneral for unmapped statuses.
func HTTPStatusToExitCode(httpStatus int) int {
	switch {
	case httpStatus >= 200 && httpStatus < 300:
		return ExitOK
	case httpStatus == 401 || httpStatus == 403:
		return ExitAuth
	case httpStatus == 404:
		return ExitNotFound
	case httpStatus == 400 || httpStatus == 422:
		return ExitValidation
	default:
		return ExitGeneral
	}
}
