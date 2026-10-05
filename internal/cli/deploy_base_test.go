package cli_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sureva-ch/sureva-cli/internal/output"
	"github.com/sureva-ch/sureva-cli/internal/sourcebase"
)

// The refusals the API gives a bad base at request time (app_sources.go
// resolveBaseSource) and the rejected row of a stale base (docs/api-reference.md,
// "Building on a release"). The messages here are reworded on purpose.
const (
	apiInvalidBase  = `{"code":"invalid_base_source_id","error":"reworded: not a source id"}`
	apiBaseNotFound = `{"code":"base_source_not_found","error":"reworded: unknown base"}`
	staleBaseWhy    = "the release this upload was built from is no longer the latest: src-5 is."
	// baseSrcID is the release a directory was pulled from: ids are UUIDs, and a
	// record whose id is not one is not sent as a base.
	baseSrcID      = "3c1b0d5e-4f6a-4a2b-9c3d-1e2f3a4b5c6d"
	latestListJSON = `[{"id":"src-5-id","app_id":"app-1","seq":5,"status":"ready","release_tag":"src-5","created_at":"2026-10-04T10:00:00Z","updated_at":"2026-10-04T10:00:00Z"},{"id":"` + baseSrcID + `","app_id":"app-1","seq":2,"status":"ready","release_tag":"src-2","created_at":"2026-10-03T10:00:00Z","updated_at":"2026-10-03T10:00:00Z"}]`
)

func writeRecord(t *testing.T, dir string, b sourcebase.Base) {
	t.Helper()
	if _, err := sourcebase.Write(dir, b); err != nil {
		t.Fatal(err)
	}
}

func pulledAs(appID string) sourcebase.Base {
	return sourcebase.Base{AppID: appID, SourceID: baseSrcID, ReleaseTag: "src-2", SHA256: "old-sha"}
}

// sentBase decodes the base_source_id of the create-upload request; ok is false
// when the request carried no body at all.
func sentBase(t *testing.T, f *deployFake) (id string, hasBody bool) {
	t.Helper()
	if f.createReqBody == "" {
		return "", false
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(f.createReqBody), &body); err != nil {
		t.Fatalf("create request body %q: %v", f.createReqBody, err)
	}
	id, _ = body["base_source_id"].(string)
	return id, true
}

func TestDeploy_SendsThePulledReleaseAsBase(t *testing.T) {
	f := newDeployFake(t)
	dir := deployProject(t)
	writeRecord(t, dir, pulledAs(testAppID))
	outBuf, errBuf, exec := newTestRoot(t, f.api)

	if err := exec(deployArgs(dir)...); exitCode(err) != 0 {
		t.Fatalf("exit %d: %s", exitCode(err), errBuf)
	}

	if id, _ := sentBase(t, f); id != baseSrcID {
		t.Errorf("base_source_id sent = %q, want %s (body %q)", id, baseSrcID, f.createReqBody)
	}
	res := decodeJSON(t, outBuf)
	if res["base_source_id"] != baseSrcID || res["base_sent"] != true {
		t.Errorf("base_source_id/base_sent = %v/%v", res["base_source_id"], res["base_sent"])
	}
}

func TestDeploy_SendsNothingWithoutAUsableRecord(t *testing.T) {
	cases := map[string]func(t *testing.T, dir string){
		"never pulled": func(*testing.T, string) {},
		"another app":  func(t *testing.T, dir string) { writeRecord(t, dir, pulledAs("other-app")) },
		"damaged record": func(t *testing.T, dir string) {
			if err := os.MkdirAll(filepath.Join(dir, ".sureva"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(sourcebase.Path(dir), []byte("{broken"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			f := newDeployFake(t)
			dir := deployProject(t)
			setup(t, dir)
			outBuf, errBuf, exec := newTestRoot(t, f.api)

			if err := exec(deployArgs(dir)...); exitCode(err) != 0 {
				t.Fatalf("exit %d: %s", exitCode(err), errBuf)
			}

			if f.createReqBody != "" {
				t.Errorf("the request must carry no body, got %q", f.createReqBody)
			}
			if res := decodeJSON(t, outBuf); res["base_sent"] != false {
				t.Errorf("base_sent = %v, want false", res["base_sent"])
			}
		})
	}
}

func TestDeploy_NoBaseOverwritesOnPurpose(t *testing.T) {
	f := newDeployFake(t)
	dir := deployProject(t)
	writeRecord(t, dir, pulledAs(testAppID))
	outBuf, errBuf, exec := newTestRoot(t, f.api)

	if err := exec(deployArgs(dir, "--no-base")...); exitCode(err) != 0 {
		t.Fatalf("exit %d: %s", exitCode(err), errBuf)
	}

	if f.createReqBody != "" {
		t.Errorf("--no-base must send no base, got body %q", f.createReqBody)
	}
	res := decodeJSON(t, outBuf)
	if res["base_sent"] != false || res["base_source_id"] != baseSrcID {
		t.Errorf("base_sent/base_source_id = %v/%v", res["base_sent"], res["base_source_id"])
	}
	// The directory is the latest release afterwards, so the record moves anyway.
	if b := sourcebase.Read(dir); b == nil || b.SourceID != deploySrcID {
		t.Errorf("record = %+v, want the published release", b)
	}
}

func TestDeploy_StaleBaseHasItsOwnCodeAndNamesTheLatestRelease(t *testing.T) {
	f := newDeployFake(t)
	f.sourceSeq = []string{"validating", "rejected"}
	f.rejectWhy, f.rejectCode, f.rejectRetryable = staleBaseWhy, "stale_base", boolPtr(false)
	f.listJSON = latestListJSON
	dir := deployProject(t)
	writeRecord(t, dir, pulledAs(testAppID))
	outBuf, errBuf, exec := newTestRoot(t, f.api)

	err := exec(deployArgs(dir)...)

	if got := exitCode(err); got != output.ExitGeneral {
		t.Fatalf("exit = %d, want %d", got, output.ExitGeneral)
	}
	env := decodeJSON(t, errBuf)
	msg, _ := env["error"].(string)
	details, _ := env["details"].(map[string]any)
	if env["code"] != "stale_base" {
		t.Fatalf("code = %v, want stale_base: %v", env["code"], env)
	}
	for _, want := range []string{"src-2", "src-5", "sources pull", "--no-base", "separate directory"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q: %s", want, msg)
		}
	}
	if details["latest_release_tag"] != "src-5" || details["latest_source_id"] != "src-5-id" ||
		details["validation_code"] != "stale_base" || details["retryable"] != false {
		t.Errorf("details = %v", details)
	}
	if f.completed != 1 {
		t.Errorf("a stale base must not be retried; complete called %d times", f.completed)
	}
	if f.deployBody != nil {
		t.Error("nothing may be deployed")
	}
	if src, _ := decodeJSON(t, outBuf)["source"].(map[string]any); src["validation_code"] != "stale_base" {
		t.Errorf("source = %v", src)
	}
	if b := sourcebase.Read(dir); b == nil || b.SourceID != baseSrcID {
		t.Errorf("a refused upload must not move the record, got %+v", b)
	}
}

// Without a readable list the failure still names the code and the way out.
func TestDeploy_StaleBaseWhenTheListCannotBeRead(t *testing.T) {
	f := newDeployFake(t)
	f.sourceSeq = []string{"rejected"}
	f.rejectCode, f.rejectRetryable = "stale_base", boolPtr(false)
	f.listJSON = `not json`
	dir := deployProject(t)
	writeRecord(t, dir, pulledAs(testAppID))
	_, errBuf, exec := newTestRoot(t, f.api)

	_ = exec(deployArgs(dir)...)

	env := decodeJSON(t, errBuf)
	if env["code"] != "stale_base" || !strings.Contains(env["error"].(string), "a newer release") {
		t.Errorf("envelope = %v", env)
	}
}

func TestDeploy_UnusableBaseAtRequestTime(t *testing.T) {
	for _, tc := range []struct {
		name, body, code string
		status           int
	}{
		{"malformed", apiInvalidBase, "invalid_base_source_id", http.StatusBadRequest},
		{"unknown", apiBaseNotFound, "base_source_not_found", http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDeployFake(t)
			f.createCode, f.createBody = tc.status, tc.body
			dir := deployProject(t)
			writeRecord(t, dir, pulledAs(testAppID))
			_, errBuf, exec := newTestRoot(t, f.api)

			err := exec(deployArgs(dir)...)

			if got := exitCode(err); got != output.ExitValidation {
				t.Fatalf("exit = %d, want %d", got, output.ExitValidation)
			}
			env := decodeJSON(t, errBuf)
			msg, _ := env["error"].(string)
			if env["code"] != tc.code {
				t.Errorf("code = %v, want %s", env["code"], tc.code)
			}
			for _, want := range []string{baseSrcID, "sources pull", "--no-base", "Nothing was uploaded"} {
				if !strings.Contains(msg, want) {
					t.Errorf("message lacks %q: %s", want, msg)
				}
			}
			if f.s3Hits != 0 {
				t.Error("nothing may be uploaded")
			}
			if b := sourcebase.Read(dir); b == nil || b.SourceID != baseSrcID {
				t.Errorf("the record must be left alone, got %+v", b)
			}
		})
	}
}

func TestDeploy_RecordMovesToThePublishedRelease(t *testing.T) {
	f := newDeployFake(t)
	dir := deployProject(t)
	writeRecord(t, dir, pulledAs(testAppID))
	outBuf, errBuf, exec := newTestRoot(t, f.api)

	if err := exec(deployArgs(dir, "--wait")...); exitCode(err) != 0 {
		t.Fatalf("exit %d: %s", exitCode(err), errBuf)
	}

	b := sourcebase.Read(dir)
	if b == nil || b.AppID != testAppID || b.SourceID != deploySrcID || b.ReleaseTag != "src-3" || b.SHA256 != "stored-sha" || b.PulledAt.IsZero() {
		t.Fatalf("record = %+v", b)
	}
	if res := decodeJSON(t, outBuf); res["state_file"] != sourcebase.Path(dir) {
		t.Errorf("state_file = %v", res["state_file"])
	}
	if got := strings.Join(f.zipNames(), ","); strings.Contains(got, ".sureva/") {
		t.Errorf("the record must never be uploaded: %s", got)
	}
}

// The release is published and latest as soon as it is ready; a deployment that
// then fails does not change what the directory is.
func TestDeploy_RecordMovesEvenWhenTheDeploymentFails(t *testing.T) {
	f := newDeployFake(t)
	f.deployState = []string{"failed"}
	dir := deployProject(t)
	writeRecord(t, dir, pulledAs(testAppID))
	_, errBuf, exec := newTestRoot(t, f.api)

	err := exec(deployArgs(dir, "--wait")...)

	if exitCode(err) != output.ExitGeneral || decodeJSON(t, errBuf)["code"] != "deploy_failed" {
		t.Fatalf("exit %d, stderr %s", exitCode(err), errBuf)
	}
	if b := sourcebase.Read(dir); b == nil || b.SourceID != deploySrcID {
		t.Errorf("record = %+v, want the published release", b)
	}
}

func TestDeploy_RecordMovesWhenTheTriggerIsRefused(t *testing.T) {
	f := newDeployFake(t)
	f.deployCode, f.deployErrBody = 409, `{"error":"a deployment is already in progress for this app and environment"}`
	dir := deployProject(t)
	_, _, exec := newTestRoot(t, f.api)

	_ = exec(deployArgs(dir)...)

	if b := sourcebase.Read(dir); b == nil || b.SourceID != deploySrcID {
		t.Errorf("record = %+v, want the published release", b)
	}
}

func TestDeploy_RecordStaysWhenTheArchiveIsRejectedOrValidationTimesOut(t *testing.T) {
	for name, seq := range map[string][]string{"rejected": {"rejected"}, "still validating": {"validating"}} {
		t.Run(name, func(t *testing.T) {
			f := newDeployFake(t)
			f.sourceSeq = seq
			dir := deployProject(t)
			writeRecord(t, dir, pulledAs(testAppID))
			_, _, exec := newTestRoot(t, f.api)

			_ = exec("deploy", dir, "--app", testAppID, "--org", testOrgSlug, "--wait-interval", "1ms", "--wait-timeout", "30ms")

			if b := sourcebase.Read(dir); b == nil || b.SourceID != baseSrcID {
				t.Errorf("record = %+v, want the one it had", b)
			}
		})
	}
}

// A directory that was never pulled has no base for its first upload, but has
// one once it is published.
func TestDeploy_FirstDeployCreatesTheRecord(t *testing.T) {
	f := newDeployFake(t)
	dir := deployProject(t)
	_, errBuf, exec := newTestRoot(t, f.api)

	if err := exec(deployArgs(dir)...); exitCode(err) != 0 {
		t.Fatalf("exit %d: %s", exitCode(err), errBuf)
	}

	if f.createReqBody != "" {
		t.Errorf("the first upload has no base, got body %q", f.createReqBody)
	}
	if b := sourcebase.Read(dir); b == nil || b.SourceID != deploySrcID || b.AppID != testAppID {
		t.Errorf("record = %+v", b)
	}
}

// A record of another app is replaced: the directory now holds this app's
// latest release. The replacement is reported, so the pulled release of the
// other app is not lost without a word.
func TestDeploy_RecordOfAnotherAppIsReplacedAndReported(t *testing.T) {
	f := newDeployFake(t)
	dir := deployProject(t)
	writeRecord(t, dir, pulledAs("other-app"))
	outBuf, errBuf, exec := newTestRoot(t, f.api)

	if err := exec(deployArgs(dir)...); exitCode(err) != 0 {
		t.Fatalf("exit %d: %s", exitCode(err), errBuf)
	}

	if f.createReqBody != "" {
		t.Errorf("a record of another app is not a base, got body %q", f.createReqBody)
	}
	if b := sourcebase.Read(dir); b == nil || b.AppID != testAppID || b.SourceID != deploySrcID {
		t.Errorf("record = %+v", b)
	}
	res := decodeJSON(t, outBuf)
	replaced, _ := res["state_file_replaced"].(map[string]any)
	if replaced["app_id"] != "other-app" || replaced["source_id"] != baseSrcID || replaced["release_tag"] != "src-2" {
		t.Errorf("state_file_replaced = %v", res["state_file_replaced"])
	}
	if res["base_sent"] != false || res["state_file"] == nil {
		t.Errorf("output = %v", res)
	}
}

// Nothing is reported when no record of another app was replaced.
func TestDeploy_NoReplacementIsReportedForTheSameAppOrNoRecord(t *testing.T) {
	for name, setup := range map[string]func(t *testing.T, dir string){
		"no record": func(*testing.T, string) {},
		"same app":  func(t *testing.T, dir string) { writeRecord(t, dir, pulledAs(testAppID)) },
		"invalid source": func(t *testing.T, dir string) {
			writeRecord(t, dir, sourcebase.Base{AppID: testAppID, SourceID: "src-2-id"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newDeployFake(t)
			dir := deployProject(t)
			setup(t, dir)
			outBuf, errBuf, exec := newTestRoot(t, f.api)

			if err := exec(deployArgs(dir)...); exitCode(err) != 0 {
				t.Fatalf("exit %d: %s", exitCode(err), errBuf)
			}

			if res := decodeJSON(t, outBuf); res["state_file_replaced"] != nil {
				t.Errorf("state_file_replaced = %v", res["state_file_replaced"])
			}
		})
	}
}

// A record whose source id is not a UUID would only come back as
// invalid_base_source_id: it is not sent, the deploy goes ahead without a base
// and the output says why.
func TestDeploy_RecordWithoutAUUIDIsIgnoredAndReported(t *testing.T) {
	f := newDeployFake(t)
	dir := deployProject(t)
	writeRecord(t, dir, sourcebase.Base{AppID: testAppID, SourceID: "src-2-id", ReleaseTag: "src-2"})
	outBuf, errBuf, exec := newTestRoot(t, f.api)

	if err := exec(deployArgs(dir)...); exitCode(err) != 0 {
		t.Fatalf("exit %d: %s", exitCode(err), errBuf)
	}

	if f.createReqBody != "" {
		t.Errorf("an unusable record must not be sent, got body %q", f.createReqBody)
	}
	res := decodeJSON(t, outBuf)
	why, _ := res["base_record_ignored"].(string)
	if res["base_sent"] != false || res["base_source_id"] != nil || !strings.Contains(why, "not a UUID") {
		t.Errorf("output = %v", res)
	}
	if errBuf.Len() != 0 {
		t.Errorf("nothing is written to stderr, got %s", errBuf)
	}
	if b := sourcebase.Read(dir); b == nil || b.SourceID != deploySrcID {
		t.Errorf("the record moves to the published release, got %+v", b)
	}
}

// A usable record is not reported as ignored.
func TestDeploy_UsableRecordIsNotReportedAsIgnored(t *testing.T) {
	f := newDeployFake(t)
	dir := deployProject(t)
	writeRecord(t, dir, pulledAs(testAppID))
	outBuf, _, exec := newTestRoot(t, f.api)

	_ = exec(deployArgs(dir)...)

	if res := decodeJSON(t, outBuf); res["base_record_ignored"] != nil {
		t.Errorf("base_record_ignored = %v", res["base_record_ignored"])
	}
}

// After a validation timeout the record stays, so the next deploy may be
// refused as stale_base if the source becomes ready: the message says so and
// names the way out. A directory without a record has nothing to warn about.
func TestDeploy_ValidationTimeoutExplainsTheRecord(t *testing.T) {
	for _, tc := range []struct {
		name      string
		withBase  bool
		wantNote  bool
		extraArgs []string
	}{
		{"with a record", true, true, nil},
		{"with a record and --no-base", true, true, []string{"--no-base"}},
		{"without a record", false, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDeployFake(t)
			f.sourceSeq = []string{"validating"}
			dir := deployProject(t)
			if tc.withBase {
				writeRecord(t, dir, pulledAs(testAppID))
			}
			_, errBuf, exec := newTestRoot(t, f.api)

			_ = exec(append([]string{"deploy", dir, "--app", testAppID, "--org", testOrgSlug, "--wait-interval", "1ms", "--wait-timeout", "30ms"}, tc.extraArgs...)...)

			env := decodeJSON(t, errBuf)
			msg, _ := env["error"].(string)
			if env["code"] != "validation_timeout" {
				t.Fatalf("envelope = %v", env)
			}
			for _, want := range []string{"stale_base", "sources list", "sources pull", "--no-base", "left as it was"} {
				if got := strings.Contains(msg, want); got != tc.wantNote {
					t.Errorf("message contains %q = %v, want %v: %s", want, got, tc.wantNote, msg)
				}
			}
			if !strings.Contains(msg, "sources get") {
				t.Errorf("the original hint is gone: %s", msg)
			}
		})
	}
}

// The API's latest release is the ready one with the highest seq, which is not
// the first ready row of a list ordered by creation time.
func TestDeploy_StaleBaseNamesTheHighestSeqNotTheFirstRow(t *testing.T) {
	const list = `[` +
		`{"id":"src-6-id","app_id":"app-1","seq":6,"status":"rejected","release_tag":"src-6","created_at":"2026-10-06T10:00:00Z","updated_at":"2026-10-06T10:00:00Z"},` +
		`{"id":"src-3-id","app_id":"app-1","seq":3,"status":"ready","release_tag":"src-3","created_at":"2026-10-05T10:00:00Z","updated_at":"2026-10-07T10:00:00Z"},` +
		`{"id":"src-5-id","app_id":"app-1","seq":5,"status":"ready","release_tag":"src-5","created_at":"2026-10-04T10:00:00Z","updated_at":"2026-10-04T10:00:00Z"},` +
		`{"id":"src-4-id","app_id":"app-1","seq":4,"status":"ready","release_tag":"src-4","created_at":"2026-10-03T10:00:00Z","updated_at":"2026-10-03T10:00:00Z"}]`
	f := newDeployFake(t)
	f.sourceSeq = []string{"rejected"}
	f.rejectCode, f.rejectRetryable = "stale_base", boolPtr(false)
	f.listJSON = list
	dir := deployProject(t)
	writeRecord(t, dir, pulledAs(testAppID))
	_, errBuf, exec := newTestRoot(t, f.api)

	_ = exec(deployArgs(dir)...)

	env := decodeJSON(t, errBuf)
	details, _ := env["details"].(map[string]any)
	if details["latest_release_tag"] != "src-5" || details["latest_source_id"] != "src-5-id" || !strings.Contains(env["error"].(string), "src-5 exists") {
		t.Errorf("envelope = %v", env)
	}
}

func TestDeploy_SecondDeployFromTheSameDirectoryIsBasedOnTheFirst(t *testing.T) {
	f := newDeployFake(t)
	dir := deployProject(t)
	_, errBuf, exec := newTestRoot(t, f.api)

	if err := exec(deployArgs(dir)...); exitCode(err) != 0 {
		t.Fatalf("first deploy: exit %d: %s", exitCode(err), errBuf)
	}
	if f.createReqBody != "" {
		t.Fatalf("the first upload must carry no base, got %q", f.createReqBody)
	}
	errBuf.Reset()
	if err := exec(deployArgs(dir)...); exitCode(err) != 0 {
		t.Fatalf("second deploy: exit %d: %s", exitCode(err), errBuf)
	}

	if id, _ := sentBase(t, f); id != deploySrcID {
		t.Errorf("second upload base = %q, want the first deploy's source %s", id, deploySrcID)
	}
}

// The release is published; failing to note it in the directory must not turn
// that into a failed command.
func TestDeploy_RecordWriteFailureDoesNotFailTheCommand(t *testing.T) {
	f := newDeployFake(t)
	dir := deployProject(t)
	// A directory where the record should be: Read treats it as no record and
	// Write refuses to replace it.
	if err := os.MkdirAll(sourcebase.Path(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	outBuf, errBuf, exec := newTestRoot(t, f.api)

	err := exec(deployArgs(dir)...)

	if exitCode(err) != 0 || errBuf.Len() != 0 {
		t.Fatalf("exit %d, stderr %s", exitCode(err), errBuf)
	}
	res := decodeJSON(t, outBuf)
	msg, _ := res["state_file_error"].(string)
	if _, has := res["state_file"]; has || !strings.Contains(msg, "published") || !strings.Contains(msg, "--no-base") {
		t.Errorf("state_file/state_file_error = %v / %q", res["state_file"], msg)
	}
	if dep, _ := res["deployment"].(map[string]any); dep == nil {
		t.Error("the deployment must still have been triggered")
	}
}

func TestDeployHelpDocumentsTheBase(t *testing.T) {
	outBuf, _, exec := newTestRoot(t, "")
	if err := exec("deploy", "--help"); exitCode(err) != 0 {
		t.Fatal("help failed")
	}
	for _, want := range []string{"--no-base", "stale_base", "base_sent", "invalid_base_source_id", "base_source_not_found", "state_file", "state_file_replaced", "base_record_ignored", "validation timeout"} {
		if !strings.Contains(outBuf.String(), want) {
			t.Errorf("deploy --help is missing %q", want)
		}
	}
}
