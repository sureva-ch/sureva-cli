package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"
	"github.com/sureva-ch/sureva-cli/internal/changes"
	"github.com/sureva-ch/sureva-cli/internal/output"
)

type changesBuilder interface {
	Build(ctx context.Context, base string) (changes.Graph, error)
	BuildRelease(ctx context.Context, release string) (changes.Graph, error)
}

type changesOpener interface {
	Open(ctx context.Context, path string) error
}

// NewChangesCmd returns the `sureva changes` command.
func NewChangesCmd() *cobra.Command {
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	repoRoot := wd
	if root, err := changes.DiscoverRepoRoot(context.Background(), wd, nil); err == nil {
		repoRoot = root
	}
	builder := changes.Builder{RepoRoot: repoRoot}
	return newChangesCmdWithOpener(builder, commandChangesOpener{})
}

func newChangesCmd(builder changesBuilder) *cobra.Command {
	return newChangesCmdWithOpener(builder, commandChangesOpener{})
}

func newChangesCmdWithOpener(builder changesBuilder, opener changesOpener) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "changes",
		Short: "Print a graph of branch-changed files and internal Go imports",
		Long: `Print a graph of files changed on the current branch relative to a base
branch, plus Go internal import edges from changed Go files.

OUTPUT
  Default output opens a local interactive HTML graph.
  Use --output json for JSON or --output table for a deterministic text graph.

VALIDATION / INPUTS
  No positional arguments.
  --base: branch to compare against. Defaults to main.
  --release: a tag (e.g. v0.5.0). Graphs the range from the previous tag to it
    and adds a changelog grouped by conventional-commit type. Overrides --base.
  Changed nodes are repo-relative paths.
  Edges are formatted as: path/a.go -> path/b.go.
  First implementation resolves Go imports under this module's internal/ tree.
  Changed files include committed changes since the base branch plus the current
  working tree (staged, unstaged, and untracked), so uncommitted edits show up.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			base, _ := cmd.Flags().GetString("base")
			release, _ := cmd.Flags().GetString("release")
			var graph changes.Graph
			var err error
			if release != "" {
				graph, err = builder.BuildRelease(cmd.Context(), release)
			} else {
				graph, err = builder.Build(cmd.Context(), base)
			}
			if err != nil {
				r := output.NewRenderer(OutputFormat(cmd), cmd.OutOrStdout(), cmd.ErrOrStderr())
				code := r.RenderError(fmt.Sprintf("could not build changes graph: %v", err), "general_error", 500)
				return &ExitError{Code: code}
			}
			graph = normalizeChangesGraph(graph)
			if !changesOutputExplicitlySet(cmd) {
				path, err := writeChangesHTML(graph)
				if err != nil {
					r := output.NewRenderer(output.FormatJSON, cmd.OutOrStdout(), cmd.ErrOrStderr())
					code := r.RenderError(fmt.Sprintf("could not write changes graph: %v", err), "general_error", 500)
					return &ExitError{Code: code}
				}
				if err := opener.Open(cmd.Context(), path); err != nil {
					r := output.NewRenderer(output.FormatJSON, cmd.OutOrStdout(), cmd.ErrOrStderr())
					code := r.RenderError(fmt.Sprintf("could not open changes graph: %v", err), "general_error", 500)
					return &ExitError{Code: code}
				}
				if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "Changes graph written and opened: %s\n", path); err != nil {
					return &ExitError{Code: output.ExitGeneral}
				}
				return nil
			}
			format := OutputFormat(cmd)
			if format == output.FormatTable {
				if err := renderChangesGraph(cmd.OutOrStdout(), graph); err != nil {
					return &ExitError{Code: output.ExitGeneral}
				}
				return nil
			}
			r := output.NewRenderer(format, cmd.OutOrStdout(), cmd.ErrOrStderr())
			if err := r.Render(graph); err != nil {
				return &ExitError{Code: output.ExitGeneral}
			}
			return nil
		},
	}
	cmd.Flags().String("base", "main", "Base branch to compare against")
	cmd.Flags().String("release", "", "Graph and changelog for a release tag, compared against the previous tag (e.g. v0.5.0)")
	return cmd
}

func changesOutputExplicitlySet(cmd *cobra.Command) bool {
	if flag := cmd.Flag("output"); flag != nil {
		return flag.Changed
	}
	if root := cmd.Root(); root != nil {
		if flag := root.PersistentFlags().Lookup("output"); flag != nil {
			return flag.Changed
		}
	}
	return false
}

func writeChangesHTML(graph changes.Graph) (string, error) {
	file, err := os.CreateTemp("", "sureva-changes-*.html")
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := changes.WriteHTMLGraph(file, graph); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return path, nil
}

type commandChangesOpener struct{}

func (commandChangesOpener) Open(ctx context.Context, path string) error {
	name, args := openCommand(path)
	return exec.CommandContext(ctx, name, args...).Start()
}

func openCommand(path string) (string, []string) {
	switch runtime.GOOS {
	case "darwin":
		return "open", []string{path}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", path}
	default:
		return "xdg-open", []string{path}
	}
}

func normalizeChangesGraph(graph changes.Graph) changes.Graph {
	if graph.Nodes == nil {
		graph.Nodes = []string{}
	}
	if graph.Edges == nil {
		graph.Edges = []changes.Edge{}
	}
	return graph
}

func renderChangesGraph(w io.Writer, graph changes.Graph) error {
	header := fmt.Sprintf("Changes graph (base: %s)", graph.Base)
	if graph.Repository != "" {
		header = fmt.Sprintf("Changes graph for %s (base: %s)", graph.Repository, graph.Base)
	}
	if _, err := fmt.Fprintln(w, header); err != nil {
		return err
	}
	if len(graph.Checklist) > 0 {
		if _, err := fmt.Fprintln(w, "Checklist:"); err != nil {
			return err
		}
		for _, item := range graph.Checklist {
			mark := " "
			if item.OK {
				mark = "x"
			}
			if _, err := fmt.Fprintf(w, "  [%s] %s\n", mark, item.Label); err != nil {
				return err
			}
		}
	}
	if _, err := fmt.Fprintln(w, "Nodes:"); err != nil {
		return err
	}
	if len(graph.Nodes) == 0 {
		if _, err := fmt.Fprintln(w, "  (none)"); err != nil {
			return err
		}
	} else {
		for _, node := range graph.Nodes {
			if _, err := fmt.Fprintf(w, "  %s\n", filepath.ToSlash(node)); err != nil {
				return err
			}
		}
	}
	if _, err := fmt.Fprintln(w, "Edges:"); err != nil {
		return err
	}
	if len(graph.Edges) == 0 {
		_, err := fmt.Fprintln(w, "  (none)")
		return err
	}
	for _, edge := range graph.Edges {
		if _, err := fmt.Fprintf(w, "  %s -> %s\n", filepath.ToSlash(edge.From), filepath.ToSlash(edge.To)); err != nil {
			return err
		}
	}
	return nil
}
