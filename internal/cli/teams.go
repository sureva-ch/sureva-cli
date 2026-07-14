package cli

import (
	"github.com/spf13/cobra"
	"github.com/sureva-ch/sureva-cli/internal/output"
)

// NewTeamsCmd returns the `teams` command group.
func NewTeamsCmd() *cobra.Command {
	teams := &cobra.Command{
		Use:   "teams",
		Short: "Manage teams within an organization",
		Long: `Commands for listing and inspecting Sureva teams.

AGENT USAGE
  Default output is JSON. Pipe to jq for field extraction:
    sureva teams list --org <slug> | jq '.[].slug'`,
	}
	teams.AddCommand(newTeamsListCmd())
	return teams
}

// newTeamsListCmd returns `teams list --org <slug>`.
func newTeamsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all teams in the resolved organization",
		Long: `List all teams in the resolved organization.

VALIDATION / INPUTS
  --org: required organization slug unless a default org is configured.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, r, err := newAuthenticatedClient(cmd)
			if err != nil {
				return err
			}

			orgID, err := requireOrgID(cmd.Context(), cmd, c, r)
			if err != nil {
				return err
			}

			teams, err := c.ListTeams(cmd.Context(), orgID)
			if err != nil {
				return handleAPIError(r, err)
			}
			if err := r.Render(teams); err != nil {
				return &ExitError{Code: output.ExitGeneral}
			}
			return nil
		},
	}
}
