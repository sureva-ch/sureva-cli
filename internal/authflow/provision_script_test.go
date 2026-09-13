package authflow

import (
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// provisionScript registers the Cognito app client's callback URLs. Cognito
// matches redirect_uri as an exact string, so the script must register the
// URL Run builds for every port in DefaultPorts, and nothing else.
const provisionScript = "../../scripts/provision-cognito-cli-client.sh"

var (
	callbacksArrayRE = regexp.MustCompile(`(?s)\bCALLBACKS=\((.*?)\)`)
	quotedEntryRE    = regexp.MustCompile(`"([^"]*)"`)
)

// checkRegisteredCallbacks reports any drift between the CALLBACKS array of a
// provisioning script and the redirect URIs the CLI sends for ports.
func checkRegisteredCallbacks(script string, ports []int) error {
	m := callbacksArrayRE.FindStringSubmatch(script)
	if m == nil {
		return fmt.Errorf("no CALLBACKS array found")
	}
	registered := map[string]bool{}
	for _, entry := range quotedEntryRE.FindAllStringSubmatch(m[1], -1) {
		registered[entry[1]] = true
	}
	var missing []string
	for _, port := range ports {
		uri := callbackURL(port)
		if !registered[uri] {
			missing = append(missing, uri)
		}
		delete(registered, uri)
	}
	unexpected := slices.Sorted(maps.Keys(registered))
	if len(missing) > 0 || len(unexpected) > 0 {
		return fmt.Errorf("missing %q, unexpected %q", missing, unexpected)
	}
	return nil
}

// TestProvisionScript_RegistersEveryDefaultPort is the drift guard for the
// 2026-08-24 outage: the app clients were recreated with a callback the CLI
// never sends, and every login failed with redirect_mismatch.
func TestProvisionScript_RegistersEveryDefaultPort(t *testing.T) {
	data, err := os.ReadFile(provisionScript)
	if err != nil {
		t.Fatalf("read provisioning script: %v", err)
	}
	if err := checkRegisteredCallbacks(string(data), DefaultPorts); err != nil {
		t.Errorf("%s callbacks drifted from authflow.DefaultPorts: %v", provisionScript, err)
	}
}

// TestCheckRegisteredCallbacks proves the drift guard fails on the drift it
// exists to catch, rather than passing vacuously.
func TestCheckRegisteredCallbacks(t *testing.T) {
	ports := []int{8976, 8977}
	script := func(entries ...string) string {
		return "readonly CALLBACKS=(\n  \"" + strings.Join(entries, "\"\n  \"") + "\"\n)\n"
	}

	tests := []struct {
		name    string
		script  string
		wantErr bool
	}{
		{
			name:   "every port registered on the loopback address",
			script: script("http://127.0.0.1:8976/callback", "http://127.0.0.1:8977/callback"),
		},
		{
			name:    "localhost registered instead of the loopback address",
			script:  script("http://localhost:8976/callback", "http://localhost:8977/callback"),
			wantErr: true,
		},
		{
			name:    "a port missing",
			script:  script("http://127.0.0.1:8976/callback"),
			wantErr: true,
		},
		{
			name:    "an extra callback the CLI never sends",
			script:  script("http://127.0.0.1:8976/callback", "http://127.0.0.1:8977/callback", "http://localhost:8976/callback"),
			wantErr: true,
		},
		{
			name:    "no CALLBACKS array",
			script:  "#!/usr/bin/env bash\n",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkRegisteredCallbacks(tt.script, ports)
			if (err != nil) != tt.wantErr {
				t.Errorf("checkRegisteredCallbacks() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
