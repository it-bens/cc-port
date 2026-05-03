package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/encrypt"
	"github.com/it-bens/cc-port/internal/pipeline"
	"github.com/it-bens/cc-port/internal/remote"
	syncc "github.com/it-bens/cc-port/internal/sync"
)

// newPushCmd returns the push subcommand with closure-scoped flag locals.
// claudeDir points at the persistent root flag's local; runPushCmd reads
// it via *claudeDir at call time. applyCategorySelection (shared with
// newExportCmd) reads --from-manifest via cmd.Flags() and owns the
// exclusivity guard with --all and per-category flags.
func newPushCmd(claudeDir *string) *cobra.Command {
	var (
		asName         string
		remoteURL      string
		apply          bool
		force          bool
		passphraseEnv  string
		passphraseFile string
		fromManifest   string
	)
	cmd := &cobra.Command{
		Use:   "push <project-path>",
		Short: "Push a project archive to a remote",
		Long: "Pushes a cc-port export of <project-path> to a remote storage backend " +
			"(file:// or s3://). Dry-run by default; pass --apply to commit. " +
			"Refuses cross-machine conflicts without --force.",
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.ExactArgs(1)(cmd, args); err != nil {
				return &usageError{err: err}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPushCmd(cmd, args, *claudeDir)
		},
	}
	cmd.Flags().StringVar(&asName, "as", "",
		"stable name for the archive on the remote")
	cmd.Flags().StringVar(&remoteURL, "remote", "",
		"remote URL (file://path or s3://bucket?region=...)")
	cmd.Flags().BoolVar(&apply, "apply", false,
		"commit the upload (default is dry-run)")
	cmd.Flags().BoolVar(&force, "force", false,
		"override cross-machine conflict refusal")
	cmd.Flags().StringVar(&passphraseEnv, "passphrase-env", "",
		"name of env var containing the encryption passphrase "+
			"(mutually exclusive with --passphrase-file)")
	cmd.Flags().StringVar(&passphraseFile, "passphrase-file", "",
		"path to a file containing the encryption passphrase "+
			"(mutually exclusive with --passphrase-env)")
	cmd.Flags().StringVar(&fromManifest, "from-manifest", "",
		"path to a manifest XML with categories and placeholder declarations")
	cmd.MarkFlagsMutuallyExclusive("passphrase-env", "passphrase-file")
	registerCategoryFlags(cmd, "push")
	return cmd
}

// openPriorRead opens the prior reader pipeline for the cross-machine check.
// Returns nil for the two no-prior cases (object absent, or encrypted-with-
// --force suppression) so PlanPush leaves prior fields zero. Other errors
// translate to sync sentinels at the cmd boundary.
func openPriorRead(
	ctx context.Context,
	r *remote.Remote,
	name, pass string,
	force bool,
) (*syncc.PriorRead, error) {
	src, err := pipeline.RunReader(ctx, []pipeline.ReaderStage{
		&remote.Source{Remote: r, Key: name},
		&encrypt.ReaderStage{Pass: pass, Mode: encrypt.Permissive},
		&pipeline.MaterializeStage{},
	})
	switch {
	case errors.Is(err, remote.ErrNotFound):
		return nil, nil
	case errors.Is(err, encrypt.ErrPassphraseRequired):
		if force {
			return nil, nil
		}
		return nil, syncc.ErrPassphraseRequired
	case err != nil:
		return nil, fmt.Errorf("open prior: %w", err)
	}
	return &syncc.PriorRead{Source: src, WasEncrypted: src.Meta.WasEncrypted}, nil
}

// runPushCmd is the push subcommand body. The named return + deferred
// closes pattern is load-bearing: prior.Source.Close releases the
// decrypt-tempfile, and writer.Close commits the upload via remote.Sink.
func runPushCmd(cmd *cobra.Command, args []string, claudeDir string) (err error) {
	asName, _ := cmd.Flags().GetString("as")
	remoteURL, _ := cmd.Flags().GetString("remote")
	apply, _ := cmd.Flags().GetBool("apply")
	force, _ := cmd.Flags().GetBool("force")
	passphraseEnv, _ := cmd.Flags().GetString("passphrase-env")
	passphraseFile, _ := cmd.Flags().GetString("passphrase-file")

	if asName == "" {
		return &usageError{err: errors.New("--as <name> is required")}
	}
	if remoteURL == "" {
		return &usageError{err: errors.New("--remote <url> is required")}
	}

	passphrase, err := resolvePassphrase(passphraseEnv, passphraseFile)
	if err != nil {
		return err
	}

	projectPath, err := claude.ResolveProjectPath(args[0])
	if err != nil {
		return fmt.Errorf("resolve project path: %w", err)
	}

	claudeHome, err := claude.NewHome(claudeDir)
	if err != nil {
		return err
	}

	categories, placeholders, err := applyCategorySelection(cmd, claudeHome, projectPath)
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	r, err := remote.New(ctx, remoteURL)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := r.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("close remote: %w", cerr))
		}
	}()

	opts := syncc.PushOptions{
		ClaudeHome:        claudeHome,
		ProjectPath:       projectPath,
		Name:              asName,
		Categories:        categories,
		Placeholders:      placeholders,
		Force:             force,
		EncryptionEnabled: passphrase != "",
	}

	prior, err := openPriorRead(ctx, r, asName, passphrase, force)
	if err != nil {
		return err
	}
	if prior != nil {
		defer func() {
			if cerr := prior.Source.Close(); cerr != nil {
				err = errors.Join(err, fmt.Errorf("close prior: %w", cerr))
			}
		}()
	}

	plan, err := syncc.PlanPush(ctx, opts, prior)
	if err != nil {
		return err
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

	return applyPush(ctx, cmd, r, opts, plan, passphrase)
}

// applyPush runs the cross-machine guard, opens the writer pipeline, calls
// ExecutePush, and prints the confirmation. Lives outside runPushCmd so the
// writer's deferred Close has its own named-return scope: the upload commits
// inside Close, so a failed Close must surface to runPushCmd via the
// returned error.
//
//nolint:gocritic // hugeParam: by-value PushOptions mirrors the public Plan/Execute contract.
func applyPush(
	ctx context.Context,
	cmd *cobra.Command,
	r *remote.Remote,
	opts syncc.PushOptions,
	plan *syncc.PushPlan,
	passphrase string,
) (err error) {
	if plan.CrossMachine && !opts.Force {
		return fmt.Errorf(
			"%w: prior pushed by %s at %s",
			syncc.ErrCrossMachineConflict,
			plan.PriorPushedBy,
			plan.PriorPushedAt.Format("2006-01-02T15:04:05Z"),
		)
	}

	writer, err := pipeline.RunWriter(ctx, []pipeline.WriterStage{
		&encrypt.WriterStage{Pass: passphrase},
		&remote.Sink{Remote: r, Key: opts.Name},
	})
	if err != nil {
		return fmt.Errorf("build writer pipeline: %w", err)
	}
	defer func() {
		if cerr := writer.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("close writer: %w", cerr))
		}
	}()

	if err := syncc.ExecutePush(ctx, opts, plan, writer); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Pushed: %s/%s\n", r.URL(), opts.Name); err != nil {
		return fmt.Errorf("write push confirmation: %w", err)
	}
	return nil
}
