package cli

import (
	"errors"
	"net/http"
	"strings"

	"github.com/sureva-ch/sureva-cli/internal/client"
)

// Stable error codes cloud-api attaches to the source and deploy endpoints
// (handlers/deployments.go, app_sources.go, app_source_validation.go and
// app_source_download.go). They are the contract: the CLI switches on these and
// never on the prose of the message, which the API is free to reword.
const (
	apiCodeSourceNotFound       = "source_not_found"                 // 404, deploy: the named source does not exist
	apiCodeNoReadySource        = "no_ready_source"                  // 404, deploy: no source named and none ready
	apiCodeSourceNotReady       = "source_not_ready"                 // 409, deploy: carries source_status, validation_code
	apiCodeSourceExpired        = "source_expired"                   // 410, deploy: the stored version is gone
	apiCodeSourceNotCompletable = "source_not_completable"           // 409, complete: carries source_status
	apiCodeAppNotUploadBacked   = "app_not_upload_backed"            // 422, download of a GitHub-backed app
	apiCodeUploadLimit          = "app_source_upload_limit_exceeded" // 422, create upload: daily limit
)

// Envelope codes the CLI gives those failures. Most keep the name of the API
// code; the exceptions are noted where they are assigned.
const (
	codeNoReadySource        = "no_ready_source"
	codeSourceNotReady       = "source_not_ready"
	codeSourceExpired        = "source_expired"
	codeSourceNotCompletable = "source_not_completable"
	codeUploadLimit          = "app_source_upload_limit_exceeded"
	codeGitHubBacked         = "github_backed_app"
	codeNotFound             = "not_found"
)

// apiErrorDetails is what an *client.APIError adds to the stderr envelope as
// "details": the API's own code (so an agent still sees a code this CLI does not
// know yet) and the fields that came with it. nil when there is nothing.
func apiErrorDetails(e *client.APIError) map[string]any {
	d := map[string]any{}
	if e.ServerCode != "" {
		d["api_code"] = e.ServerCode
	}
	if e.SourceStatus != "" {
		d["source_status"] = e.SourceStatus
	}
	if e.ValidationCode != "" {
		d["validation_code"] = e.ValidationCode
	}
	if len(e.Environments) > 0 {
		d["environments"] = e.Environments
	}
	if len(d) == 0 {
		return nil
	}
	return d
}

// classifySourceError gives the failures of the source and deploy endpoints
// their own envelope code, by the API's stable `code`. A status alone is not
// enough: a 409 is a release that is not ready, an upload that cannot be
// completed or a deployment already in progress, and exit 1 does not say which.
//
// The envelope codes already released keep their names: source_not_found stays
// not_found (exit 3, a release id that does not exist) and app_not_upload_backed
// is github_backed_app. no_ready_source, source_not_completable and
// app_source_upload_limit_exceeded are new. A code this function does not know
// keeps its status-derived envelope code and is reported in details.api_code.
func classifySourceError(err error) error {
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	if apiErr.ServerCode == "" {
		classifyUncodedSourceError(apiErr)
		return apiErr
	}
	switch apiErr.ServerCode {
	case apiCodeSourceNotFound:
		apiErr.Code = codeNotFound
	case apiCodeNoReadySource:
		apiErr.Code = codeNoReadySource
	case apiCodeSourceNotReady:
		apiErr.Code = codeSourceNotReady
	case apiCodeSourceExpired:
		apiErr.Code = codeSourceExpired
	case apiCodeSourceNotCompletable:
		apiErr.Code = codeSourceNotCompletable
	case apiCodeAppNotUploadBacked:
		apiErr.Code = codeGitHubBacked
	case apiCodeUploadLimit:
		apiErr.Code = codeUploadLimit
	}
	return apiErr
}

// classifyUncodedSourceError is the fallback for a response that carries no
// `code`: an API older than the codes. The download endpoint still sends none
// for its 404, 409 and 410 (only app_not_upload_backed), so these heuristics
// are not dead yet.
//
// REMOVABLE once cloud-api sends a code on every one of these responses: a 410
// is only ever a vanished stored version, and a 409 is recognised by the message
// prefix "this source archive is <status>"; any other 409 keeps its
// status-derived code.
func classifyUncodedSourceError(apiErr *client.APIError) {
	switch {
	case apiErr.HTTPStatus == http.StatusGone:
		apiErr.Code = codeSourceExpired
	case apiErr.HTTPStatus == http.StatusConflict && strings.HasPrefix(apiErr.Message, "this source archive is "):
		apiErr.Code = codeSourceNotReady
	}
}
