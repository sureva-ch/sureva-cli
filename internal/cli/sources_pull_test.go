package cli_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sureva-ch/sureva-cli/internal/cli"
	"github.com/sureva-ch/sureva-cli/internal/output"
)

const pullURLSecret = "topsecretsignature"

type zipEntry struct {
	name string
	body string
	mode fs.FileMode
}

func zipOf(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(e.body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// pullFake is an API plus a storage endpoint, both local.
type pullFake struct {
	t       *testing.T
	api     string
	storage *httptest.Server

	mu         sync.Mutex
	appJSON    string
	archive    []byte
	reportSHA  string // overrides the real digest when set
	reportSize int64  // overrides the real size when non-zero
	omitFacts  bool   // leave size_bytes and sha256 out of the answer

	downloadStatus int // overrides the download answer when set
	downloadBody   string

	storageStatus int
	storageBody   string
	// reached, when set, is closed once the first bytes were sent; the handler
	// then blocks until release is closed.
	reached chan struct{}
	release chan struct{}

	downloadIDs []string
	storageAuth []string
	storageHits int
	appReads    int
}

func newPullFake(t *testing.T, archive []byte) *pullFake {
	t.Helper()
	f := &pullFake{t: t, archive: archive, appJSON: `{"id":"app-1","source_type":"upload"}`}
	f.storage = httptest.NewServer(http.HandlerFunc(f.serveStorage))
	t.Cleanup(f.storage.Close)

	mux := deploys_mux()
	mux.HandleFunc("GET "+deployAppPath, func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		f.appReads++
		f.mu.Unlock()
		_, _ = w.Write([]byte(f.appJSON))
	})
	mux.HandleFunc("GET "+deployAppPath+"/sources/{id}/download", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.downloadIDs = append(f.downloadIDs, r.PathValue("id"))
		if f.downloadStatus != 0 {
			w.WriteHeader(f.downloadStatus)
			_, _ = w.Write([]byte(f.downloadBody))
			return
		}
		sum, size := f.reportSHA, f.reportSize
		if sum == "" {
			sum = sha(f.archive)
		}
		if size == 0 {
			size = int64(len(f.archive))
		}
		body := map[string]any{
			"url":         f.storage.URL + "/obj/source.zip?X-Amz-Signature=" + pullURLSecret,
			"expires_at":  "2026-10-04T12:15:00Z",
			"source_id":   "src-id-3",
			"release_tag": "src-3",
		}
		if !f.omitFacts {
			body["size_bytes"] = size
			body["sha256"] = sum
		}
		_ = json.NewEncoder(w).Encode(body)
	})
	f.api = newTestServer(t, mux)
	return f
}

func (f *pullFake) serveStorage(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.storageHits++
	f.storageAuth = append(f.storageAuth, r.Header.Get("Authorization"))
	status, body, reached, release := f.storageStatus, f.storageBody, f.reached, f.release
	f.mu.Unlock()
	if status != 0 {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
		return
	}
	if reached != nil {
		_, _ = w.Write(f.archive[:len(f.archive)/2])
		w.(http.Flusher).Flush()
		close(reached)
		<-release
		return
	}
	_, _ = w.Write(f.archive)
}

func pullArgs(dir string, extra ...string) []string {
	return append([]string{"sources", "pull", testAppID, "--org", testOrgSlug, "--dir", dir}, extra...)
}

func projectZip(t *testing.T) []byte {
	return zipOf(t,
		zipEntry{"index.js", "console.log(1)", 0o644},
		zipEntry{"src/app.js", "x", 0o644},
		zipEntry{"bin/run.sh", "#!/bin/sh", 0o755},
	)
}

// assertQuiet checks that neither stream carries the presigned URL.
func assertQuiet(t *testing.T, bufs ...*bytes.Buffer) {
	t.Helper()
	for _, b := range bufs {
		if strings.Contains(b.String(), pullURLSecret) || strings.Contains(b.String(), "X-Amz-Signature") {
			t.Errorf("the presigned URL leaked into output:\n%s", b)
		}
	}
}

func envelopeCode(t *testing.T, b *bytes.Buffer) string {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal(b.Bytes(), &env); err != nil {
		t.Fatalf("stderr is not one JSON envelope: %v\n%s", err, b)
	}
	code, _ := env["code"].(string)
	return code
}

func TestSourcesPull_LatestIntoNewDirectory(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	f := newPullFake(t, projectZip(t))
	outBuf, errBuf, exec := newTestRoot(t, f.api)
	dir := filepath.Join(t.TempDir(), "app")

	err := exec(pullArgs(dir)...)

	if got := exitCode(err); got != output.ExitOK {
		t.Fatalf("exit = %d; stderr: %s", got, errBuf)
	}
	res := decodeJSON(t, outBuf)
	for key, want := range map[string]any{
		"app_id": testAppID, "source_id": "src-id-3", "release_tag": "src-3", "dir": dir,
		"files": float64(3), "sha256": sha(f.archive), "archive_bytes": float64(len(f.archive)),
		"state_file": filepath.Join(dir, ".sureva", "source.json"), "bytes": float64(len("console.log(1)") + 1 + len("#!/bin/sh")),
	} {
		if res[key] != want {
			t.Errorf("%s = %v, want %v", key, res[key], want)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "src", "app.js")); string(got) != "x" {
		t.Errorf("src/app.js = %q", got)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "bin", "run.sh")); fi == nil || fi.Mode().Perm() != 0o755 {
		t.Errorf("run.sh lost its executable bit: %v", fi)
	}
	if len(f.downloadIDs) != 1 || f.downloadIDs[0] != "latest" {
		t.Errorf("download ids = %v, want [latest]", f.downloadIDs)
	}
	for _, a := range f.storageAuth {
		if a != "" {
			t.Errorf("the API token was sent to storage: %q", a)
		}
	}
	if m, _ := filepath.Glob(filepath.Join(tmp, "sureva-pull-*")); len(m) != 0 {
		t.Errorf("temporary archive left behind: %v", m)
	}
	assertQuiet(t, outBuf, errBuf)
}

func TestSourcesPull_WritesTheStateFile(t *testing.T) {
	f := newPullFake(t, projectZip(t))
	_, errBuf, exec := newTestRoot(t, f.api)
	dir := t.TempDir()

	if err := exec(pullArgs(dir)...); exitCode(err) != 0 {
		t.Fatalf("exit %d: %s", exitCode(err), errBuf)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".sureva", "source.json"))
	if err != nil {
		t.Fatal(err)
	}
	var st map[string]any
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{"app_id": testAppID, "source_id": "src-id-3", "release_tag": "src-3", "sha256": sha(f.archive)} {
		if st[key] != want {
			t.Errorf("state %s = %v, want %v", key, st[key], want)
		}
	}
	if s, _ := st["pulled_at"].(string); !strings.HasPrefix(s, "20") {
		t.Errorf("pulled_at = %v, want a timestamp", st["pulled_at"])
	}
}

func TestSourcesPull_ExplicitSourceID(t *testing.T) {
	f := newPullFake(t, projectZip(t))
	_, errBuf, exec := newTestRoot(t, f.api)

	if err := exec(pullArgs(t.TempDir(), "--source-id", "src-id-3")...); exitCode(err) != 0 {
		t.Fatalf("exit %d: %s", exitCode(err), errBuf)
	}
	if len(f.downloadIDs) != 1 || f.downloadIDs[0] != "src-id-3" {
		t.Errorf("download ids = %v", f.downloadIDs)
	}
}

func TestSourcesPull_ChecksumMismatchExtractsNothing(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	f := newPullFake(t, projectZip(t))
	f.reportSHA = strings.Repeat("0", 64)
	outBuf, errBuf, exec := newTestRoot(t, f.api)
	dir := filepath.Join(t.TempDir(), "app")

	err := exec(pullArgs(dir)...)

	if got := exitCode(err); got != output.ExitGeneral {
		t.Fatalf("exit = %d, want %d", got, output.ExitGeneral)
	}
	if code := envelopeCode(t, errBuf); code != "checksum_mismatch" {
		t.Errorf("code = %s", code)
	}
	if _, statErr := os.Stat(dir); statErr == nil {
		t.Error("nothing may be created when the checksum does not match")
	}
	if m, _ := filepath.Glob(filepath.Join(tmp, "sureva-pull-*")); len(m) != 0 {
		t.Errorf("temporary archive left behind: %v", m)
	}
	assertQuiet(t, outBuf, errBuf)
}

func TestSourcesPull_HostileArchivesAreRefused(t *testing.T) {
	cases := map[string][]zipEntry{
		"traversal":     {{"ok.txt", "ok", 0o644}, {"../escape.txt", "x", 0o644}},
		"absolute path": {{"/tmp/escape.txt", "x", 0o644}},
		"backslash":     {{`a\..\escape.txt`, "x", 0o644}},
		"drive letter":  {{"C:/escape.txt", "x", 0o644}},
		"symlink entry": {{"ok.txt", "ok", 0o644}, {"link", "/etc/passwd", fs.ModeSymlink | 0o777}},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			f := newPullFake(t, zipOf(t, entries...))
			outBuf, errBuf, exec := newTestRoot(t, f.api)
			parent := t.TempDir()
			dir := filepath.Join(parent, "app")

			err := exec(pullArgs(dir)...)

			if got := exitCode(err); got != output.ExitGeneral {
				t.Fatalf("exit = %d, want %d; stderr: %s", got, output.ExitGeneral, errBuf)
			}
			if code := envelopeCode(t, errBuf); code != "unsafe_archive" {
				t.Errorf("code = %s", code)
			}
			if _, statErr := os.Stat(dir); statErr == nil {
				t.Error("the target must not exist after a refused archive")
			}
			if _, statErr := os.Stat(filepath.Join(parent, "escape.txt")); statErr == nil {
				t.Error("a file escaped the target")
			}
			assertQuiet(t, outBuf, errBuf)
		})
	}
}

func TestSourcesPull_ForceRefusesAnEscapeThroughAnExistingSymlink(t *testing.T) {
	outside := t.TempDir()
	dir := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	f := newPullFake(t, zipOf(t, zipEntry{"link/evil.txt", "x", 0o644}))
	_, errBuf, exec := newTestRoot(t, f.api)

	err := exec(pullArgs(dir, "--force")...)

	if got := exitCode(err); got != output.ExitGeneral {
		t.Fatalf("exit = %d; stderr: %s", got, errBuf)
	}
	if code := envelopeCode(t, errBuf); code != "unsafe_archive" {
		t.Errorf("code = %s", code)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "evil.txt")); statErr == nil {
		t.Fatal("a file was written outside the target")
	}
}

func TestSourcesPull_NonEmptyDirectory(t *testing.T) {
	f := newPullFake(t, projectZip(t))
	outBuf, errBuf, exec := newTestRoot(t, f.api)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mine.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := exec(pullArgs(dir)...)

	if got := exitCode(err); got != output.ExitValidation {
		t.Fatalf("exit = %d, want %d", got, output.ExitValidation)
	}
	if code := envelopeCode(t, errBuf); code != "dir_not_empty" {
		t.Errorf("code = %s", code)
	}
	if f.storageHits != 0 || len(f.downloadIDs) != 0 {
		t.Errorf("nothing may be downloaded for a refused directory (storage hits %d, download calls %d)", f.storageHits, len(f.downloadIDs))
	}
	if _, statErr := os.Stat(filepath.Join(dir, "index.js")); statErr == nil {
		t.Error("files were written into a refused directory")
	}

	// With --force it proceeds and leaves what the archive does not mention.
	if err := exec(pullArgs(dir, "--force")...); exitCode(err) != 0 {
		t.Fatalf("--force: exit %d: %s", exitCode(err), errBuf)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "mine.txt")); string(got) != "mine" {
		t.Errorf("mine.txt = %q, want it untouched", got)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "index.js")); statErr != nil {
		t.Errorf("index.js missing after --force: %v", statErr)
	}
	_ = outBuf
}

func TestSourcesPull_EmptyDirectoryHoldingOnlyTheStateDirIsAccepted(t *testing.T) {
	f := newPullFake(t, projectZip(t))
	_, errBuf, exec := newTestRoot(t, f.api)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".sureva"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := exec(pullArgs(dir)...); exitCode(err) != 0 {
		t.Fatalf("exit %d: %s", exitCode(err), errBuf)
	}
	if _, err := os.Stat(filepath.Join(dir, ".sureva", "source.json")); err != nil {
		t.Errorf("state file missing: %v", err)
	}
}

func TestSourcesPull_DirIsAFile(t *testing.T) {
	f := newPullFake(t, projectZip(t))
	_, errBuf, exec := newTestRoot(t, f.api)
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := exitCode(exec(pullArgs(file)...)); got != output.ExitValidation {
		t.Fatalf("exit = %d, want %d", got, output.ExitValidation)
	}
	if code := envelopeCode(t, errBuf); code != "validation_error" {
		t.Errorf("code = %s", code)
	}
}

func TestSourcesPull_APIFailures(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		status int
		body   string
		code   string
		exit   int
		want   string // substring of the message
	}{
		{"no ready release", nil, 404, `{"error":"this app has no source archive ready to download; upload one first"}`, "no_source", output.ExitNotFound, "sureva deploy"},
		{"unknown source id", []string{"--source-id", "nope"}, 404, `{"error":"source not found"}`, "not_found", output.ExitNotFound, "source not found"},
		{"not ready", nil, 409, `{"error":"this source archive is validating and cannot be downloaded; only a ready archive can"}`, "source_not_ready", output.ExitGeneral, "validating"},
		{"expired", nil, 410, `{"error":"this source archive has expired and is no longer stored; upload the source again"}`, "source_expired", output.ExitGeneral, "expired"},
		{"no storage", nil, 503, `{"error":"downloading an uploaded source is not available for this app right now"}`, "server_error", output.ExitGeneral, "server error"},
		{"github backed", nil, 422, `{"error":"this app's code comes from its linked GitHub repository","code":"app_not_upload_backed"}`, "github_backed_app", output.ExitValidation, "GitHub repository"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newPullFake(t, projectZip(t))
			f.downloadStatus, f.downloadBody = tc.status, tc.body
			outBuf, errBuf, exec := newTestRoot(t, f.api)
			dir := filepath.Join(t.TempDir(), "app")

			err := exec(pullArgs(dir, tc.args...)...)

			if got := exitCode(err); got != tc.exit {
				t.Fatalf("exit = %d, want %d; stderr: %s", got, tc.exit, errBuf)
			}
			if code := envelopeCode(t, errBuf); code != tc.code {
				t.Errorf("code = %s, want %s", code, tc.code)
			}
			if !strings.Contains(errBuf.String(), tc.want) {
				t.Errorf("message lacks %q: %s", tc.want, errBuf)
			}
			if _, statErr := os.Stat(dir); statErr == nil {
				t.Error("nothing may be created on a failure")
			}
			if f.storageHits != 0 {
				t.Error("storage must not be contacted")
			}
			assertQuiet(t, outBuf, errBuf)
		})
	}
}

func TestSourcesPull_GitHubBackedAppNamesTheRepository(t *testing.T) {
	f := newPullFake(t, projectZip(t))
	f.appJSON = `{"id":"app-1","source_type":"github","github_repo_full":"acme/site"}`
	_, errBuf, exec := newTestRoot(t, f.api)

	err := exec(pullArgs(filepath.Join(t.TempDir(), "app"))...)

	if got := exitCode(err); got != output.ExitValidation {
		t.Fatalf("exit = %d", got)
	}
	if code := envelopeCode(t, errBuf); code != "github_backed_app" {
		t.Errorf("code = %s", code)
	}
	if !strings.Contains(errBuf.String(), "acme/site") || !strings.Contains(errBuf.String(), "clone") {
		t.Errorf("message must name the repository and say to clone it: %s", errBuf)
	}
	if len(f.downloadIDs) != 0 {
		t.Error("no download should be requested for a GitHub-backed app")
	}
}

func TestSourcesPull_GitHubBackedAppFoundByTheAPI(t *testing.T) {
	// The app record does not say (source_type is not sent yet): the API's 422
	// decides, and the repository still comes from the record.
	f := newPullFake(t, projectZip(t))
	f.appJSON = `{"id":"app-1","github_repo_full":"acme/site"}`
	f.downloadStatus = 422
	f.downloadBody = `{"error":"clone it","code":"app_not_upload_backed"}`
	_, errBuf, exec := newTestRoot(t, f.api)

	_ = exec(pullArgs(filepath.Join(t.TempDir(), "app"))...)

	if code := envelopeCode(t, errBuf); code != "github_backed_app" || !strings.Contains(errBuf.String(), "acme/site") {
		t.Errorf("code = %s; %s", code, errBuf)
	}
}

func TestSourcesPull_StorageFailures(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		code   string
	}{
		{"link expired", 403, `<Error><Code>AccessDenied</Code><Message>Request has expired</Message></Error>`, "download_expired"},
		{"refused", 403, `<Error><Code>AccessDenied</Code><Message>Access Denied</Message></Error>`, "download_failed"},
		{"storage error", 500, ``, "download_failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newPullFake(t, projectZip(t))
			f.storageStatus, f.storageBody = tc.status, tc.body
			outBuf, errBuf, exec := newTestRoot(t, f.api)

			err := exec(pullArgs(filepath.Join(t.TempDir(), "app"))...)

			if got := exitCode(err); got != output.ExitGeneral {
				t.Fatalf("exit = %d", got)
			}
			if code := envelopeCode(t, errBuf); code != tc.code {
				t.Errorf("code = %s, want %s", code, tc.code)
			}
			assertQuiet(t, outBuf, errBuf)
		})
	}
}

func TestSourcesPull_StorageUnreachableIsANetworkErrorWithoutTheURL(t *testing.T) {
	f := newPullFake(t, projectZip(t))
	f.storage.Close()
	outBuf, errBuf, exec := newTestRoot(t, f.api)

	err := exec(pullArgs(filepath.Join(t.TempDir(), "app"))...)

	if got := exitCode(err); got != output.ExitNetwork {
		t.Fatalf("exit = %d, want %d; stderr: %s", got, output.ExitNetwork, errBuf)
	}
	assertQuiet(t, outBuf, errBuf)
}

func TestSourcesPull_MoreDataThanReportedIsStopped(t *testing.T) {
	f := newPullFake(t, projectZip(t))
	f.reportSize = 10
	outBuf, errBuf, exec := newTestRoot(t, f.api)
	dir := filepath.Join(t.TempDir(), "app")

	err := exec(pullArgs(dir)...)

	if got := exitCode(err); got != output.ExitGeneral {
		t.Fatalf("exit = %d", got)
	}
	if code := envelopeCode(t, errBuf); code != "download_failed" {
		t.Errorf("code = %s", code)
	}
	if _, statErr := os.Stat(dir); statErr == nil {
		t.Error("nothing may be extracted")
	}
	assertQuiet(t, outBuf, errBuf)
}

func TestSourcesPull_UnverifiableArchiveIsNotDownloaded(t *testing.T) {
	f := newPullFake(t, projectZip(t))
	f.omitFacts = true
	_, errBuf, exec := newTestRoot(t, f.api)

	err := exec(pullArgs(filepath.Join(t.TempDir(), "app"))...)

	if got := exitCode(err); got != output.ExitGeneral {
		t.Fatalf("exit = %d", got)
	}
	if code := envelopeCode(t, errBuf); code != "download_failed" {
		t.Errorf("code = %s", code)
	}
	if f.storageHits != 0 {
		t.Error("an archive without a reported checksum must not be fetched")
	}
}

func TestSourcesPull_ExpiredCredentials(t *testing.T) {
	f := newPullFake(t, projectZip(t))
	f.downloadStatus, f.downloadBody = 401, `{"error":"expired"}`
	_, errBuf, exec := newTestRoot(t, f.api)

	if got := exitCode(exec(pullArgs(filepath.Join(t.TempDir(), "app"))...)); got != output.ExitAuth {
		t.Fatalf("exit = %d, want %d", got, output.ExitAuth)
	}
	if code := envelopeCode(t, errBuf); code != "auth_error" {
		t.Errorf("code = %s", code)
	}
}

// Interrupting a pull while the archive is downloading must stop the request,
// remove the temporary archive and report "interrupted". The test cancels the
// context the way the signal handler does; it sends no signal.
func TestSourcesPull_InterruptedDuringDownloadRemovesArchive(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	f := newPullFake(t, projectZip(t))
	f.reached = make(chan struct{})
	f.release = make(chan struct{})
	t.Cleanup(func() { close(f.release) }) // runs before the server's Close
	outBuf, errBuf, _ := newTestRoot(t, f.api)
	dir := filepath.Join(t.TempDir(), "app")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-f.reached
		if m, _ := filepath.Glob(filepath.Join(tmp, "sureva-pull-*.zip")); len(m) != 1 {
			t.Errorf("expected the temporary archive on disk during the download, found %v", m)
		} else if fi, _ := os.Stat(m[0]); fi != nil && fi.Mode().Perm() != 0o600 {
			t.Errorf("temporary archive mode = %v, want 0600", fi.Mode().Perm())
		}
		cancel()
	}()

	root := cli.NewRootCmd()
	root.SetOut(outBuf)
	root.SetErr(errBuf)
	root.SetArgs(pullArgs(dir))
	err := root.ExecuteContext(ctx)

	if got := exitCode(err); got != output.ExitGeneral {
		t.Fatalf("exit = %d, want %d; stderr: %s", got, output.ExitGeneral, errBuf)
	}
	if code := envelopeCode(t, errBuf); code != "interrupted" {
		t.Errorf("code = %s", code)
	}
	if m, _ := filepath.Glob(filepath.Join(tmp, "sureva-pull-*")); len(m) != 0 {
		t.Errorf("temporary archive left behind: %v", m)
	}
	if _, statErr := os.Stat(dir); statErr == nil {
		t.Error("nothing may be extracted after an interruption")
	}
	assertQuiet(t, outBuf, errBuf)
}

func TestSourcesPull_HelpAndTree(t *testing.T) {
	outBuf, _, exec := newTestRoot(t, "")
	if err := exec("sources", "pull", "--help"); exitCode(err) != 0 {
		t.Fatalf("exit %d", exitCode(err))
	}
	help := outBuf.String()
	for _, want := range []string{
		"no_source", "source_expired", "source_not_ready", "github_backed_app", "checksum_mismatch", "unsafe_archive",
		"dir_not_empty", "download_failed", "download_expired", "interrupted", "extract_failed",
		"node_modules", "sureva env get", "wrapper directory", "--force", "left alone", ".sureva/source.json",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("sources pull --help is missing %q", want)
		}
	}
	if strings.Contains(help, "--live") {
		t.Error("--live is not implemented and must not be advertised")
	}

	outBuf2, _, exec2 := newTestRoot(t, "")
	if err := exec2("--help", "--json"); exitCode(err) != 0 {
		t.Fatalf("exit %d", exitCode(err))
	}
	var tree struct {
		Commands []struct {
			Name        string `json:"name"`
			Subcommands []struct {
				Name  string `json:"name"`
				Flags []struct {
					Name string `json:"name"`
				} `json:"flags"`
			} `json:"subcommands"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(outBuf2.Bytes(), &tree); err != nil {
		t.Fatal(err)
	}
	for _, c := range tree.Commands {
		if c.Name != "sources" {
			continue
		}
		for _, sub := range c.Subcommands {
			if sub.Name != "pull" {
				continue
			}
			flags := map[string]bool{}
			for _, f := range sub.Flags {
				flags[f.Name] = true
			}
			for _, want := range []string{"source-id", "dir", "force"} {
				if !flags[want] {
					t.Errorf("sources pull is missing flag --%s in the help tree", want)
				}
			}
			return
		}
	}
	t.Error("sources pull is not in the help tree")
}

func TestSourcesPull_ForceRefusesAStateFileThatIsASymlinkBeforeDownloading(t *testing.T) {
	victim := filepath.Join(t.TempDir(), "victim.txt")
	if err := os.WriteFile(victim, []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".sureva"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mine.txt"), []byte("m"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, ".sureva", "source.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	f := newPullFake(t, projectZip(t))
	_, errBuf, exec := newTestRoot(t, f.api)

	err := exec(pullArgs(dir, "--force")...)

	if got := exitCode(err); got != output.ExitValidation {
		t.Fatalf("exit = %d; stderr: %s", got, errBuf)
	}
	if code := envelopeCode(t, errBuf); code != "validation_error" {
		t.Errorf("code = %s", code)
	}
	if got, _ := os.ReadFile(victim); string(got) != "precious" {
		t.Errorf("the link target was overwritten: %q", got)
	}
	if f.storageHits != 0 {
		t.Errorf("nothing may be downloaded, storage hits = %d", f.storageHits)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "index.js")); statErr == nil {
		t.Error("files were extracted before the state file was refused")
	}
}

func TestSourcesPull_ForceRefusesAStateDirectoryThatIsASymlink(t *testing.T) {
	elsewhere := t.TempDir()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mine.txt"), []byte("m"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(dir, ".sureva")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	f := newPullFake(t, projectZip(t))
	_, errBuf, exec := newTestRoot(t, f.api)

	err := exec(pullArgs(dir, "--force")...)

	if got := exitCode(err); got != output.ExitValidation {
		t.Fatalf("exit = %d; stderr: %s", got, errBuf)
	}
	if _, statErr := os.Stat(filepath.Join(elsewhere, "source.json")); statErr == nil {
		t.Error("the state file was created through the link")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "index.js")); statErr == nil {
		t.Error("files were extracted before the state directory was refused")
	}
}

func TestSourcesPull_ForceTypeConflictIsRefusedBeforeExtracting(t *testing.T) {
	dir := t.TempDir()
	// The archive has "src/app.js"; the user has a file named "src".
	if err := os.WriteFile(filepath.Join(dir, "src"), []byte("a file"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := newPullFake(t, projectZip(t))
	_, errBuf, exec := newTestRoot(t, f.api)

	err := exec(pullArgs(dir, "--force")...)

	if got := exitCode(err); got != output.ExitValidation {
		t.Fatalf("exit = %d; stderr: %s", got, errBuf)
	}
	if !strings.Contains(errBuf.String(), "src") {
		t.Errorf("the message must name the conflicting path: %s", errBuf)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "index.js")); statErr == nil {
		t.Error("files were written before the conflict was reported")
	}
}
