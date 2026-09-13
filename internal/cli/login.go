package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/sureva-ch/sureva-cli/internal/authflow"
	"github.com/sureva-ch/sureva-cli/internal/client"
	"github.com/sureva-ch/sureva-cli/internal/credentials"
	"github.com/sureva-ch/sureva-cli/internal/output"
)

// runAuthFlow runs one browser login attempt. It is a package-level var so
// tests can override it with a stub that injects a fake BrowserOpener and
// HTTPClient into authflow.Config — no real browser or Cognito server is
// ever exercised by this package's own tests.
var runAuthFlow = authflow.Run

// saveLoginToken is replaceable in tests so persistence failures can be
// exercised without relying on platform-specific filesystem permissions.
var saveLoginToken = credentials.SaveToken

// NewLoginCmd returns the `sureva login` command: browser-based
// Authorization Code + PKCE login against Cognito, minting a PAT on success.
// This is the primary interactive login path. `auth login` remains the
// advanced PAT-import fallback —
// see NewAuthCmd.
func NewLoginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Authenticate via browser login (Cognito Authorization Code + PKCE)",
		Long: `Authenticate by opening a browser to Cognito's hosted UI, then mint and save a
personal access token (PAT).

Re-running this command overwrites the stored token only after a new PAT is
minted successfully; a failed attempt never alters the existing stored token.

VALIDATION / INPUTS
  Cognito configuration: resolved via SUREVA_COGNITO_DOMAIN /
  SUREVA_COGNITO_CLIENT_ID env vars, then the "cognito_domain" /
  "cognito_client_id" config keys, then the build-time default. The client
  id must be provisioned (see docs/cognito-cli-client.md); an empty client
  id fails fast before any browser or network call.

EXIT CODES
  0  authenticated
  1  all loopback callback ports busy, or callback timeout
  2  identity provider error, code exchange failure, or invalid callback
  4  state (CSRF) mismatch, or Cognito client id not configured
  *  PAT minting or /auth/me failure — mapped via the existing API error codes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r := output.NewRenderer(OutputFormat(cmd), cmd.OutOrStdout(), cmd.ErrOrStderr())

			configPath := configFlagOrDefault(cmd)
			cognitoDomain := credentials.CognitoDomainFromPath(configPath)
			clientID := credentials.CognitoClientIDFromPath(configPath)
			if clientID == "" {
				r.RenderError(
					"cognito client id not configured — this build was not provisioned with a Cognito app client (see docs/cognito-cli-client.md)",
					"validation_error",
					-1,
				)
				return &ExitError{Code: output.ExitValidation}
			}

			result, err := runAuthFlow(cmd.Context(), authflow.Config{
				CognitoDomain: cognitoDomain,
				ClientID:      clientID,
				Writer:        cmd.ErrOrStderr(),
			})
			if err != nil {
				return mapAuthFlowError(r, err)
			}

			apiBaseURL := credentials.APIBaseURLFromPath(configPath)
			hostname, hostErr := os.Hostname()
			if hostErr != nil || hostname == "" {
				hostname = "unknown"
			}

			mintClient := client.New(apiBaseURL, result.IDToken)
			tokenResp, err := mintClient.CreateToken(cmd.Context(), "cli-"+hostname, nil)
			if err != nil {
				return handleLoginAPIError(r, err, "failed to create a personal access token")
			}

			whoamiClient := client.New(apiBaseURL, tokenResp.Token)
			user, err := whoamiClient.Whoami(cmd.Context())
			if err != nil {
				cleanupMintedToken(cmd.Context(), whoamiClient, tokenResp.ID)
				return handleLoginAPIError(r, err, "created token failed validation")
			}

			// Persist only after both minting and PAT validation succeed. This
			// preserves an existing credential on every failed re-login path.
			if err := saveLoginToken(configPath, tokenResp.Token); err != nil {
				cleanupMintedToken(cmd.Context(), whoamiClient, tokenResp.ID)
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
}

// cleanupMintedToken revokes a PAT that was created but could not be validated
// or persisted. Cleanup is deliberately best-effort: the original error is the
// actionable failure and must never be hidden by a secondary revoke failure.
func cleanupMintedToken(ctx context.Context, patClient *client.Client, tokenID string) {
	if patClient == nil || tokenID == "" {
		return
	}
	_ = patClient.RevokeToken(ctx, tokenID)
}

// handleLoginAPIError preserves the standard API status/exit mapping without
// reflecting remote response bodies. The login request carries short-lived or
// newly minted credentials, so its error surface must remain secret-free even
// if a proxy or upstream service returns unsafe text.
func handleLoginAPIError(r *output.Renderer, err error, message string) error {
	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		code := r.RenderError(message, apiErr.Code, apiErr.HTTPStatus)
		return &ExitError{Code: code}
	}
	r.RenderError(message, "general_error", 0)
	return &ExitError{Code: output.ExitGeneral}
}

// mapAuthFlowError maps authflow.Run's typed errors to the CLI's exit-code
// contract using errors.As (never string matching or type equality), per
// the design's Error Taxonomy. CallbackError is deliberately bucketed with
// IdPError/ExchangeError as auth_error/exit 2 — a malformed callback is an
// auth-flow failure, not a client-side validation error. Exit-1 rows follow
// the existing config_error pattern: httpStatus 500 is cosmetic (Renderer's
// own mapping also yields exit 1), and the explicit *ExitError governs the
// process exit.
func mapAuthFlowError(r *output.Renderer, err error) error {
	var portsBusy *authflow.PortsBusyError
	if errors.As(err, &portsBusy) {
		r.RenderError(err.Error(), "port_error", 500)
		return &ExitError{Code: output.ExitGeneral}
	}

	var timeoutErr *authflow.TimeoutError
	if errors.As(err, &timeoutErr) {
		r.RenderError(err.Error(), "timeout_error", 500)
		return &ExitError{Code: output.ExitGeneral}
	}

	var idpErr *authflow.IdPError
	if errors.As(err, &idpErr) {
		r.RenderError(err.Error(), "auth_error", 401)
		return &ExitError{Code: output.ExitAuth}
	}

	var exchangeErr *authflow.ExchangeError
	if errors.As(err, &exchangeErr) {
		r.RenderError(err.Error(), "auth_error", 401)
		return &ExitError{Code: output.ExitAuth}
	}

	var callbackErr *authflow.CallbackError
	if errors.As(err, &callbackErr) {
		r.RenderError(err.Error(), "auth_error", 401)
		return &ExitError{Code: output.ExitAuth}
	}

	var stateMismatch *authflow.StateMismatchError
	if errors.As(err, &stateMismatch) {
		r.RenderError(err.Error(), "validation_error", -1)
		return &ExitError{Code: output.ExitValidation}
	}

	// Unreachable in practice — Run only ever returns the six typed errors
	// above (see authflow.Run's doc comment) — but handled defensively,
	// mirroring handleAPIError's own fallback for unexpected errors.
	r.RenderError(err.Error(), "general_error", 0)
	return &ExitError{Code: output.ExitGeneral}
}
