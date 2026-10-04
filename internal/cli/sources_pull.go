package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/sureva-ch/sureva-cli/internal/client"
	"github.com/sureva-ch/sureva-cli/internal/output"
	"github.com/sureva-ch/sureva-cli/internal/sourcebase"
	"github.com/sureva-ch/sureva-cli/internal/unpack"
)

// pullResult is the JSON `sureva sources pull` prints.
type pullResult struct {
	AppID      string `json:"app_id"`
	SourceID   string `json:"source_id"`
	ReleaseTag string `json:"release_tag"`
	// Dir is the absolute path the release was extracted into.
	Dir string `json:"dir"`
	// Files and Bytes count what was extracted: regular files and their
	// uncompressed size. ArchiveBytes is the size of the zip that was downloaded.
	Files        int    `json:"files"`
	Bytes        int64  `json:"bytes"`
	ArchiveBytes int64  `json:"archive_bytes"`
	SHA256       string `json:"sha256"`
	// StateFile records the release the directory is based on.
	StateFile string `json:"state_file"`
}

// newSourcesPullCmd returns `sources pull <app-id>`.
func newSourcesPullCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pull <app-id>",
		Short: "Download a release of an upload-backed app into a directory",
		Long: `Download one release of an upload-backed app and extract it. The archive is
fetched straight from storage, its sha256 is verified against the one the API
reported before anything is extracted, and the files are written with a
hardened extractor. By default the latest ready release is pulled.

AGENT USAGE
  sureva sources pull <app-id> --org <slug> --dir ./app
  sureva sources pull <app-id> --org <slug> --source-id <source-id> --dir ./app
  cd ./app && sureva deploy --app <app-id> --org <slug>

VALIDATION / INPUTS
  <app-id>: application ID of an upload-backed app (see 'apps list').
  --source-id: release to pull (see 'sources list'); default: the latest ready one.
  --dir: directory to extract into; default: the current directory. It is created
         when missing. It counts as empty when it holds nothing, or only the
         .sureva state directory of an earlier pull; otherwise the command is
         refused (dir_not_empty) unless --force.
  --force: extract into a directory that already holds files. Files in the
         archive overwrite files of the same path; files that are not in the
         archive are left alone, so a directory can end up with leftovers from
         an older release. A failure midway can leave some files written.
  --org: required organization slug unless a default org is configured.

WHAT YOU GET (read this before building)
  The tree is what the platform stored, not what was uploaded:
  - node_modules/, .git/ and .env* are never in it. Install dependencies after
    the pull (for example npm ci), and fetch environment variables with
    'sureva env get'; there is no .env file to copy.
  - a single wrapper directory that the upload had was removed, so files sit at
    the top of --dir.
  - file permission bits are kept (releases stored before that was recorded
    extract without the executable bit). Group and other never get write access.
  After a successful pull, <dir>/.sureva/source.json records app_id, source_id,
  release_tag, sha256 and the time. 'sureva deploy' never uploads .sureva/ and
  reports the recorded release as base_source_id.

SAFETY
  The archive is refused, and nothing is extracted from it, when an entry has an
  absolute path, a ".." or "." segment, a backslash, a NUL byte or a drive letter,
  is a symlink or any non-regular entry, repeats another entry, or uses the
  reserved .sureva directory. Limits: 50000 entries and 1 GiB uncompressed (the
  platform itself accepts at most 10000 entries and 100 MB). No file is written
  through a symlink that already exists in --dir. Into a new or empty directory
  the tree is built aside and moved into place only when complete.

OUTPUT (stdout JSON)
  app_id, source_id, release_tag, dir, files, bytes, archive_bytes, sha256,
  state_file. The download link is never printed.

ERRORS (stderr envelope "code"; exit code in parentheses)
  auth_error          (2) missing or expired credentials.
  validation_error    (4) bad arguments, or --dir is not a directory.
  dir_not_empty       (4) --dir already holds files; use --force.
  github_backed_app   (4) the app's code is its GitHub repository; clone it
                      (the repository is named in the message when known).
  no_source           (3) the app has no ready release yet: start a new project
                      and publish it with 'sureva deploy'. Not a failure.
  not_found           (3) unknown app, or unknown --source-id.
  source_not_ready    (1) the release is not ready (validating or rejected).
  source_expired      (1) the release is no longer stored.
  checksum_mismatch   (1) the download does not match the sha256 the API
                      reported; nothing was extracted.
  unsafe_archive      (1) the archive was refused; see the message.
  download_expired    (1) the download link ran out; run the command again.
  download_failed     (1) storage refused the download or sent more than the
                      reported size.
  extract_failed      (1) writing the files failed; the message says whether
                      some were already written.
  interrupted         (1) stopped by SIGINT or SIGTERM; the temporary archive
                      was removed.
  network_error       (5) no HTTP response.`,
		Args: cobra.ExactArgs(1),
		RunE: runSourcesPull,
	}
	cmd.Flags().String("source-id", "", "Release to pull; defaults to the latest ready release")
	cmd.Flags().String("dir", ".", "Directory to extract into (created when missing)")
	cmd.Flags().Bool("force", false, "Extract into a directory that already holds files; files not in the archive are left alone")
	return cmd
}

func runSourcesPull(cmd *cobra.Command, args []string) error {
	appID := args[0]
	sourceID, _ := cmd.Flags().GetString("source-id")
	dir, _ := cmd.Flags().GetString("dir")
	force, _ := cmd.Flags().GetBool("force")
	if sourceID == "" {
		sourceID = client.LatestSource
	}

	// SIGINT and SIGTERM cancel ctx so the download stops and the temporary
	// archive is removed by the deferred cleanup. Scoped to this command.
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	r := output.NewRenderer(OutputFormat(cmd), cmd.OutOrStdout(), cmd.ErrOrStderr())
	fail := func(msg, code string, exit int) error {
		_ = r.RenderError(msg, code, -1)
		return &ExitError{Code: exit}
	}
	interrupted := func() error {
		return fail("interrupted before the pull finished; the temporary archive was removed", "interrupted", output.ExitGeneral)
	}

	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fail("cannot resolve --dir: "+err.Error(), "validation_error", output.ExitValidation)
	}
	// Refuse before downloading anything.
	if err := unpack.Preflight(absDir, force); err != nil {
		return pullExtractFailure(ctx, fail, err)
	}

	c, r, err := newAuthenticatedClient(cmd)
	if err != nil {
		return err
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
		return fail(githubBackedMessage(app.GitHubRepoFull), "github_backed_app", output.ExitValidation)
	}

	dl, err := c.DownloadSource(ctx, orgID, appID, sourceID)
	if err != nil {
		if ctx.Err() != nil {
			return interrupted()
		}
		return handleAPIError(r, classifyDownloadError(err, sourceID == client.LatestSource, app.GitHubRepoFull))
	}
	if dl.SizeBytes == nil || dl.SHA256 == nil || *dl.SHA256 == "" {
		return fail("the API did not report the size and sha256 of the archive, so it cannot be verified; nothing was downloaded", "download_failed", output.ExitGeneral)
	}

	tmp, err := os.CreateTemp("", "sureva-pull-*.zip") // mode 0600
	if err != nil {
		return fail("could not create a temporary file: "+err.Error(), "download_failed", output.ExitGeneral)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	sum, size, err := c.DownloadSourceArchive(ctx, dl.URL, *dl.SizeBytes, tmp)
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		if ctx.Err() != nil {
			return interrupted()
		}
		return pullDownloadFailure(r, fail, err)
	}
	if !strings.EqualFold(sum, *dl.SHA256) {
		return fail(fmt.Sprintf("the downloaded archive does not match the sha256 the API reported for %s; nothing was extracted. Run the command again", dl.ReleaseTag), "checksum_mismatch", output.ExitGeneral)
	}

	base := sourcebase.Base{AppID: appID, SourceID: dl.SourceID, ReleaseTag: dl.ReleaseTag, SHA256: sum, PulledAt: time.Now().UTC()}
	res, err := unpack.Extract(ctx, tmp.Name(), absDir, unpack.Options{
		Force: force,
		Finalize: func(root string) error {
			_, werr := sourcebase.Write(root, base)
			return werr
		},
	})
	if err != nil {
		return pullExtractFailure(ctx, fail, err)
	}

	out := &pullResult{
		AppID:        appID,
		SourceID:     dl.SourceID,
		ReleaseTag:   dl.ReleaseTag,
		Dir:          absDir,
		Files:        res.Files,
		Bytes:        res.Bytes,
		ArchiveBytes: size,
		SHA256:       sum,
		StateFile:    sourcebase.Path(absDir),
	}
	if err := r.Render(out); err != nil {
		return &ExitError{Code: output.ExitGeneral}
	}
	return nil
}

func githubBackedMessage(repo string) string {
	if repo != "" {
		return "this app's code is its linked GitHub repository " + repo + "; clone it instead of pulling a source archive"
	}
	return "this app's code is its linked GitHub repository; clone it instead of pulling a source archive"
}

// classifyDownloadError gives the failures of the download request their own
// envelope codes. 404 is the one status that is two things: with the "latest"
// alias it means the app has no ready release (a normal state for an app that
// was never deployed), with an explicit id it means the id is unknown.
func classifyDownloadError(err error, latest bool, repo string) error {
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	switch {
	case apiErr.ServerCode == "app_not_upload_backed":
		apiErr.Code = "github_backed_app"
		apiErr.Message = githubBackedMessage(repo)
	case apiErr.HTTPStatus == http.StatusNotFound && latest:
		apiErr.Code = "no_source"
		apiErr.Message = "this app has no ready release yet: start a new project here and publish it with 'sureva deploy'"
	default:
		return classifySourceError(apiErr)
	}
	return apiErr
}

// pullDownloadFailure maps a failed transfer. None of these messages can carry
// the presigned URL: the client strips it from every error it returns.
func pullDownloadFailure(r *output.Renderer, fail func(string, string, int) error, err error) error {
	var de *client.DownloadError
	var apiErr *client.APIError
	switch {
	case errors.As(err, &de) && de.Expired():
		return fail("the download link expired before the archive was fetched; run the command again", "download_expired", output.ExitGeneral)
	case errors.As(err, &de):
		return fail(de.Error(), "download_failed", output.ExitGeneral)
	case errors.Is(err, client.ErrDownloadTooLarge):
		return fail("storage sent more data than the size the API reported; the download was stopped and nothing was extracted", "download_failed", output.ExitGeneral)
	case errors.As(err, &apiErr):
		return handleAPIError(r, apiErr)
	}
	return fail("could not write the downloaded archive: "+err.Error(), "download_failed", output.ExitGeneral)
}

// pullExtractFailure maps the failures of the target check and the extraction.
func pullExtractFailure(ctx context.Context, fail func(string, string, int) error, err error) error {
	var notEmpty *unpack.NotEmptyError
	var unsafe *unpack.UnsafeArchiveError
	switch {
	case ctx.Err() != nil:
		return fail("interrupted before the pull finished; the temporary archive was removed", "interrupted", output.ExitGeneral)
	case errors.As(err, &notEmpty):
		return fail(notEmpty.Error(), "dir_not_empty", output.ExitValidation)
	case errors.Is(err, unpack.ErrNotDir):
		return fail("--dir exists and is not a directory", "validation_error", output.ExitValidation)
	case errors.As(err, &unsafe):
		return fail(unsafe.Error(), "unsafe_archive", output.ExitGeneral)
	}
	return fail("could not extract the archive: "+err.Error(), "extract_failed", output.ExitGeneral)
}
