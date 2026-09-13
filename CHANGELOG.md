# Changelog

All notable changes to the sureva CLI are documented in this file.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

## [Unreleased]

## [0.1.1] - 2026-09-13

### Fixed

- Release binaries now embed the production Cognito app client, so
  `sureva login` works without configuration. The 0.1.0 binaries embedded a
  client from a retired user pool.
- The Cognito provisioning configuration now targets the `us-east-2` user pools
  that replaced the retired `eu-central-2` pools (#1).
- The provisioning script creates a Managed Login branding style only when the
  domain uses Managed Login version 2 and the client has none. It no longer
  overwrites an existing style, and it fails on branding lookup errors other
  than "not found" instead of treating them as absence.
- `go test` now fails if the provisioning script's callback URLs drift from
  the loopback ports the CLI binds.

## [0.1.0] - 2026-09-13

Initial public release.

### Added

- `sureva login` authenticates through Cognito Managed Login with
  Authorization Code + PKCE, using loopback callbacks on ports 8976–8978. It
  mints a personal access token (PAT), validates it, and stores it atomically.
- `auth whoami`, `auth token create|list|revoke`, and `auth login`, which
  imports an existing PAT for CI and headless environments.
- `orgs list` and `teams list`.
- `apps list|get|create|delete`:
  - `apps create` validates `--type`, `--region` (`eu-central-1`,
    `eu-central-2`) and `--runtime` before calling the API, and auto-selects
    the team when the organization has exactly one.
  - `--wait` polls until the domain is active.
  - `--use-existing-repo` creates the app from an existing GitHub repository
    of the same name, leaving its contents untouched.
  - `apps delete --yes` starts an asynchronous teardown.
- `env get|set`.
- `deploys trigger|list|status|cancel`, with `--wait` on `trigger`.
- `logs`, which fetches a log snapshot.
- `services kvs tables`, which manages named KVS table namespaces.
- `changes` renders a visual report of branch-changed files and internal Go
  imports, with a readiness checklist covering tests, migrations, docs and
  changelog entries.
- `upgrade` installs the latest released version.
- Agent-first output:
  - JSON by default, with `--output table`.
  - Machine-readable `--help --json`.
  - A 0–5 exit-code contract.
- An idempotent, fail-closed provisioning script and operator guide for the
  public Cognito app client.

### Known issues

- `sureva login` fails with the release binaries because they embed a Cognito
  client from a retired user pool. Upgrade to 0.1.1, or set
  `SUREVA_COGNITO_CLIENT_ID=iugfo9d24630c3i0e03dr52ag`.

[Unreleased]: https://github.com/sureva-ch/sureva-cli/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/sureva-ch/sureva-cli/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/sureva-ch/sureva-cli/releases/tag/v0.1.0
