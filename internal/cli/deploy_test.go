package cli_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
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

const (
	deployAppPath = "/v1/orgs/" + testOrgID + "/apps/" + testAppID
	deploySrcID   = "src-new"
)

// deployFake is an API plus an S3 form endpoint, both local.
type deployFake struct {
	t   *testing.T
	api string

	mu          sync.Mutex
	appJSON     string
	maxBytes    int64
	s3Status    int
	s3Body      string
	sourceSeq   []string // statuses GET source answers with, last one repeats
	rejectWhy   string
	deployState []string // statuses GET deployment answers with, last one repeats
	createCode  int      // overrides POST /sources when set
	createBody  string
	// s3Reached, when set, is closed on the first upload request, which then
	// blocks until s3Release is closed (the body is left unread, so the server
	// cannot notice the client going away on its own).
	s3Reached chan struct{}
	s3Release chan struct{}

	partNames   []string
	zipBytes    []byte
	s3Hits      int
	deployBody  map[string]any
	completed   int
	sourceReads int
	deployReads int
}

func newDeployFake(t *testing.T) *deployFake {
	t.Helper()
	f := &deployFake{
		t:           t,
		appJSON:     `{"id":"app-1","source_type":"upload"}`,
		maxBytes:    1 << 20,
		s3Status:    http.StatusNoContent,
		sourceSeq:   []string{"validating", "ready"},
		deployState: []string{"pending", "success"},
	}
	s3 := httptest.NewServer(http.HandlerFunc(f.serveS3))
	t.Cleanup(s3.Close)

	mux := deploys_mux()
	mux.HandleFunc("GET "+deployAppPath, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(f.appJSON))
	})
	mux.HandleFunc("POST "+deployAppPath+"/sources", func(w http.ResponseWriter, r *http.Request) {
		if f.createCode != 0 {
			w.WriteHeader(f.createCode)
			_, _ = w.Write([]byte(f.createBody))
			return
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"source_id":%q,"upload":{"url":%q,"fields":{"key":"uploads/o/a/s.zip","policy":"p","x-amz-signature":"sig"}},"expires_at":"2026-10-01T10:15:00Z","max_bytes":%d,"key":"uploads/o/a/s.zip"}`,
			deploySrcID, s3.URL, f.maxBytes)
	})
	mux.HandleFunc("POST "+deployAppPath+"/sources/"+deploySrcID+"/complete", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.completed++
		f.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"source_id":"` + deploySrcID + `","status":"validating"}`))
	})
	mux.HandleFunc("GET "+deployAppPath+"/sources/"+deploySrcID, func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		status := f.sourceSeq[min(f.sourceReads, len(f.sourceSeq)-1)]
		f.sourceReads++
		f.mu.Unlock()
		body := map[string]any{"id": deploySrcID, "app_id": testAppID, "seq": 3, "status": status, "release_tag": "src-3"}
		if status == "rejected" {
			body["validation_error"] = f.rejectWhy
		}
		_ = json.NewEncoder(w).Encode(body)
	})
	mux.HandleFunc("POST "+deployAppPath+"/deployments", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&f.deployBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"deploy-1","app_id":"app-1","environment_id":"env-1","release_tag":"src-3","status":"pending"}`))
	})
	mux.HandleFunc("GET "+deployAppPath+"/deployments/deploy-1", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		status := f.deployState[min(f.deployReads, len(f.deployState)-1)]
		f.deployReads++
		f.mu.Unlock()
		fmt.Fprintf(w, `{"id":"deploy-1","app_id":"app-1","environment_id":"env-1","release_tag":"src-3","status":%q}`, status)
	})
	f.api = newTestServer(t, mux)
	return f
}

// serveS3 records the order of the multipart parts and the uploaded bytes, and
// refuses a request that carries an API credential.
func (f *deployFake) serveS3(w http.ResponseWriter, r *http.Request) {
	if f.s3Reached != nil {
		close(f.s3Reached)
		<-f.s3Release
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.s3Hits++
	if r.Header.Get("Authorization") != "" {
		f.t.Error("the API token must not be sent to the upload endpoint")
	}
	if r.ContentLength <= 0 {
		f.t.Errorf("upload must carry a Content-Length, got %d", r.ContentLength)
	}
	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		f.t.Errorf("content type: %v", err)
	}
	mr := multipart.NewReader(r.Body, params["boundary"])
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			f.t.Errorf("multipart: %v", err)
			break
		}
		f.partNames = append(f.partNames, p.FormName())
		data, _ := io.ReadAll(p)
		if p.FormName() == "file" {
			f.zipBytes = data
		}
	}
	if f.s3Status >= 300 {
		w.WriteHeader(f.s3Status)
		_, _ = w.Write([]byte(f.s3Body))
		return
	}
	w.WriteHeader(f.s3Status)
}

func (f *deployFake) zipNames() []string {
	zr, err := zip.NewReader(bytes.NewReader(f.zipBytes), int64(len(f.zipBytes)))
	if err != nil {
		f.t.Fatalf("uploaded bytes are not a zip: %v", err)
	}
	var names []string
	for _, e := range zr.File {
		names = append(names, e.Name)
	}
	return names
}

func deployProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range map[string]string{
		"index.js":                  "console.log(1)",
		"src/app.js":                "x",
		"node_modules/dep/i.js":     "x",
		"web/node_modules/dep/i.js": "x",
		".env.local":                "SECRET=1",
		"drop.txt":                  "x",
		".surevaignore":             "drop.txt\n",
	} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("index.js", filepath.Join(root, "link.js")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	return root
}

func decodeJSON(t *testing.T, b *bytes.Buffer) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b.Bytes(), &m); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, b)
	}
	return m
}

func deployArgs(dir string, extra ...string) []string {
	return append([]string{"deploy", dir, "--app", testAppID, "--org", testOrgSlug, "--wait-interval", "1ms", "--wait-timeout", "2s"}, extra...)
}

func TestDeploy_HappyPath(t *testing.T) {
	f := newDeployFake(t)
	dir := deployProject(t)
	outBuf, errBuf, exec := newTestRoot(t, f.api)

	err := exec(deployArgs(dir, "--env-id", "env-1", "--wait")...)

	if got := exitCode(err); got != output.ExitOK {
		t.Fatalf("exit = %d; stderr: %s", got, errBuf)
	}
	if errBuf.Len() != 0 {
		t.Errorf("stderr should be empty on success, got: %s", errBuf)
	}
	if got := strings.Join(f.partNames, ","); got != "key,policy,x-amz-signature,file" {
		t.Errorf("multipart parts = %s, want the form fields first and the file last", got)
	}
	if got := strings.Join(f.zipNames(), ","); got != ".surevaignore,index.js,src/app.js" {
		t.Errorf("archive entries = %s", got)
	}
	if f.completed != 1 {
		t.Errorf("complete called %d times, want 1", f.completed)
	}
	if f.deployBody["source_id"] != deploySrcID || f.deployBody["environment_id"] != "env-1" {
		t.Errorf("deployment request = %v, want source_id %s and environment_id env-1", f.deployBody, deploySrcID)
	}
	if _, has := f.deployBody["release_tag"]; has {
		t.Errorf("release_tag must not be sent: %v", f.deployBody)
	}
	res := decodeJSON(t, outBuf)
	if dep, _ := res["deployment"].(map[string]any); dep["status"] != "success" {
		t.Errorf("deployment = %v, want status success", res["deployment"])
	}
	if src, _ := res["source"].(map[string]any); src["status"] != "ready" || src["release_tag"] != "src-3" {
		t.Errorf("source = %v", res["source"])
	}
	arch, _ := res["archive"].(map[string]any)
	if arch["files"].(float64) != 3 || arch["size_bytes"].(float64) <= 0 {
		t.Errorf("archive = %v", arch)
	}
	exc, _ := arch["excluded"].(map[string]any)
	by, _ := exc["by_reason"].(map[string]any)
	if by["always_excluded"].(float64) != 3 || by["symlink"].(float64) != 1 || by["surevaignore"].(float64) != 1 {
		t.Errorf("excluded by_reason = %v", by)
	}
}

func TestDeploy_WithoutWaitReturnsPendingDeployment(t *testing.T) {
	f := newDeployFake(t)
	outBuf, errBuf, exec := newTestRoot(t, f.api)

	err := exec(deployArgs(deployProject(t))...)

	if got := exitCode(err); got != output.ExitOK {
		t.Fatalf("exit = %d; stderr: %s", got, errBuf)
	}
	res := decodeJSON(t, outBuf)
	if dep, _ := res["deployment"].(map[string]any); dep["status"] != "pending" {
		t.Errorf("deployment = %v, want pending", res["deployment"])
	}
	if f.deployReads != 0 {
		t.Errorf("deployment polled %d times without --wait", f.deployReads)
	}
}

func TestDeploy_GitHubBackedAppRefusedUpFront(t *testing.T) {
	f := newDeployFake(t)
	f.appJSON = `{"id":"app-1","source_type":"github"}`
	outBuf, errBuf, exec := newTestRoot(t, f.api)

	err := exec(deployArgs(deployProject(t))...)

	if got := exitCode(err); got != output.ExitValidation {
		t.Fatalf("exit = %d, want %d", got, output.ExitValidation)
	}
	if code := decodeJSON(t, errBuf)["code"]; code != "github_backed_app" {
		t.Errorf("code = %v", code)
	}
	if outBuf.Len() != 0 || f.s3Hits != 0 {
		t.Errorf("nothing should be printed or uploaded; stdout=%s s3=%d", outBuf, f.s3Hits)
	}
}

func TestDeploy_APIConflictForGitHubApp(t *testing.T) {
	f := newDeployFake(t)
	f.appJSON = `{"id":"app-1"}` // older API: no source_type
	f.createCode = http.StatusConflict
	f.createBody = `{"error":"this app's code comes from its linked GitHub repository; source uploads are only accepted for upload-backed apps"}`
	_, errBuf, exec := newTestRoot(t, f.api)

	err := exec(deployArgs(deployProject(t))...)

	if got := exitCode(err); got != output.ExitValidation {
		t.Fatalf("exit = %d, want %d", got, output.ExitValidation)
	}
	if code := decodeJSON(t, errBuf)["code"]; code != "github_backed_app" {
		t.Errorf("code = %v", code)
	}
}

func TestDeploy_ArchiveTooLarge(t *testing.T) {
	f := newDeployFake(t)
	f.maxBytes = 10
	outBuf, errBuf, exec := newTestRoot(t, f.api)

	err := exec(deployArgs(deployProject(t))...)

	if got := exitCode(err); got != output.ExitValidation {
		t.Fatalf("exit = %d, want %d", got, output.ExitValidation)
	}
	env := decodeJSON(t, errBuf)
	if env["code"] != "archive_too_large" {
		t.Errorf("code = %v", env["code"])
	}
	if msg, _ := env["error"].(string); !strings.Contains(msg, "index.js") || !strings.Contains(msg, "nothing was uploaded") {
		t.Errorf("message should name the largest entries: %s", msg)
	}
	if f.s3Hits != 0 {
		t.Error("an oversized archive must not be uploaded")
	}
	arch, _ := decodeJSON(t, outBuf)["archive"].(map[string]any)
	if arch["max_bytes"].(float64) != 10 {
		t.Errorf("archive = %v, want max_bytes 10", arch)
	}
}

func TestDeploy_S3RefusesAsTooLarge(t *testing.T) {
	f := newDeployFake(t)
	f.s3Status = http.StatusBadRequest
	f.s3Body = `<Error><Code>EntityTooLarge</Code><Message>Your proposed upload exceeds the maximum allowed size</Message></Error>`
	_, errBuf, exec := newTestRoot(t, f.api)

	err := exec(deployArgs(deployProject(t))...)

	if got := exitCode(err); got != output.ExitValidation {
		t.Fatalf("exit = %d, want %d", got, output.ExitValidation)
	}
	if code := decodeJSON(t, errBuf)["code"]; code != "archive_too_large" {
		t.Errorf("code = %v", code)
	}
}

func TestDeploy_S3RefusalIsUploadFailed(t *testing.T) {
	f := newDeployFake(t)
	f.s3Status = http.StatusForbidden
	f.s3Body = `<Error><Code>AccessDenied</Code><Message>Invalid according to Policy</Message></Error>`
	_, errBuf, exec := newTestRoot(t, f.api)

	err := exec(deployArgs(deployProject(t))...)

	if got := exitCode(err); got != output.ExitGeneral {
		t.Fatalf("exit = %d, want %d", got, output.ExitGeneral)
	}
	env := decodeJSON(t, errBuf)
	if env["code"] != "upload_failed" || !strings.Contains(env["error"].(string), "AccessDenied") {
		t.Errorf("envelope = %v", env)
	}
	if f.completed != 0 {
		t.Error("a failed upload must not be completed")
	}
}

func TestDeploy_SourceRejected(t *testing.T) {
	f := newDeployFake(t)
	f.sourceSeq = []string{"validating", "rejected"}
	f.rejectWhy = "the archive contains a link entry (\"x\"); links are not accepted"
	outBuf, errBuf, exec := newTestRoot(t, f.api)

	err := exec(deployArgs(deployProject(t))...)

	if got := exitCode(err); got != output.ExitValidation {
		t.Fatalf("exit = %d, want %d", got, output.ExitValidation)
	}
	env := decodeJSON(t, errBuf)
	if env["code"] != "source_rejected" || !strings.Contains(env["error"].(string), "links are not accepted") {
		t.Errorf("envelope = %v", env)
	}
	res := decodeJSON(t, outBuf)
	if src, _ := res["source"].(map[string]any); src["validation_error"] != f.rejectWhy {
		t.Errorf("source = %v, want the validation error", res["source"])
	}
	if f.deployBody != nil {
		t.Error("a rejected archive must not be deployed")
	}
}

func TestDeploy_ValidationTimeout(t *testing.T) {
	f := newDeployFake(t)
	f.sourceSeq = []string{"validating"}
	_, errBuf, exec := newTestRoot(t, f.api)

	err := exec("deploy", deployProject(t), "--app", testAppID, "--org", testOrgSlug,
		"--wait-interval", "1ms", "--wait-timeout", "30ms")

	if got := exitCode(err); got != output.ExitGeneral {
		t.Fatalf("exit = %d, want %d", got, output.ExitGeneral)
	}
	env := decodeJSON(t, errBuf)
	if env["code"] != "validation_timeout" || !strings.Contains(env["error"].(string), "sources get") {
		t.Errorf("envelope = %v", env)
	}
	if f.deployBody != nil {
		t.Error("nothing should be deployed after a validation timeout")
	}
}

func TestDeploy_DeploymentFailedPointsAtLogs(t *testing.T) {
	f := newDeployFake(t)
	f.deployState = []string{"pending", "failed"}
	outBuf, errBuf, exec := newTestRoot(t, f.api)

	err := exec(deployArgs(deployProject(t), "--wait")...)

	if got := exitCode(err); got != output.ExitGeneral {
		t.Fatalf("exit = %d, want %d", got, output.ExitGeneral)
	}
	if code := decodeJSON(t, errBuf)["code"]; code != "deploy_failed" {
		t.Errorf("code = %v", code)
	}
	res := decodeJSON(t, outBuf)
	logs, _ := res["logs"].(map[string]any)
	if logs["command"] != "sureva logs app-1 --org acme --env-id env-1" || logs["deployment_id"] != "deploy-1" || logs["environment_id"] != "env-1" {
		t.Errorf("logs = %v", logs)
	}
	if dep, _ := res["deployment"].(map[string]any); dep["status"] != "failed" {
		t.Errorf("deployment = %v", res["deployment"])
	}
}

func TestDeploy_ExpiredCredentials(t *testing.T) {
	f := newDeployFake(t)
	f.createCode = http.StatusUnauthorized
	f.createBody = `{"error":"token expired"}`
	_, errBuf, exec := newTestRoot(t, f.api)

	err := exec(deployArgs(deployProject(t))...)

	if got := exitCode(err); got != output.ExitAuth {
		t.Fatalf("exit = %d, want %d", got, output.ExitAuth)
	}
	if code := decodeJSON(t, errBuf)["code"]; code != "auth_error" {
		t.Errorf("code = %v", code)
	}
}

func TestDeploy_EmptyAfterExclusions(t *testing.T) {
	f := newDeployFake(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, errBuf, exec := newTestRoot(t, f.api)

	err := exec(deployArgs(dir)...)

	if got := exitCode(err); got != output.ExitValidation {
		t.Fatalf("exit = %d, want %d", got, output.ExitValidation)
	}
	if code := decodeJSON(t, errBuf)["code"]; code != "empty_archive" {
		t.Errorf("code = %v", code)
	}
}

func TestDeploy_RequiresAppAndDirectory(t *testing.T) {
	f := newDeployFake(t)
	_, errBuf, exec := newTestRoot(t, f.api)

	if got := exitCode(exec("deploy", t.TempDir(), "--org", testOrgSlug)); got != output.ExitValidation {
		t.Errorf("missing --app: exit = %d, want %d", got, output.ExitValidation)
	}
	if code := decodeJSON(t, errBuf)["code"]; code != "validation_error" {
		t.Errorf("code = %v", code)
	}
	if got := exitCode(exec("deploy", filepath.Join(t.TempDir(), "missing"), "--app", testAppID, "--org", testOrgSlug)); got != output.ExitValidation {
		t.Errorf("missing dir: exit = %d, want %d", got, output.ExitValidation)
	}
}

func TestHelpJSON_ListsDeploy(t *testing.T) {
	outBuf, _, exec := newTestRoot(t, "")
	if err := exec("--help", "--json"); exitCode(err) != 0 {
		t.Fatalf("exit %d", exitCode(err))
	}
	var tree struct {
		Commands []struct {
			Name  string `json:"name"`
			Flags []struct {
				Name string `json:"name"`
			} `json:"flags"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(outBuf.Bytes(), &tree); err != nil {
		t.Fatal(err)
	}
	for _, c := range tree.Commands {
		if c.Name != "deploy" {
			continue
		}
		flags := map[string]bool{}
		for _, f := range c.Flags {
			flags[f.Name] = true
		}
		for _, want := range []string{"app", "env-id", "wait", "wait-interval", "wait-timeout"} {
			if !flags[want] {
				t.Errorf("deploy is missing flag --%s in the help tree", want)
			}
		}
		return
	}
	t.Error("deploy is not in the help tree")
}

func TestDeployHelpNamesFailureCodes(t *testing.T) {
	outBuf, _, exec := newTestRoot(t, "")
	if err := exec("deploy", "--help"); exitCode(err) != 0 {
		t.Fatalf("exit %d", exitCode(err))
	}
	for _, want := range []string{"archive_too_large", "source_rejected", "validation_timeout", "deploy_failed", "auth_error", "pack_failed", "interrupted"} {
		if !strings.Contains(outBuf.String(), want) {
			t.Errorf("deploy --help is missing %q", want)
		}
	}
}

// Interrupting a deploy while the archive is uploading must stop the request,
// remove the temporary archive and report the "interrupted" code. The test
// cancels the context the way the signal handler does; it sends no signal.
func TestDeploy_InterruptedDuringUploadRemovesArchive(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	f := newDeployFake(t)
	f.s3Reached = make(chan struct{})
	f.s3Release = make(chan struct{})
	t.Cleanup(func() { close(f.s3Release) }) // runs before the server's Close
	outBuf, errBuf, _ := newTestRoot(t, f.api)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-f.s3Reached
		// The archive exists on disk while it is being sent.
		if m, _ := filepath.Glob(filepath.Join(tmp, "sureva-deploy-*.zip")); len(m) != 1 {
			t.Errorf("expected the archive on disk during the upload, found %v", m)
		}
		cancel()
	}()

	root := cli.NewRootCmd()
	root.SetOut(outBuf)
	root.SetErr(errBuf)
	root.SetArgs(deployArgs(deployProject(t)))
	err := root.ExecuteContext(ctx)

	if got := exitCode(err); got != output.ExitGeneral {
		t.Fatalf("exit = %d, want %d; stderr: %s", got, output.ExitGeneral, errBuf)
	}
	var env map[string]any
	if jerr := json.Unmarshal(errBuf.Bytes(), &env); jerr != nil {
		t.Fatalf("stderr is not one JSON envelope: %v\n%s", jerr, errBuf)
	}
	if env["code"] != "interrupted" {
		t.Errorf("code = %v, want interrupted; envelope: %s", env["code"], errBuf)
	}
	if outBuf.Len() > 0 {
		decodeJSON(t, outBuf) // stdout, when present, is one valid document
	}
	if m, _ := filepath.Glob(filepath.Join(tmp, "sureva-deploy-*.zip")); len(m) != 0 {
		t.Errorf("temporary archive left behind: %v", m)
	}
}
