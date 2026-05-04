package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/encrypt"
	"github.com/it-bens/cc-port/internal/importer"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/pipeline"
	"github.com/it-bens/cc-port/internal/remote"
	syncc "github.com/it-bens/cc-port/internal/sync"
	"github.com/it-bens/cc-port/internal/ui"
)

// newPullCmd returns the pull subcommand with closure-scoped flag locals.
// claudeDir points at the persistent root flag's local; runPullCmd reads
// it via *claudeDir at call time.
func newPullCmd(claudeDir *string) *cobra.Command {
	var (
		toPath         string
		remoteURL      string
		apply          bool
		passphraseEnv  string
		passphraseFile string
		resolutionKV   []string
		fromManifest   string
	)
	cmd := &cobra.Command{
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
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPullCmd(cmd, args, *claudeDir)
		},
	}
	cmd.Flags().StringVar(&toPath, "to", "",
		"local target path for the pulled project")
	cmd.Flags().StringVar(&remoteURL, "remote", "",
		"remote URL (file://path or s3://bucket?region=...)")
	cmd.Flags().BoolVar(&apply, "apply", false,
		"commit the import (default is dry-run)")
	cmd.Flags().StringVar(&passphraseEnv, "passphrase-env", "",
		"name of env var containing the encryption passphrase "+
			"(mutually exclusive with --passphrase-file)")
	cmd.Flags().StringVar(&passphraseFile, "passphrase-file", "",
		"path to a file containing the encryption passphrase "+
			"(mutually exclusive with --passphrase-env)")
	cmd.MarkFlagsMutuallyExclusive("passphrase-env", "passphrase-file")
	cmd.Flags().StringArrayVar(&resolutionKV, "resolution", nil,
		"resolve a placeholder non-interactively (repeatable; KEY=VALUE)")
	cmd.Flags().StringVar(&fromManifest, "from-manifest", "",
		"path to a manifest XML file with pre-filled resolutions")
	return cmd
}

// openArchiveSource opens the strict reader pipeline for pull. Translates
// remote.ErrNotFound and encrypt.ErrPassphraseRequired into sync sentinels;
// other errors propagate wrapped.
func openArchiveSource(
	ctx context.Context,
	r *remote.Remote,
	name, pass string,
) (pipeline.Source, error) {
	src, err := pipeline.RunReader(ctx, []pipeline.ReaderStage{
		&remote.Source{Remote: r, Key: name},
		&encrypt.ReaderStage{Pass: pass, Mode: encrypt.Strict},
		&pipeline.MaterializeStage{},
	})
	switch {
	case errors.Is(err, remote.ErrNotFound):
		return pipeline.Source{}, syncc.ErrRemoteNotFound
	case errors.Is(err, encrypt.ErrPassphraseRequired):
		return pipeline.Source{}, syncc.ErrPassphraseRequired
	case err != nil:
		return pipeline.Source{}, fmt.Errorf("open archive: %w", err)
	}
	return src, nil
}

// runPullCmd is the pull subcommand body. The named return + deferred
// closes pattern is load-bearing: remote.Remote owns a gocloud bucket
// whose Close error must surface, and source.Close releases the
// decrypt-tempfile.
func runPullCmd(cmd *cobra.Command, args []string, claudeDir string) (err error) {
	apply, _ := cmd.Flags().GetBool("apply")
	fromManifestPath, _ := cmd.Flags().GetString("from-manifest")

	opts, r, passphrase, err := buildPullOptions(cmd, args[0], claudeDir)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := r.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("close remote: %w", cerr))
		}
	}()

	ctx := cmd.Context()
	source, err := openArchiveSource(ctx, r, opts.Name, passphrase)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := source.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("close source: %w", cerr))
		}
	}()

	plan, err := syncc.PlanPull(ctx, opts, source)
	if err != nil {
		return err
	}

	// Without --from-manifest, the operator can resolve missing keys
	// interactively. Re-plan with the same source after prompting so
	// plan.Render and the apply-time refusal both reflect what the
	// operator just supplied. pipeline.Source.ReaderAt is safely
	// re-readable; one decrypt tempfile services every phase.
	if fromManifestPath == "" && len(plan.UnresolvedPlaceholders) > 0 {
		plan, err = resolveAndReplan(ctx, &opts, plan, source)
		if err != nil {
			return err
		}
	}

	if err := plan.Render(cmd.OutOrStdout()); err != nil {
		return fmt.Errorf("render plan: %w", err)
	}

	if !apply {
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

	result, err := syncc.ExecutePull(ctx, opts, plan, source)
	if err != nil {
		return err
	}
	renderRulesReport(cmd.ErrOrStderr(), "", result.RulesReport)

	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Pulled: %s\n", opts.TargetPath); err != nil {
		return fmt.Errorf("write pull confirmation: %w", err)
	}
	return nil
}

// buildPullOptions validates flags, resolves the target path, opens the
// remote, and assembles syncc.PullOptions. The caller owns the returned
// remote handle and the passphrase value; opts no longer carries them.
func buildPullOptions(cmd *cobra.Command, name string, claudeDir string,
) (syncc.PullOptions, *remote.Remote, string, error) {
	toPath, _ := cmd.Flags().GetString("to")
	remoteURL, _ := cmd.Flags().GetString("remote")
	passphraseEnv, _ := cmd.Flags().GetString("passphrase-env")
	passphraseFile, _ := cmd.Flags().GetString("passphrase-file")
	resolutionKV, _ := cmd.Flags().GetStringArray("resolution")
	fromManifestPath, _ := cmd.Flags().GetString("from-manifest")

	if toPath == "" {
		return syncc.PullOptions{}, nil, "", &usageError{err: errors.New("--to <target-path> is required")}
	}
	if remoteURL == "" {
		return syncc.PullOptions{}, nil, "", &usageError{err: errors.New("--remote <url> is required")}
	}

	passphrase, err := resolvePassphrase(passphraseEnv, passphraseFile)
	if err != nil {
		return syncc.PullOptions{}, nil, "", err
	}

	targetPath, err := claude.ResolveProjectPath(toPath)
	if err != nil {
		return syncc.PullOptions{}, nil, "", fmt.Errorf("resolve target path: %w", err)
	}

	claudeHome, err := claude.NewHome(claudeDir)
	if err != nil {
		return syncc.PullOptions{}, nil, "", err
	}

	flagResolutions, err := parseResolutionFlags(resolutionKV)
	if err != nil {
		return syncc.PullOptions{}, nil, "", err
	}

	var fromManifest *manifest.Metadata
	if fromManifestPath != "" {
		fromManifest, err = manifest.ReadManifest(fromManifestPath)
		if err != nil {
			return syncc.PullOptions{}, nil, "", fmt.Errorf("read manifest: %w", err)
		}
	}

	r, err := remote.New(cmd.Context(), remoteURL)
	if err != nil {
		return syncc.PullOptions{}, nil, "", err
	}

	homePath, err := resolveHomeAnchor()
	if err != nil {
		return syncc.PullOptions{}, nil, "", err
	}

	opts := syncc.PullOptions{
		ClaudeHome:        claudeHome,
		Name:              name,
		TargetPath:        targetPath,
		HomePath:          homePath,
		Resolutions:       flagResolutions,
		FromManifest:      fromManifest,
		EncryptionEnabled: passphrase != "",
	}
	return opts, r, passphrase, nil
}

// resolveAndReplan composes resolutions for every unresolved placeholder
// via importer.ResolvePlaceholders, merges the result into opts.Resolutions,
// and recomputes the plan against the same source. The second PlanPull call
// is load-bearing: render and the apply-time guard both read
// plan.UnresolvedPlaceholders, so they must reflect the prompted resolutions.
func resolveAndReplan(
	ctx context.Context, opts *syncc.PullOptions, plan *syncc.PullPlan, source pipeline.Source,
) (*syncc.PullPlan, error) {
	declaredByKey := make(map[string]manifest.Placeholder, len(plan.DeclaredPlaceholders))
	for _, placeholder := range plan.DeclaredPlaceholders {
		declaredByKey[placeholder.Key] = placeholder
	}
	prompter := func(stillUnresolved []string) (map[string]string, error) {
		out := make(map[string]string, len(stillUnresolved))
		for _, key := range stillUnresolved {
			declared, ok := declaredByKey[key]
			if !ok {
				return nil, fmt.Errorf("placeholder %s reported unresolved but not declared in archive", key)
			}
			resolved, err := ui.ResolvePlaceholder(key, declared.Original, "")
			if err != nil {
				return nil, err
			}
			out[key] = resolved
		}
		return out, nil
	}

	resolutions, err := importer.ResolvePlaceholders(plan.UnresolvedPlaceholders, opts.FromManifest, prompter)
	if err != nil {
		return nil, err
	}
	for key, value := range resolutions {
		if value == "" {
			continue
		}
		opts.Resolutions[key] = value
	}

	refreshed, err := syncc.PlanPull(ctx, *opts, source)
	if err != nil {
		return nil, err
	}
	return refreshed, nil
}
