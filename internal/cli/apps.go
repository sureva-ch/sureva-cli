package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/sureva-ch/sureva-cli/internal/client"
	"github.com/sureva-ch/sureva-cli/internal/credentials"
	"github.com/sureva-ch/sureva-cli/internal/output"
)

// errDomainFailed is a sentinel returned by the apps create poll function when
// the app's domain_status reaches the "failed" terminal state.
var errDomainFailed = errors.New("domain_failed")

// NewAppsCmd returns the `apps` command group.
func NewAppsCmd() *cobra.Command {
	apps := &cobra.Command{
		Use:   "apps",
		Short: "Manage applications",
		Long: `Commands for creating, listing, and inspecting Sureva applications.

AGENT USAGE
  Default output is JSON. Pipe to jq for field extraction:
    sureva apps list | jq '.[].id'
    sureva apps get <app-id> --org <slug> | jq '.subdomain'
    sureva apps create --name my-app --type web --region us-east-1 --org <slug>`,
	}
	apps.AddCommand(newAppsListCmd())
	apps.AddCommand(newAppsGetCmd())
	apps.AddCommand(newAppsCreateCmd())
	apps.AddCommand(newAppsDeleteCmd())
	return apps
}

// newAppsListCmd returns `apps list [--org <slug>]`.
// Without --org (and no config default) the flat GET /v1/apps is used to
// list all apps visible to the caller across every org. With --org only apps
// in that specific org are returned.
func newAppsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List applications (all orgs by default, or scoped with --org)",
		Long: `List applications visible to the authenticated user.

VALIDATION / INPUTS
  --org: optional organization slug. When omitted, the command uses the
         configured default org; if no default exists, it lists apps across
         all organizations visible to the token.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, r, err := newAuthenticatedClient(cmd)
			if err != nil {
				return err
			}

			// If a slug is available (flag or config), use the org-scoped list.
			slug, _ := cmd.Root().PersistentFlags().GetString("org")
			if slug == "" {
				slug = credentials.DefaultOrgSlugFromPath(configFlagOrDefault(cmd))
			}

			if slug != "" {
				orgID, oErr := requireOrgID(cmd.Context(), cmd, c, r)
				if oErr != nil {
					return oErr
				}
				apps, aErr := c.ListApps(cmd.Context(), orgID)
				if aErr != nil {
					return handleAPIError(r, aErr)
				}
				configPath := configFlagOrDefault(cmd)
				suffix := credentials.DomainSuffixFromPath(configPath)
				views := make([]appView, len(apps))
				for i := range apps {
					views[i] = newAppView(&apps[i], suffix)
				}
				if err := r.Render(views); err != nil {
					return &ExitError{Code: output.ExitGeneral}
				}
				return nil
			}

			// No org context — use the flat cross-org endpoint.
			apps, aErr := c.ListAllApps(cmd.Context())
			if aErr != nil {
				return handleAPIError(r, aErr)
			}
			configPath := configFlagOrDefault(cmd)
			suffix := credentials.DomainSuffixFromPath(configPath)
			views := make([]appView, len(apps))
			for i := range apps {
				views[i] = newAppView(&apps[i], suffix)
			}
			if err := r.Render(views); err != nil {
				return &ExitError{Code: output.ExitGeneral}
			}
			return nil
		},
	}
}

// newAppsGetCmd returns `apps get <app-id> --org <slug>`.
// --org is required: the API path is /orgs/{orgID}/apps/{appID}.
func newAppsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <app-id>",
		Short: "Get details for a specific application (requires --org)",
		Long: `Get details for a specific application.

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

			app, err := c.GetApp(cmd.Context(), orgID, args[0])
			if err != nil {
				return handleAPIError(r, err)
			}
			configPath := configFlagOrDefault(cmd)
			suffix := credentials.DomainSuffixFromPath(configPath)
			if err := r.Render(newAppView(app, suffix)); err != nil {
				return &ExitError{Code: output.ExitGeneral}
			}
			return nil
		},
	}
}

// newAppsCreateCmd returns `apps create` with full flag surface.
func newAppsCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new application",
		Long: `Create a new Sureva application. Team is auto-selected when the org has
exactly one team; use --team <slug-or-id> when multiple teams exist.

VALIDATION / INPUTS
  --name: slug, 1-50 chars, lowercase letters, digits, and hyphens; must start
          and end with a letter or digit (example: my-app).
  --type: one of web|web-ssr|api|sse.
  --region: one of us-east-1|us-east-2|sa-east-1.
  --runtime: required when --type is not web; one of nodejs24|python314|go126.
  --team: team slug or ID; required when the org has multiple teams.
  --wait-interval/--wait-timeout: Go duration strings (examples: 1s, 30s, 5m).

--wait blocks until the domain becomes active (useful for CI pipelines).`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			name, _ := cmd.Flags().GetString("name")
			appType, _ := cmd.Flags().GetString("type")
			region, _ := cmd.Flags().GetString("region")
			runtime, _ := cmd.Flags().GetString("runtime")
			team, _ := cmd.Flags().GetString("team")
			wait, _ := cmd.Flags().GetBool("wait")
			waitInterval, _ := cmd.Flags().GetDuration("wait-interval")
			waitTimeout, _ := cmd.Flags().GetDuration("wait-timeout")

			c, r, err := newAuthenticatedClient(cmd)
			if err != nil {
				return err
			}

			// Client-side validation: --runtime required for non-web app types.
			if appType != "web" && runtime == "" {
				r.RenderError(
					"--runtime is required when --type is not 'web' (valid values: nodejs24|python314|go126)",
					"validation_error",
					-1,
				)
				return &ExitError{Code: output.ExitValidation}
			}

			orgID, err := requireOrgID(cmd.Context(), cmd, c, r)
			if err != nil {
				return err
			}

			teamID, err := resolveTeamID(cmd.Context(), cmd, c, r, orgID, team)
			if err != nil {
				return err
			}

			req := client.CreateAppRequest{
				Name:   name,
				TeamID: teamID,
				Type:   appType,
				Region: region,
			}
			if runtime != "" {
				req.Runtime = &runtime
			}

			app, err := c.CreateApp(cmd.Context(), orgID, req)
			if err != nil {
				return handleAPIError(r, err)
			}

			if wait {
				var finalApp *client.App
				pollErr := pollUntil(cmd.Context(), waitInterval, waitTimeout, func(ctx context.Context) (bool, error) {
					a, gErr := c.GetApp(ctx, orgID, app.ID)
					if gErr != nil {
						return false, gErr
					}
					finalApp = a
					switch a.DomainStatus {
					case "active":
						return true, nil
					case "failed":
						return false, errDomainFailed
					}
					return false, nil
				})
				if pollErr != nil {
					if errors.Is(pollErr, errWaitTimeout) {
						_ = r.RenderError(
							fmt.Sprintf("timed out waiting for domain to become active; check status with 'apps get %s --org <slug>'", app.ID),
							"wait_timeout",
							-1,
						)
						return &ExitError{Code: output.ExitGeneral}
					}
					if errors.Is(pollErr, errDomainFailed) {
						_ = r.RenderError(
							fmt.Sprintf("domain provisioning failed for app %s; check status with 'apps get %s --org <slug>'", app.ID, app.ID),
							"domain_failed",
							-1,
						)
						return &ExitError{Code: output.ExitGeneral}
					}
					return handleAPIError(r, pollErr)
				}
				app = finalApp
			}

			configPath := configFlagOrDefault(cmd)
			suffix := credentials.DomainSuffixFromPath(configPath)
			if err := r.Render(newAppView(app, suffix)); err != nil {
				return &ExitError{Code: output.ExitGeneral}
			}
			return nil
		},
	}

	cmd.Flags().String("name", "", "Application slug: 1-50 lowercase letters, digits, hyphens; starts/ends alphanumeric (required)")
	cmd.Flags().String("type", "", "Application type: one of web|web-ssr|api|sse (required)")
	cmd.Flags().String("region", "", "AWS region: one of us-east-1|us-east-2|sa-east-1 (required)")
	cmd.Flags().String("runtime", "", "Runtime: one of nodejs24|python314|go126; required when --type is not web")
	cmd.Flags().String("team", "", "Team slug or ID; required when org has multiple teams, auto-selected when exactly one team exists")
	cmd.Flags().Bool("wait", false, "Wait for domain to become active before returning")
	cmd.Flags().Duration("wait-interval", 5*time.Second, "Polling interval as a Go duration when --wait is active (e.g. 5s)")
	cmd.Flags().Duration("wait-timeout", 10*time.Minute, "Maximum wait as a Go duration for domain activation (e.g. 10m)")
	return cmd
}

// newAppsDeleteCmd returns `apps delete <app-id> --yes`.
// --yes is required; absence exits 4 so the command is safe in scripts.
func newAppsDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <app-id>",
		Short: "Initiate async deletion of an application (requires --yes)",
		Long: `Initiate asynchronous teardown of an application.

Teardown is asynchronous and may take several minutes. The command returns
immediately after the API accepts the request; it does NOT wait for teardown
to finish. Use 'apps get' to poll teardown status.

VALIDATION / INPUTS
  <app-id>: application ID returned by apps list/create.
  --org: required organization slug unless a default org is configured.
  --yes: required confirmation flag to prevent accidental deletion in scripts.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			yes, _ := cmd.Flags().GetBool("yes")

			c, r, err := newAuthenticatedClient(cmd)
			if err != nil {
				return err
			}

			if !yes {
				r.RenderError(
					fmt.Sprintf("pass --yes to confirm deletion of app %q (teardown is asynchronous and may take several minutes)", args[0]),
					"validation_error",
					-1,
				)
				return &ExitError{Code: output.ExitValidation}
			}

			orgID, err := requireOrgID(cmd.Context(), cmd, c, r)
			if err != nil {
				return err
			}

			resp, err := c.DeleteApp(cmd.Context(), orgID, args[0])
			if err != nil {
				return handleAPIError(r, err)
			}
			if resp.Status == "dispatch_failed" {
				_ = r.RenderError(
					fmt.Sprintf("app %q was marked for deletion but infrastructure teardown failed to start; retry or check the API for status", args[0]),
					"teardown_dispatch_failed",
					-1,
				)
				return &ExitError{Code: output.ExitGeneral}
			}
			if err := r.Render(resp); err != nil {
				return &ExitError{Code: output.ExitGeneral}
			}
			return nil
		},
	}
	cmd.Flags().Bool("yes", false, "Confirm deletion; required to execute async teardown")
	return cmd
}

// appView wraps client.App with a render-time composed URL field.
// URL is set to "https://{subdomain}.{suffix}" when both values are non-empty;
// omitted otherwise. No production suffix is assumed. The client.App fields are promoted to the top level via
// embedding so JSON output is flat and backward-compatible.
type appView struct {
	*client.App
	URL string `json:"url,omitempty"`
}

// newAppView creates an appView from an App and the domain suffix.
func newAppView(app *client.App, suffix string) appView {
	v := appView{App: app}
	if app.Subdomain != "" && suffix != "" {
		v.URL = "https://" + app.Subdomain + "." + suffix
	}
	return v
}
