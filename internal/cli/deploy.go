package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/sureva-ch/sureva-cli/internal/client"
	"github.com/sureva-ch/sureva-cli/internal/credentials"
	"github.com/sureva-ch/sureva-cli/internal/output"
	"github.com/sureva-ch/sureva-cli/internal/pack"
	"github.com/sureva-ch/sureva-cli/internal/sourcebase"
)

// largestEntriesShown is how many archive entries a size refusal names.
const largestEntriesShown = 5

// deployResult is the JSON `sureva deploy` prints. It is built up as the steps
// complete and printed on stdout even when a later step fails, so a caller sees
// how far the deploy got (the uploaded source, the failed deployment, where its
// logs are) next to the error envelope on stderr.
type deployResult struct {
	AppID string `json:"app_id"`
	// BaseSourceID is the release the directory is based on, read from
	// .sureva/source.json when it belongs to this app. BaseSent says whether it
	// was sent to the API as the upload's base (it is not with --no-base).
	BaseSourceID string `json:"base_source_id,omitempty"`
	BaseSent     bool   `json:"base_sent"`
	// BaseRecordIgnored says why .sureva/source.json, which names this app, was
	// not used as the base: its source id is not a UUID, so no base was sent.
	BaseRecordIgnored string             `json:"base_record_ignored,omitempty"`
	Archive           *archiveSummary    `json:"archive,omitempty"`
	Source            *client.AppSource  `json:"source,omitempty"`
	Deployment        *client.Deployment `json:"deployment,omitempty"`
	// ValidationRetries counts how often 'complete' was repeated because
	// validation was refused for a reason on the platform's side.
	ValidationRetries int `json:"validation_retries,omitempty"`
	// StateFile is .sureva/source.json after it was moved to the release just
	// published; StateFileError says why it could not be (the release is still
	// published, but the next deploy from this directory will not be based on it).
	StateFile      string `json:"state_file,omitempty"`
	StateFileError string `json:"state_file_error,omitempty"`
	// StateFileReplaced is the record of ANOTHER app that StateFile replaced.
	// The directory is this app's latest release now; the release it was pulled
	// from is no longer recorded here.
	StateFileReplaced *replacedRecord `json:"state_file_replaced,omitempty"`
	// Logs says how to fetch the logs of a deployment that failed.
	Logs *logsHint `json:"logs,omitempty"`
}

// replacedRecord is the .sureva/source.json of another app that a deploy
// replaced.
type replacedRecord struct {
	AppID      string `json:"app_id"`
	SourceID   string `json:"source_id"`
	ReleaseTag string `json:"release_tag,omitempty"`
}

type archiveSummary struct {
	SizeBytes int64 `json:"size_bytes"`
	Files     int   `json:"files"`
	// MaxBytes is the limit the API reported for this app; zero until asked.
	MaxBytes int64 `json:"max_bytes,omitempty"`
	// Packing is "git" when the file list came from git, "walk" otherwise.
	Packing  string        `json:"packing"`
	Excluded pack.Excluded `json:"excluded"`
	Largest  []pack.Entry  `json:"largest"`
}

type logsHint struct {
	Command       string `json:"command"`
	AppID         string `json:"app_id"`
	OrgID         string `json:"org_id"`
	EnvironmentID string `json:"environment_id,omitempty"`
	DeploymentID  string `json:"deployment_id"`
}

// NewDeployCmd returns the `deploy [dir]` command.
func NewDeployCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy [dir]",
		Short: "Upload a local directory to an upload-backed app and deploy it",
		Long: `Pack a directory into a zip, upload it to an upload-backed app, wait for the
platform to validate it and deploy the resulting release. Running this command
is the explicit deploy action: one invocation chains all the steps.

AGENT USAGE
  sureva deploy --app <app-id> --org <slug>
  sureva deploy ./site --app <app-id> --org <slug> --wait
  SUREVA_TOKEN=sapi_... sureva deploy --app <app-id> --org <slug> --wait

VALIDATION / INPUTS
  [dir]: directory to deploy; defaults to the current directory.
  --app: required; application ID of an upload-backed app.
  --org: required organization slug unless a default org is configured.
  --env-id: environment UUID; defaults to the production environment.
  --wait: wait for the deployment to reach a terminal state.
  --no-base: do not send the recorded base release (see BASE RELEASE); upload
         without a base and overwrite the latest release on purpose.
  --wait-interval/--wait-timeout: Go duration strings; the interval must be
         positive. The interval also paces the wait for archive validation,
         which always happens; the timeout
         bounds each of the two waits separately. Validation retries (see
         below) and their pauses count against the validation timeout.

BASE RELEASE
  A directory filled by 'sources pull' records its release in
  .sureva/source.json. When that record belongs to --app, deploy sends its
  source id as base_source_id with the upload request, and the platform refuses
  to publish the archive if that release is no longer the app's latest ready one
  (so two agents that pulled the same release cannot overwrite each other). No
  record, a damaged one, another app's or one whose source_id is not a UUID:
  nothing is sent (output base_record_ignored says why for the last). --no-base
  sends nothing either, on purpose.
  Once the new release is ready, deploy rewrites .sureva/source.json to it (its
  source id, release tag, the sha256 of the stored archive and the time), also in
  a directory that was never pulled and also when the deployment then fails or
  times out, because the directory is that release from then on. A record that
  cannot be written does not fail the command: state_file_error says so and the
  next deploy from this directory is not based on this release. A rejection,
  an expired source or a validation timeout leaves the record as it was; after
  a validation timeout that matters: if the source becomes ready later, the
  next deploy from this directory is rejected as stale_base (the way out is
  'sources list', then pull again or --no-base; the timeout message says so).
  A record of ANOTHER app is replaced too: pulled from app A and deployed with
  --app B, the directory is B's latest release afterwards, and the output
  carries state_file_replaced{app_id,source_id,release_tag} with the record that
  was replaced, so the pulled release of A is no longer recorded here.
  Loop for an agent: sources pull -> edit -> deploy; on stale_base pull the latest
  into a separate directory, reapply the change, deploy from there.

VALIDATION RETRIES
  When validation refuses an archive for a reason on the platform's side (the
  API marks the source retryable: storage, dispatch or a worker that did not
  finish), the archive was never judged. The command then repeats 'complete',
  pausing 2x, 4x and 8x --wait-interval (at most 1m), and fails with
  validation_unavailable only when that did not help. A refusal about the
  archive itself (retryable false) fails at once with source_rejected.
  Against an API that does not report validation attempts the repeats stop
  after 3. An API that limits them (each source gets 3 validation attempts, the
  first 'complete' included, and a retry is accepted only some seconds after
  the rejection) is followed instead: it reports attempts and max_attempts on
  the source, and 'retryable' is true only while attempts remain. A retry sent
  too early is refused with 409 source_retry_too_soon and retry_after_seconds;
  the command waits that long (plus 1s) and sends 'complete' again, which does
  not count as a repeat. If the wait does not fit in --wait-timeout it stops
  with validation_timeout and says when a retry would have been possible. When
  every attempt is used (409 source_retry_limit_reached, or a rejected source
  with attempts >= max_attempts and a platform-side validation_code) it stops
  at once with validation_unavailable: the platform failed, not the archive,
  and uploading again starts a fresh set of attempts.

WHAT IS PACKED
  Always left out, at any depth: node_modules/, .git/, .sureva/ and .env*. Also left out:
  what .gitignore ignores (inside a git work tree the file list comes from git,
  so its rules apply exactly), and what a .surevaignore file in [dir] lists (same
  syntax as .gitignore). Symlinks are skipped, never followed. Paths are stored
  relative to [dir]. The JSON output lists what was excluded and the archive
  size; the zip is built in a temporary file and removed afterwards.

OUTPUT (stdout JSON)
  app_id, base_source_id, base_sent, archive{size_bytes,files,max_bytes,packing,excluded,largest}, source
  (the release; see 'sources get', including validation_code and retryable when
  it was rejected), validation_retries (only when 'complete' was repeated),
  deployment and, when the deployment failed,
  logs{command,...} naming the command that fetches its logs. base_source_id is
  present only when .sureva/source.json names this app: it is the release the tree
  was based on; base_sent says whether it went to the API (false with --no-base).
  state_file is that record after it moved to the release just published, or
  state_file_error when it could not be written; state_file_replaced is the
  record of another app that it replaced, and base_record_ignored says why a
  record naming this app was not used as the base. On a failure after
  the upload the same JSON is printed with what completed so far.

ERRORS (stderr envelope "code"; exit code in parentheses)
  auth_error            (2) missing or expired credentials.
  validation_error      (4) bad arguments or directory.
  github_backed_app     (4) the app deploys from GitHub; use 'deploys trigger'.
  empty_archive         (4) nothing left to pack after the exclusions.
  archive_too_large     (4) over the API's limit; nothing was uploaded.
  source_rejected       (4) validation refused the archive; fix the archive.
                        The reason is in the message and in
                        source.validation_error; details.validation_code is
                        the stable cause and details.retryable is false.
  validation_unavailable (1) validation could not run (a platform-side cause)
                        and the retries did not help or the attempts are used
                        up; run the command again (a new upload starts fresh
                        attempts). details.validation_code names the cause;
                        details.retryable is true when the CLI stopped repeating
                        and false when the API has no attempts left, with
                        details.attempts and details.max_attempts when it said.
  app_source_upload_limit_exceeded (4) the app reached its daily upload limit.
  stale_base            (1) the release this directory was based on is no longer
                        the latest, so nothing was published. Not a broken
                        archive: pull the latest into a separate directory,
                        reapply the change and deploy again, or pass --no-base to
                        overwrite on purpose. details.latest_release_tag names it.
  invalid_base_source_id (4) the recorded base is not a source id (request
  base_source_not_found (4)  time) or names no source of this app; the record is
                        unusable. Nothing was uploaded. Run 'sources pull' into a
                        new directory or pass --no-base. The record is not deleted.
  upload_failed         (1) the storage endpoint refused the archive.
  upload_expired        (1) the upload form expired; run the command again.
  validation_timeout    (1) validation did not finish within --wait-timeout (retries
                        and pauses included); check 'sources get'.
  source_expired        (1) the release is no longer stored.
  source_not_ready      (1) the release is not deployable; details.source_status
                        and details.validation_code say why.
  source_not_completable (1) the upload cannot be completed (it is not pending).
  no_ready_source       (3) the deployment found no ready release.
  wait_timeout          (1) the deployment did not finish in time.
  deploy_failed         (1) the deployment failed or was cancelled.
  pack_failed           (1) the directory could not be read or zipped.
  interrupted           (1) stopped by SIGINT or SIGTERM; the temporary archive
                        was removed. Anything uploaded before that is reported
                        in stdout and stays on the platform.
  network_error         (5) no HTTP response.

  The classification follows the API's own error code; the envelope also
  carries it as details.api_code.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runDeploy,
	}
	cmd.Flags().String("app", "", "Application ID of an upload-backed app (required)")
	cmd.Flags().String("env-id", "", "Environment UUID; defaults to the production environment when empty")
	cmd.Flags().Bool("wait", false, "Wait for the deployment to reach a terminal state before returning")
	cmd.Flags().Bool("no-base", false, "Do not send the release recorded in .sureva/source.json as the upload's base: overwrite the latest release on purpose")
	cmd.Flags().Duration("wait-interval", 5*time.Second, "Polling interval as a Go duration, for validation and --wait (e.g. 5s)")
	cmd.Flags().Duration("wait-timeout", 15*time.Minute, "Maximum wait as a Go duration for validation and, separately, for the deployment (e.g. 15m)")
	return cmd
}

func runDeploy(cmd *cobra.Command, args []string) error {
	appID, _ := cmd.Flags().GetString("app")
	envID, _ := cmd.Flags().GetString("env-id")
	wait, _ := cmd.Flags().GetBool("wait")
	noBase, _ := cmd.Flags().GetBool("no-base")
	waitInterval, _ := cmd.Flags().GetDuration("wait-interval")
	waitTimeout, _ := cmd.Flags().GetDuration("wait-timeout")

	// SIGINT and SIGTERM cancel ctx instead of killing the process, so the
	// deferred cleanup of the temporary archive runs and the in-flight request
	// and polling stop. Scoped to this command: others keep the default.
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	r := output.NewRenderer(OutputFormat(cmd), cmd.OutOrStdout(), cmd.ErrOrStderr())
	var res *deployResult
	fail := func(msg, code string, exit int) error {
		_ = r.RenderError(msg, code, -1)
		return &ExitError{Code: exit}
	}
	failDetails := func(msg, code string, exit int, details map[string]any) error {
		_ = r.RenderErrorDetails(msg, code, -1, details)
		return &ExitError{Code: exit}
	}

	if appID == "" {
		return fail("--app is required: the application ID of an upload-backed app (see 'apps list')", "validation_error", output.ExitValidation)
	}
	if waitInterval <= 0 {
		return fail(waitIntervalMessage, "validation_error", output.ExitValidation)
	}
	dir := "."
	if len(args) == 1 {
		dir = args[0]
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return fail(fmt.Sprintf("%q is not a directory", dir), "validation_error", output.ExitValidation)
	}

	c, r, err := newAuthenticatedClient(cmd)
	if err != nil {
		return err
	}

	// interrupted reports that the signal context was cancelled, with what
	// completed so far on stdout like any other failure after the upload.
	interrupted := func() error {
		if res != nil {
			_ = r.Render(res)
		}
		return fail("interrupted before the deploy finished; the temporary archive was removed", "interrupted", output.ExitGeneral)
	}

	orgID, err := requireOrgID(ctx, cmd, c, r)
	if err != nil {
		return err
	}

	app, err := c.GetApp(ctx, orgID, appID)
	if err != nil {
		if ctx.Err() != nil {
			return interrupted()
		}
		return handleAPIError(r, err)
	}
	// An app without source_type predates the field: let the API decide.
	if app.SourceType == "github" {
		return fail("this app deploys from its linked GitHub repository; use 'deploys trigger' instead of 'deploy'", "github_backed_app", output.ExitValidation)
	}

	archive, err := pack.Pack(ctx, dir)
	if err != nil {
		if ctx.Err() != nil {
			return interrupted()
		}
		if errors.Is(err, pack.ErrEmpty) {
			return fail("nothing to deploy: no files are left in "+dir+" after node_modules, .git, .env* and the ignore files are applied", "empty_archive", output.ExitValidation)
		}
		return fail("could not pack "+dir+": "+err.Error(), "pack_failed", output.ExitGeneral)
	}
	defer archive.Remove()

	// The release the directory is based on is sent with the upload request so
	// the platform can refuse the upload when that release is no longer the
	// latest. --no-base sends nothing: an explicit overwrite.
	base, baseIgnored := pulledBase(dir, appID)
	baseID := ""
	if base != nil {
		baseID = base.SourceID
	}
	sendBase := baseID
	if noBase {
		sendBase = ""
	}
	res = &deployResult{AppID: appID, BaseSourceID: baseID, BaseSent: sendBase != "", BaseRecordIgnored: baseIgnored, Archive: &archiveSummary{
		SizeBytes: archive.Size,
		Files:     archive.Files,
		Packing:   archive.Mode,
		Excluded:  archive.Excluded,
		Largest:   archive.Largest(largestEntriesShown),
	}}
	// failWith prints what completed so far on stdout, then the envelope.
	failWith := func(msg, code string, exit int) error {
		_ = r.Render(res)
		return fail(msg, code, exit)
	}
	apiFail := func(err error) error {
		if ctx.Err() != nil {
			return interrupted()
		}
		_ = r.Render(res)
		return handleAPIError(r, err)
	}

	upload, err := c.CreateSourceUpload(ctx, orgID, appID, sendBase)
	if err != nil {
		var apiErr *client.APIError
		// The create endpoint answers a GitHub-backed app with a plain 409 and
		// no `code` (only the download endpoint sends app_not_upload_backed), so
		// this one is still recognised by its message. REMOVABLE once the API
		// gives that 409 a code.
		if errors.As(err, &apiErr) && apiErr.ServerCode == "" && apiErr.HTTPStatus == http.StatusConflict && strings.Contains(apiErr.Message, "linked GitHub repository") {
			return fail("this app deploys from its linked GitHub repository; use 'deploys trigger' instead of 'deploy'", codeGitHubBacked, output.ExitValidation)
		}
		if ctx.Err() != nil {
			return interrupted()
		}
		if apiErr != nil && sendBase != "" && (apiErr.ServerCode == apiCodeInvalidBase || apiErr.ServerCode == apiCodeBaseNotFound) {
			return failDetails(unusableBaseMessage(base, apiErr.Message), apiErr.ServerCode, output.ExitValidation, apiErrorDetails(apiErr))
		}
		return handleAPIError(r, classifySourceError(err))
	}
	res.Archive.MaxBytes = upload.MaxBytes

	if archive.Size > upload.MaxBytes {
		return failWith(tooLargeMessage(archive, upload.MaxBytes), "archive_too_large", output.ExitValidation)
	}

	if err := c.UploadSourceArchive(ctx, upload, archive.Path); err != nil {
		var upErr *client.UploadError
		if errors.As(err, &upErr) {
			switch {
			case upErr.S3Code == "EntityTooLarge":
				return failWith(tooLargeMessage(archive, upload.MaxBytes), "archive_too_large", output.ExitValidation)
			case upErr.S3Code == "ExpiredToken" || strings.Contains(upErr.Message, "expired"):
				return failWith("the upload form expired before the archive was sent; run the command again", "upload_expired", output.ExitGeneral)
			}
			return failWith(upErr.Error(), "upload_failed", output.ExitGeneral)
		}
		return apiFail(err)
	}

	if _, err := c.CompleteSource(ctx, orgID, appID, upload.SourceID); err != nil {
		return apiFail(classifySourceError(err))
	}

	source, retries, pollErr := awaitValidation(ctx, c, orgID, appID, upload.SourceID, waitInterval, waitTimeout)
	if source != nil {
		res.Source = source
	}
	res.ValidationRetries = retries
	if pollErr != nil {
		var (
			gaveUp  *validationUnavailableError
			tooLate *retryTooLateError
		)
		checkHint := fmt.Sprintf("check with 'sources get %s %s --org <slug>', then deploy it with 'deploys trigger %s --source-id %s'", appID, upload.SourceID, appID, upload.SourceID) + timeoutRecordNote(base, appID)
		switch {
		case errors.Is(pollErr, errWaitTimeout):
			return failWith(fmt.Sprintf("timed out waiting for validation of source %s; %s", upload.SourceID, checkHint), "validation_timeout", output.ExitGeneral)
		case errors.As(pollErr, &tooLate):
			when := time.Now().Add(tooLate.after).UTC().Format(time.RFC3339)
			return failWith(fmt.Sprintf("timed out waiting for validation of source %s: the platform failed to validate it and accepts a retry only in %d seconds (at %s), after the --wait-timeout ends; %s", upload.SourceID, int(tooLate.after.Seconds()), when, checkHint), "validation_timeout", output.ExitGeneral)
		case errors.As(pollErr, &gaveUp):
			// The row read before the refused retry still says retryable.
			if fresh, gErr := c.GetSource(ctx, orgID, appID, upload.SourceID); gErr == nil {
				source, res.Source = fresh, fresh
			}
			_ = r.Render(res)
			details := rejectionDetails(source)
			details["retryable"] = false
			details["api_code"] = apiCodeRetryLimit
			if gaveUp.attempts > 0 {
				details["attempts"] = gaveUp.attempts
			}
			if gaveUp.maxAttempts > 0 {
				details["max_attempts"] = gaveUp.maxAttempts
			}
			return failDetails(unavailableMessage(attemptsUsed(source, gaveUp.attempts, retries), rejectionReason(source)), "validation_unavailable", output.ExitGeneral, details)
		}
		return apiFail(classifySourceError(pollErr))
	}
	switch source.Status {
	case "rejected":
		if source.ValidationCode != nil && *source.ValidationCode == validationCodeStaleBase {
			_ = r.Render(res)
			msg, details := staleBaseFailure(ctx, c, orgID, appID, base, source)
			return failDetails(msg, "stale_base", output.ExitGeneral, details)
		}
		_ = r.Render(res)
		details := rejectionDetails(source)
		// Retries are over, either because the CLI stopped repeating a
		// retryable rejection or because the API says it used every attempt on
		// a cause that was not the archive's. Neither is a verdict on the archive.
		if source.IsRetryable() || platformGaveUp(source) {
			return failDetails(unavailableMessage(attemptsUsed(source, 0, retries), rejectionReason(source)), "validation_unavailable", output.ExitGeneral, details)
		}
		return failDetails("the archive was rejected by validation: "+rejectionReason(source), "source_rejected", output.ExitValidation, details)
	case "expired":
		return failWith(fmt.Sprintf("source %s expired before it could be deployed; run the command again", source.ID), "source_expired", output.ExitGeneral)
	}

	// The release is published and is the app's latest ready one: the directory
	// IS that release now, whatever happens to the deployment below, so the
	// record moves here and not when the deployment succeeds. A failed write does
	// not fail the command; it is reported in the output.
	recordPublished(dir, appID, source, res)

	deploy, err := c.TriggerDeployment(ctx, orgID, appID, "", source.ID, envID)
	if err != nil {
		return apiFail(classifySourceError(err))
	}
	res.Deployment = deploy

	if wait {
		pollErr := pollUntil(ctx, waitInterval, waitTimeout, func(ctx context.Context) (bool, error) {
			d, gErr := c.GetDeployment(ctx, orgID, appID, deploy.ID)
			if gErr != nil {
				return false, gErr
			}
			res.Deployment = d
			switch d.Status {
			case "success":
				return true, nil
			case "failed", "cancelled":
				return false, errDeployFailed
			}
			return false, nil
		})
		switch {
		case errors.Is(pollErr, errWaitTimeout):
			return failWith(fmt.Sprintf("timed out waiting for deployment %s to complete; check with 'deploys status %s %s --org <slug>'", deploy.ID, appID, deploy.ID), "wait_timeout", output.ExitGeneral)
		case errors.Is(pollErr, errDeployFailed):
			res.Logs = newLogsHint(cmd, orgID, appID, envID, res.Deployment)
			return failWith(fmt.Sprintf("deployment %s reached a failed terminal state; fetch its logs with: %s", deploy.ID, res.Logs.Command), "deploy_failed", output.ExitGeneral)
		case pollErr != nil:
			return apiFail(pollErr)
		}
	}

	if err := r.Render(res); err != nil {
		return &ExitError{Code: output.ExitGeneral}
	}
	return nil
}

// pulledBase returns the record of the release dir is based on for this app, or
// nil when dir has none, the record is unreadable, or it belongs to another app.
// A record of this app whose source id is not a UUID cannot be a base either:
// it is not sent, and ignored says why.
func pulledBase(dir, appID string) (base *sourcebase.Base, ignored string) {
	b := sourcebase.Read(dir)
	if b == nil || b.AppID != appID {
		return nil, ""
	}
	if !isUUID(b.SourceID) {
		return nil, "the source_id in " + sourcebase.Dir + "/" + sourcebase.File + " is not a UUID, so it was not sent as the base; deploying without a base"
	}
	return b, ""
}

// timeoutRecordNote is what a validation timeout adds when the directory has a
// base record: the record is left as it was, so if the source becomes ready
// after all, the next deploy from this directory is based on a release that is
// no longer the latest and is refused as stale_base.
func timeoutRecordNote(base *sourcebase.Base, appID string) string {
	if base == nil {
		return ""
	}
	return fmt.Sprintf(". %s/%s was left as it was: if this source becomes ready later, the next deploy from this directory is rejected as stale_base. "+
		"Check with 'sureva sources list %s --org <slug>', then pull the latest release again ('sureva sources pull %s --dir <new-dir>') or run deploy with --no-base",
		sourcebase.Dir, sourcebase.File, appID, appID)
}

// rejectionReason is why validation refused a source, in the API's words.
func rejectionReason(s *client.AppSource) string {
	if s != nil && s.ValidationError != nil && *s.ValidationError != "" {
		return *s.ValidationError
	}
	return "no reason was given"
}

// attemptsUsed is how many validation attempts a source used: the count of the
// API error that refused a retry, else what the source row reports, else what
// this run sent (the first complete and its repeats).
func attemptsUsed(s *client.AppSource, fromError, retries int) int {
	switch {
	case fromError > 0:
		return fromError
	case s != nil && s.Attempts != nil && *s.Attempts > 0:
		return *s.Attempts
	}
	return retries + 1
}

// unavailableMessage is the validation_unavailable message: the platform, not
// the archive, failed, and what to do about it.
func unavailableMessage(attempts int, reason string) string {
	return fmt.Sprintf("the platform could not validate the archive after %d attempts, so it was never judged: %s Run the command again: uploading anew starts a fresh budget of validation attempts. If it keeps happening, contact support", attempts, reason)
}

func tooLargeMessage(a *pack.Archive, max int64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "the archive is %d bytes, over the %d byte limit for this app; nothing was uploaded. Largest entries:", a.Size, max)
	for _, e := range a.Largest(largestEntriesShown) {
		fmt.Fprintf(&b, " %s (%d bytes);", e.Path, e.Size)
	}
	b.WriteString(" exclude them with a .surevaignore file")
	return b.String()
}

func newLogsHint(cmd *cobra.Command, orgID, appID, envFlag string, d *client.Deployment) *logsHint {
	slug, _ := cmd.Root().PersistentFlags().GetString("org")
	if slug == "" {
		slug = credentials.DefaultOrgSlugFromPath(configFlagOrDefault(cmd))
	}
	envID := envFlag
	if d != nil && d.EnvironmentID != "" {
		envID = d.EnvironmentID
	}
	command := fmt.Sprintf("sureva logs %s --org %s", appID, slug)
	if envID != "" {
		command += " --env-id " + envID
	}
	h := &logsHint{Command: command, AppID: appID, OrgID: orgID, EnvironmentID: envID}
	if d != nil {
		h.DeploymentID = d.ID
	}
	return h
}
