package credentials

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveTokenPreservesExistingConfigAndSecuresFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	original := "api_url: https://api.example.com\norg: existing-org\nnested:\n  enabled: true\ntoken: old-token\n"
	if err := os.WriteFile(cfgPath, []byte(original), 0644); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	if err := SaveToken(cfgPath, "new-token"); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	v, err := loadConfig(cfgPath)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if got := v.GetString("token"); got != "new-token" {
		t.Errorf("token = %q, want new-token", got)
	}
	if got := v.GetString("api_url"); got != "https://api.example.com" {
		t.Errorf("api_url = %q, want preserved value", got)
	}
	if got := v.GetString("org"); got != "existing-org" {
		t.Errorf("org = %q, want existing-org", got)
	}
	if !v.GetBool("nested.enabled") {
		t.Error("nested.enabled was not preserved")
	}
	info, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Errorf("config mode = %04o, want 0600", got)
	}
}

func TestSaveTokenRejectsCorruptConfigWithoutChangingIt(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	original := []byte("token: old-token\ninvalid: [unterminated\n")
	if err := os.WriteFile(cfgPath, original, 0600); err != nil {
		t.Fatalf("seed corrupt config: %v", err)
	}

	if err := SaveToken(cfgPath, "new-token"); err == nil {
		t.Fatal("SaveToken succeeded with corrupt config, want error")
	}
	got, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config after failure: %v", err)
	}
	if string(got) != string(original) {
		t.Fatalf("corrupt config changed after failure:\ngot:  %q\nwant: %q", got, original)
	}
}

func TestSaveTokenRejectsUnreadableConfigPath(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.Mkdir(cfgPath, 0700); err != nil {
		t.Fatalf("create directory at config path: %v", err)
	}
	if err := SaveToken(cfgPath, "new-token"); err == nil {
		t.Fatal("SaveToken succeeded when config path is unreadable, want error")
	}
	info, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("stat config path after failure: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("config path was replaced after read failure")
	}
}

func TestDomainSuffixFromPath(t *testing.T) {
	t.Run("environment wins", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.yaml")
		writeYAML(t, cfgPath, map[string]any{"domain_suffix": "config.example.com"})
		t.Setenv("SUREVA_DOMAIN_SUFFIX", "env.example.com")
		if got := DomainSuffixFromPath(cfgPath); got != "env.example.com" {
			t.Fatalf("suffix = %q, want env.example.com", got)
		}
	})

	t.Run("config is used", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.yaml")
		writeYAML(t, cfgPath, map[string]any{"domain_suffix": "apps.example.com"})
		t.Setenv("SUREVA_DOMAIN_SUFFIX", "")
		if got := DomainSuffixFromPath(cfgPath); got != "apps.example.com" {
			t.Fatalf("suffix = %q, want apps.example.com", got)
		}
	})

	t.Run("unset has no invented default", func(t *testing.T) {
		t.Setenv("SUREVA_DOMAIN_SUFFIX", "")
		if got := DomainSuffixFromPath(filepath.Join(t.TempDir(), "missing.yaml")); got != "" {
			t.Fatalf("suffix = %q, want empty", got)
		}
	})
}

func TestCognitoDomainFromPath(t *testing.T) {
	t.Run("environment wins and bare domains are normalized", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		writeYAML(t, path, map[string]any{"cognito_domain": "config.example.com"})
		t.Setenv("SUREVA_COGNITO_DOMAIN", "env.example.com/")
		if got := CognitoDomainFromPath(path); got != "https://env.example.com" {
			t.Fatalf("domain = %q, want https://env.example.com", got)
		}
	})

	t.Run("config supports local http", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		writeYAML(t, path, map[string]any{"cognito_domain": "http://127.0.0.1:9229/"})
		t.Setenv("SUREVA_COGNITO_DOMAIN", "")
		if got := CognitoDomainFromPath(path); got != "http://127.0.0.1:9229" {
			t.Fatalf("domain = %q, want local HTTP domain", got)
		}
	})

	t.Run("production default", func(t *testing.T) {
		t.Setenv("SUREVA_COGNITO_DOMAIN", "")
		if got := CognitoDomainFromPath(filepath.Join(t.TempDir(), "missing.yaml")); got != "https://auth.sureva.com" {
			t.Fatalf("domain = %q, want production default", got)
		}
	})
}

func TestCognitoClientIDFromPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeYAML(t, path, map[string]any{"cognito_client_id": "config-client"})
	t.Setenv("SUREVA_COGNITO_CLIENT_ID", " env-client ")
	if got := CognitoClientIDFromPath(path); got != "env-client" {
		t.Fatalf("client ID = %q, want env-client", got)
	}

	t.Setenv("SUREVA_COGNITO_CLIENT_ID", "")
	if got := CognitoClientIDFromPath(path); got != "config-client" {
		t.Fatalf("client ID = %q, want config-client", got)
	}
}
