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
SUREVA_COGNITO_CLIENT_ID=64e3vqqstenra0h3o92986tit6 \
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

| `ENVIRONMENT` | User pool | Client name | Login domain |
|---|---|---|---|
| `prod` | `eu-central-2_NcwrZjuL3` | `sureva-cli` | `auth.sureva.com` |
| `dev` | `eu-central-2_UR0k0FVwr` | `sureva-cli-dev` | `auth.dev.sureva.com` |

### Callback URLs are matched as exact strings

The client must register `http://127.0.0.1:{8976,8977,8978}/callback` — the
three ports `internal/authflow` binds, and the literal host
`internal/authflow/run.go` puts in `redirect_uri`. Cognito compares
`redirect_uri` byte-for-byte, so registering `localhost` instead of `127.0.0.1`
fails **every** login with `error=redirect_mismatch`, even though the two
resolve to the same address. The dev client was initially registered with
`localhost` and hit exactly this.

## Conventions

- `go build ./...`, `go test ./...` before finishing.
- Conventional commits. No AI attribution in commit messages.
- Never print or pass secrets as command arguments — argv is visible in `ps`.
- Generated artifacts default to English.
