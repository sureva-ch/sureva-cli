# Changelog

All notable changes to the sureva CLI are documented in this file.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

## [Unreleased]

### Removed

- The Cognito provisioning script (`scripts/provision-cognito-cli-client.sh`)
  and its callback drift test are no longer kept in this repository.
  `docs/cognito-cli-client.md` keeps the contract the app client must meet,
  which now includes the pool's Google identity provider.

## [0.3.0] - 2026-10-05

### Added

- Source and deploy errors are classified by the API's stable error `code`
  (`source_not_found`, `no_ready_source`, `source_not_ready`, `source_expired`,
  `source_not_completable`, `app_not_upload_backed`,
  `app_source_upload_limit_exceeded`) in `deploy`, `deploys trigger` and
  `sources get|list|pull`, not by the message text. The client keeps the code and
  its detail fields (`source_status`, `validation_code`, `environments`) on
  `*APIError`.
- The stderr error envelope carries an optional `details` object (omitted when
  empty) with the API's own code as `api_code` and the detail fields that came
  with it. Existing keys are unchanged.
- New envelope codes: `no_ready_source` (404, exit 3, `deploy`/`deploys trigger`
  with nothing to deploy), `app_source_upload_limit_exceeded` (422, exit 4),
  `source_not_completable` (409, exit 1) and `validation_unavailable` (exit 1,
  validation could not run on the platform's side). `sources pull` keeps
  `no_source` for the same API state.
- `sources get|list` and the `source` object of `deploy` show `validation_code`,
  `retryable` and `base_source_id`. A rejected `deploy` carries
  `details.validation_code` and `details.retryable` in its envelope.
- `deploy` repeats `complete`, with growing pauses derived from
  `--wait-interval`, when validation is rejected as `retryable`, within
  `--wait-timeout`; the output reports `validation_retries`. It repeats up to 3
  times against an API that reports no validation attempts. Against one that
  does (`attempts` and `max_attempts` on the source, `retryable` true only while
  attempts remain) it follows the API: a retry refused with `409
  source_retry_too_soon` is waited out (`retry_after_seconds` plus 1s, not
  counted as a repeat) and, when that does not fit in `--wait-timeout`, ends with
  `validation_timeout` and the time a retry would have been possible; `409
  source_retry_limit_reached`, or a rejected source with every attempt used on a
  platform-side `validation_code`, ends at once with `validation_unavailable`
  and a message that uploading again starts a fresh set of attempts. The two
  refusals have their own envelope codes (`source_retry_too_soon`,
  `source_retry_limit_reached`) where `deploy` reports them as they are, and
  `details` carries `retry_after_seconds`, `attempts` and `max_attempts`.
- `sources get|list` and the `source` object of `deploy` show `attempts` and
  `max_attempts` when the API sends them.

- `sureva deploy` sends the release recorded in `.sureva/source.json` as
  `base_source_id` with the upload request when the record belongs to `--app`,
  so the platform refuses an upload built on a release that is no longer the
  latest. No record, a damaged one or another app's: nothing is sent.
  `--no-base` sends nothing on purpose. The output reports `base_sent`.
- New envelope codes `stale_base` (a newer release exists; not a broken archive;
  `details.latest_release_tag` names it: the ready release with the highest `seq`), `invalid_base_source_id` and
  `base_source_not_found` (the recorded base is unusable; the record is left in
  place).
- After the release is `ready`, `deploy` updates `.sureva/source.json` to it
  (also in a directory that was never pulled, and also when the deployment then
  fails), through the same atomic, symlink-safe write as `sources pull`; the
  output carries `state_file`, or `state_file_error` when it could not be
  written, which does not fail the command.
- When the record replaced belongs to another app (a directory pulled from app A
  and deployed with `--app B`), the output carries
  `state_file_replaced{app_id,source_id,release_tag}`; the deploy is not refused.
  A record of this app whose `source_id` is not a UUID is not sent as the base
  (it would come back as `invalid_base_source_id`): the deploy goes ahead without
  a base and the output says why in `base_record_ignored`. The message of a
  `validation_timeout` says that the record was left as it was, that the next
  deploy may then be rejected as `stale_base`, and the way out.

### Changed

Changes to behavior of v0.2.0 that a script may depend on:

- `deploys trigger` with no ready release: envelope `code` `not_found` is now
  `no_ready_source` (exit 3, unchanged).
- Daily upload limit on `deploy`: `validation_error` is now
  `app_source_upload_limit_exceeded` (exit 4, unchanged).
- `complete` refused with 409 in `deploy`: `api_error` is now
  `source_not_completable` (exit 1, unchanged).
- A platform-side validation failure (`promote_failed`, `dispatch_failed`,
  `validation_timeout`, ...) in `deploy`: `source_rejected` (exit 4) is now
  `validation_unavailable` (exit 1), after the retries. `source_rejected` is left
  for a refusal about the archive.
- A 409 that carries an API `code` other than a source one is no longer reported
  as `source_not_ready` because of its wording.
- `--wait-timeout` of `deploy` now bounds the whole validation phase, including
  the retries and their pauses.
- The error envelope may carry a `details` object (existing keys unchanged).
- A `--wait-interval` that is zero or negative is now refused up front with
  `validation_error` (exit 4) in `deploy`, `deploys trigger --wait` and
  `apps create --wait`; it used to panic the polling ticker.
- `sources list` and `sources get` no longer document `github_backed_app`: the API
  never answers it there (only the download behind `sources pull` does). A
  GitHub-backed app has an empty list and `get` answers `not_found`.
- What `details` echoes from an API response is bounded (128 characters per
  string, 20 environments) and the size of an API error body that is read is
  limited to 1 MiB.
- The message-based recognition of `source_expired` and `source_not_ready`
  remains only as a fallback for a response that carries no `code` (the API's
  download endpoint sends none for them yet). A 409 with another API code no
  longer becomes `source_not_ready` because of its wording.

## [0.2.0] - 2026-10-04

### Added

- `sureva sources pull <app-id> [--source-id <id>] [--dir <path>] [--force]`
  downloads a release of an upload-backed app (the latest ready one by default)
  straight from storage, with a client that never carries the API token, verifies
  the `sha256` the API reported and extracts it with a hardened extractor
  (no absolute paths, `..`, backslashes, symlinks or non-regular entries; entry
  and size limits; names that collide when compared case-insensitively, a file
  and a path beneath it, and names Windows cannot hold (trailing dot or space,
  control characters, `:`, `<>"|?*`, reserved device names) are refused on every
  platform; nothing written through a symlink in the target; only permission
  bits applied). A new or empty directory is built aside and moved
  into place; a populated one needs `--force`, which leaves files that are not in
  the archive alone and checks every path against what exists before writing
  (a file-versus-directory conflict is refused up front, nothing is removed). It
  writes `.sureva/source.json` with the release the tree is based on, atomically
  and never through a symlink (refused as `validation_error` before anything is
  downloaded or extracted); in an empty target only that file is replaced. Stale
  `.sureva-pull-*` staging directories of a killed pull are removed by the next
  pull. A redirect from https to http on the storage download is refused. Distinct envelope codes: `no_source`, `source_expired`,
  `source_not_ready`, `github_backed_app`, `checksum_mismatch`, `unsafe_archive`,
  `dir_not_empty`, `download_expired`, `download_failed`, `extract_failed`,
  `interrupted`. The help and README say that dependencies and environment
  variables are not in the tree and that a wrapper directory was removed.
- `sureva deploy` reports `base_source_id` in its JSON output when the directory
  was pulled for the same app. It is not sent to the API. It reads
  `.sureva/source.json` only when it is a small regular file. `.sureva/` and
  `.sureva-pull-*` are now always left out of the archive, like `.git/`,
  whatever their letter case.
- `sureva deploy [dir] --app <app-id>` packs a directory, uploads it to an
  upload-backed app, waits for the archive to be validated and deploys the
  release, with `--org`, `--env-id` and `--wait` / `--wait-interval` /
  `--wait-timeout` as in `deploys trigger`. `node_modules/`, `.git/` and `.env*`
  are always excluded, whatever their letter case; `.gitignore` and `.surevaignore` are honoured and
  symlinks are skipped. The JSON output reports the exclusions, the archive size
  and the release; an archive over the API's limit is refused before the upload.
  Failures carry distinct envelope codes (`archive_too_large`,
  `source_rejected`, `validation_timeout`, `deploy_failed`, `github_backed_app`,
  `pack_failed`, `interrupted`, ...), and Ctrl-C or SIGTERM stops the deploy and
  removes the temporary archive. A failed deployment includes the `sureva logs`
  command to fetch its logs. Adds the `github.com/sabhiram/go-gitignore` dependency (MIT).
- `apps get`, `apps list` and `apps create` show `source_type` (`github` or
  `upload`) when the API sends it. The read endpoints do not send it until
  sureva-ch/cloud-api#345 ships, so an app without the field is unknown, not
  GitHub-backed: the key is omitted in JSON, and in the table the cell is
  blank, or the column is absent when no listed app has the field.
- `sureva sources list <app-id>` and `sureva sources get <app-id> <source-id>`
  show the releases of an upload-backed app: status, release tag, size, sha256,
  availability and, for a rejected archive, its validation error. They appear
  in `sureva --help --json`.
- `deploys trigger --source-id <id>` deploys a release of an upload-backed app,
  which is also how to roll one back. It is mutually exclusive with `--tag`,
  which is now documented as applying to GitHub-backed apps only. A failed
  deploy of a release is told apart in the stderr envelope: `source_expired`
  (410, the release is no longer stored) and `source_not_ready` (409), next to
  the existing `deploy_failed` for a failed deployment. All still exit 1; the
  root `EXIT CODES` help names them.
- `apps create --help` states that `--use-existing-repo` only applies to an org
  with a connected GitHub organization.

### Fixed

- The README's install instructions now work. The quickstart told readers to
  `go install`, which builds a binary without the Cognito app client, so
  `sureva login` fails with `validation_error: cognito client id not
  configured`. It now downloads the latest release build, and that exact
  snippet was run: it installs 0.1.1 with the production client embedded. The
  "Install with Go" section now says a source build needs
  `SUREVA_COGNITO_CLIENT_ID`, and gives the public production value. With it
  set, a source build starts the login against `auth.sureva.com`.
- The README no longer offers Homebrew. `brew install --cask
  sureva-ch/tap/sureva` fails with "Cask is unavailable": `.goreleaser.yaml`
  publishes no cask and there is no `sureva-ch/tap` repository. Homebrew is
  now listed as planned, beside Scoop.

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

[Unreleased]: https://github.com/sureva-ch/sureva-cli/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/sureva-ch/sureva-cli/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/sureva-ch/sureva-cli/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/sureva-ch/sureva-cli/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/sureva-ch/sureva-cli/releases/tag/v0.1.0
