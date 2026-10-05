// Package unpack extracts a source archive into a directory without trusting
// it. The platform already sanitizes what it stores, but a client that writes
// files from a zip must hold its own line: a bad archive must not write outside
// the target, follow a link, or fill the disk.
package unpack

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sureva-ch/sureva-cli/internal/sourcebase"
)

const (
	// DefaultMaxEntries bounds the entries of one archive. The platform accepts
	// at most 10,000, so this leaves generous headroom.
	DefaultMaxEntries = 50_000
	// DefaultMaxBytes bounds the total uncompressed bytes of one archive. The
	// platform accepts at most 100 MB, so this leaves generous headroom while
	// still stopping a zip bomb.
	DefaultMaxBytes int64 = 1 << 30
)

// stagingPrefix starts the name of the hidden directory an extraction builds a
// tree in. One left behind by a killed pull is removed by the next pull.
const stagingPrefix = ".sureva-pull-"

// Limits caps what one extraction may produce. A zero field takes its default.
type Limits struct {
	MaxEntries int
	MaxBytes   int64
}

// Options configures Extract.
type Options struct {
	// Force allows extracting into a directory that already holds files.
	// Existing files that are not in the archive are left alone.
	Force  bool
	Limits Limits
	// Finalize, when set, runs after every entry is written and before a staged
	// tree is moved into place, so extra files land in the same step. root is
	// the tree about to become the target.
	Finalize func(root string) error
}

// Result counts what was written.
type Result struct {
	Files int
	Bytes int64
}

// UnsafeArchiveError means the archive was refused: a hostile or malformed
// entry, or a limit exceeded. Nothing from a refused archive is kept, except
// when the refusal happens while writing into a populated directory (--force),
// where files written earlier stay.
type UnsafeArchiveError struct {
	Entry  string
	Reason string
}

func (e *UnsafeArchiveError) Error() string {
	if e.Entry == "" {
		return "unsafe archive: " + e.Reason
	}
	return fmt.Sprintf("unsafe archive: entry %q %s", e.Entry, e.Reason)
}

// NotEmptyError means the target directory already holds files.
type NotEmptyError struct{ Dir string }

func (e *NotEmptyError) Error() string {
	return fmt.Sprintf("%s is not empty; use --force to extract into it anyway", e.Dir)
}

// ConflictError means an archive entry cannot be placed because of what already
// exists in the target: a directory where the archive has a file, or a file
// where it needs a directory. It is found before anything is written and no
// user content is removed to make room.
type ConflictError struct {
	Path   string
	Reason string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("cannot extract: %s %s", e.Path, e.Reason)
}

// PartialError means extraction failed after some files were already placed in
// a directory that cannot be rolled back (a populated --force target, or the
// final move of a staged tree).
type PartialError struct {
	Written int
	Err     error
}

func (e *PartialError) Error() string {
	return fmt.Sprintf("%v (%d files were already written and were left in place)", e.Err, e.Written)
}
func (e *PartialError) Unwrap() error { return e.Err }

// ErrNotDir means the target exists and is not a directory.
var ErrNotDir = errors.New("target exists and is not a directory")

type targetState int

const (
	targetMissing targetState = iota
	targetEmpty
	targetPopulated
)

// inspect classifies dir. A directory is empty when it holds nothing, or only
// the state directory `sureva sources pull` leaves behind. root is the
// symlink-resolved absolute path (the absolute path when dir is missing).
func inspect(dir string) (root string, st targetState, err error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", 0, err
	}
	info, err := os.Stat(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return abs, targetMissing, nil
	}
	if err != nil {
		return "", 0, err
	}
	if !info.IsDir() {
		return "", 0, ErrNotDir
	}
	root, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return "", 0, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", 0, err
	}
	for _, e := range entries {
		if e.Name() == sourcebase.Dir && e.IsDir() {
			continue
		}
		if isStaging(e) {
			continue
		}
		return root, targetPopulated, nil
	}
	return root, targetEmpty, nil
}

// isStaging reports whether e is a real directory named like the ones
// MkdirTemp makes for stagingPrefix. A symlink never matches.
func isStaging(e fs.DirEntry) bool {
	suffix, ok := strings.CutPrefix(e.Name(), stagingPrefix)
	if !ok || suffix == "" || !e.IsDir() {
		return false
	}
	for _, r := range suffix {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// removeStaleStaging deletes staging directories an interrupted pull left in
// root. Only exact matches that are real directories are touched; RemoveAll
// does not follow links.
func removeStaleStaging(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if isStaging(e) {
			if err := os.RemoveAll(filepath.Join(root, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// Preflight reports whether dir can be extracted into: a *NotEmptyError when it
// holds files and force is false, ErrNotDir when it is a file, a
// *sourcebase.StateError when the state directory or record is a link. It lets
// a caller refuse before downloading anything.
func Preflight(dir string, force bool) error {
	root, st, err := inspect(dir)
	if err != nil {
		return err
	}
	if st == targetPopulated && !force {
		return &NotEmptyError{Dir: dir}
	}
	if st != targetMissing {
		return sourcebase.Check(root)
	}
	return nil
}

type entry struct {
	file  *zip.File
	rel   string
	isDir bool
	perm  fs.FileMode
}

// Extract unpacks the zip at archivePath into dir.
//
// The whole archive is checked before the first byte is written: every name,
// every entry type, the entry count and the declared size. Names must be
// relative, forward-slash, free of "..", "." , NUL, backslash and drive
// letters; links and every other non-regular entry are refused; the top-level
// name of the state directory is reserved. Sizes are enforced again while
// writing, because a zip bomb lies in its headers.
//
// Into a new or empty directory the tree is built in a hidden staging directory
// inside it and moved into place only when complete, so a failure leaves the
// directory as it was. Into a populated directory (Force) files are written in
// place; a failure midway is reported as a *PartialError. Either way no file is
// written outside dir, and a path that passes through a symlink already in dir
// is refused. Only permission bits are applied, without group or other write.
func Extract(ctx context.Context, archivePath, dir string, opts Options) (*Result, error) {
	lim := opts.Limits
	if lim.MaxEntries <= 0 {
		lim.MaxEntries = DefaultMaxEntries
	}
	if lim.MaxBytes <= 0 {
		lim.MaxBytes = DefaultMaxBytes
	}

	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, &UnsafeArchiveError{Reason: "not a readable zip: " + err.Error()}
	}
	defer func() { _ = zr.Close() }()

	entries, err := plan(zr.File, lim)
	if err != nil {
		return nil, err
	}

	root, st, err := inspect(dir)
	if err != nil {
		return nil, err
	}
	if st != targetMissing {
		// Refuse before anything is written, and before anything is removed.
		if st == targetPopulated && !opts.Force {
			return nil, &NotEmptyError{Dir: dir}
		}
		if err := sourcebase.Check(root); err != nil {
			return nil, err
		}
		if st == targetPopulated {
			if err := checkInPlace(root, entries); err != nil {
				return nil, err
			}
		}
		if err := removeStaleStaging(root); err != nil {
			return nil, err
		}
	}
	if st == targetPopulated {
		res, written, err := write(ctx, root, entries, lim)
		if err == nil && opts.Finalize != nil {
			err = opts.Finalize(root)
		}
		if err != nil {
			return nil, inPlaceFailure(ctx, err, written)
		}
		return res, nil
	}
	return extractStaged(ctx, root, st == targetMissing, entries, lim, opts)
}

// inPlaceFailure keeps a refusal or cancellation as it is and wraps a write
// failure with how many files had landed.
func inPlaceFailure(ctx context.Context, err error, written int) error {
	var unsafe *UnsafeArchiveError
	if ctx.Err() != nil || errors.As(err, &unsafe) || written == 0 {
		return err
	}
	return &PartialError{Written: written, Err: err}
}

func extractStaged(ctx context.Context, root string, created bool, entries []entry, lim Limits, opts Options) (*Result, error) {
	if created {
		if err := os.MkdirAll(root, 0o755); err != nil {
			return nil, err
		}
		resolved, err := filepath.EvalSymlinks(root)
		if err != nil {
			return nil, err
		}
		root = resolved
	}
	stage, err := os.MkdirTemp(root, stagingPrefix)
	if err != nil {
		if created {
			_ = os.Remove(root)
		}
		return nil, err
	}
	// Until the move starts, a failure undoes everything: the staging tree goes
	// and a directory this call created goes with it.
	undo := func() {
		_ = os.RemoveAll(stage)
		if created {
			_ = os.Remove(root)
		}
	}

	res, _, err := write(ctx, stage, entries, lim)
	if err == nil && opts.Finalize != nil {
		err = opts.Finalize(stage)
	}
	if err != nil {
		undo()
		return nil, err
	}
	if ctx.Err() != nil {
		undo()
		return nil, ctx.Err()
	}

	children, err := os.ReadDir(stage)
	if err != nil {
		undo()
		return nil, err
	}
	moved := 0
	for _, c := range children {
		from, to := filepath.Join(stage, c.Name()), filepath.Join(root, c.Name())
		if c.Name() == sourcebase.Dir {
			// An empty target may hold the state directory of an earlier pull.
			// It is never removed or recursed into: only the files the new tree
			// brings replace their namesakes (a rename never follows a link at
			// its destination).
			if err := mergeStateDir(from, to); err != nil {
				_ = os.RemoveAll(stage)
				return nil, &PartialError{Written: moved, Err: fmt.Errorf("move %s into place: %w", c.Name(), err)}
			}
			moved++
			continue
		}
		if err := os.Rename(from, to); err != nil {
			_ = os.RemoveAll(stage)
			return nil, &PartialError{Written: moved, Err: fmt.Errorf("move %s into place: %w", c.Name(), err)}
		}
		moved++
	}
	// What is left is the emptied state directory of the staged tree.
	_ = os.RemoveAll(stage)
	return res, nil
}

// mergeStateDir moves the children of the freshly written state directory from
// into to. When to does not exist the whole directory moves; when it exists as
// a real directory only its namesake children are replaced and the rest stays.
func mergeStateDir(from, to string) error {
	fi, err := os.Lstat(to)
	if errors.Is(err, fs.ErrNotExist) {
		return os.Rename(from, to)
	}
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a plain directory", to)
	}
	children, err := os.ReadDir(from)
	if err != nil {
		return err
	}
	for _, c := range children {
		if err := os.Rename(filepath.Join(from, c.Name()), filepath.Join(to, c.Name())); err != nil {
			return err
		}
	}
	return nil
}

// plan validates every entry and returns the ones to write. It writes nothing.
func plan(files []*zip.File, lim Limits) ([]entry, error) {
	if len(files) > lim.MaxEntries {
		return nil, &UnsafeArchiveError{Reason: fmt.Sprintf("has %d entries, more than the limit of %d", len(files), lim.MaxEntries)}
	}
	out := make([]entry, 0, len(files))
	seen := make(map[string]struct{}, len(files))
	names := make(claims, len(files))
	var declared uint64
	for _, f := range files {
		rel, isDir, reason := cleanName(f.Name)
		if reason != "" {
			return nil, &UnsafeArchiveError{Entry: f.Name, Reason: reason}
		}
		if isReserved(strings.SplitN(rel, "/", 2)[0]) {
			return nil, &UnsafeArchiveError{Entry: f.Name, Reason: "uses the reserved " + sourcebase.Dir + " directory"}
		}
		mode := f.Mode()
		switch {
		case isDir:
			if mode.Type() != 0 && !mode.IsDir() {
				return nil, &UnsafeArchiveError{Entry: f.Name, Reason: "is not a regular file or directory"}
			}
		case mode.Type() != 0:
			return nil, &UnsafeArchiveError{Entry: f.Name, Reason: "is a link or another non-regular entry"}
		}
		if _, dup := seen[rel]; dup {
			return nil, &UnsafeArchiveError{Entry: f.Name, Reason: "appears more than once"}
		}
		seen[rel] = struct{}{}
		if reason := names.claim(rel, isDir); reason != "" {
			return nil, &UnsafeArchiveError{Entry: f.Name, Reason: reason}
		}

		if !isDir {
			declared += f.UncompressedSize64
			if declared > uint64(lim.MaxBytes) {
				return nil, &UnsafeArchiveError{Reason: fmt.Sprintf("would expand beyond the limit of %d bytes", lim.MaxBytes)}
			}
		}
		// Only permission bits are kept: never setuid, setgid or sticky. Group and
		// other never get write access. Releases stored before modes were
		// recorded carry none or a FAT-style 0666; both end up 0644. The owner
		// can always read and write what was extracted.
		perm := mode.Perm() &^ 0o022
		if perm == 0 {
			perm = 0o644
		}
		out = append(out, entry{file: f, rel: rel, isDir: isDir, perm: perm | 0o600})
	}
	return out, nil
}

// claim records what an entry occupies. Names are compared case-folded, so the
// verdict is the same on every platform: on a case-insensitive filesystem
// (macOS, Windows) "a/B" and "A/b" are one file. It is also where a file and a
// path beneath it are caught.
type claim struct {
	name string
	kind claimKind
}

type claimKind int

const (
	claimFile    claimKind = iota
	claimDir               // listed as a directory entry
	claimImplied           // only exists as the parent of another entry
)

type claims map[string]claim

// claim returns "" when rel can be placed next to everything seen so far, else
// the reason it cannot.
func (c claims) claim(rel string, isDir bool) string {
	parts := strings.Split(rel, "/")
	for i := 1; i < len(parts); i++ {
		parent := strings.Join(parts[:i], "/")
		key := foldName(parent)
		switch got, ok := c[key]; {
		case !ok:
			c[key] = claim{name: parent, kind: claimImplied}
		case got.kind == claimFile:
			return fmt.Sprintf("is inside %q, which the archive also has as a file", got.name)
		}
	}
	key := foldName(rel)
	got, ok := c[key]
	switch {
	case !ok:
	case got.kind == claimImplied && isDir:
	case got.kind == claimImplied:
		return "is a file, but the archive also has entries inside it"
	default:
		return fmt.Sprintf("collides with %q, which is the same name on a case-insensitive filesystem", got.name)
	}
	kind := claimFile
	if isDir {
		kind = claimDir
	}
	c[key] = claim{name: rel, kind: kind}
	return ""
}

// foldName is the key names are compared by: case-insensitive.
func foldName(s string) string { return strings.ToLower(s) }

// isReserved reports whether a top-level name is the state directory, in any
// case and with the trailing dots and spaces Windows ignores.
func isReserved(top string) bool {
	return strings.EqualFold(strings.TrimRight(top, ". "), sourcebase.Dir)
}

// cleanName turns a zip entry name into a relative slash path, or says why it
// cannot be used. A trailing slash marks a directory.
func cleanName(name string) (rel string, isDir bool, reason string) {
	switch {
	case name == "":
		return "", false, "has an empty name"
	case strings.ContainsRune(name, 0):
		return "", false, "contains a NUL byte"
	case strings.Contains(name, `\`):
		return "", false, "contains a backslash"
	case strings.HasPrefix(name, "/"):
		return "", false, "is an absolute path"
	case len(name) >= 2 && name[1] == ':' && (name[0]|0x20 >= 'a' && name[0]|0x20 <= 'z'):
		return "", false, "starts with a drive letter"
	}
	isDir = strings.HasSuffix(name, "/")
	for _, part := range strings.Split(strings.TrimSuffix(name, "/"), "/") {
		switch part {
		case "":
			return "", false, "has an empty path segment"
		case ".", "..":
			return "", false, `has a "` + part + `" path segment`
		}
		if reason := badSegment(part); reason != "" {
			return "", false, reason
		}
	}
	return strings.TrimSuffix(name, "/"), isDir, ""
}

// windowsReserved are the device names Windows reserves, with or without an
// extension and in any case.
var windowsReserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true, "CONIN$": true, "CONOUT$": true,
}

func init() {
	for i := '1'; i <= '9'; i++ {
		windowsReserved["COM"+string(i)] = true
		windowsReserved["LPT"+string(i)] = true
	}
	for _, sup := range []string{"¹", "²", "³"} {
		windowsReserved["COM"+sup] = true
		windowsReserved["LPT"+sup] = true
	}
}

// badSegment says why one path segment cannot be written on every supported
// platform, or returns "". The rules are the same everywhere so one archive
// behaves the same everywhere: Windows strips trailing dots and spaces (so
// ".sureva." would become the reserved name and ".. " would become ".."),
// reserves device names, and treats ":" as a stream or drive separator. The
// characters <>"|?* are refused as well: Windows cannot create them and
// frameworks do not use them in file names ([id], (group), @slot and $ are
// fine), so refusing costs a real project nothing.
func badSegment(part string) string {
	if last := part[len(part)-1]; last == '.' || last == ' ' {
		return "has a name ending in a dot or a space, which Windows silently strips"
	}
	for _, r := range part {
		switch {
		case r < 0x20 || r == 0x7f:
			return "contains a control character"
		case r == ':':
			return `contains ":" (a drive or stream separator on Windows)`
		case strings.ContainsRune(`<>"|?*`, r):
			return "contains a character Windows does not allow in file names (" + string(r) + ")"
		}
	}
	base, _, _ := strings.Cut(part, ".")
	if windowsReserved[strings.ToUpper(strings.TrimRight(base, " "))] {
		return "uses a device name reserved on Windows"
	}
	return ""
}

// checkInPlace compares every entry with what already exists under root, before
// anything is written. A symlink anywhere on an entry's path is refused (never
// followed or replaced); a directory where the archive has a file, or a file
// where it needs a directory, is a conflict. No existing content is removed.
func checkInPlace(root string, entries []entry) error {
	for _, e := range entries {
		if err := checkNoSymlink(root, e.rel); err != nil {
			return err
		}
		cur := root
		parts := strings.Split(e.rel, "/")
		for i, part := range parts {
			cur = filepath.Join(cur, part)
			fi, err := os.Lstat(cur)
			if err != nil {
				break // the rest does not exist yet
			}
			rel := strings.Join(parts[:i+1], "/")
			last := i == len(parts)-1
			switch {
			case !last && !fi.IsDir():
				return &ConflictError{Path: rel, Reason: "is a file in the target, but the archive needs a directory there"}
			case last && e.isDir && !fi.IsDir():
				return &ConflictError{Path: rel, Reason: "is a file in the target, but the archive has a directory there"}
			case last && !e.isDir && fi.IsDir():
				return &ConflictError{Path: rel, Reason: "is a directory in the target, but the archive has a file there"}
			}
		}
	}
	return nil
}

// checkNoSymlink refuses rel when any existing component of root/rel is a
// symlink, so a write can never be redirected out of root.
func checkNoSymlink(root, rel string) error {
	cur := root
	for _, part := range strings.Split(rel, "/") {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if err != nil {
			return nil // the rest does not exist yet, so it cannot be a link
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return &UnsafeArchiveError{Entry: rel, Reason: "would be written through a symlink that already exists in the target"}
		}
	}
	return nil
}

// write creates entries under root and reports how many files landed. root must
// not be reachable through a symlink the archive does not control.
func write(ctx context.Context, root string, entries []entry, lim Limits) (*Result, int, error) {
	res := &Result{}
	var total int64
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, res.Files, err
		}
		dest := filepath.Join(root, filepath.FromSlash(e.rel))
		if !within(root, dest) {
			return nil, res.Files, &UnsafeArchiveError{Entry: e.rel, Reason: "resolves outside the target directory"}
		}
		if e.isDir {
			if err := mkdirAll(root, e.rel); err != nil {
				return nil, res.Files, err
			}
			continue
		}
		if err := mkdirAll(root, path(e.rel)); err != nil {
			return nil, res.Files, err
		}
		n, err := writeFile(ctx, dest, e, &total, lim.MaxBytes)
		if err != nil {
			return nil, res.Files, err
		}
		res.Files++
		res.Bytes += n
	}
	return res, res.Files, nil
}

// path returns the directory part of a slash path, "" for a top-level name.
func path(rel string) string {
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		return rel[:i]
	}
	return ""
}

func within(root, dest string) bool {
	r, err := filepath.Rel(root, dest)
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) && !filepath.IsAbs(r)
}

// mkdirAll creates rel under root one component at a time with mode 0755,
// refusing to pass through a symlink or over a file.
func mkdirAll(root, rel string) error {
	if rel == "" {
		return nil
	}
	cur := root
	for _, part := range strings.Split(rel, "/") {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if err := os.Mkdir(cur, 0o755); err != nil {
				return err
			}
			if err := os.Chmod(cur, 0o755); err != nil {
				return err
			}
		case err != nil:
			return err
		case fi.Mode()&fs.ModeSymlink != 0:
			return &UnsafeArchiveError{Entry: rel, Reason: "would be written through a symlink that already exists in the target"}
		case !fi.IsDir():
			return fmt.Errorf("%s exists and is not a directory", cur)
		}
	}
	return nil
}

func writeFile(ctx context.Context, dest string, e entry, total *int64, maxBytes int64) (int64, error) {
	if fi, err := os.Lstat(dest); err == nil {
		switch {
		case fi.Mode()&fs.ModeSymlink != 0:
			return 0, &UnsafeArchiveError{Entry: e.rel, Reason: "would overwrite a symlink that already exists in the target"}
		case fi.IsDir():
			return 0, fmt.Errorf("%s exists and is a directory", dest)
		}
		// Replace rather than truncate, so a read-only file or a hard link to
		// something else is never written through.
		if err := os.Remove(dest); err != nil {
			return 0, err
		}
	}
	rc, err := e.file.Open()
	if err != nil {
		return 0, &UnsafeArchiveError{Entry: e.rel, Reason: "cannot be read: " + err.Error()}
	}
	defer func() { _ = rc.Close() }()

	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, err
	}
	src := &guard{ctx: ctx, r: rc, entry: e.rel, left: int64(e.file.UncompressedSize64), total: total, max: maxBytes}
	n, copyErr := io.Copy(out, src)
	closeErr := out.Close()
	switch {
	case copyErr != nil:
		var ge *guardError
		if errors.As(copyErr, &ge) {
			return 0, ge.err
		}
		return 0, copyErr
	case closeErr != nil:
		return 0, closeErr
	}
	// Chmod is explicit so the umask cannot drop a permission bit.
	if err := os.Chmod(dest, e.perm); err != nil {
		return 0, err
	}
	return n, nil
}

// guard reads one entry while enforcing its declared size, the total budget and
// cancellation. Errors from the archive side are wrapped so the caller can tell
// them from a disk error.
type guard struct {
	ctx   context.Context
	r     io.Reader
	entry string
	left  int64 // bytes the entry declared it would hold, minus those read
	total *int64
	max   int64
}

type guardError struct{ err error }

func (g *guardError) Error() string { return g.err.Error() }

func (g *guard) Read(p []byte) (int, error) {
	if err := g.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := g.r.Read(p)
	g.left -= int64(n)
	*g.total += int64(n)
	switch {
	case g.left < 0:
		return n, &guardError{&UnsafeArchiveError{Entry: g.entry, Reason: "is larger than it declares"}}
	case *g.total > g.max:
		return n, &guardError{&UnsafeArchiveError{Reason: fmt.Sprintf("expands beyond the limit of %d bytes", g.max)}}
	case err != nil && err != io.EOF:
		return n, &guardError{&UnsafeArchiveError{Entry: g.entry, Reason: "is corrupt: " + err.Error()}}
	}
	return n, err
}
