package changes

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestBuildGraph(t *testing.T) {
	repo := newGraphFixture(t)

	tests := []struct {
		name    string
		changed []Change
		want    Graph
	}{
		{
			name:    "changed go file resolves internal imports to repo files",
			changed: []Change{{Path: "internal/cli/changes.go", Status: StatusModified}},
			want: Graph{
				Base:  "main",
				Nodes: []string{"internal/cli/changes.go"},
				Edges: []Edge{
					{From: "internal/cli/changes.go", To: "internal/changes/git.go"},
					{From: "internal/cli/changes.go", To: "internal/changes/graph.go"},
				},
				Statuses: map[string]string{"internal/cli/changes.go": "modified"},
			},
		},
		{
			name:    "new go file is tagged and resolves imports",
			changed: []Change{{Path: "internal/cli/changes.go", Status: StatusNew}},
			want: Graph{
				Base:  "main",
				Nodes: []string{"internal/cli/changes.go"},
				Edges: []Edge{
					{From: "internal/cli/changes.go", To: "internal/changes/git.go"},
					{From: "internal/cli/changes.go", To: "internal/changes/graph.go"},
				},
				Statuses: map[string]string{"internal/cli/changes.go": "new"},
			},
		},
		{
			name:    "deleted go file is a node but is not parsed for imports",
			changed: []Change{{Path: "internal/cli/changes.go", Status: StatusDeleted}},
			want: Graph{
				Base:     "main",
				Nodes:    []string{"internal/cli/changes.go"},
				Edges:    nil,
				Statuses: map[string]string{"internal/cli/changes.go": "deleted"},
			},
		},
		{
			name:    "changed non go file remains a node without edges",
			changed: []Change{{Path: "README.md", Status: StatusModified}},
			want: Graph{
				Base:     "main",
				Nodes:    []string{"README.md"},
				Edges:    nil,
				Statuses: map[string]string{"README.md": "modified"},
			},
		},
		{
			name:    "external imports are ignored and nodes are sorted",
			changed: []Change{{Path: "README.md", Status: StatusModified}, {Path: "internal/cli/external.go", Status: StatusNew}},
			want: Graph{
				Base:     "main",
				Nodes:    []string{"README.md", "internal/cli/external.go"},
				Edges:    nil,
				Statuses: map[string]string{"README.md": "modified", "internal/cli/external.go": "new"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BuildGraph(repo, "main", tt.changed)
			if err != nil {
				t.Fatalf("BuildGraph: %v", err)
			}
			got.Repository = ""
			got.Checklist = nil // checklist is covered by TestBuildChecklist
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("BuildGraph() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestBuildUsesGitChangedFiles(t *testing.T) {
	repo := newGraphFixture(t)
	git := &fakeGit{
		files: []Change{{Path: "internal/cli/changes.go", Status: StatusModified}},
		diffs: map[string]string{"internal/cli/changes.go": "diff --git a/x b/x\n+added"},
		churn: map[string]Churn{"internal/cli/changes.go": {Added: 5, Deleted: 2}},
	}

	graph, err := (Builder{RepoRoot: repo, Git: git}).Build(context.Background(), "develop")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if git.base != "develop" {
		t.Fatalf("git base = %q, want develop", git.base)
	}
	if graph.Base != "develop" {
		t.Fatalf("graph base = %q, want develop", graph.Base)
	}
	if graph.Repository != filepath.Base(repo) {
		t.Fatalf("graph repository = %q, want %q", graph.Repository, filepath.Base(repo))
	}
	if len(graph.Edges) != 2 {
		t.Fatalf("edges len = %d, want 2", len(graph.Edges))
	}
	if graph.Diffs["internal/cli/changes.go"] != "diff --git a/x b/x\n+added" {
		t.Fatalf("graph diffs = %#v, want attached diff", graph.Diffs)
	}
	if got := graph.Churn["internal/cli/changes.go"]; got != (Churn{Added: 5, Deleted: 2}) {
		t.Fatalf("graph churn = %#v, want {5 2}", got)
	}
}

func TestBuildGraphAddsRepositoryName(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "sureva-cli")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}

	graph, err := BuildGraph(repo, "main", []Change{{Path: "README.md", Status: StatusModified}})
	if err != nil {
		t.Fatalf("BuildGraph: %v", err)
	}
	if graph.Repository != "sureva-cli" {
		t.Fatalf("repository = %q, want sureva-cli", graph.Repository)
	}
}

func TestParseNumstat(t *testing.T) {
	out := []byte(strings.Join([]string{
		"5\t2\tinternal/a.go",
		"-\t-\tassets/logo.png",
		"3\t0\tinternal/old.go => internal/new.go",
	}, "\n"))

	churn := parseNumstat(out)
	if got := churn["internal/a.go"]; got != (Churn{Added: 5, Deleted: 2}) {
		t.Fatalf("a.go churn = %#v, want {5 2}", got)
	}
	if got := churn["assets/logo.png"]; got != (Churn{}) {
		t.Fatalf("binary churn = %#v, want zero", got)
	}
	if got := churn["internal/new.go"]; got != (Churn{Added: 3, Deleted: 0}) {
		t.Fatalf("rename churn keyed by new path = %#v, want {3 0}", got)
	}
}

func TestParseUnifiedDiff(t *testing.T) {
	out := []byte(strings.Join([]string{
		"diff --git a/internal/a.go b/internal/a.go",
		"index 111..222 100644",
		"--- a/internal/a.go",
		"+++ b/internal/a.go",
		"@@ -1 +1 @@",
		"-old",
		"+new",
		"diff --git a/README.md b/README.md",
		"index 333..444 100644",
		"--- a/README.md",
		"+++ b/README.md",
		"@@ -0,0 +1 @@",
		"+docs",
	}, "\n"))

	diffs := parseUnifiedDiff(out)
	if len(diffs) != 2 {
		t.Fatalf("parsed %d files, want 2: %#v", len(diffs), diffs)
	}
	if !strings.Contains(diffs["internal/a.go"], "+new") || !strings.Contains(diffs["internal/a.go"], "-old") {
		t.Fatalf("internal/a.go diff missing hunk lines: %q", diffs["internal/a.go"])
	}
	if !strings.HasPrefix(diffs["README.md"], "diff --git a/README.md") {
		t.Fatalf("README.md diff should start at its own header: %q", diffs["README.md"])
	}
	if strings.Contains(diffs["README.md"], "internal/a.go") {
		t.Fatalf("README.md diff leaked another file's section: %q", diffs["README.md"])
	}
}

func TestGitChangedFiles(t *testing.T) {
	tests := []struct {
		name    string
		runner  *fakeRunner
		want    []Change
		wantErr bool
	}{
		{
			name: "unions committed, working tree and untracked with status precedence",
			runner: &fakeRunner{outputs: [][]byte{
				[]byte("abc123\n"),
				[]byte("M\tinternal/b.go\nA\tinternal/a.go\nD\tinternal/e.go\n"),
				[]byte("M\tinternal/c.go\nM\tinternal/a.go\nR100\tinternal/old.go\tinternal/f.go\n"),
				[]byte("internal/d.go\n"),
			}},
			want: []Change{
				{Path: "internal/a.go", Status: StatusNew},
				{Path: "internal/b.go", Status: StatusModified},
				{Path: "internal/c.go", Status: StatusModified},
				{Path: "internal/d.go", Status: StatusNew},
				{Path: "internal/e.go", Status: StatusDeleted},
				{Path: "internal/f.go", Status: StatusRenamed},
			},
		},
		{
			name:    "merge base failure returns error",
			runner:  &fakeRunner{errAt: 1, outputs: [][]byte{[]byte("fatal: no merge base")}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := (Git{RepoRoot: "/repo", Runner: tt.runner}).ChangedFiles(context.Background(), "main")
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("ChangedFiles: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ChangedFiles() = %#v, want %#v", got, tt.want)
			}
			wantCommands := [][]string{
				{"git", "merge-base", "main", "HEAD"},
				{"git", "diff", "--name-status", "--diff-filter=ACMRD", "abc123", "HEAD"},
				{"git", "diff", "--name-status", "--diff-filter=ACMRD", "HEAD"},
				{"git", "ls-files", "--others", "--exclude-standard"},
			}
			if !reflect.DeepEqual(tt.runner.commands, wantCommands) {
				t.Fatalf("commands = %#v, want %#v", tt.runner.commands, wantCommands)
			}
		})
	}
}

func TestDiscoverRepoRoot(t *testing.T) {
	runner := &fakeRunner{outputs: [][]byte{[]byte("/repo\n")}}

	got, err := DiscoverRepoRoot(context.Background(), "/repo/internal/cli", runner)
	if err != nil {
		t.Fatalf("DiscoverRepoRoot: %v", err)
	}
	if got != "/repo" {
		t.Fatalf("root = %q, want /repo", got)
	}
	wantCommands := [][]string{{"git", "rev-parse", "--show-toplevel"}}
	if !reflect.DeepEqual(runner.commands, wantCommands) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, wantCommands)
	}
	if !reflect.DeepEqual(runner.dirs, []string{"/repo/internal/cli"}) {
		t.Fatalf("dirs = %#v, want start dir", runner.dirs)
	}
}

func TestDiscoverRepoRootFailure(t *testing.T) {
	runner := &fakeRunner{errAt: 1, outputs: [][]byte{[]byte("fatal: not a git repository")}}

	_, err := DiscoverRepoRoot(context.Background(), "/tmp", runner)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestWriteHTMLGraphIncludesSafeDeterministicNodesAndEdges(t *testing.T) {
	graph := Graph{
		Repository: "sureva-cli",
		Base:       "main",
		Nodes:      []string{"internal/cli/<changes>.go", "README.md"},
		Edges:      []Edge{{From: "internal/cli/<changes>.go", To: "internal/changes/graph.go"}},
	}
	var first bytes.Buffer
	if err := WriteHTMLGraph(&first, graph); err != nil {
		t.Fatalf("WriteHTMLGraph: %v", err)
	}
	var second bytes.Buffer
	if err := WriteHTMLGraph(&second, graph); err != nil {
		t.Fatalf("WriteHTMLGraph second: %v", err)
	}
	if first.String() != second.String() {
		t.Fatal("WriteHTMLGraph output is not deterministic")
	}
	html := first.String()
	if strings.Contains(html, "internal/cli/<changes>.go") {
		t.Fatalf("HTML contains unescaped raw node label: %s", html)
	}
	if !strings.Contains(html, "application/octet-stream") {
		t.Fatalf("HTML missing embedded graph data script: %s", html)
	}
	decoded := decodeEmbeddedGraphData(t, html)
	for _, want := range []string{
		`"base":"main"`,
		`"repository":"sureva-cli"`,
		`"id":"README.md"`,
		`"type":"docs"`,
		`"id":"internal/changes/graph.go"`,
		`"status":"context"`,
		`"from":"internal/cli/\u003cchanges\u003e.go","to":"internal/changes/graph.go"`,
		`activeTypes`,
		`applyFilters`,
		`target.status === 'context'`,
		`source.status === 'context'`,
		`if (graph.repository) { titleEl.textContent = graph.repository; document.title = graph.repository + ' changes'; }`,
		`path.style.display = (visibleIds.has(source.id) && visibleIds.has(target.id)) ? '' : 'none'`,
		`group.style.display = visibleIds.has(nodes[i].id) ? '' : 'none'`,
		`const content = (kind === 'add' || kind === 'del') ? line.slice(1) : line`,
	} {
		if !strings.Contains(decoded+html, want) {
			t.Fatalf("HTML/decoded graph data missing %q in decoded=%s", want, decoded)
		}
	}
}

func TestBuildGraphWithoutGoModSkipsEdges(t *testing.T) {
	repo := t.TempDir() // non-Go repo: no go.mod
	writeFile(t, repo, "src/index.js", "console.log('hi')\n")

	graph, err := BuildGraph(repo, "main", []Change{
		{Path: "src/index.js", Status: StatusModified},
		{Path: "internal/app.go", Status: StatusNew},
	})
	if err != nil {
		t.Fatalf("BuildGraph without go.mod should not error: %v", err)
	}
	if len(graph.Nodes) != 2 {
		t.Fatalf("nodes = %#v, want both files", graph.Nodes)
	}
	if len(graph.Edges) != 0 {
		t.Fatalf("edges = %#v, want none without go.mod", graph.Edges)
	}
	if graph.Statuses["src/index.js"] != "modified" {
		t.Fatalf("statuses not populated: %#v", graph.Statuses)
	}
}

func TestBuildGraphResolvesJSImports(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "src/app.ts", "import { x } from './util';\nimport React from 'react';\nrequire('../lib/helper');\n")
	writeFile(t, repo, "src/util.ts", "export const x = 1\n")
	writeFile(t, repo, "lib/helper.js", "module.exports = {}\n")

	graph, err := BuildGraph(repo, "main", []Change{{Path: "src/app.ts", Status: StatusNew}})
	if err != nil {
		t.Fatalf("BuildGraph: %v", err)
	}
	want := []Edge{
		{From: "src/app.ts", To: "lib/helper.js"},
		{From: "src/app.ts", To: "src/util.ts"},
	}
	if !reflect.DeepEqual(graph.Edges, want) {
		t.Fatalf("edges = %#v, want %#v (bare 'react' import must be excluded)", graph.Edges, want)
	}
}

func TestBuildGraphResolvesAstroImports(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "src/pages/index.astro", "---\nimport Layout from '../layouts/Layout.astro'\nimport { t } from '../lib/i18n'\n---\n<Layout />\n")
	writeFile(t, repo, "src/layouts/Layout.astro", "<slot />\n")
	writeFile(t, repo, "src/lib/i18n.ts", "export const t = 1\n")

	graph, err := BuildGraph(repo, "main", []Change{{Path: "src/pages/index.astro", Status: StatusNew}})
	if err != nil {
		t.Fatalf("BuildGraph: %v", err)
	}
	want := []Edge{
		{From: "src/pages/index.astro", To: "src/layouts/Layout.astro"},
		{From: "src/pages/index.astro", To: "src/lib/i18n.ts"},
	}
	if !reflect.DeepEqual(graph.Edges, want) {
		t.Fatalf("edges = %#v, want %#v", graph.Edges, want)
	}
}

func TestBuildGraphResolvesPythonImports(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, "pkg/main.py", "from . import util\nfrom pkg.sub import thing\nimport os\n")
	writeFile(t, repo, "pkg/util.py", "")
	writeFile(t, repo, "pkg/sub.py", "")

	graph, err := BuildGraph(repo, "main", []Change{{Path: "pkg/main.py", Status: StatusModified}})
	if err != nil {
		t.Fatalf("BuildGraph: %v", err)
	}
	want := []Edge{
		{From: "pkg/main.py", To: "pkg/sub.py"},
		{From: "pkg/main.py", To: "pkg/util.py"},
	}
	if !reflect.DeepEqual(graph.Edges, want) {
		t.Fatalf("edges = %#v, want %#v (stdlib 'os' import must be excluded)", graph.Edges, want)
	}
}

func TestBuildChecklist(t *testing.T) {
	flags := func(nodes []string) map[string]bool {
		out := map[string]bool{}
		for _, item := range buildChecklist(nodes) {
			out[item.Key] = item.OK
		}
		return out
	}

	all := flags([]string{
		"internal/cli/changes.go",
		"internal/cli/changes_test.go",
		"docs/guide.md",
		"CHANGELOG.md",
		"database/migrations/20260701123000_create_users.sql",
	})
	for _, key := range []string{"code", "tests", "docs", "changelog", "migrations"} {
		if !all[key] {
			t.Fatalf("%s should be satisfied", key)
		}
	}

	// A lone changelog must not count as docs or code; a test file is not code.
	partial := flags([]string{"CHANGELOG.md", "pkg/foo_test.go", "db/migrate/20260701123000_create_users.rb", "V2__add_index.sql", "001_create_users.sql"})
	if partial["docs"] {
		t.Fatal("changelog alone must not satisfy docs")
	}
	if partial["code"] {
		t.Fatal("test and migration files alone must not satisfy source code")
	}
	if !partial["tests"] || !partial["changelog"] || !partial["migrations"] {
		t.Fatalf("tests/changelog/migrations should be satisfied: %#v", partial)
	}

	liquibase := flags([]string{"db/changelog.xml", "database/changelog.yaml", "database/changelog.json"})
	if !liquibase["migrations"] {
		t.Fatalf("liquibase changelog files should satisfy migrations: %#v", liquibase)
	}
	if liquibase["changelog"] {
		t.Fatalf("liquibase changelog files must not satisfy release changelog: %#v", liquibase)
	}

	rootJSONChangelog := flags([]string{"CHANGELOG.json"})
	if !rootJSONChangelog["changelog"] || rootJSONChangelog["migrations"] {
		t.Fatalf("root changelog should be release changelog only: %#v", rootJSONChangelog)
	}
}

func TestNodeChecklistType(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		status string
		want   string
	}{
		{name: "context", path: "internal/changes/graph.go", status: "context", want: "context"},
		{name: "source code", path: "internal/changes/graph.go", status: "modified", want: "code"},
		{name: "test", path: "internal/changes/graph_test.go", status: "modified", want: "tests"},
		{name: "docs", path: "docs/guide.md", status: "modified", want: "docs"},
		{name: "migration", path: "db/migrate/20260701123000_create_users.rb", status: "modified", want: "migrations"},
		{name: "liquibase changelog migration", path: "db/changelog.xml", status: "modified", want: "migrations"},
		{name: "database changelog migration", path: "database/changelog.yaml", status: "modified", want: "migrations"},
		{name: "changelog", path: "CHANGELOG.md", status: "modified", want: "changelog"},
		{name: "nested changelog is not release changelog", path: "docs/CHANGELOG.md", status: "modified", want: "docs"},
		{name: "uncategorized", path: "package.json", status: "modified", want: "other"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nodeChecklistType(tt.path, tt.status); got != tt.want {
				t.Fatalf("nodeChecklistType(%q, %q) = %q, want %q", tt.path, tt.status, got, tt.want)
			}
		})
	}
}

func TestParseCommits(t *testing.T) {
	out := []byte(strings.Join([]string{
		"abc123\tfeat(cli): add thing",
		"def456\tfix: correct bug",
		"aaa111\tfeat!: drop old flag",
		"bbb222\tmerge branch noise without colon",
	}, "\n"))

	commits := parseCommits(out)
	if len(commits) != 4 {
		t.Fatalf("parsed %d commits, want 4", len(commits))
	}
	if commits[0] != (Commit{Hash: "abc123", Type: "feat", Scope: "cli", Subject: "add thing"}) {
		t.Fatalf("commit[0] = %#v", commits[0])
	}
	if commits[1] != (Commit{Hash: "def456", Type: "fix", Subject: "correct bug"}) {
		t.Fatalf("commit[1] = %#v", commits[1])
	}
	if !commits[2].Breaking || commits[2].Type != "feat" {
		t.Fatalf("commit[2] should be breaking feat: %#v", commits[2])
	}
	if commits[3].Type != "" || commits[3].Subject != "merge branch noise without colon" {
		t.Fatalf("non-conventional commit[3] = %#v", commits[3])
	}
}

func TestGroupCommits(t *testing.T) {
	groups := groupCommits([]Commit{
		{Type: "feat", Subject: "a"},
		{Type: "fix", Subject: "b"},
		{Type: "feat", Breaking: true, Subject: "c"},
		{Type: "chore", Subject: "d"},
	})

	if len(groups) != 4 {
		t.Fatalf("got %d groups, want 4: %#v", len(groups), groups)
	}
	if groups[0].Title != "💥 Breaking Changes" || len(groups[0].Commits) != 1 {
		t.Fatalf("group[0] = %#v", groups[0])
	}
	if groups[1].Title != "✨ Features" || len(groups[1].Commits) != 1 {
		t.Fatalf("breaking feat must not double-count in Features: %#v", groups[1])
	}
	if !strings.Contains(groups[3].Title, "Other") {
		t.Fatalf("chore should fall into Other: %#v", groups[3])
	}
}

func TestBuildRelease(t *testing.T) {
	repo := newGraphFixture(t)
	git := &fakeGit{
		prevTag: "v0.4.0",
		files:   []Change{{Path: "internal/cli/changes.go", Status: StatusNew}},
		diffs:   map[string]string{"internal/cli/changes.go": "diff --git a/x b/x\n+x"},
		churn:   map[string]Churn{"internal/cli/changes.go": {Added: 10}},
		commits: []Commit{{Hash: "a", Type: "feat", Subject: "add"}},
	}

	graph, err := (Builder{RepoRoot: repo, Git: git}).BuildRelease(context.Background(), "v0.5.0")
	if err != nil {
		t.Fatalf("BuildRelease: %v", err)
	}
	if git.releaseTag != "v0.5.0" {
		t.Fatalf("PreviousTag called with %q, want v0.5.0", git.releaseTag)
	}
	if git.rangeFrom != "v0.4.0" || git.rangeTo != "v0.5.0" {
		t.Fatalf("range = %q..%q, want v0.4.0..v0.5.0", git.rangeFrom, git.rangeTo)
	}
	if graph.Release != "v0.5.0" || graph.Base != "v0.4.0" {
		t.Fatalf("release=%q base=%q", graph.Release, graph.Base)
	}
	if len(graph.Changelog) != 1 || graph.Changelog[0].Title != "✨ Features" {
		t.Fatalf("changelog = %#v", graph.Changelog)
	}
	if graph.Churn["internal/cli/changes.go"].Added != 10 {
		t.Fatalf("churn not attached: %#v", graph.Churn)
	}
}

func TestBuildReleaseFirstReleaseUsesEmptyTree(t *testing.T) {
	repo := newGraphFixture(t)
	git := &fakeGit{prevTag: ""} // no previous tag

	if _, err := (Builder{RepoRoot: repo, Git: git}).BuildRelease(context.Background(), "v0.1.0"); err != nil {
		t.Fatalf("BuildRelease: %v", err)
	}
	if git.rangeFrom != emptyTree {
		t.Fatalf("range from = %q, want empty tree %q", git.rangeFrom, emptyTree)
	}
}

func newGraphFixture(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	writeFile(t, repo, "go.mod", "module example.com/project\n\ngo 1.25\n")
	writeFile(t, repo, "README.md", "docs\n")
	writeFile(t, repo, "internal/cli/changes.go", `package cli

import (
	"fmt"
	"example.com/project/internal/changes"
	"github.com/spf13/cobra"
)

var _, _ = fmt.Println, changes.Graph{}
var _ = cobra.Command{}
`)
	writeFile(t, repo, "internal/cli/external.go", `package cli

import "github.com/spf13/cobra"

var _ = cobra.Command{}
`)
	writeFile(t, repo, "internal/changes/git.go", "package changes\n")
	writeFile(t, repo, "internal/changes/graph.go", "package changes\n")
	writeFile(t, repo, "internal/changes/graph_test.go", "package changes\n")
	return repo
}

func decodeEmbeddedGraphData(t *testing.T, html string) string {
	t.Helper()
	match := regexp.MustCompile(`<script id="graph-data" type="application/octet-stream">([^<]+)</script>`).FindStringSubmatch(html)
	if len(match) != 2 {
		t.Fatalf("graph data script not found in %s", html)
	}
	data, err := base64.URLEncoding.DecodeString(match[1])
	if err != nil {
		t.Fatalf("decode graph data: %v", err)
	}
	return string(data)
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

type fakeGit struct {
	base       string
	files      []Change
	diffs      map[string]string
	churn      map[string]Churn
	commits    []Commit
	prevTag    string
	releaseTag string
	rangeFrom  string
	rangeTo    string
	err        error
}

func (f *fakeGit) ChangedFiles(_ context.Context, base string) ([]Change, error) {
	f.base = base
	return f.files, f.err
}

func (f *fakeGit) Diffs(_ context.Context, _ string) (map[string]string, error) {
	return f.diffs, f.err
}

func (f *fakeGit) Churn(_ context.Context, _ string) (map[string]Churn, error) {
	return f.churn, f.err
}

func (f *fakeGit) PreviousTag(_ context.Context, tag string) (string, error) {
	f.releaseTag = tag
	return f.prevTag, f.err
}

func (f *fakeGit) ChangedFilesRange(_ context.Context, from, to string) ([]Change, error) {
	f.rangeFrom, f.rangeTo = from, to
	return f.files, f.err
}

func (f *fakeGit) DiffsRange(_ context.Context, _, _ string) (map[string]string, error) {
	return f.diffs, f.err
}

func (f *fakeGit) ChurnRange(_ context.Context, _, _ string) (map[string]Churn, error) {
	return f.churn, f.err
}

func (f *fakeGit) Log(_ context.Context, _, _ string) ([]Commit, error) {
	return f.commits, f.err
}

type fakeRunner struct {
	outputs  [][]byte
	errAt    int
	commands [][]string
	dirs     []string
}

func (f *fakeRunner) Run(_ context.Context, dir string, name string, args ...string) ([]byte, error) {
	f.dirs = append(f.dirs, dir)
	command := append([]string{name}, args...)
	f.commands = append(f.commands, command)
	call := len(f.commands)
	if f.errAt == call {
		out := []byte(nil)
		if call <= len(f.outputs) {
			out = f.outputs[call-1]
		}
		return out, errors.New("command failed")
	}
	if call <= len(f.outputs) {
		return f.outputs[call-1], nil
	}
	return nil, nil
}
