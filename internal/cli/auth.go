package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/sureva-ch/sureva-cli/internal/client"
	"github.com/sureva-ch/sureva-cli/internal/credentials"
	"github.com/sureva-ch/sureva-cli/internal/output"
)

// NewAuthCmd returns the `auth` parent command with its subcommands wired in.
func NewAuthCmd() *cobra.Command {
	auth := &cobra.Command{
		Use:   "auth",
		Short: "Manage authentication and personal access tokens",
		Long: `Commands for authentication and personal access token (PAT) management.

AGENT USAGE
  For CI and agents, obtain an existing PAT and set SUREVA_TOKEN:
    export SUREVA_TOKEN=sapi_<hex>

  Interactive users should run sureva login. sureva auth login is an advanced
  fallback that imports and verifies an existing PAT.`,
	}

	auth.AddCommand(newAuthLoginCmd())
	auth.AddCommand(newAuthWhoamiCmd())
	auth.AddCommand(newAuthTokenCmd())
	return auth
}

type loginOutput struct {
	Status     string           `json:"status"`
	ConfigPath string           `json:"config_path"`
	User       *client.AuthUser `json:"user"`
}

// newAuthLoginCmd returns the `auth login` command (spec B-04).
// Until browser device-flow exists in cloud-api, this command bootstraps login
// from a PAT, verifies it with /auth/me, then persists it to the config file.
func newAuthLoginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with a personal access token",
		Long: `Authenticate the CLI by saving a personal access token (PAT) to the config file.

The token is verified with /v1/auth/me before it is persisted.

Examples:
  printf '%s' "$SUREVA_TOKEN" | sureva auth login --token-stdin

This advanced fallback does not issue a PAT; provide an existing PAT to import
and verify. For normal interactive authentication, run sureva login.

VALIDATION / INPUTS
  Token source: exactly one of --token-stdin, hidden --token, or SUREVA_TOKEN.
  PAT format: backend-issued personal access token, usually sapi_<hex>.
  Verification: token must pass /v1/auth/me before it is saved.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r := output.NewRenderer(OutputFormat(cmd), cmd.OutOrStdout(), cmd.ErrOrStderr())

			token, err := loginTokenFromFlagsOrEnv(cmd)
			if err != nil {
				r.RenderError(err.Error(), "validation_error", -1)
				return &ExitError{Code: output.ExitValidation}
			}

			configPath := configFlagOrDefault(cmd)
			c := client.New(credentials.APIBaseURLFromPath(configPath), token)
			user, err := c.Whoami(cmd.Context())
			if err != nil {
				return handleAPIError(r, err)
			}

			if err := credentials.SaveToken(configPath, token); err != nil {
				r.RenderError(fmt.Sprintf("failed to save token: %v", err), "config_error", 500)
				return &ExitError{Code: output.ExitGeneral}
			}

			if err := r.Render(loginOutput{
				Status:     "authenticated",
				ConfigPath: configPath,
				User:       user,
			}); err != nil {
				return &ExitError{Code: output.ExitGeneral}
			}
			return nil
		},
	}
	cmd.Flags().String("token", "", "Personal access token to verify and save (prefer --token-stdin; command arguments can be visible in shell history)")
	_ = cmd.Flags().MarkHidden("token")
	cmd.Flags().Bool("token-stdin", false, "Read one personal access token from stdin; safer than passing secrets as arguments")
	return cmd
}

func loginTokenFromFlagsOrEnv(cmd *cobra.Command) (string, error) {
	token, _ := cmd.Flags().GetString("token")
	token = strings.TrimSpace(token)

	tokenStdin, _ := cmd.Flags().GetBool("token-stdin")
	if token != "" && tokenStdin {
		return "", fmt.Errorf("use either --token or --token-stdin, not both")
	}
	if tokenStdin {
		data, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", fmt.Errorf("read token from stdin: %w", err)
		}
		token = strings.TrimSpace(string(data))
	}
	if token == "" {
		token = strings.TrimSpace(credentials.TokenFromEnvironment())
	}
	if token == "" {
		return "", fmt.Errorf("provide a personal access token with --token, --token-stdin, or SUREVA_TOKEN")
	}
	return token, nil
}

// newAuthWhoamiCmd returns the `auth whoami` command.
func newAuthWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the identity of the currently authenticated user",
		Long: `Show the identity associated with the current token.

VALIDATION / INPUTS
  Authentication: requires a valid saved token or SUREVA_TOKEN.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, r, err := newAuthenticatedClient(cmd)
			if err != nil {
				return err
			}
			user, err := c.Whoami(cmd.Context())
			if err != nil {
				return handleAPIError(r, err)
			}
			if err := r.Render(user); err != nil {
				return &ExitError{Code: output.ExitGeneral}
			}
			return nil
		},
	}
}

// newAuthTokenCmd returns the `auth token` parent command.
func newAuthTokenCmd() *cobra.Command {
	token := &cobra.Command{
		Use:   "token",
		Short: "Manage personal access tokens (PATs)",
	}
	token.AddCommand(newAuthTokenCreateCmd())
	token.AddCommand(newAuthTokenListCmd())
	token.AddCommand(newAuthTokenRevokeCmd())
	return token
}

// createTokenOutput is the stdout shape for `auth token create`.
// The Token field holds the raw PAT value; the Warning field reminds the user
// to store it immediately (the raw value is never shown again).
type createTokenOutput struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Token     string     `json:"token"`
	LastFour  string     `json:"last_four"`
	Status    string     `json:"status"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	Warning   string     `json:"warning"`
}

// newAuthTokenCreateCmd returns the `auth token create` command (spec B-04a).
func newAuthTokenCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a personal access token (shown once)",
		Long: `Create a personal access token (PAT) for headless authentication.

The raw token (sapi_<hex>) is printed exactly once in the JSON response.
Store it immediately — it cannot be retrieved again.
This command requires an existing authenticated PAT and cannot issue the first PAT.

VALIDATION / INPUTS
  --name: human-readable label for the token (example: ci-deploy).
  --expires-at: optional RFC 3339 timestamp (example: 2026-12-31T00:00:00Z).

EXIT CODES
  0  token created
  2  not authenticated
  4  validation error (e.g. invalid expires_at format)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _ := cmd.Flags().GetString("name")
			expiresStr, _ := cmd.Flags().GetString("expires-at")

			c, r, err := newAuthenticatedClient(cmd)
			if err != nil {
				return err
			}

			var expiresAt *time.Time
			if expiresStr != "" {
				t, parseErr := time.Parse(time.RFC3339, expiresStr)
				if parseErr != nil {
					r.RenderError(
						fmt.Sprintf("invalid --expires-at value %q: must be RFC 3339 (e.g. 2026-12-31T00:00:00Z)", expiresStr),
						"validation_error",
						-1,
					)
					return &ExitError{Code: output.ExitValidation}
				}
				expiresAt = &t
			}

			resp, err := c.CreateToken(cmd.Context(), name, expiresAt)
			if err != nil {
				return handleAPIError(r, err)
			}

			out := createTokenOutput{
				ID:        resp.ID,
				Name:      resp.Name,
				Token:     resp.Token,
				LastFour:  resp.LastFour,
				Status:    resp.Status,
				ExpiresAt: resp.ExpiresAt,
				CreatedAt: resp.CreatedAt,
				Warning:   "Token shown once — store it securely. It cannot be retrieved again.",
			}
			if err := r.Render(out); err != nil {
				return &ExitError{Code: output.ExitGeneral}
			}
			return nil
		},
	}
	cmd.Flags().String("name", "cli-token", "Human-readable token label (e.g. ci-deploy)")
	cmd.Flags().String("expires-at", "", "Token expiry in RFC 3339 format (e.g. 2026-12-31T00:00:00Z); omit for no expiry")
	return cmd
}

// newAuthTokenListCmd returns the `auth token list` command (spec A-03).
func newAuthTokenListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List personal access tokens owned by the current user",
		Long: `List personal access tokens owned by the current user.

VALIDATION / INPUTS
  Authentication: requires a valid saved token or SUREVA_TOKEN.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, r, err := newAuthenticatedClient(cmd)
			if err != nil {
				return err
			}
			tokens, err := c.ListTokens(cmd.Context())
			if err != nil {
				return handleAPIError(r, err)
			}
			if err := r.Render(tokens); err != nil {
				return &ExitError{Code: output.ExitGeneral}
			}
			return nil
		},
	}
}

// newAuthTokenRevokeCmd returns the `auth token revoke <id>` command (spec A-04).
func newAuthTokenRevokeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <token-id>",
		Short: "Revoke a personal access token",
		Long: `Revoke a personal access token.

VALIDATION / INPUTS
  <token-id>: token ID returned by auth token list/create.
  Authentication: requires a valid saved token or SUREVA_TOKEN.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, r, err := newAuthenticatedClient(cmd)
			if err != nil {
				return err
			}
			if err := c.RevokeToken(cmd.Context(), args[0]); err != nil {
				return handleAPIError(r, err)
			}
			if err := r.Render(map[string]string{"message": "token revoked"}); err != nil {
				return &ExitError{Code: output.ExitGeneral}
			}
			return nil
		},
	}
}
