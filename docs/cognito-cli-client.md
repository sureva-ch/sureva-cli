# The Sureva CLI Cognito client

`sureva login` requires a dedicated **public, secretless** Cognito app client. The provisioning script is not kept in this repository; this page records the contract the client must meet.

The client exists once per environment. A release build takes its client ID from the repository Actions variable `SUREVA_COGNITO_CLIENT_ID`.

| `ENVIRONMENT` | User pool | Client name | Client ID | Login domain | Managed Login |
|---|---|---|---|---|---|
| `prod` | `us-east-2_cpg7ZyK2M` | `sureva-cli` | `iugfo9d24630c3i0e03dr52ag` | `auth.sureva.com` | version 2 |
| `dev` | `us-east-2_DRIUL20UO` | `sureva-cli-dev` | `3aochit9b7f1f58m0c1cgffa1k` | `auth.dev.sureva.com` | version 1 |

The identity moved from `eu-central-2` to `us-east-2` on 2026-08-24; the
`eu-central-2` pools no longer exist.

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
SUREVA_COGNITO_CLIENT_ID=3aochit9b7f1f58m0c1cgffa1k \
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
hit exactly this, and so did both clients after the 2026-08-24 cutover
(issue #1).

Register exactly these three URLs and remove any other callback, such as a
leftover `http://localhost:8976/callback`. They must match
`authflow.DefaultPorts`; nothing in this repository checks the registered list.

If PAT validation or local persistence fails after minting, `sureva login`
attempts to revoke the new PAT without replacing any existing local token. A
response-loss immediately after server-side creation remains ambiguous because
the CLI has not received the token ID needed for revocation; investigate and
revoke any orphaned token during incident cleanup.

## Required client contract

| Setting | Value |
|---|---|
| Name | `sureva-cli` (`sureva-cli-dev` in dev) |
| Client type | Public; `GenerateSecret=false` |
| OAuth grant | Authorization code only |
| PKCE | S256, enforced by the CLI flow |
| Scopes | `openid email profile` |
| Identity providers | `COGNITO`, plus the pool's Google provider when the pool has one |
| Callbacks | `http://127.0.0.1:8976/callback`, `http://127.0.0.1:8977/callback`, `http://127.0.0.1:8978/callback` |
| Token revocation | Enabled |
| Managed Login | A branding style for the client when the domain uses Managed Login version 2 |

`AllowedOAuthFlowsUserPoolClient` must be enabled or Cognito ignores the callback, scopes, and OAuth flow configuration.

`update-user-pool-client` replaces the whole client and resets every omitted field to its default. When the client already exists, build the update from a `describe-user-pool-client` snapshot and override only the fields in this table.

### Managed Login version 2 needs a branding style per client

Under Managed Login version 2, an app client without a branding style shows "Login pages unavailable. Please contact an administrator." instead of the sign-in page. `curl` does not reveal this: `/oauth2/authorize` still answers `302` to `/login`. API-created clients get no style automatically.

When `describe-user-pool-domain` reports `ManagedLoginVersion: 2`, check `describe-managed-login-branding-by-client`. If that returns `ResourceNotFoundException`, create a style with `--use-cognito-provided-values`. Never modify an existing style, because it may carry custom branding. Version 1 domains (classic hosted UI) need no style.

## Runtime trust boundary

After Cognito returns an authorization code, the CLI exchanges it with PKCE for an `id_token`. It sends that token only as the bearer credential for `POST /v1/auth/tokens`. The API validates the user-pool issuer, signature, expiry, and `token_use`, then returns a PAT. The CLI validates that PAT through `GET /v1/auth/me` before atomically replacing the saved token.

The API currently accepts valid ID tokens from any app client in this user pool. Restricting the accepted audience to the dedicated `sureva-cli` client is recommended future hardening, but is intentionally outside this CLI change.

## Verification checklist

- The client description contains no `ClientSecret`.
- The three callback URLs match exactly; wildcard and non-loopback callbacks are absent.
- On a Managed Login version 2 domain, `describe-managed-login-branding-by-client` returns a style for the client.
- A release fails before packaging when `SUREVA_COGNITO_CLIENT_ID` is empty.
- `sureva login` completes, while a failed re-login preserves the previous PAT.
