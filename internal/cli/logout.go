package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/sureva-ch/sureva-cli/internal/client"
	"github.com/sureva-ch/sureva-cli/internal/credentials"
	"github.com/sureva-ch/sureva-cli/internal/output"
)

// clearLogoutToken and newLogoutClient are replaceable in tests so local
// persistence failures and the client wiring can be exercised without relying
// on platform-specific filesystem permissions.
var (
	clearLogoutToken = credentials.ClearToken
	newLogoutClient  = client.New
)

// logoutOutput is the stdout shape for `logout` and `auth logout`.
// Status is "logged_out" or "not_logged_in". Warning is omitted when there is
// nothing to flag; it never contains the token or remote response text.
type logoutOutput struct {
	Status       string `json:"status"`
	ConfigPath   string `json:"config_path"`
	TokenRevoked bool   `json:"token_revoked"`
	Warning      string `json:"warning,omitempty"`
}

// newLogoutCmd returns the logout command shared by `sureva logout` and
// `sureva auth logout`. Each call builds a fresh command because cobra commands
// cannot have two parents.
func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove the saved token and revoke it on the server",
		Long: `Remove the personal access token (PAT) saved in the config file.

The saved token is revoked on the server on a best-effort basis, then removed
from the config file whether or not the revoke succeeded. Other config keys are
kept. Only the saved token is affected: SUREVA_TOKEN is never used or changed.
If it is still set, commands keep authenticating with it until it is unset.

The server never returns raw token values, so the saved token is identified by
its last four characters. It is revoked only when exactly one of your tokens
matches; otherwise the revoke is skipped and the output carries a warning.
Check 'sureva auth token list' and revoke the token manually with
'sureva auth token revoke <token-id>'.

VALIDATION / INPUTS
  No arguments or command-specific flags.
  Config file: --config, or the default config path.

EXIT CODES
  0  logged out, or no saved token (including when the revoke was skipped)
  1  the saved token could not be removed from the config file`,
		Args: cobra.NoArgs,
		RunE: runLogout,
	}
}

func runLogout(cmd *cobra.Command, _ []string) error {
	r := output.NewRenderer(OutputFormat(cmd), cmd.OutOrStdout(), cmd.ErrOrStderr())
	configPath := configFlagOrDefault(cmd)

	saved, err := credentials.SavedTokenFromPath(configPath)
	if err != nil {
		r.RenderError(fmt.Sprintf("failed to read config: %v", err), "config_error", 500)
		return &ExitError{Code: output.ExitGeneral}
	}

	out := logoutOutput{Status: "not_logged_in", ConfigPath: configPath}
	var warnings []string

	if saved != "" {
		revoked, reason := revokeSavedToken(cmd.Context(), configPath, strings.TrimSpace(saved))
		out.TokenRevoked = revoked
		if !revoked {
			warnings = append(warnings, fmt.Sprintf(
				"the token could not be revoked on the server (%s); check 'sureva auth token list' and revoke it manually with 'sureva auth token revoke <token-id>'",
				reason))
		}

		if err := clearLogoutToken(configPath); err != nil {
			r.RenderError(fmt.Sprintf("failed to remove saved token: %v", err), "config_error", 500)
			return &ExitError{Code: output.ExitGeneral}
		}
		out.Status = "logged_out"
	}

	if strings.TrimSpace(credentials.TokenFromEnvironment()) != "" {
		warnings = append(warnings, "SUREVA_TOKEN is still set in the environment; commands keep authenticating with it until it is unset")
	}
	out.Warning = strings.Join(warnings, "; ")

	if err := r.Render(out); err != nil {
		return &ExitError{Code: output.ExitGeneral}
	}
	return nil
}

// revokeSavedToken revokes the server-side token that matches the saved token
// and reports whether it did. It authenticates with the saved token, never with
// SUREVA_TOKEN. The server never returns raw token values, so the match is by
// last four characters and is only trusted when it is unique. The returned
// reason is a fixed phrase: remote response bodies are never reflected, for the
// same reason as handleLoginAPIError.
func revokeSavedToken(ctx context.Context, configPath, saved string) (bool, string) {
	if len(saved) < 4 {
		return false, "the saved token is too short to identify"
	}
	lastFour := saved[len(saved)-4:]

	c := newLogoutClient(credentials.APIBaseURLFromPath(configPath), saved)
	tokens, err := c.ListTokens(ctx)
	if err != nil {
		return false, "the token list could not be fetched"
	}

	var matches []client.Token
	for _, t := range tokens {
		if t.LastFour == lastFour {
			matches = append(matches, t)
		}
	}
	switch len(matches) {
	case 0:
		return false, "no matching token was found"
	case 1:
	default:
		return false, "more than one token matches"
	}

	if err := c.RevokeToken(ctx, matches[0].ID); err != nil {
		return false, "the revoke request failed"
	}
	return true, ""
}
