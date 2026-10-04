// Package pack builds the source archive that `sureva deploy` uploads: a zip of
// a directory with the files a remote build must never receive left out.
//
// Which files are packed is decided in two layers. A fixed set is always
// excluded (node_modules, .git, .env*, .sureva), because dependencies are installed by
// the remote build and secrets belong in environment variables; .sureva holds the
// local record of the release a directory was pulled from. On top of that,
// the project's own ignore rules apply: .gitignore with exact git semantics when
// the directory is inside a git work tree (the file list comes from git itself),
// and a gitignore-syntax matcher otherwise. A .surevaignore file adds patterns
// in both cases.
package pack

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	ignore "github.com/sabhiram/go-gitignore"
)

// Exclusion reasons reported in Archive.Excluded.
const (
	ReasonAlways       = "always_excluded"
	ReasonGitignore    = "gitignore"
	ReasonSurevaIgnore = "surevaignore"
	ReasonSymlink      = "symlink"
	ReasonNotRegular   = "not_regular"
)

// SurevaIgnoreFile is the name of the project-level ignore file.
const SurevaIgnoreFile = ".surevaignore"

// ErrEmpty means nothing was left to pack once the exclusions were applied.
var ErrEmpty = errors.New("no files to pack after exclusions")

// maxReportedExclusions bounds Excluded.Entries so the JSON output stays small
// for a tree with thousands of ignored files.
const maxReportedExclusions = 50

// zipModTime is stamped on every entry so the same tree packs to the same bytes.
var zipModTime = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)

// Exclusion is one thing left out of the archive. A directory is reported once
// (with a trailing slash) rather than once per file it holds.
type Exclusion struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
	// Files is how many files the entry covers, when known. A directory pruned
	// during a plain walk is not counted, so the field is omitted there.
	Files int `json:"files,omitempty"`
}

// Excluded summarises what was left out.
type Excluded struct {
	Total    int            `json:"total"`
	ByReason map[string]int `json:"by_reason"`
	// Entries lists the largest groups first, capped at maxReportedExclusions.
	Entries   []Exclusion `json:"entries"`
	Truncated bool        `json:"truncated,omitempty"`
}

// Entry is one file in the archive.
type Entry struct {
	Path string `json:"path"`
	// Size is the compressed size inside the archive, which is what counts
	// against the upload limit.
	Size int64 `json:"size_bytes"`
}

// Archive is a finished zip on disk. The caller removes it with Remove.
type Archive struct {
	Path     string
	Size     int64
	Files    int
	Excluded Excluded
	// Mode is "git" when the file list came from git, "walk" otherwise.
	Mode    string
	entries []Entry
}

// Remove deletes the temporary archive file.
func (a *Archive) Remove() {
	if a != nil && a.Path != "" {
		_ = os.Remove(a.Path)
	}
}

// Largest returns up to n entries ordered by compressed size, biggest first.
func (a *Archive) Largest(n int) []Entry {
	out := append([]Entry(nil), a.entries...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Size != out[j].Size {
			return out[i].Size > out[j].Size
		}
		return out[i].Path < out[j].Path
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// Pack zips dir into a temporary file, which is removed again when packing
// fails or ctx is cancelled. Paths inside the archive are relative to
// dir, use forward slashes and have no wrapper directory; permission bits are
// preserved and entries are sorted by path. Symlinks are never followed and
// never added.
func Pack(ctx context.Context, dir string) (*Archive, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}

	surevaIgnore, err := loadIgnoreFile(filepath.Join(root, SurevaIgnoreFile))
	if err != nil {
		return nil, err
	}

	c := &collector{root: root, surevaIgnore: surevaIgnore, groups: map[groupKey]*Exclusion{}}
	mode := "walk"
	if files, ok := gitFileList(ctx, root); ok {
		mode = "git"
		err = c.fromList(files)
	} else {
		err = c.walk(ctx, "", nil)
	}
	if err != nil {
		return nil, err
	}
	if len(c.files) == 0 {
		return nil, ErrEmpty
	}
	sort.Strings(c.files)

	a := &Archive{Mode: mode, Excluded: c.summary()}
	if err := a.write(ctx, root, c.files); err != nil {
		a.Remove()
		return nil, err
	}
	return a, nil
}

func (a *Archive) write(ctx context.Context, root string, files []string) (err error) {
	f, err := os.CreateTemp("", "sureva-deploy-*.zip")
	if err != nil {
		return fmt.Errorf("create temp archive: %w", err)
	}
	a.Path = f.Name()
	defer func() {
		if cerr := f.Close(); err == nil && cerr != nil {
			err = cerr
		}
	}()

	zw := zip.NewWriter(f)
	headers := make([]*zip.FileHeader, 0, len(files))
	for _, rel := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		fh, werr := addFile(zw, root, rel)
		if werr != nil {
			return werr
		}
		headers = append(headers, fh)
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("finish archive: %w", err)
	}

	// Sizes are final only once the writer has flushed each entry.
	for _, fh := range headers {
		a.entries = append(a.entries, Entry{Path: fh.Name, Size: int64(fh.CompressedSize64)})
	}
	a.Files = len(files)
	st, err := f.Stat()
	if err != nil {
		return err
	}
	a.Size = st.Size()
	return nil
}

func addFile(zw *zip.Writer, root, rel string) (*zip.FileHeader, error) {
	full := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Lstat(full)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s changed while packing: no longer a regular file", rel)
	}
	fh := &zip.FileHeader{Name: rel, Method: zip.Deflate, Modified: zipModTime}
	fh.SetMode(info.Mode().Perm())
	w, err := zw.CreateHeader(fh)
	if err != nil {
		return nil, err
	}
	src, err := os.Open(full)
	if err != nil {
		return nil, err
	}
	defer func() { _ = src.Close() }()
	if _, err := io.Copy(w, src); err != nil {
		return nil, fmt.Errorf("pack %s: %w", rel, err)
	}
	return fh, nil
}

// groupKey identifies one reported exclusion.
type groupKey struct{ path, reason string }

type collector struct {
	root         string
	surevaIgnore *ignore.GitIgnore
	files        []string
	groups       map[groupKey]*Exclusion
}

func (c *collector) exclude(p, reason string, files int) {
	k := groupKey{p, reason}
	if g, ok := c.groups[k]; ok {
		g.Files += files
		return
	}
	c.groups[k] = &Exclusion{Path: p, Reason: reason, Files: files}
}

func (c *collector) summary() Excluded {
	out := Excluded{ByReason: map[string]int{}, Entries: []Exclusion{}}
	for _, g := range c.groups {
		out.Entries = append(out.Entries, *g)
		n := g.Files
		if n == 0 {
			n = 1
		}
		out.Total += n
		out.ByReason[g.Reason] += n
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		a, b := out.Entries[i], out.Entries[j]
		if a.Files != b.Files {
			return a.Files > b.Files
		}
		return a.Path < b.Path
	})
	if len(out.Entries) > maxReportedExclusions {
		out.Entries = out.Entries[:maxReportedExclusions]
		out.Truncated = true
	}
	return out
}

// fromList filters a flat file list produced by git.
func (c *collector) fromList(files []string) error {
	for _, rel := range files {
		if prefix, ok := alwaysExcluded(rel); ok {
			c.exclude(prefix, ReasonAlways, 1)
			continue
		}
		info, err := os.Lstat(filepath.Join(c.root, filepath.FromSlash(rel)))
		if err != nil {
			// Tracked but deleted from the work tree: nothing to pack.
			continue
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			c.exclude(rel, ReasonSymlink, 1)
		case !info.Mode().IsRegular():
			// A submodule is a directory here, and a socket or device is
			// not file content. Neither belongs in a source archive.
			c.exclude(rel, ReasonNotRegular, 1)
		case c.surevaIgnore != nil && c.surevaIgnore.MatchesPath(rel):
			c.exclude(rel, ReasonSurevaIgnore, 1)
		default:
			c.files = append(c.files, rel)
		}
	}
	return nil
}

// ignoreLayer is a .gitignore found during a walk; base is the directory that
// holds it ("" for the root, otherwise "sub/dir/").
type ignoreLayer struct {
	base string
	gi   *ignore.GitIgnore
}

// walk lists dir (relative to the root, "" for the root) without following
// symlinks, applying .gitignore files as it descends. A path is dropped when
// any layer drops it; a negation in a nested file does not re-include a path a
// parent file ignored, which only matters for trees outside git, where the
// exact semantics are not available anyway.
func (c *collector) walk(ctx context.Context, dir string, layers []ignoreLayer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Join(c.root, filepath.FromSlash(dir)))
	if err != nil {
		return err
	}
	if gi, err := loadIgnoreFile(filepath.Join(c.root, filepath.FromSlash(dir), ".gitignore")); err != nil {
		return err
	} else if gi != nil {
		layers = append(layers[:len(layers):len(layers)], ignoreLayer{base: dirPrefix(dir), gi: gi})
	}

	for _, e := range entries { // os.ReadDir sorts by name
		rel := path.Join(dir, e.Name())
		isDir := e.IsDir()
		if prefix, ok := alwaysExcluded(rel); ok {
			c.exclude(displayPath(prefix, isDir && prefix == rel), ReasonAlways, 0)
			continue
		}
		switch {
		case e.Type()&fs.ModeSymlink != 0:
			c.exclude(rel, ReasonSymlink, 0)
		case isDir:
			if reason := c.ignoredBy(rel+"/", layers); reason != "" {
				c.exclude(rel+"/", reason, 0)
				continue
			}
			if err := c.walk(ctx, rel, layers); err != nil {
				return err
			}
		case !e.Type().IsRegular():
			c.exclude(rel, ReasonNotRegular, 0)
		default:
			if reason := c.ignoredBy(rel, layers); reason != "" {
				c.exclude(rel, reason, 0)
				continue
			}
			c.files = append(c.files, rel)
		}
	}
	return nil
}

func (c *collector) ignoredBy(rel string, layers []ignoreLayer) string {
	for i := len(layers) - 1; i >= 0; i-- {
		l := layers[i]
		if strings.HasPrefix(rel, l.base) && l.gi.MatchesPath(strings.TrimPrefix(rel, l.base)) {
			return ReasonGitignore
		}
	}
	if c.surevaIgnore != nil && c.surevaIgnore.MatchesPath(rel) {
		return ReasonSurevaIgnore
	}
	return ""
}

func dirPrefix(dir string) string {
	if dir == "" {
		return ""
	}
	return dir + "/"
}

func displayPath(p string, isDir bool) string {
	if isDir {
		return p + "/"
	}
	return p
}

// alwaysExcluded reports whether rel has a component that is never packed, and
// returns the path up to and including that component, so a whole directory can
// be reported once.
func alwaysExcluded(rel string) (string, bool) {
	parts := strings.Split(rel, "/")
	for i, name := range parts {
		// Case-insensitive: on macOS and Windows, .ENV is the same file as .env.
		lower := strings.ToLower(name)
		if lower == "node_modules" || lower == ".git" || lower == ".sureva" || strings.HasPrefix(lower, ".env") {
			prefix := strings.Join(parts[:i+1], "/")
			if i < len(parts)-1 {
				prefix += "/"
			}
			return prefix, true
		}
	}
	return "", false
}

func loadIgnoreFile(p string) (*ignore.GitIgnore, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", filepath.Base(p), err)
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	return ignore.CompileIgnoreLines(lines...), nil
}

// gitFileList returns the files git would track or consider untracked-but-not-
// ignored under dir, relative to dir. The second result is false when dir is not
// inside a git work tree or git is unavailable. Arguments go straight to exec
// and the output is NUL-separated, so no file name passes through a shell or is
// altered by git's path quoting.
func gitFileList(ctx context.Context, dir string) ([]string, bool) {
	inside := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--is-inside-work-tree")
	out, err := inside.Output()
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		return nil, false
	}
	ls := exec.CommandContext(ctx, "git", "-C", dir, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	out, err = ls.Output()
	if err != nil {
		return nil, false
	}
	seen := map[string]struct{}{}
	var files []string
	for _, name := range bytes.Split(out, []byte{0}) {
		if len(name) == 0 {
			continue
		}
		s := string(name)
		if _, dup := seen[s]; dup { // a conflicted index lists a path once per stage
			continue
		}
		seen[s] = struct{}{}
		files = append(files, s)
	}
	return files, true
}
