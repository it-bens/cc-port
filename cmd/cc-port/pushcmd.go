package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"

	"github.com/spf13/cobra"

	"github.com/it-bens/cc-port/internal/credentials"
	"github.com/it-bens/cc-port/internal/encrypt"
	"github.com/it-bens/cc-port/internal/export"
	"github.com/it-bens/cc-port/internal/pipeline"
	"github.com/it-bens/cc-port/internal/progress"
	"github.com/it-bens/cc-port/internal/remote"
	syncc "github.com/it-bens/cc-port/internal/sync"
	"github.com/it-bens/cc-port/internal/tool"
)

// newPushCmd returns the push subcommand with closure-scoped flag locals.
func newPushCmd(toolSet *tool.Set, flags *toolFlags, banner Banner) *cobra.Command {
	var (
		asName          string
		remoteURL       string
		apply           bool
		force           bool
		passphraseEnv   string
		passphraseFile  string
		fromManifest    string
		credentialsFile string
		noPrompt        bool
	)
	cmd := &cobra.Command{
		Use:   "push <project-path>",
		Short: "Push a project archive to a remote",
		Long: "Pushes a cc-port export of <project-path>, across every selected tool, to a " +
			"remote storage backend (file:// or s3://). Dry-run by default; pass --apply to " +
			"commit. Refuses cross-machine conflicts without --force.\n\n" +
			remote.URLDoc,
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.ExactArgs(1)(cmd, args); err != nil {
				return &usageError{err: err}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPushCmd(cmd, args, toolSet, flags, banner)
		},
	}
	cmd.Flags().StringVar(&asName, "as", "",
		"stable name for the archive on the remote")
	cmd.Flags().StringVar(&remoteURL, "remote", "",
		"remote URL (file:// or s3://; see --help for examples and provider setup)")
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
	cmd.Flags().StringVar(&credentialsFile, "credentials-file", "",
		"path to a .env-style AWS credentials file (AWS_ACCESS_KEY_ID, "+
			"AWS_SECRET_ACCESS_KEY, optional AWS_SESSION_TOKEN; mode 0600)")
	cmd.Flags().BoolVar(&noPrompt, "no-prompt", false,
		"disable the interactive prompt fallback for missing credentials")
	cmd.MarkFlagsMutuallyExclusive("passphrase-env", "passphrase-file")
	registerCategoryFlags(cmd, "push")
	return cmd
}

// openPriorRead opens the prior reader pipeline for the cross-machine check.
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

// runPushCmd is the push subcommand body.
func runPushCmd(cmd *cobra.Command, args []string, toolSet *tool.Set, flags *toolFlags, banner Banner) (err error) {
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

	projectPath, err := tool.ResolveProjectPath(args[0])
	if err != nil {
		return fmt.Errorf("resolve project path: %w", err)
	}

	targets, err := resolveTargets(toolSet, flags)
	if err != nil {
		return err
	}

	selection, placeholders, err := applyCategorySelection(cmd, targets, projectPath, banner)
	if err != nil {
		return err
	}

	credentialsFile, _ := cmd.Flags().GetString("credentials-file")
	noPrompt, _ := cmd.Flags().GetBool("no-prompt")

	credentialsProvider, err := credentials.Resolve(cmd.Context(), credentials.ResolveOptions{
		Path:   credentialsFile,
		Prompt: !noPrompt,
	})
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	r, err := remote.New(ctx, remoteURL, remote.Deps{Credentials: credentialsProvider})
	if err != nil {
		return err
	}
	defer func() {
		if cerr := r.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("close remote: %w", cerr))
		}
	}()

	opts := syncc.PushOptions{
		Targets:           targets,
		ProjectPath:       projectPath,
		Name:              asName,
		Selected:          selection,
		Placeholders:      placeholders,
		Hostname:          os.Hostname,
		Getenv:            os.Getenv,
		CurrentUser:       user.Current,
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

	var (
		plan   *syncc.PushPlan
		result *export.Result
	)
	progErr := runWithProgress(cmd, func(ctx context.Context, reporter progress.Reporter) error {
		opts.Reporter = reporter

		planned, err := syncc.PlanPush(ctx, opts, prior)
		if err != nil {
			return err
		}
		plan = planned

		if !apply {
			return nil
		}

		runResult, err := applyPush(ctx, r, opts, planned, passphrase)
		if err != nil {
			return err
		}
		result = runResult
		return nil
	})

	return renderPushOutcome(cmd, r, opts, plan, result, apply, progErr)
}

// renderPushOutcome writes the push summary and, on apply, the tool warnings
// and the "Pushed:" confirmation. It runs after runWithProgress tears down the
// ledger: the ledger holds the terminal in raw mode, where a bare "\n" moves
// down without a carriage return, so writing the summary before teardown
// staircases every line. Failures writing the summary, the "(no changes)"
// hint, and the "Pushed:" confirmation fold into progErr; the tool warnings
// are best-effort stderr diagnostics whose write failures are not.
//
//nolint:gocritic // hugeParam: by-value PushOptions mirrors the public Plan/Execute contract.
func renderPushOutcome(
	cmd *cobra.Command,
	r *remote.Remote,
	opts syncc.PushOptions,
	plan *syncc.PushPlan,
	result *export.Result,
	apply bool,
	progErr error,
) error {
	if plan != nil {
		if rerr := plan.Render(cmd.OutOrStdout(), apply); rerr != nil {
			progErr = errors.Join(progErr, fmt.Errorf("render plan: %w", rerr))
		}
	}
	if progErr == nil && !apply {
		if _, werr := fmt.Fprintln(cmd.OutOrStdout(), "(no changes; pass --apply to commit)"); werr != nil {
			progErr = errors.Join(progErr, fmt.Errorf("write apply hint: %w", werr))
		}
	}
	if apply && result != nil {
		renderToolWarnings(cmd.ErrOrStderr(), opts.Targets, result.ByTool)
		if _, werr := fmt.Fprintf(cmd.OutOrStdout(), "Pushed: %s/%s\n", r.URL(), opts.Name); werr != nil {
			progErr = errors.Join(progErr, fmt.Errorf("write push confirmation: %w", werr))
		}
	}
	return progErr
}

// applyPush runs the cross-machine guard, opens the writer pipeline, and
// calls ExecutePush. It performs no rendering: the caller writes the summary
// and confirmation once the progress ledger has torn down.
//
//nolint:gocritic // hugeParam: by-value PushOptions mirrors the public Plan/Execute contract.
func applyPush(
	ctx context.Context,
	r *remote.Remote,
	opts syncc.PushOptions,
	plan *syncc.PushPlan,
	passphrase string,
) (result *export.Result, err error) {
	if plan.CrossMachine && !opts.Force {
		return nil, fmt.Errorf(
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
		return nil, fmt.Errorf("build writer pipeline: %w", err)
	}
	defer func() {
		if cerr := writer.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("close writer: %w", cerr))
		}
	}()

	uploadPhase := opts.Reporter.Phase("upload", plan.PriorSize, progress.UnitBytes)
	countingWriter := progress.CountingWriter(writer, uploadPhase)

	result, err = syncc.ExecutePush(ctx, opts, plan, countingWriter)
	if err != nil {
		return nil, err
	}
	uploadPhase.End("")
	return result, nil
}
