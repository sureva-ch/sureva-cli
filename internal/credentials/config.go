package credentials

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// DefaultAPIBaseURL is the production API endpoint.
// Override with SUREVA_API_URL for local development or testing. Client routes
// add the /v1 prefix to this origin.
const DefaultAPIBaseURL = "https://api.sureva.com"

// DefaultCognitoDomain is the production Managed Login domain used by
// `sureva login`. It may be overridden for development and tests.
var DefaultCognitoDomain = "https://auth.sureva.com"

// DefaultCognitoClientID is the public (secretless) Cognito app client ID.
// Release builds inject it with -ldflags; source builds may use the env or
// config-file overrides below.
var DefaultCognitoClientID string

// DomainSuffixFromPath returns the domain suffix to use for app URL composition.
// Precedence: SUREVA_DOMAIN_SUFFIX env var → config "domain_suffix" key.
// It returns an empty string when no suffix is configured because the CLI has
// no verified production app-domain default to assume.
// Uses the same resolution pattern as DefaultOrgSlugFromPath and apiBaseURLWithConfig.
func DomainSuffixFromPath(configPath string) string {
	if s := os.Getenv("SUREVA_DOMAIN_SUFFIX"); s != "" {
		return s
	}
	v, err := loadConfig(configPath)
	if err != nil {
		return ""
	}
	if s := v.GetString("domain_suffix"); s != "" {
		return s
	}
	return ""
}

// DefaultConfigPath returns the OS-appropriate path for the CLI config file.
//
//   - Linux/macOS: $XDG_CONFIG_HOME/sureva/config.yaml (usually ~/.config/sureva/config.yaml)
//   - Windows:     %APPDATA%\sureva\config.yaml
func DefaultConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		// Fallback to home directory when UserConfigDir fails.
		dir, _ = os.UserHomeDir()
	}
	return filepath.Join(dir, "sureva", "config.yaml")
}

// TokenFromEnvironment returns the Sureva token from the environment.
func TokenFromEnvironment() string {
	return os.Getenv("SUREVA_TOKEN")
}

// loadConfig reads the YAML config file at path using viper and returns the
// resulting key-value map. It returns an empty map (not an error) when the
// file does not exist, so callers can still rely on defaults.
func loadConfig(path string) (*viper.Viper, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if errors.As(err, &notFound) || os.IsNotExist(err) {
			return v, nil
		}
		return nil, err
	}
	return v, nil
}

// SaveToken writes the given token into the config file at path under the
// "token" key. Existing configuration must be readable and valid: otherwise
// SaveToken fails without changing it. The replacement is written atomically
// with mode 0600 (owner read/write only).
func SaveToken(path, token string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}

	v, err := loadConfig(path)
	if err != nil {
		return fmt.Errorf("read existing config: %w", err)
	}

	v.Set("token", token)

	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.yaml")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(0600); err != nil {
		return fmt.Errorf("secure temporary config: %w", err)
	}
	if err := v.WriteConfigTo(tmp); err != nil {
		return fmt.Errorf("write temporary config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync temporary config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary config: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	committed = true
	return nil
}

// apiBaseURLWithConfig returns the API base URL to use.
// Precedence: SUREVA_API_URL env var → config file api_url → DefaultAPIBaseURL.
func apiBaseURLWithConfig(configPath string) string {
	if u := os.Getenv("SUREVA_API_URL"); u != "" {
		return u
	}
	v, err := loadConfig(configPath)
	if err != nil {
		return DefaultAPIBaseURL
	}
	if u := v.GetString("api_url"); u != "" {
		return u
	}
	return DefaultAPIBaseURL
}

// APIBaseURL returns the API base URL using the default config path.
func APIBaseURL() string {
	return apiBaseURLWithConfig(DefaultConfigPath())
}

// APIBaseURLFromPath returns the API base URL using the given config file path.
// Intended for tests and commands that honour the --config flag.
func APIBaseURLFromPath(configPath string) string {
	return apiBaseURLWithConfig(configPath)
}

// CognitoDomainFromPath returns the Managed Login domain. Precedence is
// SUREVA_COGNITO_DOMAIN, cognito_domain in config, then the production default.
func CognitoDomainFromPath(configPath string) string {
	if domain := strings.TrimSpace(os.Getenv("SUREVA_COGNITO_DOMAIN")); domain != "" {
		return normalizeCognitoDomain(domain)
	}
	v, err := loadConfig(configPath)
	if err == nil {
		if domain := strings.TrimSpace(v.GetString("cognito_domain")); domain != "" {
			return normalizeCognitoDomain(domain)
		}
	}
	return normalizeCognitoDomain(DefaultCognitoDomain)
}

// CognitoClientIDFromPath returns the public app client ID. Precedence is
// SUREVA_COGNITO_CLIENT_ID, cognito_client_id in config, then the build value.
func CognitoClientIDFromPath(configPath string) string {
	if clientID := strings.TrimSpace(os.Getenv("SUREVA_COGNITO_CLIENT_ID")); clientID != "" {
		return clientID
	}
	v, err := loadConfig(configPath)
	if err == nil {
		if clientID := strings.TrimSpace(v.GetString("cognito_client_id")); clientID != "" {
			return clientID
		}
	}
	return strings.TrimSpace(DefaultCognitoClientID)
}

func normalizeCognitoDomain(domain string) string {
	domain = strings.TrimRight(strings.TrimSpace(domain), "/")
	if strings.Contains(domain, "://") {
		return domain
	}
	return "https://" + domain
}

// ResolveWithConfigPath is the testable version of Resolve that uses an explicit
// config file path instead of DefaultConfigPath(). Use this in command code so
// the --config flag is respected.
func ResolveWithConfigPath(configPath string) (string, error) {
	return resolveWithConfig(configPath)
}

// DefaultOrgSlug returns the default org slug from the environment or config.
// Precedence: SUREVA_ORG env var → config file "org" key.
func DefaultOrgSlug() string {
	return DefaultOrgSlugFromPath(DefaultConfigPath())
}

// DefaultOrgSlugFromPath returns the default org slug using the given config path.
// Intended for commands that honour the --config flag.
func DefaultOrgSlugFromPath(configPath string) string {
	if o := os.Getenv("SUREVA_ORG"); o != "" {
		return o
	}
	v, err := loadConfig(configPath)
	if err != nil {
		return ""
	}
	return v.GetString("org")
}
