# Sureva CLI

Agent-first command-line interface for the Sureva cloud platform.

## Quickstart (agents and CI)

```bash
# 1. Install
go install github.com/sureva-ch/sureva-cli/cmd/sureva@latest

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

### Install with Homebrew

```bash
brew install --cask sureva-ch/tap/sureva
```

Upgrade or uninstall with standard Homebrew commands:

```bash
brew upgrade --cask sureva
brew uninstall --cask sureva
```

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

### Package managers

Scoop bucket distribution is planned but not available yet. Follow the releases
page for announcements.

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

### Deployments

```bash
sureva deploys trigger <app-id> --org <slug> --tag v1.2.3
sureva deploys trigger <app-id> --org <slug> --tag v1.2.3 --env-id <uuid>

# Trigger and wait for terminal state (success|failed|cancelled)
sureva deploys trigger <app-id> --org <slug> --tag v1.2.3 --wait

sureva deploys list <app-id> --org <slug>
sureva deploys status <app-id> <deploy-id> --org <slug>
```

**`--wait` flags** (available on `apps create` and `deploys trigger`):

| Flag | Default | Description |
|------|---------|-------------|
| `--wait` | false | Block until terminal state |
| `--wait-interval` | 5s | Polling interval |
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
