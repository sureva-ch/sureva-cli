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
)

// largestEntriesShown is how many archive entries a size refusal names.
const largestEntriesShown = 5

// deployResult is the JSON `sureva deploy` prints. It is built up as the steps
// complete and printed on stdout even when a later step fails, so a caller sees
// how far the deploy got (the uploaded source, the failed deployment, where its
// logs are) next to the error envelope on stderr.
type deployResult struct {
	AppID      string             `json:"app_id"`
	Archive    *archiveSummary    `json:"archive,omitempty"`
	Source     *client.AppSource  `json:"source,omitempty"`
	Deployment *client.Deployment `json:"deployment,omitempty"`
	// Logs says how to fetch the logs of a deployment that failed.
	Logs *logsHint `json:"logs,omitempty"`
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
  --wait-interval/--wait-timeout: Go duration strings. The interval also paces
         the wait for archive validation, which always happens; the timeout
         bounds each of the two waits separately.

WHAT IS PACKED
  Always left out, at any depth: node_modules/, .git/ and .env*. Also left out:
  what .gitignore ignores (inside a git work tree the file list comes from git,
  so its rules apply exactly), and what a .surevaignore file in [dir] lists (same
  syntax as .gitignore). Symlinks are skipped, never followed. Paths are stored
  relative to [dir]. The JSON output lists what was excluded and the archive
  size; the zip is built in a temporary file and removed afterwards.

OUTPUT (stdout JSON)
  app_id, archive{size_bytes,files,max_bytes,packing,excluded,largest}, source
  (the release; see 'sources get'), deployment and, when the deployment failed,
  logs{command,...} naming the command that fetches its logs. On a failure after
  the upload the same JSON is printed with what completed so far.

ERRORS (stderr envelope "code"; exit code in parentheses)
  auth_error            (2) missing or expired credentials.
  validation_error      (4) bad arguments or directory.
  github_backed_app     (4) the app deploys from GitHub; use 'deploys trigger'.
  empty_archive         (4) nothing left to pack after the exclusions.
  archive_too_large     (4) over the API's limit; nothing was uploaded.
  source_rejected       (4) validation refused the archive; the reason is in
                        the message and in source.validation_error.
  upload_failed         (1) the storage endpoint refused the archive.
  upload_expired        (1) the upload form expired; run the command again.
  validation_timeout    (1) validation did not finish; check 'sources get'.
  source_expired        (1) the release is no longer stored.
  source_not_ready      (1) the release is not deployable.
  wait_timeout          (1) the deployment did not finish in time.
  deploy_failed         (1) the deployment failed or was cancelled.
  pack_failed           (1) the directory could not be read or zipped.
  interrupted           (1) stopped by SIGINT or SIGTERM; the temporary archive
                        was removed. Anything uploaded before that is reported
                        in stdout and stays on the platform.
  network_error         (5) no HTTP response.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runDeploy,
	}
	cmd.Flags().String("app", "", "Application ID of an upload-backed app (required)")
	cmd.Flags().String("env-id", "", "Environment UUID; defaults to the production environment when empty")
	cmd.Flags().Bool("wait", false, "Wait for the deployment to reach a terminal state before returning")
	cmd.Flags().Duration("wait-interval", 5*time.Second, "Polling interval as a Go duration, for validation and --wait (e.g. 5s)")
	cmd.Flags().Duration("wait-timeout", 15*time.Minute, "Maximum wait as a Go duration for validation and, separately, for the deployment (e.g. 15m)")
	return cmd
}

func runDeploy(cmd *cobra.Command, args []string) error {
	appID, _ := cmd.Flags().GetString("app")
	envID, _ := cmd.Flags().GetString("env-id")
	wait, _ := cmd.Flags().GetBool("wait")
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

	if appID == "" {
		return fail("--app is required: the application ID of an upload-backed app (see 'apps list')", "validation_error", output.ExitValidation)
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

	res = &deployResult{AppID: appID, Archive: &archiveSummary{
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

	upload, err := c.CreateSourceUpload(ctx, orgID, appID)
	if err != nil {
		var apiErr *client.APIError
		if errors.As(err, &apiErr) && apiErr.HTTPStatus == http.StatusConflict && strings.Contains(apiErr.Message, "linked GitHub repository") {
			return fail("this app deploys from its linked GitHub repository; use 'deploys trigger' instead of 'deploy'", "github_backed_app", output.ExitValidation)
		}
		if ctx.Err() != nil {
			return interrupted()
		}
		return handleAPIError(r, err)
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

	var source *client.AppSource
	pollErr := pollUntil(ctx, waitInterval, waitTimeout, func(ctx context.Context) (bool, error) {
		s, gErr := c.GetSource(ctx, orgID, appID, upload.SourceID)
		if gErr != nil {
			return false, gErr
		}
		source = s
		switch s.Status {
		case "ready", "rejected", "expired":
			return true, nil
		}
		return false, nil
	})
	if source != nil {
		res.Source = source
	}
	if pollErr != nil {
		if errors.Is(pollErr, errWaitTimeout) {
			return failWith(fmt.Sprintf("timed out waiting for validation of source %s; check with 'sources get %s %s --org <slug>', then deploy it with 'deploys trigger %s --source-id %s'", upload.SourceID, appID, upload.SourceID, appID, upload.SourceID), "validation_timeout", output.ExitGeneral)
		}
		return apiFail(pollErr)
	}
	switch source.Status {
	case "rejected":
		reason := "no reason was given"
		if source.ValidationError != nil && *source.ValidationError != "" {
			reason = *source.ValidationError
		}
		return failWith("the archive was rejected by validation: "+reason, "source_rejected", output.ExitValidation)
	case "expired":
		return failWith(fmt.Sprintf("source %s expired before it could be deployed; run the command again", source.ID), "source_expired", output.ExitGeneral)
	}

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
