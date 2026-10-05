package unpack

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sureva-ch/sureva-cli/internal/sourcebase"
)

type zent struct {
	name string
	body string
	mode fs.FileMode // zero means a plain file with no mode recorded
	// method overrides the compression method (zip.Store or zip.Deflate).
	method uint16
}

func makeZip(t *testing.T, ents ...zent) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range ents {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.method != 0 || e.method == zip.Store {
			h.Method = e.method
		}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return writeBytes(t, buf.Bytes())
}

func writeBytes(t *testing.T, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "a.zip")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func isUnsafe(err error) bool {
	var u *UnsafeArchiveError
	return errors.As(err, &u)
}

func TestExtract_IntoNewDirectory(t *testing.T) {
	z := makeZip(t,
		zent{name: "index.js", body: "one", mode: 0o644},
		zent{name: "bin/run.sh", body: "#!/bin/sh", mode: 0o755},
		zent{name: "empty/", mode: fs.ModeDir | 0o755},
		zent{name: "old.txt", body: "no mode recorded"},
	)
	dir := filepath.Join(t.TempDir(), "deep", "new")

	res, err := Extract(context.Background(), z, dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 3 {
		t.Errorf("files = %d, want 3", res.Files)
	}
	if got := readFile(t, filepath.Join(dir, "bin", "run.sh")); got != "#!/bin/sh" {
		t.Errorf("run.sh = %q", got)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(filepath.Join(dir, "bin", "run.sh"))
		if fi.Mode().Perm() != 0o755 {
			t.Errorf("run.sh mode = %v, want 0755", fi.Mode().Perm())
		}
		fi, _ = os.Stat(filepath.Join(dir, "old.txt"))
		if fi.Mode().Perm() != 0o644 {
			t.Errorf("a file without a recorded mode = %v, want 0644", fi.Mode().Perm())
		}
		fi, _ = os.Stat(filepath.Join(dir, "bin"))
		if fi.Mode().Perm() != 0o755 {
			t.Errorf("parent directory mode = %v, want 0755", fi.Mode().Perm())
		}
	}
	if fi, err := os.Stat(filepath.Join(dir, "empty")); err != nil || !fi.IsDir() {
		t.Errorf("empty directory entry was not created: %v", err)
	}
	assertNoStaging(t, dir)
}

func assertNoStaging(t *testing.T, dir string) {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(dir, ".sureva-pull-*"))
	if len(m) != 0 {
		t.Errorf("staging directory left behind: %v", m)
	}
}

func TestExtract_SetuidSetgidStickyAreDropped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits")
	}
	z := makeZip(t, zent{name: "evil", body: "x", mode: 0o755 | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky})
	dir := t.TempDir()

	if _, err := Extract(context.Background(), z, dir, Options{}); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(filepath.Join(dir, "evil"))
	if fi.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0 {
		t.Errorf("special bits survived: %v", fi.Mode())
	}
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("perm = %v, want 0755", fi.Mode().Perm())
	}
}

func TestExtract_HostileNamesWriteNothing(t *testing.T) {
	cases := map[string]string{
		"traversal":         "../escape.txt",
		"nested traversal":  "a/../../escape.txt",
		"absolute":          "/etc/escape.txt",
		"backslash":         `a\b.txt`,
		"backslash escape":  `..\escape.txt`,
		"drive letter":      "C:/escape.txt",
		"drive letter bare": "c:escape.txt",
		"nul byte":          "a\x00b.txt",
		"dot segment":       "./a.txt",
		"empty segment":     "a//b.txt",
		"state directory":   ".sureva/source.json",
		"state dir folded":  ".SUREVA/source.json",
	}
	for name, entry := range cases {
		t.Run(name, func(t *testing.T) {
			// A harmless first entry proves nothing is written before the check.
			z := makeZip(t, zent{name: "ok.txt", body: "ok"}, zent{name: entry, body: "bad"})
			parent := t.TempDir()
			dir := filepath.Join(parent, "out")

			_, err := Extract(context.Background(), z, dir, Options{})

			if !isUnsafe(err) {
				t.Fatalf("err = %v, want an UnsafeArchiveError", err)
			}
			if _, statErr := os.Stat(dir); !errors.Is(statErr, fs.ErrNotExist) {
				t.Errorf("the target was created or left behind: %v", statErr)
			}
			if _, statErr := os.Stat(filepath.Join(parent, "escape.txt")); statErr == nil {
				t.Error("a file escaped the target")
			}
		})
	}
}

func TestExtract_SymlinkEntryRefused(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	h := &zip.FileHeader{Name: "link"}
	h.SetMode(fs.ModeSymlink | 0o777)
	w, _ := zw.CreateHeader(h)
	_, _ = w.Write([]byte("/etc/passwd"))
	_ = zw.Close()
	dir := filepath.Join(t.TempDir(), "out")

	_, err := Extract(context.Background(), writeBytes(t, buf.Bytes()), dir, Options{})

	if !isUnsafe(err) || !strings.Contains(err.Error(), "link") {
		t.Fatalf("err = %v, want a refusal naming the link", err)
	}
	if _, statErr := os.Lstat(dir); statErr == nil {
		t.Error("the target must not exist after a refused archive")
	}
}

func TestExtract_DuplicateEntryRefused(t *testing.T) {
	z := makeZip(t, zent{name: "a.txt", body: "1"}, zent{name: "a.txt", body: "2"})
	if _, err := Extract(context.Background(), z, filepath.Join(t.TempDir(), "o"), Options{}); !isUnsafe(err) {
		t.Fatalf("err = %v, want an UnsafeArchiveError", err)
	}
}

func TestExtract_EntryCountLimit(t *testing.T) {
	z := makeZip(t, zent{name: "a", body: "1"}, zent{name: "b", body: "2"}, zent{name: "c", body: "3"})
	_, err := Extract(context.Background(), z, filepath.Join(t.TempDir(), "o"), Options{Limits: Limits{MaxEntries: 2}})
	if !isUnsafe(err) || !strings.Contains(err.Error(), "entries") {
		t.Fatalf("err = %v, want the entry limit", err)
	}
}

func TestExtract_ZipBombRefusedByDeclaredSize(t *testing.T) {
	z := makeZip(t, zent{name: "zeros", body: strings.Repeat("\x00", 64<<10)}) // deflates to a few dozen bytes
	dir := filepath.Join(t.TempDir(), "o")

	_, err := Extract(context.Background(), z, dir, Options{Limits: Limits{MaxBytes: 1024}})

	if !isUnsafe(err) || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("err = %v, want the size limit", err)
	}
	if _, statErr := os.Stat(dir); statErr == nil {
		t.Error("nothing may be written for a refused archive")
	}
}

// patchDeclaredSize rewrites the uncompressed size in every central directory
// header, the way a bomb understates what it holds.
func patchDeclaredSize(t *testing.T, b []byte, size uint32) {
	t.Helper()
	sig := []byte{'P', 'K', 1, 2}
	patched := false
	for i := 0; i+46 <= len(b); i++ {
		if bytes.Equal(b[i:i+4], sig) {
			binary.LittleEndian.PutUint32(b[i+24:], size)
			patched = true
		}
	}
	if !patched {
		t.Fatal("no central directory header found")
	}
}

func TestExtract_EntryLargerThanItDeclares(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.CreateHeader(&zip.FileHeader{Name: "lie.bin", Method: zip.Store})
	_, _ = w.Write(bytes.Repeat([]byte("A"), 4096))
	_ = zw.Close()
	b := buf.Bytes()
	patchDeclaredSize(t, b, 10)
	dir := filepath.Join(t.TempDir(), "o")

	_, err := Extract(context.Background(), writeBytes(t, b), dir, Options{})

	if !isUnsafe(err) {
		t.Fatalf("err = %v, want an UnsafeArchiveError", err)
	}
	if _, statErr := os.Stat(dir); statErr == nil {
		t.Error("a half-written tree was left behind")
	}
}

func TestExtract_TotalBudgetEnforcedWhileWriting(t *testing.T) {
	// Two entries that each declare a small size but whose real bytes together
	// exceed the budget: the declared sum is under it, the stream is not.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range []string{"a", "b"} {
		w, _ := zw.CreateHeader(&zip.FileHeader{Name: n, Method: zip.Store})
		_, _ = w.Write(bytes.Repeat([]byte("A"), 600))
	}
	_ = zw.Close()
	b := buf.Bytes()
	// Declare 100 bytes for each entry: sum 200 <= budget 1000, real bytes 1200.
	patchDeclaredSize(t, b, 100)
	_, err := Extract(context.Background(), writeBytes(t, b), filepath.Join(t.TempDir(), "o"), Options{Limits: Limits{MaxBytes: 1000}})
	if !isUnsafe(err) {
		t.Fatalf("err = %v, want an UnsafeArchiveError", err)
	}
}

func TestExtract_NotEmptyWithoutForce(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mine.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	z := makeZip(t, zent{name: "a.txt", body: "a"})

	_, err := Extract(context.Background(), z, dir, Options{})

	var ne *NotEmptyError
	if !errors.As(err, &ne) {
		t.Fatalf("err = %v, want NotEmptyError", err)
	}
	if err := Preflight(dir, false); !errors.As(err, &ne) {
		t.Errorf("Preflight = %v, want NotEmptyError", err)
	}
	if err := Preflight(dir, true); err != nil {
		t.Errorf("Preflight with force = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "a.txt")); statErr == nil {
		t.Error("nothing may be written into a refused directory")
	}
}

func TestExtract_ForceLeavesOtherFilesAndOverwritesTheArchiveOnes(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"mine.txt": "mine", "a.txt": "old"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o444); err != nil {
			t.Fatal(err)
		}
	}
	z := makeZip(t, zent{name: "a.txt", body: "new"}, zent{name: "sub/b.txt", body: "b"})

	res, err := Extract(context.Background(), z, dir, Options{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 2 {
		t.Errorf("files = %d", res.Files)
	}
	if got := readFile(t, filepath.Join(dir, "a.txt")); got != "new" {
		t.Errorf("a.txt = %q, want it overwritten", got)
	}
	if got := readFile(t, filepath.Join(dir, "mine.txt")); got != "mine" {
		t.Errorf("mine.txt = %q, want it untouched", got)
	}
}

func TestExtract_StateDirectoryAloneCountsAsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".sureva"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".sureva", "source.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	z := makeZip(t, zent{name: "a.txt", body: "a"})

	_, err := Extract(context.Background(), z, dir, Options{Finalize: func(root string) error {
		_, werr := sourcebase.Write(root, sourcebase.Base{SourceID: "new"})
		return werr
	}})
	if err != nil {
		t.Fatalf("a directory holding only .sureva must count as empty: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, "a.txt")); got != "a" {
		t.Errorf("a.txt = %q", got)
	}
	if b := sourcebase.Read(dir); b == nil || b.SourceID != "new" {
		t.Errorf("the old state file should have been replaced by the new one, got %+v", b)
	}
	assertNoStaging(t, dir)
}

func TestExtract_EscapeThroughExistingSymlinkRefused(t *testing.T) {
	outside := t.TempDir()
	dir := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	z := makeZip(t, zent{name: "ok.txt", body: "ok"}, zent{name: "link/evil.txt", body: "evil"})

	_, err := Extract(context.Background(), z, dir, Options{Force: true})

	if !isUnsafe(err) || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("err = %v, want a symlink refusal", err)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "evil.txt")); statErr == nil {
		t.Fatal("a file was written outside the target through a symlink")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "ok.txt")); statErr == nil {
		t.Error("the refusal must come before any write")
	}
}

func TestExtract_OverwritingAnExistingSymlinkFileRefused(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "target.txt")
	if err := os.WriteFile(outside, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "a.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	z := makeZip(t, zent{name: "a.txt", body: "evil"})

	_, err := Extract(context.Background(), z, dir, Options{Force: true})

	if !isUnsafe(err) {
		t.Fatalf("err = %v, want an UnsafeArchiveError", err)
	}
	if got := readFile(t, outside); got != "keep" {
		t.Errorf("the file behind the symlink was changed: %q", got)
	}
}

func TestExtract_TargetThatIsASymlinkToADirectoryIsUsed(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	z := makeZip(t, zent{name: "a.txt", body: "a"})
	if _, err := Extract(context.Background(), z, link, Options{}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(real, "a.txt")); got != "a" {
		t.Errorf("a.txt = %q", got)
	}
}

func TestExtract_TargetIsAFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	z := makeZip(t, zent{name: "a.txt", body: "a"})
	if _, err := Extract(context.Background(), z, f, Options{}); !errors.Is(err, ErrNotDir) {
		t.Fatalf("err = %v, want ErrNotDir", err)
	}
}

func TestExtract_NotAZip(t *testing.T) {
	_, err := Extract(context.Background(), writeBytes(t, []byte("not a zip")), filepath.Join(t.TempDir(), "o"), Options{})
	if !isUnsafe(err) {
		t.Fatalf("err = %v, want an UnsafeArchiveError", err)
	}
}

func TestExtract_CancelledLeavesNothing(t *testing.T) {
	z := makeZip(t, zent{name: "a.txt", body: "a"})
	parent := t.TempDir()
	dir := filepath.Join(parent, "o")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Extract(ctx, z, dir, Options{})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if _, statErr := os.Stat(dir); statErr == nil {
		t.Error("the target must not be left behind")
	}
}

func TestExtract_FinalizeFailureUndoesStagedTree(t *testing.T) {
	dir := t.TempDir()
	z := makeZip(t, zent{name: "a.txt", body: "a"})
	_, err := Extract(context.Background(), z, dir, Options{Finalize: func(string) error { return errors.New("boom") }})
	if err == nil {
		t.Fatal("want the Finalize error")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("the directory must be as it was, found %d entries", len(entries))
	}
}

// assertNothingWritten checks that a refused extraction left dir exactly as it
// was: missing or empty.
func assertNothingWritten(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("a refused archive must write nothing, found %d entries in %s", len(entries), dir)
	}
}

func TestExtract_CollidingNamesRefusedBeforeAnyWrite(t *testing.T) {
	cases := []struct {
		name string
		ents []zent
	}{
		{"file case collision", []zent{{name: "first.txt", body: "1"}, {name: "a/B", body: "x"}, {name: "A/b", body: "y"}}},
		{"same directory", []zent{{name: "first.txt", body: "1"}, {name: "Readme", body: "x"}, {name: "README", body: "y"}}},
		{"file then directory of other case", []zent{{name: "first.txt", body: "1"}, {name: "a", body: "x"}, {name: "A/b", body: "y"}}},
		{"directory entry vs file", []zent{{name: "first.txt", body: "1"}, {name: "d/", mode: fs.ModeDir | 0o755}, {name: "D", body: "y"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "out")
			_, err := Extract(context.Background(), makeZip(t, tc.ents...), dir, Options{})
			if !isUnsafe(err) {
				t.Fatalf("want an *UnsafeArchiveError, got %v", err)
			}
			assertNothingWritten(t, dir)
		})
	}
}

func TestExtract_FileAndPathBeneathItRefusedAsUnsafeArchive(t *testing.T) {
	cases := []struct {
		name string
		ents []zent
	}{
		{"file then child", []zent{{name: "first.txt", body: "1"}, {name: "a", body: "x"}, {name: "a/b", body: "y"}}},
		{"child then file", []zent{{name: "first.txt", body: "1"}, {name: "a/b", body: "y"}, {name: "a", body: "x"}}},
		{"deep child then file", []zent{{name: "first.txt", body: "1"}, {name: "a/b/c", body: "y"}, {name: "a/b", body: "x"}}},
		{"other case", []zent{{name: "first.txt", body: "1"}, {name: "A", body: "x"}, {name: "a/b", body: "y"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, force := range []bool{false, true} {
				dir := t.TempDir()
				if force {
					if err := os.WriteFile(filepath.Join(dir, "mine"), []byte("m"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				_, err := Extract(context.Background(), makeZip(t, tc.ents...), dir, Options{Force: force})
				if !isUnsafe(err) {
					t.Fatalf("force=%v: want an *UnsafeArchiveError, got %v", force, err)
				}
				if _, statErr := os.Stat(filepath.Join(dir, "first.txt")); statErr == nil {
					t.Errorf("force=%v: an entry was written before the refusal", force)
				}
			}
		})
	}
}

func TestExtract_ForceTypeConflictWithExistingContentRefusedUpFront(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, dir string)
		ents  []zent
		path  string
	}{
		{
			name: "directory where the archive has a file",
			setup: func(t *testing.T, dir string) {
				if err := os.MkdirAll(filepath.Join(dir, "x"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "x", "keep"), []byte("k"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			ents: []zent{{name: "first.txt", body: "1"}, {name: "x", body: "file"}},
			path: "x",
		},
		{
			name: "file where the archive needs a directory",
			setup: func(t *testing.T, dir string) {
				if err := os.WriteFile(filepath.Join(dir, "y"), []byte("mine"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			ents: []zent{{name: "first.txt", body: "1"}, {name: "y/z.txt", body: "z"}},
			path: "y",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(t, dir)

			_, err := Extract(context.Background(), makeZip(t, tc.ents...), dir, Options{Force: true})

			if err == nil {
				t.Fatal("want a refusal")
			}
			var partial *PartialError
			if errors.As(err, &partial) {
				t.Fatalf("the conflict must be found before writing, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.path) {
				t.Errorf("the message must name %q: %v", tc.path, err)
			}
			if _, statErr := os.Stat(filepath.Join(dir, "first.txt")); statErr == nil {
				t.Error("an entry was written before the conflict was reported")
			}
			if tc.path == "x" {
				if got := readFile(t, filepath.Join(dir, "x", "keep")); got != "k" {
					t.Error("user content was removed")
				}
			}
		})
	}
}

func TestExtract_PlatformHostileNamesRefused(t *testing.T) {
	names := []string{
		".sureva.", ".sureva ", ".SUREVA./x", ".. ", "a/.. ", "trailingdot.", "trailingspace ", "dir./file",
		"CON", "con", "Con.txt", "PRN", "AUX.log", "NUL", "COM1", "com9.txt", "LPT1", "lpt9.x", "src/NUL.js", "CON .txt", "COM¹",
		"a:b", "C:", "file.txt:stream", "dir/a:b",
		"a<b", "a>b", `a"b`, "a|b", "a?b", "a*b",
		"tab\tname", "new\nline", "bell\x07", "del\x7f",
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "out")
			_, err := Extract(context.Background(), makeZip(t, zent{name: "ok.txt", body: "1"}, zent{name: name, body: "x"}), dir, Options{})
			if !isUnsafe(err) {
				t.Fatalf("want an *UnsafeArchiveError, got %v", err)
			}
			assertNothingWritten(t, dir)
		})
	}
}

func TestExtract_ConventionalNamesAreStillAccepted(t *testing.T) {
	names := []string{
		"app/[id]/page.tsx", "app/(group)/x.ts", "@slot/page.tsx", "$layout.js", "a+b.txt", "con-sole.txt", "console.log",
		"communicate.md", "com10.txt", "auxiliary", ".env.example", ".github/workflows/ci.yml", "a b.txt", "...x", "README.md",
		"café.txt", "_next/static/a.js", "a~1.txt", "a,b;c=d.txt", "a#b%c&d.txt", "a'b.txt", "a!b@c.txt",
	}
	var ents []zent
	for _, n := range names {
		ents = append(ents, zent{name: n, body: "x"})
	}
	dir := filepath.Join(t.TempDir(), "out")
	if _, err := Extract(context.Background(), makeZip(t, ents...), dir, Options{}); err != nil {
		t.Fatalf("conventional names must extract: %v", err)
	}
}

func TestExtract_StaleStagingDirectoriesAreRemovedAndDoNotCountAsContent(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, ".sureva-pull-123456")
	if err := os.MkdirAll(filepath.Join(stale, "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "deep", "f"), []byte("half"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Not the exact pattern: left alone.
	lookalike := filepath.Join(dir, ".sureva-pull-keep")
	if err := os.MkdirAll(lookalike, 0o755); err != nil {
		t.Fatal(err)
	}
	// A symlink with the exact pattern is never followed or removed.
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "precious"), []byte("p"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, ".sureva-pull-999")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// The lookalike and the link count as user content, so --force is needed.
	if _, err := Extract(context.Background(), makeZip(t, zent{name: "a.txt", body: "a"}), dir, Options{Force: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(stale); err == nil {
		t.Error("the stale staging directory was not removed")
	}
	if _, err := os.Stat(lookalike); err != nil {
		t.Error("a directory that is not an exact staging name was removed")
	}
	if _, err := os.Lstat(link); err != nil {
		t.Error("a symlink was removed")
	}
	if got := readFile(t, filepath.Join(outside, "precious")); got != "p" {
		t.Error("the link was followed")
	}
}

func TestExtract_OnlyStaleStagingCountsAsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".sureva-pull-42"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Preflight(dir, false); err != nil {
		t.Fatalf("a killed pull's staging directory must not block the next pull: %v", err)
	}
	if _, err := Extract(context.Background(), makeZip(t, zent{name: "a.txt", body: "a"}), dir, Options{}); err != nil {
		t.Fatal(err)
	}
	assertNoStaging(t, dir)
}

func TestExtract_EmptyTargetKeepsTheStateDirectoryAndReplacesOnlyTheRecord(t *testing.T) {
	dir := t.TempDir()
	sd := filepath.Join(dir, ".sureva")
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sd, "source.json"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sd, "notes.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	z := makeZip(t, zent{name: "a.txt", body: "a"})
	fin := func(root string) error {
		_, err := sourcebase.Write(root, sourcebase.Base{SourceID: "new"})
		return err
	}

	if _, err := Extract(context.Background(), z, dir, Options{Finalize: fin}); err != nil {
		t.Fatal(err)
	}
	if b := sourcebase.Read(dir); b == nil || b.SourceID != "new" {
		t.Errorf("record = %+v", b)
	}
	if got := readFile(t, filepath.Join(sd, "notes.txt")); got != "mine" {
		t.Error("a file the command does not own was removed from .sureva")
	}
}

func TestExtract_EmptyTargetRefusesAStateRecordThatIsASymlinkBeforeWriting(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".sureva"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, ".sureva", "source.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := Extract(context.Background(), makeZip(t, zent{name: "a.txt", body: "a"}), dir, Options{})
	var se *sourcebase.StateError
	if !errors.As(err, &se) {
		t.Fatalf("want a *sourcebase.StateError, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "a.txt")); statErr == nil {
		t.Error("files were written before the refusal")
	}
	if got := readFile(t, victim); got != "precious" {
		t.Error("the link target changed")
	}
}
