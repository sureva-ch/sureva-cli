package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/sureva-ch/sureva-cli/internal/changes"
	"github.com/sureva-ch/sureva-cli/internal/output"
)

func changesExitCode(err error) int {
	if err == nil {
		return output.ExitOK
	}
	if exit, ok := err.(*ExitError); ok {
		return exit.Code
	}
	return output.ExitGeneral
}

func TestChangesCommandDefaultOutputOpensVisualGraph(t *testing.T) {
	builder := &fakeChangesBuilder{graph: changes.Graph{
		Repository: "sureva-cli",
		Base:       "main",
		Nodes:      []string{"README.md", "internal/cli/changes.go"},
		Edges:      []changes.Edge{{From: "internal/cli/changes.go", To: "internal/changes/graph.go"}},
	}}
	opener := &fakeChangesOpener{}
	cmd := newChangesCmdWithOpener(builder, opener)
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)

	err := cmd.Execute()
	if got := changesExitCode(err); got != output.ExitOK {
		t.Fatalf("exit code = %d, want 0; stderr: %s", got, errOut.String())
	}
	if builder.base != "main" {
		t.Fatalf("base = %q, want main", builder.base)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if opener.path == "" {
		t.Fatal("opener path is empty")
	}
	if !strings.HasPrefix(opener.path, os.TempDir()) {
		t.Fatalf("opener path = %q, want temp file path", opener.path)
	}
	if !strings.Contains(errOut.String(), "Changes graph written and opened: ") {
		t.Fatalf("stderr = %q, want visual graph message", errOut.String())
	}
	html, err := os.ReadFile(opener.path)
	if err != nil {
		t.Fatalf("read visual graph: %v", err)
	}
	if !strings.Contains(string(html), "sureva changes") {
		t.Fatalf("visual graph HTML missing title: %s", string(html))
	}
}

func TestChangesCommandExplicitJSONOutput(t *testing.T) {
	builder := &fakeChangesBuilder{graph: changes.Graph{
		Repository: "sureva-cli",
		Base:       "main",
		Nodes:      []string{"README.md", "internal/cli/changes.go"},
		Edges:      []changes.Edge{{From: "internal/cli/changes.go", To: "internal/changes/graph.go"}},
		Checklist: []changes.ChecklistItem{
			{Key: "migrations", Label: "Database migrations", OK: false},
		},
	}}
	opener := &fakeChangesOpener{}
	cmd := newChangesCmdWithOpener(builder, opener)
	cmd.PersistentFlags().String("output", "json", "Output format")
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--output", "json"})

	err := cmd.Execute()
	if got := changesExitCode(err); got != output.ExitOK {
		t.Fatalf("exit code = %d, want 0; stderr: %s", got, errOut.String())
	}
	if opener.path != "" {
		t.Fatalf("opener path = %q, want empty", opener.path)
	}
	var got changes.Graph
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", err, out.String())
	}
	if !reflect.DeepEqual(got, builder.graph) {
		t.Fatalf("stdout JSON = %#v, want %#v", got, builder.graph)
	}
}

func TestChangesCommandExplicitJSONThroughRoot(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "root flag before command",
			args: []string{"--output", "json", "changes"},
		},
		{
			name: "persistent flag after command",
			args: []string{"changes", "--output", "json"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			graph := changes.Graph{
				Base:  "main",
				Nodes: []string{"README.md", "internal/cli/changes.go"},
				Edges: []changes.Edge{{From: "internal/cli/changes.go", To: "internal/changes/graph.go"}},
			}
			builder := &fakeChangesBuilder{graph: graph}
			opener := &fakeChangesOpener{}
			cmd := newRootWithFakeChanges(builder, opener)
			var out, errOut bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			cmd.SetArgs(tt.args)

			err := cmd.Execute()
			if got := changesExitCode(err); got != output.ExitOK {
				t.Fatalf("exit code = %d, want 0; stderr: %s", got, errOut.String())
			}
			if opener.path != "" {
				t.Fatalf("opener path = %q, want empty", opener.path)
			}
			var got changes.Graph
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("stdout is not valid JSON: %v\noutput: %s", err, out.String())
			}
			if !reflect.DeepEqual(got, graph) {
				t.Fatalf("stdout JSON = %#v, want %#v", got, graph)
			}
			if errOut.Len() != 0 {
				t.Fatalf("stderr = %q, want empty", errOut.String())
			}
		})
	}
}

func TestChangesCommandJSONUsesEmptyArrays(t *testing.T) {
	builder := &fakeChangesBuilder{graph: changes.Graph{Base: "main"}}
	cmd := newChangesCmd(builder)
	cmd.PersistentFlags().String("output", "json", "Output format")
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--output", "json"})

	err := cmd.Execute()
	if got := changesExitCode(err); got != output.ExitOK {
		t.Fatalf("exit code = %d, want 0; stderr: %s", got, errOut.String())
	}
	if !strings.Contains(out.String(), `"nodes": []`) {
		t.Fatalf("stdout should render empty nodes as JSON array: %s", out.String())
	}
	if !strings.Contains(out.String(), `"edges": []`) {
		t.Fatalf("stdout should render empty edges as JSON array: %s", out.String())
	}
}

func TestChangesCommandTableOutput(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		graph    changes.Graph
		wantBase string
		wantOut  string
	}{
		{
			name: "default base renders nodes and edges",
			args: []string{"--output", "table"},
			graph: changes.Graph{
				Base:  "main",
				Nodes: []string{"README.md", "internal/cli/changes.go"},
				Edges: []changes.Edge{{From: "internal/cli/changes.go", To: "internal/changes/graph.go"}},
			},
			wantBase: "main",
			wantOut: strings.Join([]string{
				"Changes graph (base: main)",
				"Nodes:",
				"  README.md",
				"  internal/cli/changes.go",
				"Edges:",
				"  internal/cli/changes.go -> internal/changes/graph.go",
				"",
			}, "\n"),
		},
		{
			name: "repository and checklist render in table",
			args: []string{"--output", "table"},
			graph: changes.Graph{
				Repository: "sureva-cli",
				Base:       "main",
				Nodes:      []string{"db/migrations/20260701120000_create_users.sql"},
				Checklist: []changes.ChecklistItem{
					{Key: "migrations", Label: "Database migrations", OK: true},
				},
			},
			wantBase: "main",
			wantOut: strings.Join([]string{
				"Changes graph for sureva-cli (base: main)",
				"Checklist:",
				"  [x] Database migrations",
				"Nodes:",
				"  db/migrations/20260701120000_create_users.sql",
				"Edges:",
				"  (none)",
				"",
			}, "\n"),
		},
		{
			name: "custom base renders nodes without edges",
			args: []string{"--output", "table", "--base", "develop"},
			graph: changes.Graph{
				Base:  "develop",
				Nodes: []string{"README.md"},
			},
			wantBase: "develop",
			wantOut: strings.Join([]string{
				"Changes graph (base: develop)",
				"Nodes:",
				"  README.md",
				"Edges:",
				"  (none)",
				"",
			}, "\n"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := &fakeChangesBuilder{graph: tt.graph}
			cmd := newChangesCmd(builder)
			cmd.PersistentFlags().String("output", "json", "Output format")
			var out, errOut bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			cmd.SetArgs(tt.args)

			err := cmd.Execute()
			if got := changesExitCode(err); got != output.ExitOK {
				t.Fatalf("exit code = %d, want 0; stderr: %s", got, errOut.String())
			}
			if builder.base != tt.wantBase {
				t.Fatalf("base = %q, want %q", builder.base, tt.wantBase)
			}
			if out.String() != tt.wantOut {
				t.Fatalf("stdout = %q, want %q", out.String(), tt.wantOut)
			}
		})
	}
}

func TestChangesCommandReleaseFlag(t *testing.T) {
	builder := &fakeChangesBuilder{graph: changes.Graph{
		Base:    "v0.4.0",
		Release: "v0.5.0",
		Nodes:   []string{"internal/cli/changes.go"},
	}}
	opener := &fakeChangesOpener{}
	cmd := newChangesCmdWithOpener(builder, opener)
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--release", "v0.5.0"})

	if err := cmd.Execute(); changesExitCode(err) != output.ExitOK {
		t.Fatalf("exit code non-zero; stderr: %s", errOut.String())
	}
	if builder.release != "v0.5.0" {
		t.Fatalf("BuildRelease called with %q, want v0.5.0", builder.release)
	}
	if builder.base != "" {
		t.Fatalf("Build should not run in release mode; base = %q", builder.base)
	}
	if opener.path == "" {
		t.Fatal("release mode should open the visual graph")
	}
}

func TestChangesCommandRejectsArgs(t *testing.T) {
	builder := &fakeChangesBuilder{graph: changes.Graph{Base: "main"}}
	cmd := newChangesCmd(builder)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"unexpected"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected arg validation error, got nil")
	}
	if builder.called {
		t.Fatal("builder should not run when positional args are provided")
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
}

func TestChangesCommandError(t *testing.T) {
	builder := &fakeChangesBuilder{err: errors.New("git failed")}
	cmd := newChangesCmd(builder)
	cmd.SilenceUsage = true
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)

	err := cmd.Execute()
	if got := changesExitCode(err); got != output.ExitGeneral {
		t.Fatalf("exit code = %d, want %d", got, output.ExitGeneral)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if !strings.Contains(errOut.String(), "could not build changes graph: git failed") {
		t.Fatalf("stderr = %q, want graph error", errOut.String())
	}
}

func TestRootIncludesChangesCommand(t *testing.T) {
	root := NewRootCmd()
	cmd, _, err := root.Find([]string{"changes"})
	if err != nil {
		t.Fatalf("find changes command: %v", err)
	}
	if cmd == nil || cmd.Name() != "changes" {
		t.Fatalf("root changes command = %#v", cmd)
	}
}

type fakeChangesBuilder struct {
	base    string
	release string
	called  bool
	graph   changes.Graph
	err     error
}

func (f *fakeChangesBuilder) Build(_ context.Context, base string) (changes.Graph, error) {
	f.base = base
	f.called = true
	return f.graph, f.err
}

func (f *fakeChangesBuilder) BuildRelease(_ context.Context, release string) (changes.Graph, error) {
	f.release = release
	f.called = true
	return f.graph, f.err
}

type fakeChangesOpener struct {
	path string
}

func (f *fakeChangesOpener) Open(_ context.Context, path string) error {
	f.path = path
	return nil
}

func newRootWithFakeChanges(builder changesBuilder, opener changesOpener) *cobra.Command {
	root := &cobra.Command{Use: "sureva"}
	root.PersistentFlags().StringP("output", "o", "json", "Output format")
	root.AddCommand(newChangesCmdWithOpener(builder, opener))
	return root
}
