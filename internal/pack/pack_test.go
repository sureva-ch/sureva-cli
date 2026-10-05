package pack

import (
	"archive/zip"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func write(t *testing.T, root, rel, content string, mode os.FileMode) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func names(t *testing.T, a *Archive) []string {
	t.Helper()
	zr, err := zip.OpenReader(a.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zr.Close() }()
	var out []string
	for _, f := range zr.File {
		out = append(out, f.Name)
	}
	return out
}

func reasons(a *Archive) map[string]string {
	m := map[string]string{}
	for _, e := range a.Excluded.Entries {
		m[e.Path] = e.Reason
	}
	return m
}

// project lays out a tree that exercises every always-excluded rule, a nested
// node_modules, a symlink and the ignore files.
func project(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, root, "index.js", "console.log(1)", 0o644)
	write(t, root, "bin/run.sh", "#!/bin/sh\n", 0o755)
	write(t, root, "src/app.js", "x", 0o644)
	write(t, root, "node_modules/dep/index.js", "x", 0o644)
	write(t, root, "packages/web/node_modules/dep/index.js", "x", 0o644)
	write(t, root, ".env", "SECRET=1", 0o600)
	write(t, root, ".env.local", "SECRET=2", 0o600)
	write(t, root, "config/.env.production", "SECRET=3", 0o600)
	write(t, root, ".git/config", "x", 0o644)
	write(t, root, "debug.log", "x", 0o644)
	write(t, root, "secret/notes.txt", "x", 0o644)
	write(t, root, "docs/draft.md", "x", 0o644)
	write(t, root, ".gitignore", "*.log\n", 0o644)
	write(t, root, ".surevaignore", "secret/\ndocs/draft.md\n", 0o644)
	if err := os.Symlink("index.js", filepath.Join(root, "link.js")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	return root
}

func wantFiles() []string {
	return []string{".gitignore", ".surevaignore", "bin/run.sh", "index.js", "src/app.js"}
}

func checkPacked(t *testing.T, a *Archive, wantMode string) {
	t.Helper()
	if a.Mode != wantMode {
		t.Errorf("mode = %q, want %q", a.Mode, wantMode)
	}
	got := names(t, a)
	if !sort.StringsAreSorted(got) {
		t.Errorf("entries not sorted: %v", got)
	}
	want := wantFiles()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("entries = %v, want %v", got, want)
	}
	r := reasons(a)
	for path, reason := range map[string]string{
		"node_modules/":              ReasonAlways,
		"packages/web/node_modules/": ReasonAlways,
		".env":                       ReasonAlways,
		".env.local":                 ReasonAlways,
		"config/.env.production":     ReasonAlways,
		"link.js":                    ReasonSymlink,
		"secret/notes.txt":           ReasonSurevaIgnore,
		"docs/draft.md":              ReasonSurevaIgnore,
	} {
		if wantMode == "walk" && strings.HasSuffix(path, "notes.txt") {
			path, reason = "secret/", ReasonSurevaIgnore // a walk prunes the directory
		}
		if r[path] != reason {
			t.Errorf("exclusion %q = %q, want %q (all: %v)", path, r[path], reason, r)
		}
	}
}

func TestPack_Walk(t *testing.T) {
	root := project(t)
	a, err := Pack(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Remove()
	checkPacked(t, a, "walk")
	if r := reasons(a); r["debug.log"] != ReasonGitignore {
		t.Errorf("debug.log should be excluded by .gitignore in walk mode: %v", r)
	}
	if _, err := os.Stat(a.Path); err != nil {
		t.Fatal(err)
	}
}

func TestPack_Git(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := project(t)
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git init: %v %s", err, out)
	}
	a, err := Pack(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Remove()
	checkPacked(t, a, "git")
}

func TestPack_PreservesModeAndStripsWrapper(t *testing.T) {
	root := project(t)
	a, err := Pack(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Remove()
	zr, err := zip.OpenReader(a.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zr.Close() }()
	for _, f := range zr.File {
		if f.Name == "bin/run.sh" && f.Mode().Perm() != 0o755 {
			t.Errorf("bin/run.sh mode = %v, want 0755", f.Mode().Perm())
		}
		if f.Mode()&os.ModeSymlink != 0 {
			t.Errorf("%s is a symlink entry", f.Name)
		}
		if strings.Contains(f.Name, `\`) || strings.HasPrefix(f.Name, "/") {
			t.Errorf("bad entry name %q", f.Name)
		}
	}
}

func TestPack_Deterministic(t *testing.T) {
	root := project(t)
	a, err := Pack(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Remove()
	b, err := Pack(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Remove()
	da, _ := os.ReadFile(a.Path)
	db, _ := os.ReadFile(b.Path)
	if string(da) != string(db) {
		t.Error("the same tree packed to different bytes")
	}
}

func TestPack_Empty(t *testing.T) {
	root := t.TempDir()
	write(t, root, "node_modules/x.js", "x", 0o644)
	write(t, root, ".env", "x", 0o644)
	if _, err := Pack(context.Background(), root); err != ErrEmpty {
		t.Fatalf("err = %v, want ErrEmpty", err)
	}
}

func TestPack_NestedGitignoreInWalk(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.txt", "x", 0o644)
	write(t, root, "sub/.gitignore", "local.txt\n", 0o644)
	write(t, root, "sub/local.txt", "x", 0o644)
	write(t, root, "sub/keep.txt", "x", 0o644)
	write(t, root, "other/local.txt", "x", 0o644)
	a, err := Pack(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Remove()
	got := strings.Join(names(t, a), ",")
	if want := "a.txt,other/local.txt,sub/.gitignore,sub/keep.txt"; got != want {
		t.Errorf("entries = %s, want %s", got, want)
	}
}

func TestLargest(t *testing.T) {
	root := t.TempDir()
	write(t, root, "small.txt", "x", 0o644)
	// Incompressible-enough content: distinct bytes keep the compressed size ordered.
	big := make([]byte, 0, 4096)
	for i := range 4096 {
		big = append(big, byte(i*7+i/13))
	}
	write(t, root, "big.bin", string(big), 0o644)
	a, err := Pack(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Remove()
	l := a.Largest(1)
	if len(l) != 1 || l[0].Path != "big.bin" {
		t.Errorf("Largest(1) = %v, want big.bin", l)
	}
}

// packBoth packs root in walk mode and, when git is available, in git mode,
// and returns the archive entries for each mode. Each case builds its own
// tree, so two names that differ only by case never collide on disk.
func packBoth(t *testing.T, root string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	a, err := Pack(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Remove()
	out["walk"] = names(t, a)

	if _, err := exec.LookPath("git"); err != nil {
		return out
	}
	if b, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Logf("git init: %v %s", err, b)
		return out
	}
	g, err := Pack(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Remove()
	out["git"] = names(t, g)
	return out
}

func TestPack_AlwaysExcludedNames(t *testing.T) {
	cases := []struct {
		name string
		rel  string
	}{
		{"env", ".env"},
		{"env upper", ".ENV"},
		{"env mixed", ".Env.production"},
		{"env nested upper", "pkg/.ENV.local"},
		{"env nested", "config/.env.production"},
		{"envrc", ".envrc"},
		{"environment", ".environment"},
		{"node_modules mixed", "Node_Modules/a.js"},
		{"node_modules nested", "pkg/node_modules/a.js"},
		{"git file", "sub/.git"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, "keep.txt", "x", 0o644)
			write(t, root, tc.rel, "SECRET=1", 0o600)
			for mode, got := range packBoth(t, root) {
				if strings.Join(got, ",") != "keep.txt" {
					t.Errorf("%s mode packed %v, want only keep.txt (%s must be excluded)", mode, got, tc.rel)
				}
			}
		})
	}
}

// A directory named .git (any case) holds repository metadata. It is tested
// in walk mode only: a directory like that inside a git work tree would be a
// nested repository or corrupt the fixture.
func TestPack_AlwaysExcludedGitDirectories(t *testing.T) {
	for _, rel := range []string{".git/config", "pkg/.git/config", ".GIT/config", "pkg/.Git/config"} {
		t.Run(rel, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, "keep.txt", "x", 0o644)
			write(t, root, rel, "x", 0o644)
			a, err := Pack(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Remove()
			if got := strings.Join(names(t, a), ","); got != "keep.txt" {
				t.Errorf("packed %s, want only keep.txt", got)
			}
		})
	}
}

func TestPack_SymlinksAreSkipped(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	write(t, outside, "secret.txt", "outside", 0o644)
	write(t, outside, "dir/inner.txt", "outside", 0o644)
	write(t, root, "keep.txt", "x", 0o644)
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "file-link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "dir"), filepath.Join(root, "dir-link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for mode, got := range packBoth(t, root) {
		if strings.Join(got, ",") != "keep.txt" {
			t.Errorf("%s mode packed %v, want only keep.txt", mode, got)
		}
	}
}
