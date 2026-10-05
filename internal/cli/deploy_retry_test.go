package cli_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sureva-ch/sureva-cli/internal/cli"
	"github.com/sureva-ch/sureva-cli/internal/output"
)

// Bodies of the refusals cloud-api writes for POST .../complete
// (handlers/app_source_validation.go retryRefusal and refuseCompletion, flat
// fields next to error and code).
const (
	apiRetryTooSoon20 = `{"code":"source_retry_too_soon","error":"this source was decided too recently to be retried; try again in 20 seconds","source_status":"rejected","retry_after_seconds":20}`
	apiRetryTooSoon30 = `{"code":"source_retry_too_soon","error":"this source was decided too recently to be retried; try again in 30 seconds","source_status":"rejected","retry_after_seconds":30}`
	apiRetryLimit     = `{"code":"source_retry_limit_reached","error":"this source has used all 3 of its validation attempts and cannot be retried; request a new upload","source_status":"rejected","attempts":3,"max_attempts":3}`
)

// recordPauses replaces the pause between retries with one that returns at once
// and notes how long it was asked to wait.
func recordPauses(t *testing.T) func() []time.Duration {
	t.Helper()
	var (
		mu     sync.Mutex
		pauses []time.Duration
	)
	t.Cleanup(cli.SetPauseForTest(func(_ context.Context, d time.Duration) error {
		mu.Lock()
		defer mu.Unlock()
		pauses = append(pauses, d)
		return nil
	}))
	return func() []time.Duration {
		mu.Lock()
		defer mu.Unlock()
		return append([]time.Duration(nil), pauses...)
	}
}

// platformRow makes every rejected row look as the API with attempt limits
// writes it: counters, and retryable true only while attempts remain.
func platformRow(code string, attempts int, retryable bool) func(map[string]any, string) {
	return func(row map[string]any, status string) {
		row["attempts"], row["max_attempts"] = attempts, 3
		if status == "rejected" {
			row["validation_code"], row["retryable"] = code, retryable
			row["validation_error"] = "the archive passed validation but could not be stored."
		}
	}
}

func TestDeploy_TooSoonIsWaitedOutAndNotCountedAsARetry(t *testing.T) {
	pauses := recordPauses(t)
	f := newDeployFake(t)
	f.sourceSeq = []string{"validating", "rejected", "validating", "ready"}
	f.rowHook = platformRow("promote_failed", 1, true)
	f.completeScript = []fakeReply{{http.StatusConflict, apiRetryTooSoon20}} // then 202
	outBuf, errBuf, exec := newTestRoot(t, f.api)

	err := exec(deployArgs(deployProject(t), "--wait-timeout", "10m")...)

	if err != nil {
		t.Fatalf("exit %d; stderr: %s", exitCode(err), errBuf)
	}
	if f.completed != 3 {
		t.Errorf("complete called %d times, want 3 (first, refused retry, accepted retry)", f.completed)
	}
	// 2x the interval before the first retry, then what the API asked for plus a
	// one second margin.
	want := []time.Duration{2 * time.Millisecond, 21 * time.Second}
	if got := pauses(); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("pauses = %v, want %v", got, want)
	}
	if res := decodeJSON(t, outBuf); res["validation_retries"] != float64(1) {
		t.Errorf("validation_retries = %v, want 1: a refused retry is not a retry", res["validation_retries"])
	}
	if f.deployBody == nil {
		t.Error("the deployment must follow once validation succeeds")
	}
}

func TestDeploy_TooSoonWaitIsAtLeastOneSecond(t *testing.T) {
	pauses := recordPauses(t)
	f := newDeployFake(t)
	f.sourceSeq = []string{"rejected", "ready"}
	f.rowHook = platformRow("promote_failed", 1, true)
	f.completeScript = []fakeReply{{http.StatusConflict, `{"code":"source_retry_too_soon","error":"soon","retry_after_seconds":0}`}}
	_, errBuf, exec := newTestRoot(t, f.api)

	if err := exec(deployArgs(deployProject(t), "--wait-timeout", "10m")...); err != nil {
		t.Fatalf("exit %d; stderr: %s", exitCode(err), errBuf)
	}
	if got := pauses(); len(got) != 2 || got[1] != time.Second {
		t.Errorf("pauses = %v, want the second to be 1s", got)
	}
}

// When the cooldown would end after --wait-timeout the command stops at once
// with validation_timeout and says when a retry would have been possible.
func TestDeploy_TooSoonBeyondTheWaitTimeoutIsValidationTimeout(t *testing.T) {
	pauses := recordPauses(t)
	f := newDeployFake(t)
	f.sourceSeq = []string{"rejected"}
	f.rowHook = platformRow("promote_failed", 1, true)
	f.completeScript = []fakeReply{{http.StatusConflict, apiRetryTooSoon30}}
	_, errBuf, exec := newTestRoot(t, f.api)

	err := exec(deployArgs(deployProject(t))...) // --wait-timeout 2s

	if got := exitCode(err); got != output.ExitGeneral {
		t.Fatalf("exit = %d, want %d", got, output.ExitGeneral)
	}
	env := decodeJSON(t, errBuf)
	msg, _ := env["error"].(string)
	if env["code"] != "validation_timeout" || !strings.Contains(msg, "30 seconds") || !strings.Contains(msg, "(at ") {
		t.Errorf("envelope = %v", env)
	}
	if f.completed != 2 {
		t.Errorf("complete called %d times, want 2", f.completed)
	}
	if got := pauses(); len(got) != 1 {
		t.Errorf("pauses = %v: the 31s wait must not be started", got)
	}
}

func TestDeploy_RetryLimitReachedIsValidationUnavailable(t *testing.T) {
	recordPauses(t)
	f := newDeployFake(t)
	f.sourceSeq = []string{"rejected"}
	f.rowHook = platformRow("promote_failed", 2, true) // the row read before the refusal
	f.completeScript = []fakeReply{{http.StatusConflict, apiRetryLimit}}
	outBuf, errBuf, exec := newTestRoot(t, f.api)

	err := exec(deployArgs(deployProject(t))...)

	if got := exitCode(err); got != output.ExitGeneral {
		t.Fatalf("exit = %d, want %d", got, output.ExitGeneral)
	}
	env := decodeJSON(t, errBuf)
	details, _ := env["details"].(map[string]any)
	msg, _ := env["error"].(string)
	if env["code"] != "validation_unavailable" || details["api_code"] != "source_retry_limit_reached" ||
		details["attempts"] != float64(3) || details["max_attempts"] != float64(3) || details["retryable"] != false ||
		details["validation_code"] != "promote_failed" {
		t.Errorf("envelope = %v", env)
	}
	if !strings.Contains(msg, "after 3 attempts") || !strings.Contains(msg, "fresh budget") || strings.Contains(msg, "fix the archive") {
		t.Errorf("message = %q", msg)
	}
	if res := decodeJSON(t, outBuf); res["source"] == nil {
		t.Errorf("stdout = %v", res)
	}
	if f.deployBody != nil {
		t.Error("nothing may be deployed")
	}
}

// The API's own count is what the row says: the third attempt was rejected and
// retryable is false because none are left, not because of the archive.
func TestDeploy_AttemptsExhaustedOnAPlatformCauseIsValidationUnavailable(t *testing.T) {
	recordPauses(t)
	f := newDeployFake(t)
	f.sourceSeq = []string{"rejected"}
	f.rowHook = platformRow("promote_failed", 3, false)
	outBuf, errBuf, exec := newTestRoot(t, f.api)

	err := exec(deployArgs(deployProject(t))...)

	if got := exitCode(err); got != output.ExitGeneral {
		t.Fatalf("exit = %d, want %d", got, output.ExitGeneral)
	}
	env := decodeJSON(t, errBuf)
	details, _ := env["details"].(map[string]any)
	if env["code"] != "validation_unavailable" || details["retryable"] != false || details["attempts"] != float64(3) ||
		details["max_attempts"] != float64(3) || details["validation_code"] != "promote_failed" {
		t.Errorf("envelope = %v", env)
	}
	if f.completed != 1 {
		t.Errorf("complete called %d times: a row that is not retryable must not be retried", f.completed)
	}
	src, _ := decodeJSON(t, outBuf)["source"].(map[string]any)
	if src["attempts"] != float64(3) || src["max_attempts"] != float64(3) {
		t.Errorf("source = %v", src)
	}
}

// With every attempt used, a rejection about the archive (or a code this CLI
// does not know) is still a rejection of the archive.
func TestDeploy_AttemptsExhaustedOnTheArchiveStaysSourceRejected(t *testing.T) {
	for _, code := range []string{"credentials_found", "archive_too_large", "a_code_from_the_future"} {
		t.Run(code, func(t *testing.T) {
			f := newDeployFake(t)
			f.sourceSeq = []string{"rejected"}
			f.rowHook = platformRow(code, 3, false)
			_, errBuf, exec := newTestRoot(t, f.api)

			err := exec(deployArgs(deployProject(t))...)

			if got := exitCode(err); got != output.ExitValidation {
				t.Fatalf("exit = %d, want %d", got, output.ExitValidation)
			}
			if env := decodeJSON(t, errBuf); env["code"] != "source_rejected" {
				t.Errorf("envelope = %v", env)
			}
		})
	}
}

// The full timeline against an API with the limits: two refused-then-accepted
// retries and a third rejection that leaves no attempts.
func TestDeploy_TimelineAgainstTheLimitedAPI(t *testing.T) {
	pauses := recordPauses(t)
	f := newDeployFake(t)
	// Reads: poll (validating, rejected), after retry 1 (validating, rejected),
	// after retry 2 (validating, rejected for good).
	f.sourceSeq = []string{"validating", "rejected", "validating", "rejected", "validating", "rejected"}
	f.rowHook = func(row map[string]any, status string) {
		f.mu.Lock()
		reads := f.sourceReads
		f.mu.Unlock()
		attempts := 1 + (reads-1)/2
		platformRow("promote_failed", attempts, attempts < 3)(row, status)
	}
	f.completeScript = []fakeReply{
		{http.StatusConflict, apiRetryTooSoon20}, {http.StatusAccepted, `{"source_id":"` + deploySrcID + `","status":"validating"}`},
		{http.StatusConflict, `{"code":"source_retry_too_soon","error":"soon","source_status":"rejected","retry_after_seconds":10}`},
	}
	outBuf, errBuf, exec := newTestRoot(t, f.api)

	err := exec(deployArgs(deployProject(t), "--wait-timeout", "10m")...)

	if got := exitCode(err); got != output.ExitGeneral {
		t.Fatalf("exit = %d; stderr %s", got, errBuf)
	}
	if env := decodeJSON(t, errBuf); env["code"] != "validation_unavailable" {
		t.Errorf("envelope = %v", env)
	}
	want := []time.Duration{2 * time.Millisecond, 21 * time.Second, 4 * time.Millisecond, 11 * time.Second}
	got := pauses()
	if len(got) != len(want) {
		t.Fatalf("pauses = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("pauses = %v, want %v", got, want)
			break
		}
	}
	if f.completed != 5 {
		t.Errorf("complete called %d times, want 5 (first, 2 accepted retries, 2 refused)", f.completed)
	}
	if res := decodeJSON(t, outBuf); res["validation_retries"] != float64(2) {
		t.Errorf("validation_retries = %v, want 2", res["validation_retries"])
	}
}

// An API without the new fields keeps the fixed budget: three repeats paced
// 2x, 4x and 8x the interval, and no counters in the output.
func TestDeploy_APIWithoutAttemptFieldsKeepsTheFixedBudget(t *testing.T) {
	pauses := recordPauses(t)
	f := newDeployFake(t)
	f.sourceSeq = []string{"rejected"}
	f.rejectCode, f.rejectRetryable = "promote_failed", boolPtr(true)
	outBuf, errBuf, exec := newTestRoot(t, f.api)

	err := exec(deployArgs(deployProject(t))...)

	if got := exitCode(err); got != output.ExitGeneral {
		t.Fatalf("exit = %d", got)
	}
	if f.completed != 4 {
		t.Errorf("complete called %d times, want the first plus 3 repeats", f.completed)
	}
	want := []time.Duration{2 * time.Millisecond, 4 * time.Millisecond, 8 * time.Millisecond}
	got := pauses()
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("pauses = %v, want %v", got, want)
	}
	env := decodeJSON(t, errBuf)
	details, _ := env["details"].(map[string]any)
	msg, _ := env["error"].(string)
	if env["code"] != "validation_unavailable" || details["retryable"] != true || !strings.Contains(msg, "after 4 attempts") {
		t.Errorf("envelope = %v", env)
	}
	for _, k := range []string{"attempts", "max_attempts"} {
		if _, ok := details[k]; ok {
			t.Errorf("details.%s present although the API sent none: %v", k, details)
		}
	}
	if src, _ := decodeJSON(t, outBuf)["source"].(map[string]any); src["attempts"] != nil || src["max_attempts"] != nil {
		t.Errorf("source = %v", src)
	}
}

// A 409 the CLI does not know is reported as it is, not retried or waited for.
func TestDeploy_OtherConflictOnRetryAborts(t *testing.T) {
	pauses := recordPauses(t)
	f := newDeployFake(t)
	f.sourceSeq = []string{"rejected"}
	f.rowHook = platformRow("promote_failed", 1, true)
	f.completeScript = []fakeReply{{http.StatusConflict, `{"code":"something_else","error":"no"}`}}
	_, errBuf, exec := newTestRoot(t, f.api)

	err := exec(deployArgs(deployProject(t))...)

	if got := exitCode(err); got != output.ExitGeneral {
		t.Fatalf("exit = %d", got)
	}
	if env := decodeJSON(t, errBuf); env["code"] != "api_error" {
		t.Errorf("envelope = %v", env)
	}
	if len(pauses()) != 1 {
		t.Errorf("pauses = %v, want only the pause before the retry", pauses())
	}
}

// An API that keeps answering too soon is not waited for forever.
func TestDeploy_TooSoonRepeatedIsReportedAsIs(t *testing.T) {
	recordPauses(t)
	f := newDeployFake(t)
	f.sourceSeq = []string{"rejected"}
	f.rowHook = platformRow("promote_failed", 1, true)
	for range 10 {
		f.completeScript = append(f.completeScript, fakeReply{http.StatusConflict, apiRetryTooSoon20})
	}
	_, errBuf, exec := newTestRoot(t, f.api)

	err := exec(deployArgs(deployProject(t), "--wait-timeout", "10m")...)

	if got := exitCode(err); got != output.ExitGeneral {
		t.Fatalf("exit = %d", got)
	}
	env := decodeJSON(t, errBuf)
	details, _ := env["details"].(map[string]any)
	if env["code"] != "source_retry_too_soon" || details["retry_after_seconds"] != float64(20) {
		t.Errorf("envelope = %v", env)
	}
}

// Interrupting while waiting out the cooldown ends with "interrupted". The test
// cancels the context the way the signal handler does; it sends no signal.
func TestDeploy_InterruptedDuringTheTooSoonWait(t *testing.T) {
	inWait := make(chan struct{})
	var once sync.Once
	t.Cleanup(cli.SetPauseForTest(func(ctx context.Context, d time.Duration) error {
		if d < time.Second {
			return nil // the pause before the retry
		}
		once.Do(func() { close(inWait) })
		<-ctx.Done()
		return ctx.Err()
	}))
	f := newDeployFake(t)
	f.sourceSeq = []string{"rejected"}
	f.rowHook = platformRow("promote_failed", 1, true)
	f.completeScript = []fakeReply{{http.StatusConflict, apiRetryTooSoon20}}
	outBuf, errBuf, _ := newTestRoot(t, f.api)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-inWait
		cancel()
	}()
	root := cli.NewRootCmd()
	root.SetOut(outBuf)
	root.SetErr(errBuf)
	root.SetArgs(deployArgs(deployProject(t), "--wait-timeout", "10m"))
	err := root.ExecuteContext(ctx)

	if got := exitCode(err); got != output.ExitGeneral {
		t.Fatalf("exit = %d; stderr: %s", got, errBuf)
	}
	var env map[string]any
	if jerr := json.Unmarshal(errBuf.Bytes(), &env); jerr != nil || env["code"] != "interrupted" {
		t.Errorf("envelope = %v (%v)\n%s", env, jerr, errBuf)
	}
	if f.deployBody != nil {
		t.Error("nothing may be deployed after an interruption")
	}
}

// Interrupting during the pause before the retry ends the same way.
func TestDeploy_InterruptedDuringTheRetryPause(t *testing.T) {
	inPause := make(chan struct{})
	t.Cleanup(cli.SetPauseForTest(func(ctx context.Context, _ time.Duration) error {
		close(inPause)
		<-ctx.Done()
		return ctx.Err()
	}))
	f := newDeployFake(t)
	f.sourceSeq = []string{"rejected"}
	f.rowHook = platformRow("promote_failed", 1, true)
	outBuf, errBuf, _ := newTestRoot(t, f.api)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-inPause
		cancel()
	}()
	root := cli.NewRootCmd()
	root.SetOut(outBuf)
	root.SetErr(errBuf)
	root.SetArgs(deployArgs(deployProject(t), "--wait-timeout", "10m"))
	err := root.ExecuteContext(ctx)

	if got := exitCode(err); got != output.ExitGeneral {
		t.Fatalf("exit = %d; stderr: %s", got, errBuf)
	}
	var env map[string]any
	if jerr := json.Unmarshal(errBuf.Bytes(), &env); jerr != nil || env["code"] != "interrupted" {
		t.Errorf("envelope = %v (%v)\n%s", env, jerr, errBuf)
	}
	if f.completed != 1 {
		t.Errorf("complete called %d times, want only the first", f.completed)
	}
}

func TestDeploy_RejectsANonPositiveWaitInterval(t *testing.T) {
	for _, v := range []string{"0", "0s", "-5s"} {
		t.Run(v, func(t *testing.T) {
			f := newDeployFake(t)
			_, errBuf, exec := newTestRoot(t, f.api)

			err := exec("deploy", deployProject(t), "--app", testAppID, "--org", testOrgSlug, "--wait-interval", v)

			if got := exitCode(err); got != output.ExitValidation {
				t.Fatalf("exit = %d, want %d; stderr: %s", got, output.ExitValidation, errBuf)
			}
			if env := decodeJSON(t, errBuf); env["code"] != "validation_error" || !strings.Contains(env["error"].(string), "--wait-interval") {
				t.Errorf("envelope = %v", env)
			}
			if f.completed != 0 {
				t.Error("nothing may be uploaded")
			}
		})
	}
}
