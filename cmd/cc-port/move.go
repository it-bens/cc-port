package main

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/it-bens/cc-port/internal/move"
	"github.com/it-bens/cc-port/internal/progress"
	"github.com/it-bens/cc-port/internal/tool"
)

// newMoveCmd returns the move subcommand with closure-scoped flag locals.
func newMoveCmd(toolSet *tool.Set, flags *toolFlags) *cobra.Command {
	var apply bool
	cmd := &cobra.Command{
		Use:   "move <old-path> <new-path>",
		Short: "Move a project and update references across every selected tool",
		Long: "Renames a project directory and rewrites every selected tool's references.\n" +
			"Default is dry-run — use --apply to execute.",
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.ExactArgs(2)(cmd, args); err != nil {
				return &usageError{err: err}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			moveOptions, err := parseMoveOptions(cmd, args)
			if err != nil {
				return err
			}

			targets, err := resolveTargets(toolSet, flags)
			if err != nil {
				return err
			}

			if !apply {
				return runMoveDryRun(ctx, cmd.OutOrStdout(), targets, moveOptions)
			}
			return runWithProgress(cmd, func(ctx context.Context, reporter progress.Reporter) error {
				moveOptions.Reporter = reporter
				result, err := move.Apply(ctx, targets, moveOptions)
				if err != nil {
					return err
				}
				renderApplyResult(cmd.OutOrStdout(), result)
				if result.Failed() {
					return fmt.Errorf("one or more tools failed to move; see the table above")
				}
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "execute the move (default is dry-run)")
	cmd.Flags().Bool(
		"refs-only", false,
		"update references only, do not move project directory on disk",
	)
	cmd.Flags().Bool(
		"deep", false,
		"also rewrite paths in narrative bodies (e.g. session transcripts)",
	)
	return cmd
}

// parseMoveOptions turns the cobra command + positional args into a
// move.Options. Kept pure (no lock, no domain call) so it is
// unit-testable in isolation.
func parseMoveOptions(cmd *cobra.Command, args []string) (move.Options, error) {
	oldPath, err := tool.ResolveProjectPath(args[0])
	if err != nil {
		return move.Options{}, fmt.Errorf("resolve old path: %w", err)
	}
	newPath, err := tool.ResolveProjectPath(args[1])
	if err != nil {
		return move.Options{}, fmt.Errorf("resolve new path: %w", err)
	}
	refsOnly, _ := cmd.Flags().GetBool("refs-only")
	deepRewrite, _ := cmd.Flags().GetBool("deep")
	return move.Options{
		OldPath:     oldPath,
		NewPath:     newPath,
		RefsOnly:    refsOnly,
		DeepRewrite: deepRewrite,
	}, nil
}

func runMoveDryRun(ctx context.Context, stdout io.Writer, targets []tool.Target, moveOptions move.Options) error {
	movePlan, err := move.DryRun(ctx, targets, moveOptions)
	if err != nil {
		return err
	}
	activeWriters := make(map[string][]tool.ActiveWriter, len(targets))
	witnessErrors := make(map[string]error, len(targets))
	for _, target := range targets {
		writers, witnessErr := target.Workspace.ActiveWriters()
		if witnessErr != nil {
			witnessErrors[target.Tool.Name()] = witnessErr
			continue
		}
		activeWriters[target.Tool.Name()] = writers
	}

	_, _ = fmt.Fprintln(stdout, "cc-port move (dry-run)")
	_, _ = fmt.Fprintf(stdout, "  %s -> %s\n\n", moveOptions.OldPath, moveOptions.NewPath)

	for _, toolPlan := range movePlan.ByTool {
		_, _ = fmt.Fprintf(stdout, "  [%s]\n", toolPlan.Tool)
		if toolPlan.Absent {
			_, _ = fmt.Fprintln(stdout, "    (project unknown to this tool; nothing to move)")
			continue
		}
		total := 0
		for _, surface := range toolPlan.Surfaces {
			total += surface.Count
		}
		_, _ = fmt.Fprintf(stdout, "    References (%d changes)\n", total)
		for _, surface := range toolPlan.Surfaces {
			if surface.Count == 0 {
				continue
			}
			_, _ = fmt.Fprintf(stdout, "      %-24s %d\n", surface.Name, surface.Count)
		}
		for _, warning := range toolPlan.Warnings {
			_, _ = fmt.Fprintf(stdout, "    ! %s\n", warning)
		}
		if witnessErr, ok := witnessErrors[toolPlan.Tool]; ok {
			_, _ = fmt.Fprintf(stdout, "    ! could not inspect active writers: %v\n", witnessErr)
		}
		for _, writer := range activeWriters[toolPlan.Tool] {
			_, _ = fmt.Fprintf(stdout, "    ! active %s writer: pid=%d cwd=%s\n", displayName(targets, toolPlan.Tool), writer.Pid, writer.Cwd)
		}
		_, _ = fmt.Fprintln(stdout)
	}
	for _, warning := range movePlan.Warnings {
		_, _ = fmt.Fprintf(stdout, "  ! %s\n", warning)
	}

	_, _ = fmt.Fprintln(stdout, "  Run with --apply to execute.")
	return nil
}

func displayName(targets []tool.Target, toolName string) string {
	for _, target := range targets {
		if target.Tool.Name() == toolName {
			return target.Tool.DisplayName()
		}
	}
	return toolName
}

func renderApplyResult(stdout io.Writer, result *move.ApplyResult) {
	_, _ = fmt.Fprintln(stdout, "cc-port move")
	for _, toolResult := range result.ByTool {
		switch {
		case toolResult.Absent:
			_, _ = fmt.Fprintf(stdout, "  [%s] project unknown to this tool; skipped\n", toolResult.Tool)
		case toolResult.Success:
			_, _ = fmt.Fprintf(stdout, "  [%s] OK\n", toolResult.Tool)
			for _, warning := range toolResult.Warnings {
				_, _ = fmt.Fprintf(stdout, "    ! %s\n", warning)
			}
		default:
			_, _ = fmt.Fprintf(stdout, "  [%s] FAILED: %v\n", toolResult.Tool, toolResult.Err)
		}
	}
	for _, warning := range result.Warnings {
		_, _ = fmt.Fprintf(stdout, "  ! %s\n", warning)
	}
}
