# Sureva CLI

Agent-first command-line interface for the Sureva cloud platform.

## Quickstart (agents and CI)

```bash
# 1. Install the latest release build (macOS/Linux; Windows and details under "Install")
PLATFORM=darwin_arm64   # or darwin_amd64, linux_amd64, linux_arm64
VERSION=$(curl -fsSL https://api.github.com/repos/sureva-ch/sureva-cli/releases/latest \
  | sed -n 's/.*"tag_name": "v\{0,1\}\([^"]*\)".*/\1/p')
curl -fsSL "https://github.com/sureva-ch/sureva-cli/releases/latest/download/sureva_${VERSION}_${PLATFORM}.tar.gz" \
  | tar -xz && sudo mv sureva /usr/local/bin/

# 2. Authenticate interactively
sureva login

# 3. Run
sureva apps list                         # JSON array of your apps
sureva --help --json | jq '.commands'    # full command tree for LLM agents
```

## Agent-first contract

JSON is the **default** output format for automation. Exception: `sureva changes` opens a local visual graph unless `--output json` or `--output table` is explicit. Commands print JSON error envelopes to stderr, so agent/script paths should pass `--output json` when they need guaranteed JSON stdout.

```bash
# List apps — stdout is valid JSON
sureva apps list

# Get the machine-readable command tree (for agents and scripts)
sureva --help --json | jq '.commands[].name'
```

Error envelopes on stderr always follow this shape:
```json
{ "error": "app not found", "code": "not_found", "http_status": 404 }
```

When the API sent its own stable error code, or facts that come with it, the
envelope also carries `details` (omitted otherwise):
```json
{ "error": "this source archive is rejected ...", "code": "source_not_ready", "http_status": 409,
  "details": { "api_code": "source_not_ready", "source_status": "rejected", "validation_code": "archive_empty" } }
```
`code` is the CLI's vocabulary and is what scripts switch on; `details.api_code`
is the API's code, so a code this CLI version does not know is still visible.
Source and deploy errors are classified by the API's `code`, never by its message.

Exit codes:

| Code | Meaning |
|------|---------|
| 0 | success |
| 1 | general / API error |
| 2 | auth error (401 / 403 / missing token) |
| 3 | not found (404) |
| 4 | validation / bad input (400 / 422) |
| 5 | network error (no HTTP response) |

## Install

### Download a release binary (recommended)

Pre-built binaries for Linux, macOS, and Windows are available on the
[Releases page](https://github.com/sureva-ch/sureva-cli/releases).

**macOS / Linux:**
```bash
# Set PLATFORM to one of: darwin_arm64, darwin_amd64, linux_amd64, linux_arm64
PLATFORM=darwin_arm64
VERSION=$(curl -fsSL https://api.github.com/repos/sureva-ch/sureva-cli/releases/latest \
  | sed -n 's/.*"tag_name": "v\{0,1\}\([^"]*\)".*/\1/p')

curl -L "https://github.com/sureva-ch/sureva-cli/releases/latest/download/sureva_${VERSION}_${PLATFORM}.tar.gz" \
  | tar -xz
sudo mv sureva /usr/local/bin/
```

**Windows (PowerShell):**
```powershell
$Repo = "sureva-ch/sureva-cli"
$Release = Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest"
$Version = $Release.tag_name.TrimStart("v")
$Arch = "amd64" # Use "arm64" for Windows on ARM.
$Archive = "sureva_${Version}_windows_${Arch}.zip"
$InstallDir = "$env:USERPROFILE\bin"

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
Invoke-WebRequest "https://github.com/$Repo/releases/latest/download/$Archive" -OutFile $Archive
Expand-Archive $Archive -DestinationPath $InstallDir -Force
```

Checksums are published alongside each release in `checksums.txt`.

Once installed as a standalone binary, you can update in place with:

```bash
sureva upgrade          # download and install the latest release
sureva upgrade --check  # report current vs latest without changing anything
```

`upgrade` downloads the release asset for your OS/arch, verifies its SHA-256
checksum against `checksums.txt`, and replaces the running binary atomically.
If the CLI was installed via Homebrew it will not self-replace — it points you
at `brew upgrade --cask sureva` so Homebrew keeps managing the version.

### Install with Go

```bash
go install github.com/sureva-ch/sureva-cli/cmd/sureva@latest
```

Requires Go 1.25.11 or later, matching `go.mod`. The binary is placed in
`$GOBIN` (default `~/go/bin`).

**A source build cannot log in on its own.** Release builds compile in the
public Cognito app client ID. `go install` does not, so `sureva login` fails
with `validation_error: cognito client id not configured`. Give it the
production client ID, which is public and secretless:

```bash
export SUREVA_COGNITO_CLIENT_ID=iugfo9d24630c3i0e03dr52ag
sureva login
```

You can also set `cognito_client_id` in the config file. The development
values, and how the client is provisioned, are in
[`docs/cognito-cli-client.md`](docs/cognito-cli-client.md). Use a release build
unless you need to build from source.

### Package managers

Homebrew and Scoop distribution are planned but not available yet: there is no
`sureva-ch/tap` tap, and `brew install --cask sureva-ch/tap/sureva` fails with
"Cask is unavailable". Follow the releases page for announcements. `sureva
upgrade` already recognises a Homebrew install and defers to `brew upgrade`, so
nothing changes for the CLI when the tap ships.

## Authentication

### Interactive login (recommended)

```bash
sureva login
```

The CLI opens Sureva Managed Login in your browser using OAuth Authorization
Code with PKCE. It receives the callback on `127.0.0.1` port 8976, 8977, or
8978, mints a PAT through `/v1/auth/tokens`, validates it through `/v1/auth/me`,
and only then atomically saves it. If the browser cannot open, copy the URL
printed in the terminal. A failed re-login preserves the existing token.

### CI and agents

Set the `SUREVA_TOKEN` environment variable to a personal access token:

```bash
export SUREVA_TOKEN=sapi_<hex>
sureva apps list
```

After interactive login, the CLI can create additional tokens for automation:
```bash
sureva auth token create --name "ci-deploy"
# stdout: {"id":"...","name":"ci-deploy","token":"sapi_...","last_four":"...","warning":"Token shown once..."}
```

The authenticated token-management endpoint is
`POST https://api.sureva.com/v1/auth/tokens`.

### Import an existing PAT (advanced fallback)

To verify and save a PAT without putting it in shell history:

```bash
printf '%s' "$SUREVA_TOKEN" | sureva auth login --token-stdin
sureva auth whoami
```

Tokens can also be stored manually in the config file at:
- **Linux/macOS**: `~/.config/sureva/config.yaml`
- **Windows**: `%APPDATA%\sureva\config.yaml`

```yaml
token: sapi_<hex>
org: my-org          # default organization slug (used when --org is omitted)
api_url: https://api.sureva.com   # override for local dev
```

The file must be `0600` (owner read/write only). CI **must** use `SUREVA_TOKEN`.

## Command reference

### Global flags

| Flag | Default | Description |
|------|---------|-------------|
| `--output json\|table` | `json` | Output format (JSON for agents, table for humans; overrides `changes` visual default) |
| `--org <slug>` | — | Organization slug (overrides config default) |
| `--config <path>` | `~/.config/sureva/config.yaml` | Config file path |
| `--json` | — | With `--help`, emit machine-readable JSON command tree |

### Version and help

```bash
sureva --version                       # JSON: {"version":"...","commit":"...","built_at":"..."}
sureva --help --json                   # machine-readable command tree for agents
sureva upgrade                         # update a standalone binary to the latest release
sureva upgrade --check                 # report current vs latest without changing anything
```

`upgrade` is a no-op for Homebrew installs: it returns
`{"status":"managed_by_homebrew"}` and tells you to run `brew upgrade --cask sureva`.

### Auth

```bash
sureva login                            # primary browser login with PKCE
sureva auth whoami                     # show current user identity
printf '%s' "$SUREVA_TOKEN" | sureva auth login --token-stdin # advanced PAT import

sureva auth token create               # create an additional PAT; requires authentication
sureva auth token create --name ci-deploy --expires-at 2026-12-31T00:00:00Z
sureva auth token list                 # list your PATs (raw value never shown)
sureva auth token revoke <token-id>    # revoke a PAT
```

`auth login` is a non-interactive fallback: it imports and verifies an existing
PAT with `/v1/auth/me`. Use top-level `sureva login` for normal authentication.

### Organizations

```bash
sureva orgs list                       # list orgs you belong to
```

### Teams

```bash
sureva teams list --org <slug>         # list teams in an org
```

### Applications

```bash
sureva apps list                       # list all visible apps (cross-org)
sureva apps list --org <slug>          # list apps in a specific org
sureva apps get <app-id> --org <slug>  # get app details (includes composed url field)

# Create an app (team auto-selected when org has exactly one team)
sureva apps create --name my-app --type web --region eu-central-1 --org <slug>
sureva apps create --name my-api --type api --runtime nodejs24 --region eu-central-1 --team <slug> --org <slug>

# Create and wait for domain to become active
sureva apps create --name my-app --type web --region eu-central-1 --org <slug> --wait

# Delete an app (async teardown — --yes required)
sureva apps delete <app-id> --org <slug> --yes
```

**App types**: `web` | `web-ssr` | `api` | `sse`

**Source type**: an app is either GitHub-backed (`source_type: "github"`) or
upload-backed (`"upload"`, an org without a connected GitHub organization). The
field is omitted when the API does not report it; treat that as unknown, not as
`github`. `--use-existing-repo` only applies to GitHub-connected orgs.

**Runtimes** (required for non-web types): `nodejs24` | `python314` | `go126`
**Regions**: `eu-central-1` | `eu-central-2`

The CLI only composes an app `url` when a verified deployment suffix is supplied
with `SUREVA_DOMAIN_SUFFIX` or the `domain_suffix` config key. Without one, the
field is omitted rather than assuming a production domain.

### Environment variables

```bash
sureva env get <app-id> --org <slug>          # list env vars (values masked as ***)
sureva env get <app-id> --org <slug> --reveal # show plaintext values
sureva env set <app-id> --org <slug> KEY=value OTHER=value2
printf 'API_KEY=secret\n' | sureva env set <app-id> --org <slug> --from-stdin
sureva env set <app-id> --org <slug> --from-file .env
# env set is a full replacement (PUT) — omitted keys are deleted
```

Prefer `--from-stdin` or `--from-file` for secret values. Passing `KEY=value`
directly can expose secrets in shell history or process listings.

### Services

```bash
# Managed KVS (DynamoDB-backed data plane) — tables only; creating the first table activates KVS.
sureva services kvs tables list <app-id> --org <slug>
sureva services kvs tables create <app-id> --org <slug> --name sessions --minute-limit 300
sureva services kvs tables rotate <app-id> sessions --org <slug>
sureva services kvs tables delete <app-id> sessions --org <slug> --yes
```

KVS is available for `api`, `web-ssr`, and `sse` apps. Plaintext KVS tokens are
shown only on enable/create/rotate responses; store them immediately.

### Sources (upload-backed apps)

An upload-backed app has releases instead of a GitHub repository: each accepted
archive is a release with the tag `src-<seq>` and an id.

```bash
sureva sources list <app-id> --org <slug>                # releases, newest first
sureva sources get <app-id> <source-id> --org <slug>     # one release
sureva sources pull <app-id> --org <slug> --dir ./app    # download the code
```

Each row carries `status` (`pending` | `validating` | `rejected` | `ready` |
`expired`), `release_tag`, `size_bytes`, `sha256`, `seq`, `created_at`,
`available` and, for a rejected archive, `validation_error` (prose, may be
reworded), `validation_code` (the stable cause, for example `credentials_found`
or `promote_failed`) and `retryable`. `retryable: true` means the platform
failed and sending the same archive again can succeed; `false` means the archive
has to change. A row rejected before the API recorded codes has neither. An API
that limits validation attempts also reports `attempts` and `max_attempts`, and
then `retryable` is true only while attempts remain. A GitHub-backed app has no
releases: `sources list` is empty (only `sources pull` reports
`github_backed_app` for it).
`available: false` on a `ready` release means its stored version is gone and it
can no longer be deployed.

### Pull the code of an upload-backed app

`sources pull` is how a developer or a coding agent gets the code of an
upload-backed app to work on, the counterpart of `git clone` for a GitHub-backed
one:

```bash
sureva sources pull <app-id> --org <slug> --dir ./app                    # latest ready release
sureva sources pull <app-id> --org <slug> --dir ./app --source-id <id>   # a specific release
```

The API hands out a short-lived download link; the archive is fetched straight
from storage with a client that never carries your API token, its `sha256` is
checked against the one the API reported, and only then is anything extracted.
`--dir` defaults to the current directory and is created when missing; a
`--dir` that is a symlink is resolved and the files are written under the
resolved path. It must be empty (nothing in it, or only the `.sureva/` directory of an earlier pull)
unless you pass `--force`; with `--force` files in the archive overwrite files
of the same path and files that are not in the archive are left alone, so the
directory can keep leftovers from an older release. Into a new or empty
directory the tree is built aside and moved into place only when complete
(leftover `.sureva-pull-*` staging directories of a killed pull are removed).
With `--force`, every path is checked against what exists before anything is
written: a directory where the archive has a file, or the reverse, is refused
and names the path; nothing is removed to make room.

Extraction does not trust the archive: absolute paths, `..` or `.` segments,
backslashes, NUL bytes, drive letters, symlinks and every other non-regular
entry are refused (nothing is extracted), as are archives over 50,000 entries or
1 GiB uncompressed. Names that differ only in letter case, a file together with
a path beneath it, and names Windows cannot hold (a trailing dot or space,
control characters, `:`, any of `<>"|?*`, device names such as `CON`, `NUL`,
`COM1`, `LPT1`) are refused on every platform, so one archive behaves the same
everywhere. No file is written through a symlink already in `--dir`.
Only permission bits are applied, never setuid, setgid or sticky, and group and
other never get write access.

**After a pull, know what the tree is.** It is what the platform stored, not
what was uploaded:

- `node_modules/`, `.git/` and `.env*` are never in it. Install dependencies
  (for example `npm ci`), and get environment variables with `sureva env get`;
  there is no `.env` file to copy.
- A single wrapper directory the upload had was removed, so the files sit at the
  top of `--dir`.
- File permission bits are kept; releases stored before that was recorded
  extract without the executable bit.

A successful pull writes `<dir>/.sureva/source.json` (`app_id`, `source_id`,
`release_tag`, `sha256`, `pulled_at`): the release the tree is based on.
The record is written atomically and never through a symlink: if `.sureva` is
not a plain directory, or `source.json` is not a regular file, the pull is
refused before anything is downloaded. In an empty `--dir` only `source.json`
is replaced; the rest of an existing `.sureva/` stays. `deploy` never uploads
`.sureva/`, and reports the recorded release as
`base_source_id` in its JSON output. It is not sent to the API.

stdout is one JSON object: `app_id`, `source_id`, `release_tag`, `dir`, `files`,
`bytes` (uncompressed), `archive_bytes`, `sha256` and `state_file`. The download
link is never printed. Failures an agent should tell apart, by the envelope
`code`:

| `code` | Exit | Meaning |
|---|---|---|
| `auth_error` | 2 | credentials missing or expired |
| `no_source` | 3 | the app has no ready release yet: start a new project and publish it with `deploy`; not a failure (`deploy` and `deploys trigger` call the same API state `no_ready_source`; `no_source` is the name `pull` has always used) |
| `not_found` | 3 | unknown app or unknown `--source-id` |
| `dir_not_empty` | 4 | `--dir` already holds files; use `--force` |
| `github_backed_app` | 4 | the app's code is its GitHub repository: clone it (named in the message when known) |
| `validation_error` | 4 | bad arguments; `--dir` is not a directory; or, with `--force`, a path conflicts with what exists (file versus directory) or `.sureva` is a link; nothing was written |
| `source_not_ready` | 1 | the release is not ready (validating or rejected) |
| `source_expired` | 1 | the release is no longer stored |
| `checksum_mismatch` | 1 | the download does not match the reported `sha256`; nothing was extracted |
| `unsafe_archive` | 1 | the archive was refused; see the message |
| `download_expired` | 1 | the download link ran out; run the command again |
| `download_failed` | 1 | storage refused the download or sent more than the reported size |
| `extract_failed` | 1 | writing the files failed; the message says whether some were written |
| `interrupted` | 1 | stopped by SIGINT or SIGTERM; the temporary archive was removed |
| `network_error` | 5 | no HTTP response |

### Deploy a local directory

For an upload-backed app, `deploy` packs a directory, uploads it, waits for the
platform to validate it and deploys the resulting release in one command:

```bash
sureva deploy --app <app-id> --org <slug>                 # current directory
sureva deploy ./site --app <app-id> --org <slug> --env-id <uuid> --wait
SUREVA_TOKEN=sapi_... sureva deploy --app <app-id> --org <slug> --wait   # CI / agents
```

`--wait`, `--wait-interval` and `--wait-timeout` behave as in `deploys trigger`;
the interval also paces the wait for archive validation, which always happens,
and the timeout bounds the validation wait and the deployment wait separately.
A GitHub-backed app is refused up front (`github_backed_app`): use
`deploys trigger` for it.

**Validation that could not run is retried.** When the API reports a rejection
as `retryable` (storage, dispatch or a worker that did not finish: the archive
was never judged), `deploy` repeats the `complete` call, pausing 2x, 4x and 8x
`--wait-interval` (at most one minute), because the API restarts validation of
such a source. Against an API that does not report validation attempts it
repeats up to 3 times. An API that limits them is followed instead: each source
gets 3 validation attempts (the first `complete` included), the source reports
`attempts` and `max_attempts`, and `retryable` is true only while attempts
remain. A retry sent too early is refused with `409 source_retry_too_soon` and
`retry_after_seconds`; `deploy` waits that long (plus one second) and sends
`complete` again, which does not count as a repeat. If that wait does not fit in
`--wait-timeout` the command stops with `validation_timeout` and says when a
retry would have been possible. Retries and pauses count against the validation
`--wait-timeout`; the output reports `validation_retries`. When the repeats did
not help, or every attempt is used (`409 source_retry_limit_reached`, or a
rejected source with `attempts >= max_attempts` and a platform-side
`validation_code`), the command fails with `validation_unavailable`: the platform
failed, not the archive, and running `deploy` again uploads anew and starts a
fresh set of attempts. A refusal about the archive itself (`retryable: false`,
for example `credentials_found`) fails at once with `source_rejected`, also when
its attempts are used up. `--wait-interval` must be positive.

What is packed: `node_modules/`, `.git/`, `.sureva/` and `.env*` are always left
out, at any depth. So is everything `.gitignore` ignores (inside a git work tree the file
list comes from `git ls-files`, so git's own rules apply; elsewhere `.gitignore`
files are read during the walk) and everything a `.surevaignore` file in the
directory lists, which uses the `.gitignore` syntax. Symlinks are skipped, never
followed. The JSON output reports what was excluded (grouped, with counts) and
the archive size; the zip is built in a temporary file and removed afterwards.
An archive over the limit the API reports for the app is refused before the
upload, naming its largest entries.

stdout is one JSON object: `app_id`, `base_source_id` (only for a directory
filled by `sources pull`), `archive`, `source` (the release, as in
`sources get`) and `deployment`. After a failure that follows the upload, the
same object is printed with what completed, next to the error envelope on
stderr; after a failed deployment it also carries `logs.command`, the `sureva
logs` command that fetches the logs. Failures an agent should tell apart, by
the envelope `code`:

| `code` | Exit | Meaning |
|---|---|---|
| `auth_error` | 2 | credentials missing or expired |
| `archive_too_large` | 4 | over the API's limit; nothing was uploaded |
| `source_rejected` | 4 | validation refused the archive, so fix the archive; the reason is in the message and `source.validation_error`, the stable cause in `details.validation_code` (`details.retryable` is false) |
| `empty_archive` | 4 | nothing left to pack after the exclusions |
| `github_backed_app` | 4 | the app deploys from GitHub |
| `app_source_upload_limit_exceeded` | 4 | the app reached its daily upload limit; try again tomorrow (UTC) |
| `validation_unavailable` | 1 | validation could not run (a platform-side cause; `details.validation_code` names it) and the retries did not help or the API has no attempts left (`details.retryable`, `details.attempts`, `details.max_attempts`); run the command again, which starts a fresh set of attempts |
| `source_not_completable` | 1 | the upload cannot be completed again (`details.source_status` says why) |
| `source_expired` / `source_not_ready` | 1 | the release is no longer stored / not deployable (`details.source_status`, `details.validation_code`) |
| `no_ready_source` | 3 | the deployment found no ready release |
| `validation_timeout` | 1 | validation did not finish within `--wait-timeout`, retries and pauses included, or the next retry would only be accepted after it; see `sources get` |
| `pack_failed` | 1 | the directory could not be read or zipped |
| `interrupted` | 1 | stopped by SIGINT or SIGTERM; the temporary archive was removed |
| `upload_failed` / `upload_expired` | 1 | the storage endpoint refused the archive / the upload form expired |
| `wait_timeout` | 1 | the deployment did not finish in time |
| `deploy_failed` | 1 | the deployment failed or was cancelled |

Changed since v0.2.0 (scripts that matched these `code` or exit values need
updating; the same list is in the CHANGELOG):

- `deploys trigger` with no ready release: `not_found` is now `no_ready_source`
  (exit 3, unchanged).
- Daily upload limit: `validation_error` is now `app_source_upload_limit_exceeded`
  (exit 4, unchanged).
- `complete` refused with 409: `api_error` is now `source_not_completable` (exit 1,
  unchanged).
- A platform-side validation failure (`promote_failed`, `dispatch_failed`,
  `validation_timeout`, ...): `source_rejected` (exit 4) is now
  `validation_unavailable` (exit 1), after the retries.
- A 409 that carries an API `code` other than a source one is no longer reported
  as `source_not_ready` because of its wording.
- `--wait-timeout` now bounds the whole validation phase, including the retries
  and their pauses.
- The error envelope may carry a `details` object (additive).
- A `--wait-interval` that is not positive is now a `validation_error` (exit 4)
  in `deploy`, `deploys trigger --wait` and `apps create --wait`; it used to
  panic.

### Deployments

```bash
sureva deploys trigger <app-id> --org <slug> --tag v1.2.3
sureva deploys trigger <app-id> --org <slug> --tag v1.2.3 --env-id <uuid>

# Trigger and wait for terminal state (success|failed|cancelled)
sureva deploys trigger <app-id> --org <slug> --tag v1.2.3 --wait

# Upload-backed app: deploy a release by id (also how you roll back).
# With neither --tag nor --source-id the latest ready release is deployed.
sureva deploys trigger <app-id> --org <slug> --source-id <source-id>

sureva deploys list <app-id> --org <slug>
sureva deploys status <app-id> <deploy-id> --org <slug>
```

`--tag` selects a release of a GitHub-backed app and is rejected for an
upload-backed one; `--source-id` is the reverse. They are mutually exclusive
(usage error, exit 4, no request sent). The API's answer is passed through, and
exit 1 alone does not say which failure it was, so read `code` in the stderr
envelope:

| `code` | HTTP | Meaning |
|--------|------|---------|
| `source_expired` | 410 | The release is no longer stored; upload the source again |
| `source_not_ready` | 409 | The release is pending, validating, rejected or expired; `details.source_status` and `details.validation_code` say which and why |
| `no_ready_source` | 404 | No `--source-id` was given and the app has no ready release (exit 3) |
| `not_found` | 404 | Unknown release (exit 3) |
| `validation_error` | 400 | `--tag` on an upload-backed app or `--source-id` on a GitHub-backed one (exit 4) |
| `deploy_failed` | n/a | With `--wait`: the deployment itself failed or was cancelled |

**`--wait` flags** (available on `apps create` and `deploys trigger`):

| Flag | Default | Description |
|------|---------|-------------|
| `--wait` | false | Block until terminal state |
| `--wait-interval` | 5s | Polling interval; must be positive (`validation_error`, exit 4, otherwise) |
| `--wait-timeout` | 10m (create) / 15m (deploys) | Max wait time |

Timeout exits 1 with `code: "wait_timeout"`. Non-success terminal exits 1 with `code: "domain_failed"` or `"deploy_failed"`.

### Logs

```bash
sureva logs <app-id> --org <slug>              # fetch log snapshot (non-streaming)
sureva logs <app-id> --org <slug> --env-id <uuid>
```

### Changes

Visualize what changed on the current branch (including uncommitted work) as an
interactive graph of files and internal import edges. Works in any repository:
import edges are resolved for Go, JavaScript/TypeScript (including Astro, Vue,
and Svelte), and Python; other languages still show as files without edges.
Click a node to see its diff with syntax highlighting and `+/-` counts. A
readiness **checklist** flags whether the change set includes source code,
tests, documentation, and a changelog entry.

```bash
sureva changes                    # open the visual graph (branch vs main)
sureva changes --base develop     # compare against another branch/ref
sureva changes --release v0.5.0   # graph + changelog for a release, vs the previous tag
sureva changes --output json      # full payload for automation
sureva changes --output table     # deterministic text
```

With `--release`, the graph covers the range from the previous tag to the given
tag and a **Changelog** tab groups the commits by conventional-commit type
(features, fixes, breaking changes, …). Import edges are resolved from the
working tree, so they are exact when the tag equals `HEAD` and approximate for
older tags.

## Output formats

Commands default to JSON stdout for automation, except `sureva changes`, which opens a visual graph unless `--output` is explicit. Use `--output table` for human-readable text:

```bash
sureva apps list --output table
sureva changes --output json
```

## Development

```bash
go build ./...
go vet ./...
go test ./...
```
