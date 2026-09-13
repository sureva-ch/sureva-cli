// Command sureva is the agent-first CLI for the Sureva cloud platform.
//
// It emits JSON to stdout by default, except commands with documented visual
// defaults such as `changes`; JSON error envelopes always go to stderr.
// All exit codes are defined in internal/output/exit.go.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/sureva-ch/sureva-cli/internal/cli"
	"github.com/sureva-ch/sureva-cli/internal/output"
)

func main() {
	root := cli.NewRootCmd()
	if err := root.Execute(); err != nil {
		// Commands that set a specific exit code return *cli.ExitError and have
		// already written the error envelope to stderr via the Renderer.
		var ee *cli.ExitError
		if errors.As(err, &ee) {
			os.Exit(ee.Code)
		}
		// Fallback for unhandled errors (should not normally occur).
		fmt.Fprintf(os.Stderr, `{"error":%q,"code":"general_error"}`+"\n", err.Error())
		os.Exit(output.ExitGeneral)
	}
}
