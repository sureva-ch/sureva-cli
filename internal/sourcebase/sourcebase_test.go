package sourcebase

import (
	"os"
	"path/filepath"
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
