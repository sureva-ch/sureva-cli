package cli_test

import (
	"bytes"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sureva-ch/sureva-cli/internal/output"
)

// The bodies below are what cloud-api's handlers write, copied from
// writeErrorDetails call sites on its main branch (handlers/deployments.go,
// app_sources.go, app_source_validation.go, app_source_download.go) and from
// docs/api-reference.md. The CLI must classify them by the `code` field alone:
// the messages are deliberately NOT the strings the API sends today, so a CLI
// that went back to reading prose fails here, and an API that renames a code or
// moves a detail field is caught by updating the fixture from the new handler.
const (
	// deployments.go resolveDeploySource.
	apiSourceNotFound     = `{"code":"source_not_found","error":"source not found"}`
	apiNoReadySource      = `{"code":"no_ready_source","error":"reworded: nothing is ready"}`
	apiSourceNotReady     = `{"code":"source_not_ready","error":"reworded: not deployable","source_status":"rejected","validation_code":"archive_empty"}`
	apiSourceNotReadyBare = `{"code":"source_not_ready","error":"reworded: not deployable","source_status":"validating"}`
	apiSourceExpired      = `{"code":"source_expired","error":"reworded: gone"}`
	// app_source_validation.go CompleteAppSourceUpload.
	apiNotCompletable = `{"code":"source_not_completable","error":"reworded","source_status":"ready"}`
	// app_source_download.go and app_sources.go.
	apiNotUploadBacked = `{"code":"app_not_upload_backed","error":"reworded: clone the repository"}`
	apiUploadLimit     = `{"code":"app_source_upload_limit_exceeded","error":"reworded: limit"}`
)

func TestContract_DeployErrorCodesOnTrigger(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		code    string
		exit    int
		details map[string]string
	}{
		{"source_not_found", 404, apiSourceNotFound, "not_found", output.ExitNotFound, map[string]string{"api_code": "source_not_found"}},
		{"no_ready_source", 404, apiNoReadySource, "no_ready_source", output.ExitNotFound, map[string]string{"api_code": "no_ready_source"}},
		{"source_not_ready with validation_code", 409, apiSourceNotReady, "source_not_ready", output.ExitGeneral,
			map[string]string{"api_code": "source_not_ready", "source_status": "rejected", "validation_code": "archive_empty"}},
		{"source_not_ready without validation_code", 409, apiSourceNotReadyBare, "source_not_ready", output.ExitGeneral,
			map[string]string{"source_status": "validating"}},
		{"source_expired", 410, apiSourceExpired, "source_expired", output.ExitGeneral, map[string]string{"api_code": "source_expired"}},
		// A 409 with another code is not a source problem and keeps its status code.
		{"other 409", 409, `{"code":"deployment_in_progress","error":"this source archive is busy"}`, "api_error", output.ExitGeneral,
			map[string]string{"api_code": "deployment_in_progress"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := deploys_mux()
			mux.HandleFunc("/v1/orgs/"+testOrgID+"/apps/"+testAppID+"/deployments", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			_, errBuf, exec := newTestRoot(t, newTestServer(t, mux))

			err := exec("deploys", "trigger", testAppID, "--org", testOrgSlug, "--source-id", "s1")

			assertEnvelope(t, errBuf, exitCode(err), tc.exit, tc.code, tc.details)
		})
	}
}

func TestContract_SourcesGetAndListClassifyByCode(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		code       string
		exit       int
	}{
		{"app_not_upload_backed", apiNotUploadBacked, 422, "github_backed_app", output.ExitValidation},
		{"source_not_found", apiSourceNotFound, 404, "not_found", output.ExitNotFound},
	} {
		for _, sub := range [][]string{{"sources", "list", testAppID}, {"sources", "get", testAppID, "s1"}} {
			t.Run(tc.name+"/"+sub[1], func(t *testing.T) {
				mux := deploys_mux()
				mux.HandleFunc(sourcesPath, func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
				})
				mux.HandleFunc(sourcesPath+"/s1", func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
				})
				_, errBuf, exec := newTestRoot(t, newTestServer(t, mux))

				err := exec(append(sub, "--org", testOrgSlug)...)

				assertEnvelope(t, errBuf, exitCode(err), tc.exit, tc.code, nil)
			})
		}
	}
}

// The row shape the API writes for a rejected source: models.AppSource plus the
// view's retryable. validation_code and retryable must reach the output.
func TestContract_SourceRowShapeReachesTheOutput(t *testing.T) {
	const row = `{"id":"5d0a","org_id":"o","app_id":"app-1","seq":4,"status":"rejected","aws_region":"us-east-2",` +
		`"upload_key":"k","validation_error":"the archive appears to contain credentials","validation_code":"credentials_found",` +
		`"base_source_id":"3c1b","created_at":"2026-10-04T10:00:00Z","updated_at":"2026-10-04T10:00:05Z","retryable":false}`
	mux := deploys_mux()
	mux.HandleFunc(sourcesPath+"/5d0a", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(row)) })
	mux.HandleFunc(sourcesPath, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("[" + row + "]")) })
	outBuf, _, exec := newTestRoot(t, newTestServer(t, mux))

	if err := exec("sources", "get", testAppID, "5d0a", "--org", testOrgSlug); err != nil {
		t.Fatal(err)
	}
	got := decodeJSON(t, outBuf)
	if got["validation_code"] != "credentials_found" || got["retryable"] != false || got["base_source_id"] != "3c1b" {
		t.Errorf("sources get lost fields: %v", got)
	}

	outBuf.Reset()
	if err := exec("sources", "list", testAppID, "--org", testOrgSlug); err != nil {
		t.Fatal(err)
	}
	if s := outBuf.String(); !strings.Contains(s, `"validation_code": "credentials_found"`) || !strings.Contains(s, `"retryable": false`) {
		t.Errorf("sources list lost fields: %s", s)
	}
}

func TestContract_PullCodes(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		status int
		body   string
		code   string
		exit   int
	}{
		// The download endpoint sends no code for its 404 today (the uncoded
		// case is covered in TestSourcesPull_APIFailures); a coded one is
		// handled the same.
		{"no_ready_source keeps no_source", nil, 404, apiNoReadySource, "no_source", output.ExitNotFound},
		{"source_not_found", []string{"--source-id", "x"}, 404, apiSourceNotFound, "not_found", output.ExitNotFound},
		{"app_not_upload_backed", nil, 422, apiNotUploadBacked, "github_backed_app", output.ExitValidation},
		{"source_not_ready", nil, 409, apiSourceNotReady, "source_not_ready", output.ExitGeneral},
		{"source_expired", nil, 410, apiSourceExpired, "source_expired", output.ExitGeneral},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newPullFake(t, projectZip(t))
			f.downloadStatus, f.downloadBody = tc.status, tc.body
			_, errBuf, exec := newTestRoot(t, f.api)

			err := exec(pullArgs(filepath.Join(t.TempDir(), "app"), tc.args...)...)

			assertEnvelope(t, errBuf, exitCode(err), tc.exit, tc.code, nil)
		})
	}
}

// assertEnvelope checks the stderr envelope: its code, the exit code and the
// listed string details.
func assertEnvelope(t *testing.T, errBuf *bytes.Buffer, gotExit, wantExit int, wantCode string, details map[string]string) {
	t.Helper()
	env := decodeJSON(t, errBuf)
	if gotExit != wantExit {
		t.Errorf("exit = %d, want %d; envelope %v", gotExit, wantExit, env)
	}
	if env["code"] != wantCode {
		t.Errorf("code = %v, want %s; envelope %v", env["code"], wantCode, env)
	}
	got, _ := env["details"].(map[string]any)
	for k, v := range details {
		if got[k] != v {
			t.Errorf("details.%s = %v, want %s; envelope %v", k, got[k], v, env)
		}
	}
}
