package cli

import (
	"github.com/spf13/cobra"
	"github.com/sureva-ch/sureva-cli/internal/output"
)

// NewSourcesCmd returns the `sources` command group.
func NewSourcesCmd() *cobra.Command {
	sources := &cobra.Command{
		Use:   "sources",
		Short: "Inspect the source releases of an upload-backed app",
		Long: `Commands for inspecting the releases of an upload-backed app (an app whose
code is uploaded as an archive rather than read from a GitHub repository).
Each accepted archive is a release with the tag src-<seq> and an id.

AGENT USAGE
  List releases and pick one to deploy or roll back to:
    sureva sources list <app-id> --org <slug> | jq '.[] | select(.status=="ready")'
    sureva deploys trigger <app-id> --org <slug> --source-id <source-id>

  Inspect one release, including why it was rejected:
    sureva sources get <app-id> <source-id> --org <slug> | jq '{validation_code, retryable, validation_error}'

  Fetch the code of an app to work on it (dependencies and environment
  variables are not in it; see 'sources pull --help'):
    sureva sources pull <app-id> --org <slug> --dir ./app

STATUS
  pending|validating  not deployable yet
  rejected            validation refused it; validation_code is the stable
                      reason and retryable says whether sending the same
                      archive again can succeed (true: the platform failed,
                      false: the archive has to change); validation_error is
                      prose for a person
  ready               deployable; "available" is false when the stored
                      version is gone, in which case a deploy exits 1 with
                      code source_expired
  expired             no longer stored; upload the source again

ERRORS (stderr envelope "code"; the API's own code is in details.api_code)
  not_found                (3) unknown app or release.
  auth_error               (2) missing or expired credentials.
  network_error            (5) no HTTP response.

An app that deploys from GitHub has no releases: 'list' is empty and 'get'
answers not_found. Only 'sources pull' reports github_backed_app for it.`,
	}
	sources.AddCommand(newSourcesListCmd())
	sources.AddCommand(newSourcesGetCmd())
	sources.AddCommand(newSourcesPullCmd())
	return sources
}

// newSourcesListCmd returns `sources list <app-id>`.
func newSourcesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <app-id>",
		Short: "List the source releases of an upload-backed app",
		Long: `List the source releases of an upload-backed app, newest first.

VALIDATION / INPUTS
  <app-id>: application ID returned by apps list/create.
  --org: required organization slug unless a default org is configured.

A GitHub-backed app has no source releases: the list is empty.`,
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

			sources, err := c.ListSources(cmd.Context(), orgID, args[0])
			if err != nil {
				return handleAPIError(r, classifySourceError(err))
			}
			if err := r.Render(sources); err != nil {
				return &ExitError{Code: output.ExitGeneral}
			}
			return nil
		},
	}
}

// newSourcesGetCmd returns `sources get <app-id> <source-id>`.
func newSourcesGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <app-id> <source-id>",
		Short: "Get one source release of an upload-backed app",
		Long: `Get one source release of an upload-backed app, including validation_code,
retryable and validation_error when the archive was rejected.

VALIDATION / INPUTS
  <app-id>: application ID returned by apps list/create.
  <source-id>: release ID returned by sources list.
  --org: required organization slug unless a default org is configured.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, r, err := newAuthenticatedClient(cmd)
			if err != nil {
				return err
			}

			orgID, err := requireOrgID(cmd.Context(), cmd, c, r)
			if err != nil {
				return err
			}

			source, err := c.GetSource(cmd.Context(), orgID, args[0], args[1])
			if err != nil {
				return handleAPIError(r, classifySourceError(err))
			}
			if err := r.Render(source); err != nil {
				return &ExitError{Code: output.ExitGeneral}
			}
			return nil
		},
	}
}
