# Provision the Sureva CLI Cognito client

`sureva login` requires a dedicated **public, secretless** Cognito app client. The repository includes an idempotent provisioning script that runs against either environment.

## Quick path

1. Authenticate the AWS CLI to account `255398768146` with permission to manage Cognito app clients and Managed Login branding.
2. Run `ENVIRONMENT=prod scripts/provision-cognito-cli-client.sh` (or `ENVIRONMENT=dev`).
3. Set the repository Actions variable `SUREVA_COGNITO_CLIENT_ID` to the printed public client ID.

`ENVIRONMENT` has no default: running the script without it exits non-zero
rather than targeting production. `infra/lib/config.sh` maps it to the pool,
client name and login domain:

| `ENVIRONMENT` | User pool | Client name | Login domain |
|---|---|---|---|
| `prod` | `eu-central-2_NcwrZjuL3` | `sureva-cli` | `auth.sureva.com` |
| `dev` | `eu-central-2_UR0k0FVwr` | `sureva-cli-dev` | `auth.dev.sureva.com` |

The script fails closed unless it finds the expected account, region `eu-central-2`, the environment's user pool, and its active login domain. It never prints a token or client secret.

## Pointing the CLI at a non-production environment

The CLI already resolves every environment-specific value at runtime, so no
build or profile switch is needed:

| Variable | Selects | Default |
|---|---|---|
| `SUREVA_API_URL` | API base URL | `https://api.sureva.com` |
| `SUREVA_COGNITO_DOMAIN` | Hosted UI domain | config file `cognito_domain` |
| `SUREVA_COGNITO_CLIENT_ID` | Public app client | build-time value |

```sh
SUREVA_API_URL=https://api.dev.sureva.com \
SUREVA_COGNITO_DOMAIN=https://auth.dev.sureva.com \
SUREVA_COGNITO_CLIENT_ID=64e3vqqstenra0h3o92986tit6 \
  sureva login
```

Each takes precedence over the config file, which takes precedence over the
compiled-in default.

### Callback URLs are matched as exact strings

The client must register `http://127.0.0.1:{8976,8977,8978}/callback` — the
three ports `internal/authflow` binds, and the literal host
`internal/authflow/run.go` puts in `redirect_uri`. Cognito compares
`redirect_uri` byte-for-byte, so registering `localhost` instead of `127.0.0.1`
fails every login with `error=redirect_mismatch` even though the two resolve to
the same address. The dev client was initially registered with `localhost` and
hit exactly this.

If PAT validation or local persistence fails after minting, `sureva login`
attempts to revoke the new PAT without replacing any existing local token. A
response-loss immediately after server-side creation remains ambiguous because
the CLI has not received the token ID needed for revocation; investigate and
revoke any orphaned token during incident cleanup.

## Required client contract

| Setting | Value |
|---|---|
| Name | `sureva-cli` |
| Client type | Public; `GenerateSecret=false` |
| OAuth grant | Authorization code only |
| PKCE | S256, enforced by the CLI flow |
| Scopes | `openid email profile` |
| Identity provider | `COGNITO` |
| Callbacks | `http://127.0.0.1:8976/callback`, `http://127.0.0.1:8977/callback`, `http://127.0.0.1:8978/callback` |
| Token revocation | Enabled |
| Managed Login | Cognito-provided default branding |

`AllowedOAuthFlowsUserPoolClient` must be enabled or Cognito ignores the callback, scopes, and OAuth flow configuration. API-created clients do not receive Managed Login branding automatically, so the script explicitly creates or updates the branding with Cognito-provided values.

## Runtime trust boundary

After Cognito returns an authorization code, the CLI exchanges it with PKCE for an `id_token`. It sends that token only as the bearer credential for `POST /v1/auth/tokens`. The API validates the user-pool issuer, signature, expiry, and `token_use`, then returns a PAT. The CLI validates that PAT through `GET /v1/auth/me` before atomically replacing the saved token.

The API currently accepts valid ID tokens from any app client in this user pool. Restricting the accepted audience to the dedicated `sureva-cli` client is recommended future hardening, but is intentionally outside this CLI change.

## Verification checklist

- The client description contains no `ClientSecret`.
- The three callback URLs match exactly; wildcard and non-loopback callbacks are absent.
- A release fails before packaging when `SUREVA_COGNITO_CLIENT_ID` is empty.
- `sureva login` completes, while a failed re-login preserves the previous PAT.
