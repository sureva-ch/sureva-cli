package changes

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Edge is a source-file to imported-file relationship.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Status describes how a file changed relative to the base branch.
type Status string

const (
	StatusNew      Status = "new"      // added or untracked
	StatusModified Status = "modified" // edited in place
	StatusDeleted  Status = "deleted"  // removed
	StatusRenamed  Status = "renamed"  // moved to a new path
)

// Change is a file that differs from the base branch, tagged with its status.
type Change struct {
	Path   string
	Status Status
}

// Churn counts the lines added and deleted for a file relative to the base.
type Churn struct {
	Added   int `json:"added"`
	Deleted int `json:"deleted"`
}

// Commit is a single changelog entry parsed from a conventional-commit subject.
type Commit struct {
	Hash     string `json:"hash"`
	Type     string `json:"type"`
	Scope    string `json:"scope"`
	Subject  string `json:"subject"`
	Breaking bool   `json:"breaking"`
}

// ChangelogGroup is a titled bucket of commits (e.g. Features, Fixes).
type ChangelogGroup struct {
	Title   string   `json:"title"`
	Commits []Commit `json:"commits"`
}

// emptyTree is git's canonical empty tree object, used as the "from" side when a
// release has no previous tag (first release).
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// Graph contains changed files, internal Go import edges, and a per-file status
// map keyed by node path (values are Status strings). Import targets that were
// not themselves changed do not appear in Statuses; consumers render them as
// context nodes.
type Graph struct {
	Repository string            `json:"repository,omitempty"`
	Base       string            `json:"base"`
	Nodes      []string          `json:"nodes"`
	Edges      []Edge            `json:"edges"`
	Statuses   map[string]string `json:"statuses,omitempty"`
	Diffs      map[string]string `json:"diffs,omitempty"`
	Churn      map[string]Churn  `json:"churn,omitempty"`
	Release    string            `json:"release,omitempty"`
	Changelog  []ChangelogGroup  `json:"changelog,omitempty"`
	Checklist  []ChecklistItem   `json:"checklist,omitempty"`
}

// ChecklistItem is a yes/no readiness signal computed from the changed files.
type ChecklistItem struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	OK    bool   `json:"ok"`
}

// Builder builds a changes graph for a repository.
type Builder struct {
	RepoRoot string
	Git      GitClient
}

// Build compares HEAD with base and builds a graph from changed files.
func (b Builder) Build(ctx context.Context, base string) (Graph, error) {
	if strings.TrimSpace(base) == "" {
		base = "main"
	}
	git := b.Git
	if git == nil {
		git = Git{RepoRoot: b.RepoRoot}
	}
	changed, err := git.ChangedFiles(ctx, base)
	if err != nil {
		return Graph{}, err
	}
	graph, err := BuildGraph(b.RepoRoot, base, changed)
	if err != nil {
		return Graph{}, err
	}
	diffs, err := git.Diffs(ctx, base)
	if err != nil {
		return Graph{}, err
	}
	graph.Repository = repositoryName(b.RepoRoot)
	graph.Diffs = diffs
	churn, err := git.Churn(ctx, base)
	if err != nil {
		return Graph{}, err
	}
	graph.Churn = churn
	return graph, nil
}

// BuildRelease builds a graph plus changelog for the range between the tag
// before release and release. Nodes, diffs, and churn come from git and are
// exact; import edges are resolved from the working tree, so they are accurate
// when release equals HEAD and approximate for older tags.
func (b Builder) BuildRelease(ctx context.Context, release string) (Graph, error) {
	release = strings.TrimSpace(release)
	if release == "" {
		return Graph{}, fmt.Errorf("release tag is required")
	}
	git := b.Git
	if git == nil {
		git = Git{RepoRoot: b.RepoRoot}
	}
	from, err := git.PreviousTag(ctx, release)
	if err != nil {
		return Graph{}, err
	}
	fromRef := from
	if fromRef == "" {
		fromRef = emptyTree
	}
	changed, err := git.ChangedFilesRange(ctx, fromRef, release)
	if err != nil {
		return Graph{}, err
	}
	graph, err := BuildGraph(b.RepoRoot, from, changed)
	if err != nil {
		return Graph{}, err
	}
	diffs, err := git.DiffsRange(ctx, fromRef, release)
	if err != nil {
		return Graph{}, err
	}
	graph.Diffs = diffs
	churn, err := git.ChurnRange(ctx, fromRef, release)
	if err != nil {
		return Graph{}, err
	}
	graph.Churn = churn
	commits, err := git.Log(ctx, from, release)
	if err != nil {
		return Graph{}, err
	}
	graph.Changelog = groupCommits(commits)
	graph.Release = release
	return graph, nil
}

// groupCommits buckets commits by conventional-commit type in a fixed display
// order; breaking changes are pulled into their own group regardless of type.
func groupCommits(commits []Commit) []ChangelogGroup {
	order := []struct{ key, title string }{
		{"breaking", "💥 Breaking Changes"},
		{"feat", "✨ Features"},
		{"fix", "🐛 Fixes"},
		{"perf", "⚡ Performance"},
		{"refactor", "♻️ Refactoring"},
		{"docs", "📝 Documentation"},
		{"other", "🔧 Other"},
	}
	buckets := make(map[string][]Commit)
	for _, c := range commits {
		key := "other"
		if c.Breaking {
			key = "breaking"
		} else {
			switch c.Type {
			case "feat", "fix", "perf", "refactor", "docs":
				key = c.Type
			}
		}
		buckets[key] = append(buckets[key], c)
	}
	var groups []ChangelogGroup
	for _, o := range order {
		if cs := buckets[o.key]; len(cs) > 0 {
			groups = append(groups, ChangelogGroup{Title: o.title, Commits: cs})
		}
	}
	return groups
}

// BuildGraph builds a deterministic import graph for the supplied changes.
// Import edges are resolved per language (Go, JS/TS, Python); files in other
// languages, or repos without the relevant manifest, simply produce no edges,
// so the command works in any repository.
func BuildGraph(repoRoot, base string, changes []Change) (Graph, error) {
	changes = uniqueSortedChanges(changes)
	nodes := make([]string, 0, len(changes))
	statuses := make(map[string]string, len(changes))
	for _, change := range changes {
		nodes = append(nodes, change.Path)
		statuses[change.Path] = string(change.Status)
	}
	return Graph{
		Repository: repositoryName(repoRoot),
		Base:       base,
		Nodes:      nodes,
		Edges:      buildImportEdges(repoRoot, changes),
		Statuses:   statuses,
		Checklist:  buildChecklist(nodes),
	}, nil
}

func repositoryName(repoRoot string) string {
	repoRoot = strings.TrimSpace(repoRoot)
	if repoRoot == "" {
		return ""
	}
	clean := filepath.Clean(repoRoot)
	base := filepath.Base(clean)
	if base == "." || base == string(filepath.Separator) {
		return ""
	}
	return base
}

// ReadModulePath returns the module directive from go.mod.
func ReadModulePath(repoRoot string) (string, error) {
	data, err := os.ReadFile(filepath.Join(repoRoot, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("read go.mod: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "module" {
			return fields[1], nil
		}
	}
	return "", fmt.Errorf("go.mod has no module directive")
}

func parseFileImports(path string) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	imports := make([]string, 0, len(file.Imports))
	for _, spec := range file.Imports {
		value, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, err
		}
		imports = append(imports, value)
	}
	sort.Strings(imports)
	return imports, nil
}

func isInternalImport(modulePath, importPath string) bool {
	prefix := modulePath + "/internal/"
	return strings.HasPrefix(importPath, prefix)
}

func resolveImportFiles(repoRoot, modulePath, importPath string) ([]string, error) {
	relDir := strings.TrimPrefix(importPath, modulePath+"/")
	absDir := filepath.Join(repoRoot, filepath.FromSlash(relDir))
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files = append(files, filepath.ToSlash(filepath.Join(relDir, name)))
	}
	sort.Strings(files)
	return files, nil
}

// statusRank orders statuses so that, when the same path shows up from several
// sources (committed diff, working tree, untracked), the most significant one
// wins: a deletion trumps an add, an add trumps a rename, a rename trumps a
// plain edit.
var statusRank = map[Status]int{
	StatusModified: 1,
	StatusRenamed:  2,
	StatusNew:      3,
	StatusDeleted:  4,
}

func uniqueSortedChanges(changes []Change) []Change {
	byPath := make(map[string]Status)
	for _, change := range changes {
		path := filepath.ToSlash(strings.TrimSpace(change.Path))
		if path == "" {
			continue
		}
		status := change.Status
		if status == "" {
			status = StatusModified
		}
		if current, ok := byPath[path]; !ok || statusRank[status] > statusRank[current] {
			byPath[path] = status
		}
	}
	out := make([]Change, 0, len(byPath))
	for path, status := range byPath {
		out = append(out, Change{Path: path, Status: status})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
