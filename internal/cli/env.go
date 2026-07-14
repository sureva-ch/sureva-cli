package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/sureva-ch/sureva-cli/internal/output"
)

// envEntry is the CLI representation of a single environment variable.
// Exported for test assertions.
type envEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// NewEnvCmd returns the `env` command group.
func NewEnvCmd() *cobra.Command {
	env := &cobra.Command{
		Use:   "env",
		Short: "Manage application environment variables",
		Long: `Commands for reading and writing application environment variables.

SECURITY
  Values are masked (***) by default. Use --reveal to print plaintext values.
  env set replaces ALL env vars (PUT semantics) — omitted keys are deleted.

AGENT USAGE
  sureva env get <app-id> --org <slug> | jq '.[] | select(.key=="API_KEY")'
  printf 'API_KEY=...\n' | sureva env set <app-id> --org <slug> --from-stdin`,
	}
	env.AddCommand(newEnvGetCmd())
	env.AddCommand(newEnvSetCmd())
	return env
}

// newEnvGetCmd returns `env get <app-id>`.
// Values are masked by default; --reveal shows plaintext.
func newEnvGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <app-id>",
		Short: "Get environment variables for an app (values masked by default)",
		Long: `Get environment variables for an application.

VALIDATION / INPUTS
  <app-id>: application ID returned by apps list/create.
  --org: required organization slug unless a default org is configured.
  --reveal: prints plaintext secret values; omitted values are masked as ***.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reveal, _ := cmd.Flags().GetBool("reveal")

			c, r, err := newAuthenticatedClient(cmd)
			if err != nil {
				return err
			}

			orgID, err := requireOrgID(cmd.Context(), cmd, c, r)
			if err != nil {
				return err
			}

			env, err := c.GetEnv(cmd.Context(), orgID, args[0])
			if err != nil {
				return handleAPIError(r, err)
			}

			entries := make([]envEntry, 0, len(env))
			for k, v := range env {
				val := "***"
				if reveal {
					val = v
				}
				entries = append(entries, envEntry{Key: k, Value: val})
			}
			sort.Slice(entries, func(i, j int) bool {
				return entries[i].Key < entries[j].Key
			})

			if err := r.Render(entries); err != nil {
				return &ExitError{Code: output.ExitGeneral}
			}
			return nil
		},
	}
	cmd.Flags().Bool("reveal", false, "Show plaintext env values; default masks values as ***")
	return cmd
}

// newEnvSetCmd returns `env set <app-id> KEY=VALUE...`.
// This is a full replacement (PUT): omitted keys are deleted.
func newEnvSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <app-id> KEY=VALUE [KEY=VALUE...]",
		Short: "Set environment variables for an app (full replace — omitted keys are deleted)",
		Long: `Set application environment variables with full replacement semantics.

SECURITY
  Prefer --from-stdin or --from-file for secret values. Passing KEY=VALUE as
  command arguments can expose secrets in shell history and process listings.

VALIDATION / INPUTS
  <app-id>: application ID returned by apps list/create.
  KEY=VALUE: one or more entries; key must not be empty and value may be empty.
  --from-file/--from-stdin: one KEY=VALUE entry per line; blank lines and lines
                            starting with # are ignored.
  Input sources: use exactly one of KEY=VALUE arguments, --from-file, or
                 --from-stdin.
  Semantics: full replacement; omitted keys are deleted.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appID := args[0]
			pairs := args[1:]
			fromFile, _ := cmd.Flags().GetString("from-file")
			fromStdin, _ := cmd.Flags().GetBool("from-stdin")

			vars, parseErr := envVarsFromInput(cmd, pairs, fromFile, fromStdin)
			if parseErr != nil {
				r := output.NewRenderer(OutputFormat(cmd), cmd.OutOrStdout(), cmd.ErrOrStderr())
				r.RenderError(parseErr.Error(), "validation_error", -1)
				return &ExitError{Code: output.ExitValidation}
			}

			c, r, err := newAuthenticatedClient(cmd)
			if err != nil {
				return err
			}

			orgID, err := requireOrgID(cmd.Context(), cmd, c, r)
			if err != nil {
				return err
			}

			if err := c.SetEnv(cmd.Context(), orgID, appID, vars); err != nil {
				return handleAPIError(r, err)
			}

			if err := r.Render(map[string]string{"message": "env updated"}); err != nil {
				return &ExitError{Code: output.ExitGeneral}
			}
			return nil
		},
	}
	cmd.Flags().String("from-file", "", "Read KEY=VALUE entries from a file, one per line; exclusive with args and --from-stdin")
	cmd.Flags().Bool("from-stdin", false, "Read KEY=VALUE entries from stdin, one per line; exclusive with args and --from-file")
	return cmd
}

func envVarsFromInput(cmd *cobra.Command, pairs []string, fromFile string, fromStdin bool) (map[string]string, error) {
	sources := 0
	if len(pairs) > 0 {
		sources++
	}
	if fromFile != "" {
		sources++
	}
	if fromStdin {
		sources++
	}
	if sources == 0 {
		return nil, fmt.Errorf("provide KEY=VALUE arguments, --from-file, or --from-stdin")
	}
	if sources > 1 {
		return nil, fmt.Errorf("use only one env input source: KEY=VALUE arguments, --from-file, or --from-stdin")
	}

	switch {
	case len(pairs) > 0:
		return parseEnvPairs(pairs)
	case fromFile != "":
		f, err := os.Open(fromFile)
		if err != nil {
			return nil, fmt.Errorf("read --from-file: %w", err)
		}
		vars, parseErr := parseEnvLines(f)
		closeErr := f.Close()
		if parseErr != nil {
			return nil, parseErr
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close --from-file: %w", closeErr)
		}
		return vars, nil
	case fromStdin:
		return parseEnvLines(cmd.InOrStdin())
	default:
		return nil, fmt.Errorf("provide KEY=VALUE arguments, --from-file, or --from-stdin")
	}
}

func parseEnvLines(r io.Reader) (map[string]string, error) {
	scanner := bufio.NewScanner(r)
	pairs := make([]string, 0)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		pairs = append(pairs, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read env input: %w", err)
	}
	return parseEnvPairs(pairs)
}

func parseEnvPairs(pairs []string) (map[string]string, error) {
	vars := make(map[string]string, len(pairs))
	for _, kv := range pairs {
		idx := strings.IndexByte(kv, '=')
		if idx < 0 {
			return nil, fmt.Errorf("invalid argument %q: expected KEY=VALUE format", kv)
		}
		key := kv[:idx]
		if key == "" {
			return nil, fmt.Errorf("invalid argument %q: key must not be empty", kv)
		}
		vars[key] = kv[idx+1:]
	}
	if len(vars) == 0 {
		return nil, fmt.Errorf("env input is empty")
	}
	return vars, nil
}
