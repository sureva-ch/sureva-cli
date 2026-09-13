package cli

import (
	"github.com/spf13/cobra"
	"github.com/sureva-ch/sureva-cli/internal/output"
)

// NewLogsCmd returns the `logs <app-id>` command.
func NewLogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs <app-id>",
		Short: "Fetch a log snapshot for an application (non-streaming)",
		Long: `Fetch a recent snapshot of CloudWatch log events for a Lambda-backed application.

The command returns immediately with the latest log events. Streaming / watch
mode is deferred to a future release.

If the environment has not been provisioned yet, an empty events array is returned
(not an error).

VALIDATION / INPUTS
  <app-id>: application ID returned by apps list/create.
  --org: required organization slug unless a default org is configured.
  --env-id: environment UUID; defaults to the production environment when empty.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			envID, _ := cmd.Flags().GetString("env-id")

			c, r, err := newAuthenticatedClient(cmd)
			if err != nil {
				return err
			}

			orgID, err := requireOrgID(cmd.Context(), cmd, c, r)
			if err != nil {
				return err
			}

			// The logs route requires an environment ID; resolve the app's
			// default environment when the flag is omitted.
			if envID == "" {
				env, envErr := c.DefaultEnvironment(cmd.Context(), orgID, args[0])
				if envErr != nil {
					return handleAPIError(r, envErr)
				}
				envID = env.ID
			}

			logs, err := c.GetLogs(cmd.Context(), orgID, args[0], envID)
			if err != nil {
				return handleAPIError(r, err)
			}
			if err := r.Render(logs); err != nil {
				return &ExitError{Code: output.ExitGeneral}
			}
			return nil
		},
	}
	cmd.Flags().String("env-id", "", "Environment UUID; defaults to the production environment when empty")
	return cmd
}
