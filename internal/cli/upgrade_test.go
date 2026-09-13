package cli

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeVersion(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"strips leading v", "v0.2.1", "0.2.1"},
		{"already bare", "0.2.1", "0.2.1"},
		{"trims whitespace", "  v1.0.0\n", "1.0.0"},
		{"dev passthrough", "dev", "dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeVersion(tt.in); got != tt.want {
				t.Fatalf("normalizeVersion(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want int
	}{
		{"equal", "0.2.1", "0.2.1", 0},
		{"patch newer", "0.2.2", "0.2.1", 1},
		{"patch older", "0.2.1", "0.2.2", -1},
		{"minor newer", "0.3.0", "0.2.9", 1},
		{"major older", "1.0.0", "2.0.0", -1},
		{"missing patch equals zero", "1.2", "1.2.0", 0},
		{"prerelease suffix ignored", "0.2.1-rc1", "0.2.1", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := compareVersions(tt.a, tt.b); got != tt.want {
				t.Fatalf("compareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestAssetName(t *testing.T) {
	tests := []struct {
		name         string
		version      string
		goos, goarch string
		want         string
	}{
		{"darwin arm64 tarball", "0.2.1", "darwin", "arm64", "sureva_0.2.1_darwin_arm64.tar.gz"},
		{"linux amd64 tarball", "0.2.1", "linux", "amd64", "sureva_0.2.1_linux_amd64.tar.gz"},
		{"windows amd64 zip", "0.2.1", "windows", "amd64", "sureva_0.2.1_windows_amd64.zip"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := assetName(tt.version, tt.goos, tt.goarch); got != tt.want {
				t.Fatalf("assetName = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestChecksumFor(t *testing.T) {
	sums := "abc123  sureva_0.2.1_darwin_arm64.tar.gz\n" +
		"def456  sureva_0.2.1_linux_amd64.tar.gz\n"

	t.Run("found", func(t *testing.T) {
		got, err := checksumFor(sums, "sureva_0.2.1_linux_amd64.tar.gz")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "def456" {
			t.Fatalf("got %q, want def456", got)
		}
	})

	t.Run("missing entry errors", func(t *testing.T) {
		if _, err := checksumFor(sums, "sureva_9.9.9_linux_amd64.tar.gz"); err == nil {
			t.Fatal("expected error for missing asset, got nil")
		}
	})
}

func TestIsHomebrewManaged(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"caskroom", "/opt/homebrew/Caskroom/sureva/0.2.1/sureva", true},
		{"cellar", "/usr/local/Cellar/sureva/0.2.1/bin/sureva", true},
		{"standalone usr local bin", "/usr/local/bin/sureva", false},
		{"home bin", "/home/dev/.local/bin/sureva", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isHomebrewManaged(tt.path); got != tt.want {
				t.Fatalf("isHomebrewManaged(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestExtractBinaryTarGz(t *testing.T) {
	want := []byte("fake-binary-contents")

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	// A decoy file plus the real binary, to prove name matching works.
	writeTarFile(t, tw, "README.md", []byte("docs"))
	writeTarFile(t, tw, binaryName, want)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := extractBinary(buf.Bytes(), "linux")
	if err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("extracted %q, want %q", got, want)
	}
}

func TestExtractBinaryTarGzMissing(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	writeTarFile(t, tw, "README.md", []byte("docs"))
	_ = tw.Close()
	_ = gz.Close()

	if _, err := extractBinary(buf.Bytes(), "linux"); err == nil {
		t.Fatal("expected error when binary absent, got nil")
	}
}

func TestExtractBinaryZip(t *testing.T) {
	want := []byte("fake-windows-binary")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(binaryName + ".exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(want); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := extractBinary(buf.Bytes(), "windows")
	if err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("extracted %q, want %q", got, want)
	}
}

func TestReplaceBinary(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "sureva")

	if err := replaceBinary(dst, []byte("v1")); err != nil {
		t.Fatalf("first install: %v", err)
	}
	assertFileContents(t, dst, "v1")

	// Replacing an existing binary must overwrite it in place.
	if err := replaceBinary(dst, []byte("v2")); err != nil {
		t.Fatalf("replace: %v", err)
	}
	assertFileContents(t, dst, "v2")

	// No leftover temp files should remain in the directory.
	entries, err := filepath.Glob(filepath.Join(dir, ".sureva-upgrade-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temp files left behind: %v", entries)
	}
}

func writeTarFile(t *testing.T, tw *tar.Writer, name string, data []byte) {
	t.Helper()
	hdr := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(data))}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatal(err)
	}
}

func assertFileContents(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Fatalf("contents = %q, want %q", got, want)
	}
}
