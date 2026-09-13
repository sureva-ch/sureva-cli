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

// NewDeploysCmd returns the `deploys` command group.
func NewDeploysCmd() *cobra.Command {
	deploys := &cobra.Command{
		Use:   "deploys",
		Short: "Manage application deployments",
		Long: `Commands for triggering and inspecting Sureva deployments.

AGENT USAGE
  Trigger and poll:
    sureva deploys trigger <app-id> --org <slug> --tag v1.2.3
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

// newDeploysTriggerCmd returns `deploys trigger <app-id> [--tag <tag>] [--env-id <id>] [--wait]`.
func newDeploysTriggerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trigger <app-id>",
		Short: "Trigger a new deployment for an application",
		Long: `Trigger a new deployment for an application.

VALIDATION / INPUTS
  <app-id>: application ID returned by apps list/create.
  --org: required organization slug unless a default org is configured.
  --tag: release tag to deploy (example: v1.2.3); required by the API for API
         and SSE app types.
  --env-id: environment UUID; defaults to the production environment when empty.
  --wait-interval/--wait-timeout: Go duration strings (examples: 1s, 30s, 15m).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tag, _ := cmd.Flags().GetString("tag")
			envID, _ := cmd.Flags().GetString("env-id")
			wait, _ := cmd.Flags().GetBool("wait")
			waitInterval, _ := cmd.Flags().GetDuration("wait-interval")
			waitTimeout, _ := cmd.Flags().GetDuration("wait-timeout")

			c, r, err := newAuthenticatedClient(cmd)
			if err != nil {
				return err
			}

			orgID, err := requireOrgID(cmd.Context(), cmd, c, r)
			if err != nil {
				return err
			}

			deploy, err := c.TriggerDeployment(cmd.Context(), orgID, args[0], tag, envID)
			if err != nil {
				return handleAPIError(r, err)
			}

			if wait {
				var finalDeploy *client.Deployment
				pollErr := pollUntil(cmd.Context(), waitInterval, waitTimeout, func(ctx context.Context) (bool, error) {
					d, gErr := c.GetDeployment(ctx, orgID, args[0], deploy.ID)
					if gErr != nil {
						return false, gErr
					}
					finalDeploy = d
					switch d.Status {
					case "success":
						return true, nil
					case "failed", "cancelled":
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
	cmd.Flags().String("tag", "", "Release tag to deploy (e.g. v1.2.3); required for API and SSE app types")
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
