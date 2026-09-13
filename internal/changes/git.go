package changes

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// GitClient collects branch changes relative to a base branch.
type GitClient interface {
	ChangedFiles(ctx context.Context, base string) ([]Change, error)
	Diffs(ctx context.Context, base string) (map[string]string, error)
	Churn(ctx context.Context, base string) (map[string]Churn, error)
	PreviousTag(ctx context.Context, tag string) (string, error)
	ChangedFilesRange(ctx context.Context, from, to string) ([]Change, error)
	DiffsRange(ctx context.Context, from, to string) (map[string]string, error)
	ChurnRange(ctx context.Context, from, to string) (map[string]Churn, error)
	Log(ctx context.Context, from, to string) ([]Commit, error)
}

// CommandRunner abstracts command execution so tests do not need a real Git repo.
type CommandRunner interface {
	Run(ctx context.Context, dir, name string, args ...string) ([]byte, error)
}

// ExecRunner runs commands on the local machine.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

// Git uses the git CLI to discover changed files.
type Git struct {
	RepoRoot string
	Runner   CommandRunner
}

func (g Git) runner() CommandRunner {
	if g.Runner != nil {
		return g.Runner
	}
	return ExecRunner{}
}

// DiscoverRepoRoot returns the absolute Git repository root for startDir.
func DiscoverRepoRoot(ctx context.Context, startDir string, runner CommandRunner) (string, error) {
	if strings.TrimSpace(startDir) == "" {
		startDir = "."
	}
	if runner == nil {
		runner = ExecRunner{}
	}
	out, err := runner.Run(ctx, startDir, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("discover git root: %w: %s", err, strings.TrimSpace(string(out)))
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return "", fmt.Errorf("discover git root: empty result")
	}
	return root, nil
}

// ChangedFiles returns repo-relative paths that differ from base: committed
// changes since merge-base(base, HEAD), plus the current working tree
// (staged, unstaged, and untracked). The union ensures the graph reflects the
// real delta whether or not those changes have been committed yet — which is
// what makes it useful for inspecting edits left behind by an AI agent.
func (g Git) ChangedFiles(ctx context.Context, base string) ([]Change, error) {
	if strings.TrimSpace(base) == "" {
		base = "main"
	}
	runner := g.runner()
	mergeBaseOut, err := runner.Run(ctx, g.RepoRoot, "git", "merge-base", base, "HEAD")
	if err != nil {
		return nil, fmt.Errorf("find merge-base with %q: %w: %s", base, err, strings.TrimSpace(string(mergeBaseOut)))
	}
	mergeBase := strings.TrimSpace(string(mergeBaseOut))
	if mergeBase == "" {
		return nil, fmt.Errorf("find merge-base with %q: empty result", base)
	}

	committed, err := runner.Run(ctx, g.RepoRoot, "git", "diff", "--name-status", "--diff-filter=ACMRD", mergeBase, "HEAD")
	if err != nil {
		return nil, fmt.Errorf("list changed files from %s to HEAD: %w: %s", mergeBase, err, strings.TrimSpace(string(committed)))
	}

	// Tracked working tree changes relative to HEAD (staged and unstaged).
	worktree, err := runner.Run(ctx, g.RepoRoot, "git", "diff", "--name-status", "--diff-filter=ACMRD", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("list working tree changes: %w: %s", err, strings.TrimSpace(string(worktree)))
	}

	// Untracked files not yet added, respecting .gitignore.
	untracked, err := runner.Run(ctx, g.RepoRoot, "git", "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, fmt.Errorf("list untracked files: %w: %s", err, strings.TrimSpace(string(untracked)))
	}

	changes := parseNameStatus(committed)
	changes = append(changes, parseNameStatus(worktree)...)
	changes = append(changes, parseUntracked(untracked)...)
	return uniqueSortedChanges(changes), nil
}

// parseNameStatus parses `git diff --name-status` output. Each line is a status
// code followed by tab-separated paths; renames carry both old and new paths,
// and the new path is the one that exists on disk.
func parseNameStatus(out []byte) []Change {
	var changes []Change
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			continue
		}
		path := strings.TrimSpace(fields[len(fields)-1])
		if path == "" {
			continue
		}
		changes = append(changes, Change{Path: path, Status: statusFromCode(fields[0])})
	}
	return changes
}

func parseUntracked(out []byte) []Change {
	var changes []Change
	for _, line := range strings.Split(string(out), "\n") {
		path := strings.TrimSpace(line)
		if path == "" {
			continue
		}
		changes = append(changes, Change{Path: path, Status: StatusNew})
	}
	return changes
}

func statusFromCode(code string) Status {
	if code == "" {
		return StatusModified
	}
	switch code[0] {
	case 'A', 'C':
		return StatusNew
	case 'D':
		return StatusDeleted
	case 'R':
		return StatusRenamed
	default:
		return StatusModified
	}
}

const maxDiffBytes = 20000

// Diffs returns a per-file unified diff of the full delta from base to the
// current working tree, keyed by repo-relative path. Tracked changes (committed
// and uncommitted) come from a single `git diff <mergeBase>`; untracked files
// are synthesized as all-added content since git diff does not report them.
func (g Git) Diffs(ctx context.Context, base string) (map[string]string, error) {
	if strings.TrimSpace(base) == "" {
		base = "main"
	}
	runner := g.runner()
	mergeBaseOut, err := runner.Run(ctx, g.RepoRoot, "git", "merge-base", base, "HEAD")
	if err != nil {
		return nil, fmt.Errorf("find merge-base with %q: %w: %s", base, err, strings.TrimSpace(string(mergeBaseOut)))
	}
	mergeBase := strings.TrimSpace(string(mergeBaseOut))
	if mergeBase == "" {
		return nil, fmt.Errorf("find merge-base with %q: empty result", base)
	}

	out, err := runner.Run(ctx, g.RepoRoot, "git", "diff", mergeBase, "--")
	if err != nil {
		return nil, fmt.Errorf("diff working tree against %s: %w: %s", mergeBase, err, strings.TrimSpace(string(out)))
	}
	diffs := parseUnifiedDiff(out)

	untracked, err := runner.Run(ctx, g.RepoRoot, "git", "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, fmt.Errorf("list untracked files: %w: %s", err, strings.TrimSpace(string(untracked)))
	}
	for _, line := range strings.Split(string(untracked), "\n") {
		path := strings.TrimSpace(line)
		if path == "" {
			continue
		}
		if body := readAddedFile(g.RepoRoot, path); body != "" {
			diffs[filepath.ToSlash(path)] = body
		}
	}
	return diffs, nil
}

var diffHeaderRe = regexp.MustCompile(`^diff --git a/.* b/(.*)$`)

// parseUnifiedDiff splits a combined `git diff` into per-file diffs keyed by the
// new (b/) path of each section.
func parseUnifiedDiff(out []byte) map[string]string {
	diffs := make(map[string]string)
	var path string
	var buf []string
	flush := func() {
		if path != "" && len(buf) > 0 {
			diffs[path] = capText(strings.Join(buf, "\n"))
		}
		buf = nil
	}
	for _, line := range strings.Split(string(out), "\n") {
		if m := diffHeaderRe.FindStringSubmatch(line); m != nil {
			flush()
			path = filepath.ToSlash(strings.TrimSpace(m[1]))
		}
		if path != "" {
			buf = append(buf, line)
		}
	}
	flush()
	return diffs
}

// readAddedFile renders an untracked file as an all-added diff body.
func readAddedFile(repoRoot, path string) string {
	data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(path)))
	if err != nil {
		return ""
	}
	if !utf8.Valid(data) {
		return "new file: " + path + "\n(binary file)"
	}
	truncated := false
	if len(data) > maxDiffBytes {
		data = data[:maxDiffBytes]
		truncated = true
	}
	var b strings.Builder
	b.WriteString("new file: " + path + "\n@@ new file @@\n")
	for _, line := range strings.Split(string(data), "\n") {
		b.WriteString("+")
		b.WriteString(line)
		b.WriteString("\n")
	}
	if truncated {
		b.WriteString("… (truncated)\n")
	}
	return b.String()
}

func capText(s string) string {
	if len(s) <= maxDiffBytes {
		return s
	}
	return s[:maxDiffBytes] + "\n… (truncated)"
}

// Churn returns per-file added/deleted line counts for the full delta from base
// to the working tree. Tracked files come from `git diff --numstat`; untracked
// files are counted as all-added from their contents.
func (g Git) Churn(ctx context.Context, base string) (map[string]Churn, error) {
	if strings.TrimSpace(base) == "" {
		base = "main"
	}
	runner := g.runner()
	mergeBaseOut, err := runner.Run(ctx, g.RepoRoot, "git", "merge-base", base, "HEAD")
	if err != nil {
		return nil, fmt.Errorf("find merge-base with %q: %w: %s", base, err, strings.TrimSpace(string(mergeBaseOut)))
	}
	mergeBase := strings.TrimSpace(string(mergeBaseOut))
	if mergeBase == "" {
		return nil, fmt.Errorf("find merge-base with %q: empty result", base)
	}

	out, err := runner.Run(ctx, g.RepoRoot, "git", "diff", "--numstat", mergeBase, "--")
	if err != nil {
		return nil, fmt.Errorf("numstat against %s: %w: %s", mergeBase, err, strings.TrimSpace(string(out)))
	}
	churn := parseNumstat(out)

	untracked, err := runner.Run(ctx, g.RepoRoot, "git", "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, fmt.Errorf("list untracked files: %w: %s", err, strings.TrimSpace(string(untracked)))
	}
	for _, line := range strings.Split(string(untracked), "\n") {
		path := strings.TrimSpace(line)
		if path == "" {
			continue
		}
		churn[filepath.ToSlash(path)] = Churn{Added: countAddedLines(g.RepoRoot, path)}
	}
	return churn, nil
}

// parseNumstat parses `git diff --numstat` lines: "<added>\t<deleted>\t<path>".
// Binary files report "-" counts (treated as 0); rename paths keep the new path.
func parseNumstat(out []byte) map[string]Churn {
	churn := make(map[string]Churn)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.SplitN(strings.TrimRight(line, "\r"), "\t", 3)
		if len(fields) < 3 {
			continue
		}
		path := fields[2]
		if idx := strings.Index(path, " => "); idx >= 0 {
			path = path[idx+len(" => "):]
			path = strings.TrimSuffix(path, "}")
		}
		path = filepath.ToSlash(strings.TrimSpace(path))
		if path == "" {
			continue
		}
		churn[path] = Churn{Added: atoiOrZero(fields[0]), Deleted: atoiOrZero(fields[1])}
	}
	return churn
}

func atoiOrZero(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

// PreviousTag returns the most recent tag reachable from tag's first parent, or
// an empty string (not an error) when tag has no predecessor (first release).
func (g Git) PreviousTag(ctx context.Context, tag string) (string, error) {
	out, err := g.runner().Run(ctx, g.RepoRoot, "git", "describe", "--tags", "--abbrev=0", tag+"^")
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(string(out)), nil
}

// ChangedFilesRange returns the files changed between two refs, with status.
func (g Git) ChangedFilesRange(ctx context.Context, from, to string) ([]Change, error) {
	out, err := g.runner().Run(ctx, g.RepoRoot, "git", "diff", "--name-status", "--diff-filter=ACMRD", from, to)
	if err != nil {
		return nil, fmt.Errorf("diff %s..%s: %w: %s", from, to, err, strings.TrimSpace(string(out)))
	}
	return uniqueSortedChanges(parseNameStatus(out)), nil
}

// DiffsRange returns the per-file unified diff between two refs.
func (g Git) DiffsRange(ctx context.Context, from, to string) (map[string]string, error) {
	out, err := g.runner().Run(ctx, g.RepoRoot, "git", "diff", from, to)
	if err != nil {
		return nil, fmt.Errorf("diff %s..%s: %w: %s", from, to, err, strings.TrimSpace(string(out)))
	}
	return parseUnifiedDiff(out), nil
}

// ChurnRange returns per-file added/deleted counts between two refs.
func (g Git) ChurnRange(ctx context.Context, from, to string) (map[string]Churn, error) {
	out, err := g.runner().Run(ctx, g.RepoRoot, "git", "diff", "--numstat", from, to)
	if err != nil {
		return nil, fmt.Errorf("numstat %s..%s: %w: %s", from, to, err, strings.TrimSpace(string(out)))
	}
	return parseNumstat(out), nil
}

// Log returns the non-merge commits in from..to (or all commits up to to when
// from is empty), parsed as conventional-commit changelog entries.
func (g Git) Log(ctx context.Context, from, to string) ([]Commit, error) {
	rangeArg := to
	if strings.TrimSpace(from) != "" {
		rangeArg = from + ".." + to
	}
	out, err := g.runner().Run(ctx, g.RepoRoot, "git", "log", rangeArg, "--no-merges", "--pretty=format:%H%x09%s")
	if err != nil {
		return nil, fmt.Errorf("log %s: %w: %s", rangeArg, err, strings.TrimSpace(string(out)))
	}
	return parseCommits(out), nil
}

var conventionalRe = regexp.MustCompile(`^([a-z]+)(?:\(([^)]+)\))?(!)?:\s*(.+)$`)

func parseCommits(out []byte) []Commit {
	var commits []Commit
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) < 2 {
			continue
		}
		hash, subject := parts[0], parts[1]
		commit := Commit{Hash: hash, Subject: strings.TrimSpace(subject)}
		if m := conventionalRe.FindStringSubmatch(subject); m != nil {
			commit.Type = m[1]
			commit.Scope = m[2]
			commit.Breaking = m[3] == "!"
			commit.Subject = strings.TrimSpace(m[4])
		}
		commits = append(commits, commit)
	}
	return commits
}

func countAddedLines(repoRoot, path string) int {
	data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(path)))
	if err != nil || !utf8.Valid(data) || len(data) == 0 {
		return 0
	}
	n := bytes.Count(data, []byte("\n"))
	if data[len(data)-1] != '\n' {
		n++
	}
	return n
}
