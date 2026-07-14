package credentials

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolve_EnvironmentTakesPriority(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	writeYAML(t, cfgPath, map[string]any{"token": "file-token"})
	t.Setenv("SUREVA_TOKEN", "env-token")
	token, err := resolveWithConfig(cfgPath)
	if err != nil {
		t.Fatalf("resolveWithConfig: %v", err)
	}
	if token != "env-token" {
		t.Fatalf("token = %q, want env-token", token)
	}
}

func TestResolve_ConfigFileToken(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	writeYAML(t, cfgPath, map[string]any{"token": "file-token"})
	t.Setenv("SUREVA_TOKEN", "")
	token, err := resolveWithConfig(cfgPath)
	if err != nil {
		t.Fatalf("resolveWithConfig: %v", err)
	}
	if token != "file-token" {
		t.Fatalf("token = %q, want file-token", token)
	}
}

func TestResolve_NoCredentials(t *testing.T) {
	t.Setenv("SUREVA_TOKEN", "")
	if _, err := resolveWithConfig(filepath.Join(t.TempDir(), "missing.yaml")); err != ErrNoCredentials {
		t.Fatalf("err = %v, want ErrNoCredentials", err)
	}
}

func TestSaveTokenCreatesFileWith0600(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := SaveToken(cfgPath, "sapi_abc123"); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}
	info, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("file mode = %04o, want 0600", got)
	}
}

func TestDefaultConfigPath_UsesSurevaDirectory(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if got := DefaultConfigPath(); !strings.Contains(got, filepath.Join("sureva", "config.yaml")) {
		t.Fatalf("DefaultConfigPath = %q, want sureva/config.yaml", got)
	}
}

func TestAPIBaseURL(t *testing.T) {
	t.Run("production default", func(t *testing.T) {
		t.Setenv("SUREVA_API_URL", "")
		if got := apiBaseURLWithConfig(filepath.Join(t.TempDir(), "missing.yaml")); got != "https://api.sureva.com" {
			t.Fatalf("API base URL = %q", got)
		}
	})
	t.Run("environment override", func(t *testing.T) {
		t.Setenv("SUREVA_API_URL", "http://localhost:8080")
		if got := apiBaseURLWithConfig(filepath.Join(t.TempDir(), "missing.yaml")); got != "http://localhost:8080" {
			t.Fatalf("API base URL = %q", got)
		}
	})
}

func writeYAML(t *testing.T, path string, data map[string]any) {
	t.Helper()
	var content strings.Builder
	for key, value := range data {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		content.WriteString(key)
		content.WriteString(": ")
		content.Write(encoded)
		content.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(content.String()), 0600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
}
