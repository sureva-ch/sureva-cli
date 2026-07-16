# Changelog

All notable changes to the sureva CLI are documented in this file.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

## [Unreleased]

## [0.10.1] - 2026-07-15

### Fixed

- `apps create --region` help and README now list the current supported regions
  `eu-central-1` and `eu-central-2`. They previously advertised the retired
  `us-east-1|us-east-2|sa-east-1` regions, which the API rejects.

### Added

- `apps create` now validates `--region` client-side, failing fast with a clear
  validation error when the region is missing or unsupported (mirrors the
  existing `--runtime` check) instead of deferring to a server-side rejection.

### Added

- `sureva login` is the primary authentication path, using Cognito Managed
  Login with Authorization Code + PKCE, loopback callbacks on ports 8976–8978,
  PAT minting through `/v1/auth/tokens`, and validation before atomic storage.
- Added an idempotent, fail-closed provisioning script and operator guide for
  the dedicated public `sureva-cli` Cognito app client.

### Changed

- App URLs are composed only when `SUREVA_DOMAIN_SUFFIX` or `domain_suffix` is
  configured, avoiding assumptions about production application domains.
- `sureva auth login` remains available as the advanced existing-PAT import
  fallback for CI and headless environments.

## [0.9.0] - 2026-07-02

### Added

- `changes` visual report now shows the repository name as the report title and includes repository metadata in machine-readable output.
- `changes` readiness checklist now detects database migrations.
- `changes` visual checklist items can be toggled as graph filters, making it possible to focus the file graph on tests, migrations, docs, source code, or changelog entries.

### Changed

- `changes` diff panel now relies on red/green line color for deleted/added lines and hides redundant leading diff markers.

### Fixed

- `changes` visual checklist filters now hide SVG nodes and edges reliably and keep filter controls aligned with the rest of the sidebar UI.

## [0.1.0] - 2026-06-12

Initial public release. Includes the full agent-first command surface: `auth` (whoami, token create/list/revoke), `orgs list`, `teams list`, `apps` (list, get, create, delete), `env` (get, set), `deploys` (trigger, list, status), `logs`, JSON-first output with `--output table`, machine-readable `--help --json`, and the 0–5 exit-code contract.

### Added

- `teams list --org <slug>` — list all teams in an organization; output defaults to JSON, supports `--output table`.
- `apps create` — create a new application with flags `--name`, `--type`, `--region`, `--runtime`, `--team`. Team is auto-selected when the org has exactly one team; `--team <slug-or-id>` required when multiple teams exist. `--runtime` is required for non-web app types (`api`, `web-ssr`, `sse`). Missing `--runtime` exits 4 before any API call.
- `apps create --wait` — poll `domain_status` until `active`; timeout exits 1 with `code: wait_timeout`, domain provisioning failure exits 1 with `code: domain_failed`. Configurable with `--wait-interval` (default 5s) and `--wait-timeout` (default 10m).
- `apps delete <app-id> --yes` — initiate async teardown; `--yes` is required (exit 4 without it). 202 response renders `{"status":"deleting"}` and never claims the app has been removed synchronously.
- `apps list` now includes the composed `url` field (same `https://{subdomain}.{suffix}` logic as `apps get`).
- `apps get` includes a composed `url` field when the app has a non-null
  subdomain and a domain suffix is explicitly configured.
- `apps delete` now exits 1 with `code: teardown_dispatch_failed` when the API returns `{"status":"dispatch_failed"}` on 202, indicating the app was marked for deletion but infrastructure teardown failed to start. Normal dispatch (`{"status":"deleting"}`) still exits 0.
- `deploys trigger --wait` — poll deployment status until a terminal state (`success`, `failed`, `cancelled`); success exits 0, non-success exits 1 with `code: deploy_failed`, timeout exits 1 with `code: wait_timeout`. Configurable with `--wait-interval` (default 5s) and `--wait-timeout` (default 15m).
