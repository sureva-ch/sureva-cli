// Package client provides a hand-rolled HTTP client for the cloud-api.
// All methods return *APIError on failure so the command layer can call
// Renderer.RenderError and os.Exit with the canonical exit code.
package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// APIError is returned by all client methods on failure.
//
// For HTTP errors, HTTPStatus holds the response status code and Code holds the
// stable error category string (auth_error, not_found, validation_error, server_error,
// api_error). For network failures where no HTTP response was received, HTTPStatus is 0
// and Code is "network_error".
//
// The command layer passes Message, Code, and HTTPStatus to Renderer.RenderError and
// calls os.Exit with the returned code. HTTPStatus=0 maps to ExitNetwork (5).
type APIError struct {
	HTTPStatus int
	Code       string
	Message    string
}

func (e *APIError) Error() string {
	if e.HTTPStatus == 0 {
		return fmt.Sprintf("network error: %s", e.Message)
	}
	return fmt.Sprintf("API error %d (%s): %s", e.HTTPStatus, e.Code, e.Message)
}

// apiErrorBody is the JSON shape returned by cloud-api on error.
type apiErrorBody struct {
	Error string `json:"error"`
}

// parseErrorResponse reads and closes the response body and constructs an *APIError.
// A 401 response always overrides the server message with actionable guidance that
// directs the user to SUREVA_TOKEN, regardless of what the server said.
func parseErrorResponse(resp *http.Response) *APIError {
	defer func() {
		_ = resp.Body.Close()
	}()

	data, _ := io.ReadAll(resp.Body)
	var envelope apiErrorBody
	_ = json.Unmarshal(data, &envelope)

	code := httpStatusToCode(resp.StatusCode)
	msg := envelope.Error
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}

	if resp.StatusCode == 401 {
		msg = "credentials invalid or expired — verify SUREVA_TOKEN is set and valid, " +
			"or re-authenticate with 'sureva login'"
	}

	return &APIError{
		HTTPStatus: resp.StatusCode,
		Code:       code,
		Message:    msg,
	}
}

// httpStatusToCode returns the stable code string for an HTTP status.
func httpStatusToCode(status int) string {
	switch {
	case status == 401 || status == 403:
		return "auth_error"
	case status == 404:
		return "not_found"
	case status == 400 || status == 422:
		return "validation_error"
	case status >= 500:
		return "server_error"
	default:
		return "api_error"
	}
}

// networkError constructs an *APIError for transport failures where no HTTP response
// was received. HTTPStatus=0 causes Renderer.RenderError to return ExitNetwork (5).
func networkError(cause error) *APIError {
	return &APIError{
		HTTPStatus: 0,
		Code:       "network_error",
		Message:    cause.Error(),
	}
}
