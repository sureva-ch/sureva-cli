package cli

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/sureva-ch/sureva-cli/internal/output"
	"github.com/sureva-ch/sureva-cli/internal/version"
)

// Release distribution constants. These mirror the goreleaser configuration:
// archives are named sureva_<version>_<os>_<arch>.<ext> where <version> is
// the git tag without its leading "v".
const (
	ghOwner    = "sureva-ch"
	ghRepo     = "sureva-cli"
	binaryName = "sureva"
)

// httpTimeoutAPI bounds the GitHub API metadata request.
const httpTimeoutAPI = 15 * time.Second

// httpTimeoutDownload bounds archive and checksum downloads.
const httpTimeoutDownload = 90 * time.Second

// NewUpgradeCmd returns the `sureva upgrade` command.
//
// It updates a standalone binary install in place. When the running binary is
// managed by Homebrew it refuses to self-replace and points the user at
// `brew upgrade sureva`, so the package manager's version tracking stays
// authoritative.
func NewUpgradeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade the CLI to the latest released version",
		Long: `Upgrade the sureva CLI to the latest GitHub release.

BEHAVIOUR
  Standalone binary install -> downloads the matching release asset for this
  OS/arch, verifies its SHA-256 checksum, and replaces the running binary.
  Homebrew install -> refuses to self-replace and tells you to run
  'brew upgrade --cask sureva' so Homebrew keeps managing the version.

VALIDATION / INPUTS
  No positional arguments.
  --check: only report current vs latest version; do not modify anything.
  Output fields: current, latest, status[, hint, binary].
  status is one of: up_to_date | upgraded | managed_by_homebrew |
  update_available | dev_build.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			checkOnly, _ := cmd.Flags().GetBool("check")
			return runUpgrade(cmd, checkOnly)
		},
	}
	cmd.Flags().Bool("check", false, "Only check for a newer version; do not download or replace anything")
	return cmd
}

// runUpgrade orchestrates the version check and, unless checkOnly is set, the
// in-place replacement of the running binary.
func runUpgrade(cmd *cobra.Command, checkOnly bool) error {
	r := output.NewRenderer(OutputFormat(cmd), cmd.OutOrStdout(), cmd.ErrOrStderr())
	ctx := cmd.Context()

	current := normalizeVersion(version.Version)

	tag, err := latestRelease(ctx)
	if err != nil {
		code := r.RenderError(
			fmt.Sprintf("could not fetch latest release: %v", err),
			"network_error",
			0,
		)
		return &ExitError{Code: code}
	}
	latest := normalizeVersion(tag)

	// "dev" builds carry no comparable version; report and stop.
	if version.Version == "dev" || current == "dev" {
		_ = r.Render(map[string]string{
			"current": version.Version,
			"latest":  latest,
			"status":  "dev_build",
			"hint":    "this is a dev build; install a release to enable upgrades",
		})
		return nil
	}

	if compareVersions(current, latest) >= 0 {
		_ = r.Render(map[string]string{
			"current": current,
			"latest":  latest,
			"status":  "up_to_date",
		})
		return nil
	}

	// A newer version exists. Resolve how this binary was installed.
	execPath, err := resolveExecPath()
	if err != nil {
		code := r.RenderError(
			fmt.Sprintf("could not locate the running binary: %v", err),
			"general_error",
			-1,
		)
		return &ExitError{Code: code}
	}

	if isHomebrewManaged(execPath) {
		_ = r.Render(map[string]string{
			"current": current,
			"latest":  latest,
			"status":  "managed_by_homebrew",
			"hint":    "run: brew upgrade --cask sureva",
		})
		return nil
	}

	if checkOnly {
		_ = r.Render(map[string]string{
			"current": current,
			"latest":  latest,
			"status":  "update_available",
			"hint":    "run: sureva upgrade",
		})
		return nil
	}

	if err := downloadAndReplace(ctx, tag, latest, execPath); err != nil {
		code := r.RenderError(
			fmt.Sprintf("upgrade failed: %v", err),
			"general_error",
			-1,
		)
		return &ExitError{Code: code}
	}

	_ = r.Render(map[string]string{
		"current": current,
		"latest":  latest,
		"status":  "upgraded",
		"binary":  execPath,
	})
	return nil
}

// ghRelease is the subset of the GitHub release payload we consume.
type ghRelease struct {
	TagName string `json:"tag_name"`
}

// latestRelease returns the tag name (e.g. "v0.2.1") of the latest GitHub
// release for the CLI repository.
func latestRelease(ctx context.Context) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", ghOwner, ghRepo)
	body, err := httpGet(ctx, url, httpTimeoutAPI)
	if err != nil {
		return "", err
	}
	var rel ghRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return "", fmt.Errorf("decode release metadata: %w", err)
	}
	if rel.TagName == "" {
		return "", fmt.Errorf("release metadata had no tag_name")
	}
	return rel.TagName, nil
}

// httpGet performs a GET with a CLI User-Agent and an optional GITHUB_TOKEN for
// higher rate limits, returning the response body for 2xx responses.
func httpGet(ctx context.Context, url string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "sureva-cli/"+version.Version)
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20)) // 64 MiB safety cap
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, url)
	}
	return body, nil
}

// assetName builds the release archive file name for the given version and
// platform, mirroring the goreleaser name_template. version must already have
// its leading "v" stripped.
func assetName(version, goos, goarch string) string {
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("%s_%s_%s_%s.%s", binaryName, version, goos, goarch, ext)
}

// downloadAndReplace downloads the release archive for this platform, verifies
// its checksum, extracts the binary, and atomically replaces execPath.
func downloadAndReplace(ctx context.Context, tag, version, execPath string) error {
	asset := assetName(version, runtime.GOOS, runtime.GOARCH)
	base := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s", ghOwner, ghRepo, tag)

	archiveData, err := httpGet(ctx, base+"/"+asset, httpTimeoutDownload)
	if err != nil {
		return fmt.Errorf("download %s: %w", asset, err)
	}

	if err := verifyChecksum(ctx, base, asset, archiveData); err != nil {
		return err
	}

	binData, err := extractBinary(archiveData, runtime.GOOS)
	if err != nil {
		return err
	}

	return replaceBinary(execPath, binData)
}

// verifyChecksum downloads checksums.txt and confirms the SHA-256 of data
// matches the entry for asset. It fails closed: any mismatch or missing entry
// aborts the upgrade.
func verifyChecksum(ctx context.Context, base, asset string, data []byte) error {
	sums, err := httpGet(ctx, base+"/checksums.txt", httpTimeoutDownload)
	if err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}
	want, err := checksumFor(string(sums), asset)
	if err != nil {
		return err
	}
	got := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(got[:]), want) {
		return fmt.Errorf("checksum mismatch for %s (refusing to install)", asset)
	}
	return nil
}

// checksumFor parses goreleaser checksums.txt content ("<hex>  <filename>" per
// line) and returns the hex checksum for asset.
func checksumFor(sums, asset string) (string, error) {
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == asset {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("no checksum entry for %s", asset)
}

// extractBinary pulls the sureva binary out of a release archive. Unix
// archives are tar.gz; Windows archives are zip.
func extractBinary(data []byte, goos string) ([]byte, error) {
	if goos == "windows" {
		return extractFromZip(data)
	}
	return extractFromTarGz(data)
}

func extractFromTarGz(data []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("open gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar: %w", err)
		}
		if filepath.Base(hdr.Name) == binaryName {
			return io.ReadAll(io.LimitReader(tr, 256<<20))
		}
	}
	return nil, fmt.Errorf("binary %q not found in archive", binaryName)
}

func extractFromZip(data []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	for _, f := range zr.File {
		base := filepath.Base(f.Name)
		if base == binaryName || base == binaryName+".exe" {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("open zip entry: %w", err)
			}
			defer func() { _ = rc.Close() }()
			return io.ReadAll(io.LimitReader(rc, 256<<20))
		}
	}
	return nil, fmt.Errorf("binary %q not found in archive", binaryName)
}

// replaceBinary writes data to a temp file in the target directory, makes it
// executable, and atomically renames it over dst. On Windows, where a running
// executable cannot be overwritten, it first moves the old binary aside.
func replaceBinary(dst string, data []byte) error {
	dir := filepath.Dir(dst)
	tmp, err := os.CreateTemp(dir, ".sureva-upgrade-*")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write new binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close new binary: %w", err)
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		cleanup()
		return fmt.Errorf("chmod new binary: %w", err)
	}

	if runtime.GOOS == "windows" {
		old := dst + ".old"
		_ = os.Remove(old)
		if err := os.Rename(dst, old); err != nil {
			cleanup()
			return fmt.Errorf("move current binary aside: %w", err)
		}
		if err := os.Rename(tmpName, dst); err != nil {
			_ = os.Rename(old, dst) // best-effort rollback
			return fmt.Errorf("install new binary: %w", err)
		}
		return nil
	}

	if err := os.Rename(tmpName, dst); err != nil {
		cleanup()
		return fmt.Errorf("install new binary (need write access to %s?): %w", dir, err)
	}
	return nil
}

// resolveExecPath returns the absolute, symlink-resolved path of the running
// binary. Resolving symlinks is what reveals Homebrew-managed installs, whose
// bin entry points into the Caskroom.
func resolveExecPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved, nil
	}
	return exe, nil
}

// isHomebrewManaged reports whether execPath lives inside a Homebrew prefix.
// Casks stage the binary under Caskroom and formulae under Cellar; the
// symlink-resolved path therefore contains one of those segments.
func isHomebrewManaged(execPath string) bool {
	p := filepath.ToSlash(execPath)
	if strings.Contains(p, "/Caskroom/") || strings.Contains(p, "/Cellar/") {
		return true
	}
	if prefix := os.Getenv("HOMEBREW_PREFIX"); prefix != "" {
		if strings.HasPrefix(p, filepath.ToSlash(prefix)+"/") {
			return true
		}
	}
	return false
}

// normalizeVersion strips a leading "v" so tags ("v0.2.1") and ldflag versions
// ("0.2.1") compare cleanly.
func normalizeVersion(s string) string {
	return strings.TrimPrefix(strings.TrimSpace(s), "v")
}

// compareVersions compares two dotted numeric versions, returning -1 if a < b,
// 0 if equal, and 1 if a > b. Non-numeric or missing components compare as 0,
// which keeps simple semver (major.minor.patch) ordering correct.
func compareVersions(a, b string) int {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		ai := versionPart(as, i)
		bi := versionPart(bs, i)
		if ai < bi {
			return -1
		}
		if ai > bi {
			return 1
		}
	}
	return 0
}

// versionPart returns the integer value of the i-th dotted component, or 0 when
// missing or non-numeric.
func versionPart(parts []string, i int) int {
	if i >= len(parts) {
		return 0
	}
	// Drop any pre-release/build suffix on the patch component (e.g. "1-rc1").
	field := parts[i]
	if idx := strings.IndexAny(field, "-+"); idx >= 0 {
		field = field[:idx]
	}
	n, err := strconv.Atoi(field)
	if err != nil {
		return 0
	}
	return n
}
