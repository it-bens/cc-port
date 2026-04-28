package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/remote"
	syncc "github.com/it-bens/cc-port/internal/sync"
	"github.com/it-bens/cc-port/internal/ui"
)

var (
	pullToPath         string
	pullRemoteURL      string
	pullApply          bool
	pullPassphraseEnv  string
	pullPassphraseFile string
	pullResolutionKV   []string
	pullFromManifest   string
)

var pullCmd = &cobra.Command{
	Use:   "pull <name>",
	Short: "Pull a project archive from a remote and apply it locally",
	Long: "Pulls a cc-port archive from a remote storage backend " +
		"(file:// or s3://) and applies it to the local target path. " +
		"Dry-run by default; pass --apply to commit. " +
		"Refuses to apply when declared placeholders remain unresolved.",
	Args: func(cmd *cobra.Command, args []string) error {
		if err := cobra.ExactArgs(1)(cmd, args); err != nil {
			return &usageError{err: err}
		}
		return nil
	},
	RunE: runPullCmd,
}

// runPullCmd is the pull subcommand body. The named return + deferred
// remote close pattern is load-bearing: remote.Remote owns a gocloud
// bucket whose Close error must surface to the caller.
func runPullCmd(cmd *cobra.Command, args []string) (err error) {
	opts, err := buildPullOptions(cmd, args[0])
	if err != nil {
		return err
	}
	defer func() {
		if cerr := opts.Remote.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("close remote: %w", cerr))
		}
	}()

	ctx := cmd.Context()
	plan, err := syncc.PlanPull(ctx, opts)
	if err != nil {
		return err
	}

	// Without --from-manifest, the operator can resolve missing keys
	// interactively. Re-plan after prompting so plan.Render and the
	// apply-time refusal both reflect what the operator just supplied.
	if pullFromManifest == "" && len(plan.UnresolvedPlaceholders) > 0 {
		plan, err = resolveAndReplan(ctx, &opts, plan)
		if err != nil {
			return err
		}
	}

	if err := plan.Render(cmd.OutOrStdout()); err != nil {
		return fmt.Errorf("render plan: %w", err)
	}

	if !pullApply {
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), "(no changes; pass --apply to commit)"); err != nil {
			return fmt.Errorf("write apply hint: %w", err)
		}
		return nil
	}

	if len(plan.UnresolvedPlaceholders) > 0 {
		return fmt.Errorf(
			"%w: %s",
			syncc.ErrUnresolvedPlaceholder,
			strings.Join(plan.UnresolvedPlaceholders, ", "),
		)
	}

	if err := syncc.ExecutePull(ctx, opts, plan); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Pulled: %s\n", opts.TargetPath); err != nil {
		return fmt.Errorf("write pull confirmation: %w", err)
	}
	return nil
}

// buildPullOptions validates flags, resolves the target path, opens the
// remote, and assembles syncc.PullOptions. The caller owns the returned
// remote handle and must Close it.
func buildPullOptions(cmd *cobra.Command, name string) (syncc.PullOptions, error) {
	if pullToPath == "" {
		return syncc.PullOptions{}, &usageError{err: errors.New("--to <target-path> is required")}
	}
	if pullRemoteURL == "" {
		return syncc.PullOptions{}, &usageError{err: errors.New("--remote <url> is required")}
	}

	passphrase, err := resolvePassphrase(pullPassphraseEnv, pullPassphraseFile)
	if err != nil {
		return syncc.PullOptions{}, err
	}

	targetPath, err := claude.ResolveProjectPath(pullToPath)
	if err != nil {
		return syncc.PullOptions{}, fmt.Errorf("resolve target path: %w", err)
	}

	claudeHome, err := claude.NewHome(claudeDir)
	if err != nil {
		return syncc.PullOptions{}, err
	}

	flagResolutions, err := parseResolutionFlags(pullResolutionKV)
	if err != nil {
		return syncc.PullOptions{}, err
	}

	var fromManifest *manifest.Metadata
	if pullFromManifest != "" {
		fromManifest, err = manifest.ReadManifest(pullFromManifest)
		if err != nil {
			return syncc.PullOptions{}, fmt.Errorf("read manifest: %w", err)
		}
	}

	r, err := remote.New(cmd.Context(), pullRemoteURL)
	if err != nil {
		return syncc.PullOptions{}, err
	}

	return syncc.PullOptions{
		ClaudeHome:   claudeHome,
		Remote:       r,
		Name:         name,
		TargetPath:   targetPath,
		Resolutions:  flagResolutions,
		FromManifest: fromManifest,
		Passphrase:   passphrase,
	}, nil
}

// resolveAndReplan prompts for every unresolved placeholder, merges the
// answers into opts.Resolutions, and recomputes the plan. The second
// PlanPull call is load-bearing: render and the apply-time guard both
// read plan.UnresolvedPlaceholders, so they must reflect the prompted
// resolutions.
func resolveAndReplan(
	ctx context.Context, opts *syncc.PullOptions, plan *syncc.PullPlan,
) (*syncc.PullPlan, error) {
	prompted, err := promptPullResolutions(plan, opts.Resolutions)
	if err != nil {
		return nil, err
	}
	for key, value := range prompted {
		opts.Resolutions[key] = value
	}
	refreshed, err := syncc.PlanPull(ctx, *opts)
	if err != nil {
		return nil, err
	}
	return refreshed, nil
}

// promptPullResolutions prompts for each unresolved placeholder via
// ui.ResolvePlaceholder. Mirrors cmd/cc-port/importcmd.go:promptImportResolutions:
// the original value is shown verbatim, the entered value is taken with
// no validation. The returned map only contains the prompted-for keys;
// callers merge it into the existing flag map.
func promptPullResolutions(plan *syncc.PullPlan, flagResolutions map[string]string) (map[string]string, error) {
	declaredByKey := make(map[string]manifest.Placeholder, len(plan.DeclaredPlaceholders))
	for _, placeholder := range plan.DeclaredPlaceholders {
		declaredByKey[placeholder.Key] = placeholder
	}
	prompted := make(map[string]string, len(plan.UnresolvedPlaceholders))
	for _, key := range plan.UnresolvedPlaceholders {
		if _, alreadyResolved := flagResolutions[key]; alreadyResolved {
			continue
		}
		declared, ok := declaredByKey[key]
		if !ok {
			return nil, fmt.Errorf("placeholder %s reported unresolved but not declared in archive", key)
		}
		resolved, err := ui.ResolvePlaceholder(key, declared.Original, "")
		if err != nil {
			return nil, err
		}
		prompted[key] = resolved
	}
	return prompted, nil
}

func init() {
	pullCmd.Flags().StringVar(&pullToPath, "to", "",
		"local target path for the pulled project")
	pullCmd.Flags().StringVar(&pullRemoteURL, "remote", "",
		"remote URL (file://path or s3://bucket?region=...)")
	pullCmd.Flags().BoolVar(&pullApply, "apply", false,
		"commit the import (default is dry-run)")
	pullCmd.Flags().StringVar(&pullPassphraseEnv, "passphrase-env", "",
		"name of env var containing the encryption passphrase "+
			"(mutually exclusive with --passphrase-file)")
	pullCmd.Flags().StringVar(&pullPassphraseFile, "passphrase-file", "",
		"path to a file containing the encryption passphrase "+
			"(mutually exclusive with --passphrase-env)")
	pullCmd.MarkFlagsMutuallyExclusive("passphrase-env", "passphrase-file")
	pullCmd.Flags().StringArrayVar(&pullResolutionKV, "resolution", nil,
		"resolve a placeholder non-interactively (repeatable; KEY=VALUE)")
	pullCmd.Flags().StringVar(&pullFromManifest, "from-manifest", "",
		"path to a manifest XML file with pre-filled resolutions")
	rootCmd.AddCommand(pullCmd)
}
