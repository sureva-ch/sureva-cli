// Package credentials implements the credential resolution chain for sureva CLI.
//
// Resolution order (first hit wins):
//  1. SUREVA_TOKEN environment variable — primary path; required for CI/agents.
//  2. Config file token field (~/.config/sureva/config.yaml on Linux/macOS,
//     %APPDATA%\sureva\config.yaml on Windows).
//
// OS keychain support is intentionally deferred to the human login slice (device flow).
// CI users MUST use SUREVA_TOKEN. This is documented in README.md.
package credentials

import (
	"errors"
)

// ErrNoCredentials is returned when no token can be found via any resolution path.
var ErrNoCredentials = errors.New("no credentials found: run 'sureva login', set SUREVA_TOKEN, or import an existing PAT with 'sureva auth login --token-stdin'")

// Resolve returns the API token using the default resolution chain.
// It is the entry point for all CLI commands that need authentication.
func Resolve() (string, error) {
	return resolveWithConfig(DefaultConfigPath())
}

// resolveWithConfig is the testable core of Resolve. It accepts an explicit
// config file path so tests can use t.TempDir() instead of the real config dir.
func resolveWithConfig(configPath string) (string, error) {
	// Step 1: SUREVA_TOKEN environment variable.
	if token := TokenFromEnvironment(); token != "" {
		return token, nil
	}

	// Step 2: Config file token field.
	v, err := loadConfig(configPath)
	if err != nil {
		return "", err
	}
	if token := v.GetString("token"); token != "" {
		return token, nil
	}

	// No credential found via any path.
	return "", ErrNoCredentials
}
