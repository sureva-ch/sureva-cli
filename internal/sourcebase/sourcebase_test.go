package sourcebase

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestWriteThenRead(t *testing.T) {
	dir := t.TempDir()
	want := Base{AppID: "app-1", SourceID: "src-9", ReleaseTag: "src-3", SHA256: "abc", PulledAt: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)}

	p, err := Write(dir, want)
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join(dir, ".sureva", "source.json") {
		t.Errorf("path = %s", p)
	}
	got := Read(dir)
	if got == nil || *got != want {
		t.Errorf("Read = %+v, want %+v", got, want)
	}
}

func TestReadMissingOrDamagedIsNil(t *testing.T) {
	dir := t.TempDir()
	if Read(dir) != nil {
		t.Error("a missing record must read as nil")
	}
	if err := os.MkdirAll(filepath.Join(dir, ".sureva"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"not json", `{"app_id":"a"}`} {
		if err := os.WriteFile(Path(dir), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if Read(dir) != nil {
			t.Errorf("a damaged record %q must read as nil", body)
		}
	}
}

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func TestWrite_RefusesAStateFileThatIsASymlink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim.txt")
	if err := os.WriteFile(victim, []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, victim, Path(dir))

	if _, err := Write(dir, Base{SourceID: "s"}); err == nil {
		t.Fatal("want a refusal")
	}
	if got, _ := os.ReadFile(victim); string(got) != "precious" {
		t.Errorf("the link target was overwritten: %q", got)
	}
}

func TestWrite_RefusesAStateDirectoryThatIsASymlink(t *testing.T) {
	dir := t.TempDir()
	elsewhere := t.TempDir()
	symlinkOrSkip(t, elsewhere, filepath.Join(dir, Dir))

	if _, err := Write(dir, Base{SourceID: "s"}); err == nil {
		t.Fatal("want a refusal")
	}
	if _, err := os.Stat(filepath.Join(elsewhere, File)); err == nil {
		t.Error("the state file was created through the link")
	}
}

func TestWrite_RefusesAStateFileThatIsADirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(Path(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(dir, Base{SourceID: "s"}); err == nil {
		t.Fatal("want a refusal")
	}
}

func TestWrite_ReplacesAtomicallyWithMode0600AndNoLeftovers(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(dir), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(dir, Base{SourceID: "s"}); err != nil {
		t.Fatal(err)
	}
	if got := Read(dir); got == nil || got.SourceID != "s" {
		t.Errorf("Read = %+v", got)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, Dir))
	if len(entries) != 1 {
		t.Errorf("want only %s in %s, found %d entries", File, Dir, len(entries))
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(Path(dir))
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
		}
	}
}

func TestRead_IgnoresASymlinkedOrOversizedRecord(t *testing.T) {
	good := `{"app_id":"a","source_id":"s"}`
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(t.TempDir(), "real.json")
	if err := os.WriteFile(real, []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, real, Path(dir))
	if Read(dir) != nil {
		t.Error("a symlinked record must read as nil")
	}

	big := t.TempDir()
	if err := os.MkdirAll(filepath.Join(big, Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	padded := good[:len(good)-1] + `,"pad":"` + strings.Repeat("x", 2*maxRecordBytes) + `"}`
	if err := os.WriteFile(Path(big), []byte(padded), 0o644); err != nil {
		t.Fatal(err)
	}
	if Read(big) != nil {
		t.Error("an oversized record must read as nil")
	}
}
