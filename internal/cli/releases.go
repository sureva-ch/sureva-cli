package cli

import (
	"github.com/spf13/cobra"
	"github.com/sureva-ch/sureva-cli/internal/client"
	"github.com/sureva-ch/sureva-cli/internal/output"
)

// codeUploadBacked is the envelope code of 'releases list' on an upload-backed
// app, the inverse of codeGitHubBacked. It is the CLI's own: the API answers
// that app with 200.
const codeUploadBacked = "upload_backed_app"

// NewReleasesCmd returns the `releases` command group.
func NewReleasesCmd() *cobra.Command {
	releases := &cobra.Command{
		Use:   "releases",
		Short: "List the releases of a GitHub-backed app",
		Long: `Commands for listing the releases of a GitHub-backed app (an app whose code
is read from a GitHub repository), to pick the tag to deploy. An upload-backed
app has source releases instead: see 'sureva sources'.

AGENT USAGE
  Create a release in GitHub, find its tag, then deploy it:
    sureva releases list <app-id> --org <slug> | jq -r '.[0].tag_name'
    sureva deploys trigger <app-id> --org <slug> --tag <tag>

ERRORS (stderr envelope "code"; the API's own code is in details.api_code)
  upload_backed_app        (4) the app is upload-backed; use 'sureva sources list'.
  not_found                (3) unknown app.
  auth_error               (2) missing or expired credentials.
  network_error            (5) no HTTP response.`,
	}
	releases.AddCommand(newReleasesListCmd())
	return releases
}

// newReleasesListCmd returns `releases list <app-id>`.
func newReleasesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <app-id>",
		Short: "List the releases of a GitHub-backed app",
		Long: `List the releases of a GitHub-backed app, in the order the API returns them
(newest first). The tag_name of a release is what
'sureva deploys trigger <app-id> --tag <tag>' takes.

Draft releases are not listed; prereleases are. At most 100 releases are
listed (the API returns one page). An app without releases prints [].
A tag the API's tag format rejects (for example one containing "/") is listed
but cannot be deployed.

VALIDATION / INPUTS
  <app-id>: application ID returned by apps list/create.
  --org: required organization slug unless a default org is configured.

An upload-backed app has no GitHub releases: the command makes no releases
request, prints nothing on stdout and exits 4 with code upload_backed_app.
Use 'sureva sources list <app-id>' for its releases.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, r, err := newAuthenticatedClient(cmd)
			if err != nil {
				return err
			}

			orgID, err := requireOrgID(cmd.Context(), cmd, c, r)
			if err != nil {
				return err
			}

			// The releases endpoint also answers 200 for an upload-backed app,
			// with rows that are not GitHub releases, so the app's source type
			// decides before any release is requested. An app without
			// source_type predates upload-backed apps (cloud-api reads a NULL
			// column as github), so an empty value is let through.
			app, err := c.GetApp(cmd.Context(), orgID, args[0])
			if err != nil {
				return handleAPIError(r, err)
			}
			if app.SourceType == "upload" {
				r.RenderError("this app deploys from uploaded sources and has no GitHub releases; use 'sureva sources list "+args[0]+"'", codeUploadBacked, -1)
				return &ExitError{Code: output.ExitValidation}
			}

			releases, err := c.ListReleases(cmd.Context(), orgID, args[0])
			if err != nil {
				return handleAPIError(r, err)
			}
			if err := r.Render(publishedReleases(releases)); err != nil {
				return &ExitError{Code: output.ExitGeneral}
			}
			return nil
		},
	}
}

// publishedReleases drops the drafts, keeping the order. A draft is not
// deployable: its tag may not exist in the repository yet. It never returns
// nil, so an empty result renders as [] and not null.
func publishedReleases(all []client.Release) []client.Release {
	out := make([]client.Release, 0, len(all))
	for _, rel := range all {
		if !rel.Draft {
			out = append(out, rel)
		}
	}
	return out
}
