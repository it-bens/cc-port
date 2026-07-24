package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/it-bens/cc-port/internal/credentials"
	"github.com/it-bens/cc-port/internal/encrypt"
	"github.com/it-bens/cc-port/internal/importer"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/pipeline"
	"github.com/it-bens/cc-port/internal/progress"
	"github.com/it-bens/cc-port/internal/remote"
	syncc "github.com/it-bens/cc-port/internal/sync"
	"github.com/it-bens/cc-port/internal/tool"
)

// newPullCmd returns the pull subcommand with closure-scoped flag locals.
func newPullCmd(toolSet *tool.Set, flags *toolFlags) *cobra.Command {
	var (
		toPath          string
		remoteURL       string
		apply           bool
		passphraseEnv   string
		passphraseFile  string
		fromManifest    string
		credentialsFile string
		noPrompt        bool
	)
	cmd := &cobra.Command{
		Use:   "pull <name>",
		Short: "Pull a project archive from a remote and apply it locally",
		Long: "Pulls a cc-port archive from a remote storage backend " +
			"(file:// or s3://) and applies it, across every selected tool, to the local " +
			"target path. Dry-run by default; pass --apply to commit. " +
			"Refuses to apply when declared placeholders remain unresolved.\n\n" +
			remote.URLDoc,
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.ExactArgs(1)(cmd, args); err != nil {
				return &usageError{err: err}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPullCmd(cmd, args, toolSet, flags)
		},
	}
	cmd.Flags().StringVar(&toPath, "to", "",
		"local target path for the pulled project")
	cmd.Flags().StringVar(&remoteURL, "remote", "",
		"remote URL (file:// or s3://; see --help for examples and provider setup)")
	cmd.Flags().BoolVar(&apply, "apply", false,
		"commit the import (default is dry-run)")
	cmd.Flags().StringVar(&passphraseEnv, "passphrase-env", "",
		"name of env var containing the encryption passphrase "+
			"(mutually exclusive with --passphrase-file)")
	cmd.Flags().StringVar(&passphraseFile, "passphrase-file", "",
		"path to a file containing the encryption passphrase "+
			"(mutually exclusive with --passphrase-env)")
	cmd.MarkFlagsMutuallyExclusive("passphrase-env", "passphrase-file")
	cmd.Flags().StringVar(&fromManifest, "from-manifest", "",
		"path to a manifest XML file with pre-filled resolutions")
	cmd.Flags().StringVar(&credentialsFile, "credentials-file", "",
		"path to a .env-style AWS credentials file (AWS_ACCESS_KEY_ID, "+
			"AWS_SECRET_ACCESS_KEY, optional AWS_SESSION_TOKEN; mode 0600)")
	cmd.Flags().BoolVar(&noPrompt, "no-prompt", false,
		"disable the interactive prompt fallback for missing credentials")
	return cmd
}

// openArchiveSource opens the strict reader pipeline for pull.
func openArchiveSource(
	ctx context.Context,
	r *remote.Remote,
	name, pass string,
	reporter progress.Reporter,
) (pipeline.Source, error) {
	counter := &downloadCounterStage{reporter: reporter}
	src, err := pipeline.RunReader(ctx, []pipeline.ReaderStage{
		&remote.Source{Remote: r, Key: name},
		counter,
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
	counter.End()
	return src, nil
}

// runPullCmd is the pull subcommand body.
func runPullCmd(cmd *cobra.Command, args []string, toolSet *tool.Set, flags *toolFlags) (err error) {
	apply, _ := cmd.Flags().GetBool("apply")

	opts, r, passphrase, err := buildPullOptions(cmd, args[0], toolSet, flags)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := r.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("close remote: %w", cerr))
		}
	}()

	var (
		source pipeline.Source
		plan   *syncc.PullPlan
		result *importer.Result
	)
	// The archive source outlives the first progress run, so it cannot be
	// closed inside it. This deferred close covers an early return; the
	// explicit close below is what folds a close failure into the outcome the
	// trailing hint and confirmation are decided from. Source.Close is
	// idempotent, so running both is safe.
	defer func() {
		if source.Close == nil {
			return
		}
		if cerr := source.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("close source: %w", cerr))
		}
	}()

	// Downloading and planning run under their own progress ledger so the plan
	// can render once that ledger has torn down but before ExecutePull writes.
	// The ledger holds the terminal in raw mode, where a bare "\n" moves down
	// without a carriage return, so rendering inside either run staircases
	// every line.
	progErr := runWithProgress(cmd, func(ctx context.Context, reporter progress.Reporter) error {
		opts.Reporter = reporter

		opened, err := openArchiveSource(ctx, r, opts.Name, passphrase, reporter)
		if err != nil {
			return err
		}
		source = opened

		planned, err := syncc.PlanPull(ctx, opts, source)
		if err != nil {
			return err
		}
		plan = planned
		return nil
	})

	if plan != nil {
		if rerr := plan.Render(cmd.OutOrStdout(), apply); rerr != nil {
			progErr = errors.Join(progErr, fmt.Errorf("render plan: %w", rerr))
		}
	}

	if progErr == nil && apply {
		progErr = runWithProgress(cmd, func(ctx context.Context, reporter progress.Reporter) error {
			opts.Reporter = reporter
			runResult, err := applyPull(ctx, opts, plan, source)
			if err != nil {
				return err
			}
			result = runResult
			return nil
		})
	}

	if source.Close != nil {
		if cerr := source.Close(); cerr != nil {
			progErr = errors.Join(progErr, fmt.Errorf("close source: %w", cerr))
		}
	}

	return renderPullOutcome(cmd, opts, result, apply, progErr)
}

// renderPullOutcome writes the trailing dry-run hint or, on apply, the tool
// warnings and the "Pulled:" confirmation. The plan summary itself has already
// been written by the time it runs, because an operator must see it before
// ExecutePull writes rather than after.
//
//nolint:gocritic // hugeParam: by-value PullOptions mirrors the public Plan/Execute contract.
func renderPullOutcome(
	cmd *cobra.Command, opts syncc.PullOptions, result *importer.Result, apply bool, progErr error,
) error {
	if progErr == nil && !apply {
		if _, werr := fmt.Fprintln(cmd.OutOrStdout(), "(no changes; pass --apply to commit)"); werr != nil {
			progErr = errors.Join(progErr, fmt.Errorf("write apply hint: %w", werr))
		}
	}

	if apply && result != nil {
		if len(result.SkippedTools) > 0 {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "note: archive has no data for: %s\n", strings.Join(result.SkippedTools, ", "))
		}
		if werr := renderImportWarnings(cmd.ErrOrStderr(), opts.Targets, result.Warnings); werr != nil {
			progErr = errors.Join(progErr, werr)
		}
		if _, werr := fmt.Fprintf(cmd.OutOrStdout(), "Pulled: %s\n", opts.TargetPath); werr != nil {
			progErr = errors.Join(progErr, fmt.Errorf("write pull confirmation: %w", werr))
		}
	}
	return progErr
}

// applyPull refuses an archive whose declared placeholders are still
// unresolved, then commits the import. The plan has already rendered by the
// time it runs, so the refusal reaches an operator who has seen which keys
// the summary marked MISSING.
//
//nolint:gocritic // hugeParam: by-value PullOptions mirrors the public Plan/Execute contract.
func applyPull(
	ctx context.Context, opts syncc.PullOptions, plan *syncc.PullPlan, source pipeline.Source,
) (*importer.Result, error) {
	var allUnresolved []string
	for _, toolName := range plan.Tools {
		allUnresolved = append(allUnresolved, plan.UnresolvedPlaceholders[toolName]...)
	}
	if len(allUnresolved) > 0 {
		return nil, fmt.Errorf("%w: %s", syncc.ErrUnresolvedPlaceholder, strings.Join(allUnresolved, ", "))
	}
	return syncc.ExecutePull(ctx, opts, plan, source)
}

// buildPullOptions validates flags, resolves the target path, opens the
// remote, and assembles syncc.PullOptions.
func buildPullOptions(cmd *cobra.Command, name string, toolSet *tool.Set, flags *toolFlags,
) (syncc.PullOptions, *remote.Remote, string, error) {
	toPath, _ := cmd.Flags().GetString("to")
	remoteURL, _ := cmd.Flags().GetString("remote")
	passphraseEnv, _ := cmd.Flags().GetString("passphrase-env")
	passphraseFile, _ := cmd.Flags().GetString("passphrase-file")
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

	targetPath, err := tool.ResolveProjectPath(toPath)
	if err != nil {
		return syncc.PullOptions{}, nil, "", fmt.Errorf("resolve target path: %w", err)
	}

	targets, err := resolveTargets(toolSet, flags)
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

	credentialsFile, _ := cmd.Flags().GetString("credentials-file")
	noPrompt, _ := cmd.Flags().GetBool("no-prompt")

	credentialsProvider, err := credentials.Resolve(cmd.Context(), credentials.ResolveOptions{
		Path:   credentialsFile,
		Prompt: !noPrompt,
	})
	if err != nil {
		return syncc.PullOptions{}, nil, "", err
	}

	r, err := remote.New(cmd.Context(), remoteURL, remote.Deps{Credentials: credentialsProvider})
	if err != nil {
		return syncc.PullOptions{}, nil, "", err
	}

	opts := syncc.PullOptions{
		AllTools:          toolSet,
		Targets:           targets,
		Name:              name,
		TargetPath:        targetPath,
		FromManifest:      fromManifest,
		EncryptionEnabled: passphrase != "",
	}
	return opts, r, passphrase, nil
}
