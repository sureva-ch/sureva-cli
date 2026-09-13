# AGENTS.md

Bootstrap guidance for AI coding agents working in this repository. It covers
the environment contract only — it is not a full architecture document.

## Pointing the CLI at a non-production environment

No build or profile switch is needed. The CLI resolves every
environment-specific value at runtime, each taking precedence over the config
file, which takes precedence over the compiled-in default:

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

## Provisioning the Cognito app client

`scripts/provision-cognito-cli-client.sh` derives the pool, client name and
login domain from `$ENVIRONMENT` via `infra/lib/config.sh`. **It has no
default** — running without it exits non-zero rather than targeting production.

```sh
ENVIRONMENT=prod bash scripts/provision-cognito-cli-client.sh
ENVIRONMENT=dev  bash scripts/provision-cognito-cli-client.sh
```

| `ENVIRONMENT` | User pool | Client name | Client ID | Login domain | Managed Login |
|---|---|---|---|---|---|
| `prod` | `us-east-2_cpg7ZyK2M` | `sureva-cli` | `iugfo9d24630c3i0e03dr52ag` | `auth.sureva.com` | version 2 |
| `dev` | `us-east-2_DRIUL20UO` | `sureva-cli-dev` | `3aochit9b7f1f58m0c1cgffa1k` | `auth.dev.sureva.com` | version 1 |

Region is `us-east-2`. The `eu-central-2` pools were retired in the 2026-08-24
cutover and no longer exist.

`update-user-pool-client` **replaces** the whole client; every omitted field
resets to its default. The script builds updates from a
`describe-user-pool-client` snapshot. Never hand-run a partial update.

### Managed Login version 2 needs a branding style per client

On a version 2 domain (prod), a client without a branding style shows "Login
pages unavailable" instead of the sign-in page, while `/oauth2/authorize`
still answers `302` to `/login`, so `curl` cannot detect it. The script
creates a style with Cognito-provided values only when none exists, and never
modifies an existing one.

### Callback URLs are matched as exact strings

The client must register `http://127.0.0.1:{8976,8977,8978}/callback` — the
three ports `internal/authflow` binds, and the literal host
`internal/authflow/run.go` puts in `redirect_uri`. Cognito compares
`redirect_uri` byte-for-byte, so registering `localhost` instead of `127.0.0.1`
fails **every** login with `error=redirect_mismatch`, even though the two
resolve to the same address. The dev client was initially registered with
`localhost` and hit exactly this, and so did both clients after the 2026-08-24
cutover (issue #1). `internal/authflow/provision_script_test.go` fails
`go test` if the script's callback list drifts from `authflow.DefaultPorts`.

## Conventions

- `go build ./...`, `go test ./...` before finishing.
- Conventional commits. No AI attribution in commit messages.
- Never print or pass secrets as command arguments — argv is visible in `ps`.
- Generated artifacts default to English.
