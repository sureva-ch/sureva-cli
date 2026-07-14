package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/sureva-ch/sureva-cli/internal/client"
	"github.com/sureva-ch/sureva-cli/internal/output"
)

// NewServicesCmd returns the `services` command group.
func NewServicesCmd() *cobra.Command {
	services := &cobra.Command{
		Use:   "services",
		Short: "Manage app-attached platform services",
		Long: `Commands for managing platform services attached to Sureva apps.

AGENT USAGE
  sureva services kvs tables create <app-id> --name sessions --org <slug>
  sureva services kvs tables list <app-id> --org <slug>`,
	}
	services.AddCommand(newServicesKVSCmd())
	return services
}

func newServicesKVSCmd() *cobra.Command {
	kvs := &cobra.Command{
		Use:   "kvs",
		Short: "Manage managed KVS for Lambda-backed apps",
		Long: `Manage the managed KVS service for Lambda-backed applications.

KVS is available for api, web-ssr, and sse apps. Creating the first table
activates the service. Plaintext tokens are returned only by table create and
table rotate operations. Store them immediately.`,
	}
	kvs.AddCommand(newServicesKVSTablesCmd())
	return kvs
}

func newServicesKVSTablesCmd() *cobra.Command {
	tables := &cobra.Command{
		Use:   "tables",
		Short: "Manage named KVS table namespaces",
		Long: `Manage named, isolated KVS namespaces for an application.

Each app can have at most two active KVS tables. Table create and rotate
responses include a plaintext token once. Store it immediately.`,
	}
	tables.AddCommand(newServicesKVSTablesListCmd())
	tables.AddCommand(newServicesKVSTablesCreateCmd())
	tables.AddCommand(newServicesKVSTablesDeleteCmd())
	tables.AddCommand(newServicesKVSTablesRotateCmd())
	return tables
}

func newServicesKVSTablesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <app-id>",
		Short: "List KVS tables for an app",
		Long: `List named KVS table namespaces for an application.

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
			tables, err := c.ListKVSTables(cmd.Context(), orgID, args[0])
			if err != nil {
				return handleAPIError(r, err)
			}
			if err := r.Render(tables); err != nil {
				return &ExitError{Code: output.ExitGeneral}
			}
			return nil
		},
	}
}

func newServicesKVSTablesCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <app-id>",
		Short: "Create a named KVS table for an app",
		Long: `Create a named KVS table namespace for an application.

VALIDATION / INPUTS
  <app-id>: application ID returned by apps list/create.
  --name: table name, 1-32 letters, digits, or underscores.
  --minute-limit: optional per-minute quota, 1-600; omitted defaults to 600.
  --org: required organization slug unless a default org is configured.

The response includes a plaintext table token once. Store it immediately.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _ := cmd.Flags().GetString("name")
			minuteLimit, _ := cmd.Flags().GetInt("minute-limit")
			r := output.NewRenderer(OutputFormat(cmd), cmd.OutOrStdout(), cmd.ErrOrStderr())
			if name == "" {
				r.RenderError("--name is required", "validation_error", -1)
				return &ExitError{Code: output.ExitValidation}
			}

			c, r, err := newAuthenticatedClient(cmd)
			if err != nil {
				return err
			}
			orgID, err := requireOrgID(cmd.Context(), cmd, c, r)
			if err != nil {
				return err
			}
			resp, err := c.CreateKVSTable(cmd.Context(), orgID, args[0], client.CreateKVSTableRequest{
				Name:        name,
				MinuteLimit: minuteLimit,
			})
			if err != nil {
				return handleAPIError(r, err)
			}
			if err := r.Render(resp); err != nil {
				return &ExitError{Code: output.ExitGeneral}
			}
			return nil
		},
	}
	cmd.Flags().String("name", "", "KVS table name: 1-32 letters, digits, or underscores")
	cmd.Flags().Int("minute-limit", 0, "Per-minute quota, 1-600; omitted defaults to 600")
	return cmd
}

func newServicesKVSTablesDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <app-id> <name>",
		Short: "Delete a named KVS table (requires --yes)",
		Long: `Delete a named KVS table namespace for an application.

VALIDATION / INPUTS
  <app-id>: application ID returned by apps list/create.
  <name>: table name.
  --org: required organization slug unless a default org is configured.
  --yes: required confirmation flag to prevent accidental table removal.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			yes, _ := cmd.Flags().GetBool("yes")
			r := output.NewRenderer(OutputFormat(cmd), cmd.OutOrStdout(), cmd.ErrOrStderr())
			if !yes {
				r.RenderError(
					fmt.Sprintf("pass --yes to confirm deleting KVS table %q for app %q", args[1], args[0]),
					"validation_error",
					-1,
				)
				return &ExitError{Code: output.ExitValidation}
			}

			c, r, err := newAuthenticatedClient(cmd)
			if err != nil {
				return err
			}
			orgID, err := requireOrgID(cmd.Context(), cmd, c, r)
			if err != nil {
				return err
			}
			if err := c.DeleteKVSTable(cmd.Context(), orgID, args[0], args[1]); err != nil {
				return handleAPIError(r, err)
			}
			if err := r.Render(map[string]string{"message": "kvs table deleted"}); err != nil {
				return &ExitError{Code: output.ExitGeneral}
			}
			return nil
		},
	}
	cmd.Flags().Bool("yes", false, "Confirm deleting the KVS table")
	return cmd
}

func newServicesKVSTablesRotateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rotate <app-id> <name>",
		Short: "Rotate a named KVS table token",
		Long: `Rotate a named KVS table token.

VALIDATION / INPUTS
  <app-id>: application ID returned by apps list/create.
  <name>: table name.
  --org: required organization slug unless a default org is configured.

The response includes the new plaintext table token once. Store it immediately.`,
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
			resp, err := c.RotateKVSTableToken(cmd.Context(), orgID, args[0], args[1])
			if err != nil {
				return handleAPIError(r, err)
			}
			if err := r.Render(resp); err != nil {
				return &ExitError{Code: output.ExitGeneral}
			}
			return nil
		},
	}
}
