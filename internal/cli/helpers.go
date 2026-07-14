package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/sureva-ch/sureva-cli/internal/client"
	"github.com/sureva-ch/sureva-cli/internal/credentials"
	"github.com/sureva-ch/sureva-cli/internal/output"
)

// ExitError is returned by command RunE to signal a specific exit code.
// Commands write the error to stderr via the Renderer before returning ExitError
// so main.go calls os.Exit without printing again.
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("exit status %d", e.Code)
}

// configFlagOrDefault returns the --config flag value, falling back to
// credentials.DefaultConfigPath() when the flag is absent or empty.
func configFlagOrDefault(cmd *cobra.Command) string {
	if path, _ := cmd.Root().PersistentFlags().GetString("config"); path != "" {
		return path
	}
	return credentials.DefaultConfigPath()
}

// newAuthenticatedClient resolves credentials and returns a typed API client
// together with a Renderer that writes to cmd's stdout/stderr.
//
// On credential failure it writes an auth-error envelope to stderr and returns
// a non-nil error (an *ExitError with Code=ExitAuth). The caller must return
// that error from RunE without further action.
func newAuthenticatedClient(cmd *cobra.Command) (*client.Client, *output.Renderer, error) {
	r := output.NewRenderer(OutputFormat(cmd), cmd.OutOrStdout(), cmd.ErrOrStderr())

	configPath := configFlagOrDefault(cmd)
	token, err := credentials.ResolveWithConfigPath(configPath)
	if err != nil {
		r.RenderError(
			"no credentials found — run 'sureva login', set SUREVA_TOKEN, or import an existing PAT with 'sureva auth login --token-stdin'",
			"auth_error",
			401,
		)
		return nil, r, &ExitError{Code: output.ExitAuth}
	}

	baseURL := credentials.APIBaseURLFromPath(configPath)
	c := client.New(baseURL, token)
	return c, r, nil
}

// handleAPIError renders an *client.APIError to stderr and returns an *ExitError
// with the appropriate exit code. For unexpected non-API errors it falls back to
// ExitGeneral.
func handleAPIError(r *output.Renderer, err error) error {
	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		code := r.RenderError(apiErr.Message, apiErr.Code, apiErr.HTTPStatus)
		return &ExitError{Code: code}
	}
	// Unexpected error (e.g. JSON decode bug) — treat as general.
	r.RenderError(err.Error(), "general_error", 0)
	return &ExitError{Code: output.ExitGeneral}
}

// requireOrgID resolves the org slug from --org flag (or config default) and
// returns the org UUID by calling ListOrgs. It renders a validation error and
// returns an *ExitError when the slug is absent or not found.
func requireOrgID(ctx context.Context, cmd *cobra.Command, c *client.Client, r *output.Renderer) (string, error) {
	slug, _ := cmd.Root().PersistentFlags().GetString("org")
	if slug == "" {
		slug = credentials.DefaultOrgSlugFromPath(configFlagOrDefault(cmd))
	}
	if slug == "" {
		r.RenderError(
			"org is required — specify --org <slug> or set 'org' in ~/.config/sureva/config.yaml",
			"validation_error",
			-1,
		)
		return "", &ExitError{Code: output.ExitValidation}
	}

	orgs, err := c.ListOrgs(ctx)
	if err != nil {
		return "", handleAPIError(r, err)
	}
	for _, o := range orgs {
		if o.Slug == slug {
			return o.ID, nil
		}
	}

	r.RenderError(fmt.Sprintf("org %q not found", slug), "not_found", 404)
	return "", &ExitError{Code: output.ExitNotFound}
}

// resolveTeamID returns the team UUID to use for app creation.
//
// Resolution order:
//  1. explicit: --team by slug or ID match against ListTeams
//  2. auto: org has exactly one team → auto-select its ID
//  3. fail: zero teams → exit 4 with "create a team" guidance
//  4. fail: multiple teams → exit 4 listing available slugs and requiring --team
func resolveTeamID(ctx context.Context, _ *cobra.Command, c *client.Client, r *output.Renderer, orgID, explicitTeam string) (string, error) {
	teams, err := c.ListTeams(ctx, orgID)
	if err != nil {
		return "", handleAPIError(r, err)
	}

	if explicitTeam != "" {
		for _, tm := range teams {
			if tm.Slug == explicitTeam || tm.ID == explicitTeam {
				return tm.ID, nil
			}
		}
		r.RenderError(
			fmt.Sprintf("team %q not found; available: %s", explicitTeam, joinTeamSlugs(teams)),
			"validation_error",
			-1,
		)
		return "", &ExitError{Code: output.ExitValidation}
	}

	switch len(teams) {
	case 0:
		r.RenderError(
			"org has no teams; create a team in the web UI before creating apps",
			"validation_error",
			-1,
		)
		return "", &ExitError{Code: output.ExitValidation}
	case 1:
		return teams[0].ID, nil
	default:
		r.RenderError(
			fmt.Sprintf("org has multiple teams; specify one with --team (available: %s)", joinTeamSlugs(teams)),
			"validation_error",
			-1,
		)
		return "", &ExitError{Code: output.ExitValidation}
	}
}

// joinTeamSlugs returns a comma-separated list of team slugs for error messages.
func joinTeamSlugs(teams []client.Team) string {
	slugs := make([]string, len(teams))
	for i, tm := range teams {
		slugs[i] = tm.Slug
	}
	return strings.Join(slugs, ", ")
}
