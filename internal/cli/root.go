// Package cli defines the Cobra command tree for the sureva CLI.
//
// AGENT DISCOVERABILITY NOTE
// This CLI is agent-first. JSON is the default output format, except
// `sureva changes`, which opens a visual graph unless --output is explicit.
// Run `sureva --help --json` to get the full command tree as a
// machine-readable JSON object. Agent/script paths should pass --output json.
// Commands emit JSON to stderr on error, making them composable with jq.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/sureva-ch/sureva-cli/internal/output"
	"github.com/sureva-ch/sureva-cli/internal/version"
)

// globalFormat holds the value of the --output flag across the command tree.
var globalFormat string

// NewRootCmd builds and returns the root Cobra command.
// The caller is responsible for calling Execute() and handling the returned exit code.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "sureva",
		Short: "Sureva CLI — manage apps, deployments, and secrets",
		Long: `sureva is the command-line interface for the Sureva cloud platform.

AGENT USAGE
  Default output is JSON. Exception: sureva changes opens a visual graph
  unless --output json or --output table is explicit.

  Commands print JSON error envelopes to stderr. Use --output json for scripts
  and --output table for human-readable output.

  Machine-readable command tree:
    sureva --help --json

EXIT CODES
  0  success
  1  general / API error
  2  auth error (401 / 403 / missing token)
  3  not found (404)
  4  validation / bad input (400 / 422)
  5  network error (no HTTP response)

AUTHENTICATION
  Run sureva login for interactive browser authentication.
  Set SUREVA_TOKEN to a personal access token (sapi_...) for CI and agents.
  sureva auth login imports and verifies an existing PAT as an advanced fallback.

GLOBAL FLAG VALIDATION
  --output: one of json|table. json is safest for automation and overrides
    the visual default of sureva changes.
  --org: organization slug used to resolve org-scoped API routes.
  --config: path to a sureva config YAML file.
  --json: only affects help output when combined with --help.`,

		// SilenceUsage prevents Cobra from printing usage on every error.
		SilenceUsage: true,
		// SilenceErrors lets main.go handle error printing as JSON to stderr.
		SilenceErrors: true,
	}

	// Global persistent flags available on all subcommands.
	root.PersistentFlags().StringVarP(&globalFormat, "output", "o", "json",
		`Output format: json|table; json is agent-friendly and overrides visual defaults`)
	root.PersistentFlags().String("org", "",
		"Organization slug; overrides config default for org-scoped commands")
	root.PersistentFlags().String("config", "",
		"Config YAML file path; defaults to $XDG_CONFIG_HOME/sureva/config.yaml")
	// --json persistent flag: used with --help to emit machine-readable command tree (spec B-09).
	root.PersistentFlags().Bool("json", false,
		"When used with --help, emit the command tree as machine-readable JSON")

	// Custom help function: intercepts --help --json to emit agent-readable tree (spec B-09a).
	// Checks the persistent --json flag binding first (works when --json precedes --help),
	// then falls back to scanning os.Args (covers the common --help --json order where cobra
	// triggers help before finishing flag parsing).
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		jsonFlag, _ := cmd.Root().PersistentFlags().GetBool("json")
		if jsonFlag || wantsJSONHelp() {
			emitHelpJSON(root, cmd.Root().OutOrStdout())
			return
		}
		// Brand banner on the root help only, and only on an interactive TTY
		// (printBanner self-gates). Subcommand help stays clean.
		if cmd == cmd.Root() {
			printBanner(cmd.OutOrStdout())
		}
		// Default help output. Cobra's Usage() template omits Long, so print it
		// explicitly before the generated usage/flags section. This keeps the
		// validation notes in each command's help visible to humans while preserving
		// the custom --help --json path for agents.
		if cmd.Long != "" {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), cmd.Long)
			_, _ = fmt.Fprintln(cmd.OutOrStdout())
		}
		if err := cmd.Usage(); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
		}
	})

	// Attach subcommands.
	root.AddCommand(newVersionCmd())
	root.AddCommand(NewLoginCmd())
	root.AddCommand(NewAuthCmd())
	root.AddCommand(NewOrgsCmd())
	root.AddCommand(NewTeamsCmd())
	root.AddCommand(NewAppsCmd())
	root.AddCommand(NewEnvCmd())
	root.AddCommand(NewServicesCmd())
	root.AddCommand(NewDeploysCmd())
	root.AddCommand(NewLogsCmd())
	root.AddCommand(NewChangesCmd())
	root.AddCommand(NewUpgradeCmd())

	// root RunE: --version emits JSON (spec B-10a); otherwise show help.
	root.Flags().BoolP("version", "v", false, "Print version as JSON and exit")
	root.RunE = func(cmd *cobra.Command, args []string) error {
		versionFlag, _ := cmd.Flags().GetBool("version")
		if versionFlag {
			return runVersionJSON(cmd.OutOrStdout())
		}
		return cmd.Help()
	}

	return root
}

// wantsJSONHelp returns true when the user passed --json (anywhere in os.Args).
// We check os.Args directly because cobra calls the help function before all
// flags are necessarily parsed into their targets.
func wantsJSONHelp() bool {
	for _, arg := range os.Args {
		if arg == "--json" {
			return true
		}
	}
	return false
}

// newVersionCmd returns the `sureva version` subcommand (spec B-10).
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the CLI version, commit SHA, and build timestamp as JSON",
		Long: `Print build metadata as JSON.

VALIDATION / INPUTS
  No positional arguments or command-specific flags.
  Output fields: version, commit, built_at.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runVersionJSON(cmd.OutOrStdout())
		},
	}
}

// runVersionJSON emits version info as JSON per spec B-10a.
// w receives the output — always use cmd.OutOrStdout() so tests can capture it.
func runVersionJSON(w io.Writer) error {
	out := map[string]string{
		"version":  version.Version,
		"commit":   version.Commit,
		"built_at": version.BuiltAt,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// commandJSON is the shape emitted by --help --json (spec B-09).
type commandJSON struct {
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Flags       []flagJSON    `json:"flags,omitempty"`
	Subcommands []commandJSON `json:"subcommands,omitempty"`
}

type flagJSON struct {
	Name        string `json:"name"`
	Shorthand   string `json:"shorthand,omitempty"`
	Description string `json:"description"`
	Default     string `json:"default,omitempty"`
}

// emitHelpJSON walks the cobra command tree from root and writes the
// machine-readable JSON representation to w (spec B-09a).
// Always call with cmd.Root().OutOrStdout() so tests can capture the output.
func emitHelpJSON(root *cobra.Command, w io.Writer) {
	tree := buildCommandJSON(root)
	payload := map[string]any{"commands": tree.Subcommands}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(payload)
}

func buildCommandJSON(cmd *cobra.Command) commandJSON {
	node := commandJSON{
		Name:        cmd.Name(),
		Description: cmd.Short,
	}

	seen := make(map[string]bool)
	appendFlag := func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		if seen[f.Name] {
			return
		}
		seen[f.Name] = true
		node.Flags = append(node.Flags, flagJSON{
			Name:        f.Name,
			Shorthand:   f.Shorthand,
			Description: f.Usage,
			Default:     f.DefValue,
		})
	}
	cmd.Flags().VisitAll(appendFlag)
	cmd.PersistentFlags().VisitAll(appendFlag)

	for _, sub := range cmd.Commands() {
		if !sub.Hidden {
			node.Subcommands = append(node.Subcommands, buildCommandJSON(sub))
		}
	}
	return node
}

// OutputFormat returns the resolved output.Format from the --output flag.
// Falls back to FormatJSON for any unrecognised value.
func OutputFormat(cmd *cobra.Command) output.Format {
	f, _ := cmd.Root().PersistentFlags().GetString("output")
	switch output.Format(f) {
	case output.FormatTable:
		return output.FormatTable
	default:
		return output.FormatJSON
	}
}

// WriteError writes a JSON error envelope to stderr and returns the exit code.
// Commands should use this instead of writing to stderr directly.
func WriteError(cmd *cobra.Command, message, code string, httpStatus int) int {
	r := output.Default(OutputFormat(cmd))
	return r.RenderError(message, code, httpStatus)
}

// Fatalf writes a JSON error to stderr and exits with the given code.
func Fatalf(cmd *cobra.Command, exitCode int, message, code string, httpStatus int) {
	WriteError(cmd, message, code, httpStatus)
	os.Exit(exitCode)
}
