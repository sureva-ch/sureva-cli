package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/sureva-ch/sureva-cli/internal/client"
	"github.com/sureva-ch/sureva-cli/internal/output"
)

// errDeployFailed is a sentinel returned by the deploys trigger poll function
// when the deployment reaches a non-success terminal state (failed|cancelled).
var errDeployFailed = errors.New("deploy_failed")

// isDeployFailedStatus reports whether a deployment status is a non-success
// terminal state. Shared by 'deploys trigger --wait' and 'apps create --wait'.
func isDeployFailedStatus(status string) bool {
	return status == "failed" || status == "cancelled"
}

// NewDeploysCmd returns the `deploys` command group.
func NewDeploysCmd() *cobra.Command {
	deploys := &cobra.Command{
		Use:   "deploys",
		Short: "Manage application deployments",
		Long: `Commands for triggering and inspecting Sureva deployments.

AGENT USAGE
  Trigger and poll:
    sureva releases list <app-id> --org <slug> | jq -r '.[].tag_name'
    sureva deploys trigger <app-id> --org <slug> --tag v1.2.3
    sureva deploys trigger <app-id> --org <slug> --source-id <source-id>
    sureva deploys status <app-id> <deploy-id> --org <slug>
    sureva deploys cancel <app-id> <deploy-id> --org <slug>

  List recent deployments:
    sureva deploys list <app-id> --org <slug> | jq '.[0].status'`,
	}
	deploys.AddCommand(newDeploysTriggerCmd())
	deploys.AddCommand(newDeploysListCmd())
	deploys.AddCommand(newDeploysStatusCmd())
	deploys.AddCommand(newDeploysCancelCmd())
	return deploys
}

// newDeploysTriggerCmd returns `deploys trigger <app-id> [--tag <tag> | --source-id <id>] [--env-id <id>] [--wait]`.
func newDeploysTriggerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trigger <app-id>",
		Short: "Trigger a new deployment for an application",
		Long: `Trigger a new deployment for an application.

VALIDATION / INPUTS
  <app-id>: application ID returned by apps list/create.
  --org: required organization slug unless a default org is configured.
  --tag: release tag of a GitHub-backed app (example: v1.2.3), from
         'sureva releases list <app-id>'; required by the API for api, web-ssr
         and sse app types. Rejected for an upload-backed app.
  --source-id: release ID of an upload-backed app, from 'sources list'. Deploys
         that release, which is also how you roll back. With neither --tag nor
         --source-id an upload-backed app deploys its latest ready release.
         Mutually exclusive with --tag.
  --env-id: environment UUID; defaults to the production environment when empty.
  --wait-interval/--wait-timeout: Go duration strings (examples: 1s, 30s, 15m).

ERRORS (stderr envelope "code"; exit code in parentheses). The classification
follows the API's own error code, which is repeated in details.api_code.
  source_expired      (1) 410: the release is no longer stored; upload the
                      source again.
  source_not_ready    (1) 409: the release is pending, validating, rejected or
                      expired; details.source_status says which and, for a
                      rejected one, details.validation_code why.
  no_ready_source     (3) 404: no --source-id was given and the app has no
                      ready release; run 'sureva deploy' first.
  not_found           (3) 404: unknown release.
  validation_error    (4) 400: --tag on an upload-backed app, or --source-id
                      on a GitHub-backed one.
  deploy_failed       (1) with --wait: the deployment itself failed or was
                      cancelled.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tag, _ := cmd.Flags().GetString("tag")
			sourceID, _ := cmd.Flags().GetString("source-id")
			envID, _ := cmd.Flags().GetString("env-id")
			wait, _ := cmd.Flags().GetBool("wait")
			waitInterval, _ := cmd.Flags().GetDuration("wait-interval")
			waitTimeout, _ := cmd.Flags().GetDuration("wait-timeout")

			c, r, err := newAuthenticatedClient(cmd)
			if err != nil {
				return err
			}

			if tag != "" && sourceID != "" {
				r.RenderError(
					"--tag and --source-id are mutually exclusive: --tag selects a release of a GitHub-backed app, --source-id a release of an upload-backed app",
					"validation_error",
					-1,
				)
				return &ExitError{Code: output.ExitValidation}
			}

			if wait && waitInterval <= 0 {
				r.RenderError(waitIntervalMessage, "validation_error", -1)
				return &ExitError{Code: output.ExitValidation}
			}

			orgID, err := requireOrgID(cmd.Context(), cmd, c, r)
			if err != nil {
				return err
			}

			deploy, err := c.TriggerDeployment(cmd.Context(), orgID, args[0], tag, sourceID, envID)
			if err != nil {
				return handleAPIError(r, classifySourceError(err))
			}

			if wait {
				var finalDeploy *client.Deployment
				pollErr := pollUntil(cmd.Context(), waitInterval, waitTimeout, func(ctx context.Context) (bool, error) {
					d, gErr := c.GetDeployment(ctx, orgID, args[0], deploy.ID)
					if gErr != nil {
						return false, gErr
					}
					finalDeploy = d
					if d.Status == "success" {
						return true, nil
					}
					if isDeployFailedStatus(d.Status) {
						return false, errDeployFailed
					}
					return false, nil
				})
				if pollErr != nil {
					if errors.Is(pollErr, errWaitTimeout) {
						_ = r.RenderError(
							fmt.Sprintf("timed out waiting for deployment %s to complete; check with 'deploys status %s %s --org <slug>'", deploy.ID, args[0], deploy.ID),
							"wait_timeout",
							-1,
						)
						return &ExitError{Code: output.ExitGeneral}
					}
					if errors.Is(pollErr, errDeployFailed) {
						_ = r.RenderError(
							fmt.Sprintf("deployment %s reached a failed terminal state; check with 'deploys status %s %s --org <slug>'", deploy.ID, args[0], deploy.ID),
							"deploy_failed",
							-1,
						)
						return &ExitError{Code: output.ExitGeneral}
					}
					return handleAPIError(r, pollErr)
				}
				deploy = finalDeploy
			}

			if err := r.Render(deploy); err != nil {
				return &ExitError{Code: output.ExitGeneral}
			}
			return nil
		},
	}
	cmd.Flags().String("tag", "", "Release tag of a GitHub-backed app (e.g. v1.2.3, see 'releases list'); required for api, web-ssr and sse app types; not accepted for an upload-backed app")
	cmd.Flags().String("source-id", "", "Release ID of an upload-backed app (see 'sources list'); deploys that release, also used to roll back; mutually exclusive with --tag")
	cmd.Flags().String("env-id", "", "Environment UUID; defaults to the production environment when empty")
	cmd.Flags().Bool("wait", false, "Wait for deployment to reach a terminal state before returning")
	cmd.Flags().Duration("wait-interval", 5*time.Second, "Polling interval as a Go duration when --wait is active (e.g. 5s)")
	cmd.Flags().Duration("wait-timeout", 15*time.Minute, "Maximum wait as a Go duration for deployment completion (e.g. 15m)")
	return cmd
}

// newDeploysListCmd returns `deploys list <app-id>`.
func newDeploysListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <app-id>",
		Short: "List recent deployments for an application",
		Long: `List recent deployments for an application.

VALIDATION / INPUTS
  <app-id>: application ID returned by apps list/create.
  --org: required organization slug unless a default org is configured.`,
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

			deploys, err := c.ListDeployments(cmd.Context(), orgID, args[0])
			if err != nil {
				return handleAPIError(r, err)
			}
			if err := r.Render(deploys); err != nil {
				return &ExitError{Code: output.ExitGeneral}
			}
			return nil
		},
	}
}

// newDeploysStatusCmd returns `deploys status <app-id> <deploy-id>`.
func newDeploysStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <app-id> <deploy-id>",
		Short: "Get the status of a specific deployment",
		Long: `Get the status of a specific deployment.

VALIDATION / INPUTS
  <app-id>: application ID returned by apps list/create.
  <deploy-id>: deployment ID returned by deploys trigger/list.
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

			deploy, err := c.GetDeployment(cmd.Context(), orgID, args[0], args[1])
			if err != nil {
				return handleAPIError(r, err)
			}
			if err := r.Render(deploy); err != nil {
				return &ExitError{Code: output.ExitGeneral}
			}
			return nil
		},
	}
}

// newDeploysCancelCmd returns `deploys cancel <app-id> <deploy-id>`.
func newDeploysCancelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <app-id> <deploy-id>",
		Short: "Cancel an in-flight deployment",
		Long: `Cancel an in-flight deployment.

VALIDATION / INPUTS
  <app-id>: application ID returned by apps list/create.
  <deploy-id>: deployment ID returned by deploys trigger/list.
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

			if err := c.CancelDeployment(cmd.Context(), orgID, args[0], args[1]); err != nil {
				return handleAPIError(r, err)
			}
			if err := r.Render(map[string]string{
				"id":     args[1],
				"status": "cancelled",
			}); err != nil {
				return &ExitError{Code: output.ExitGeneral}
			}
			return nil
		},
	}
}
