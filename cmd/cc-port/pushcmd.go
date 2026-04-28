package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/remote"
	syncc "github.com/it-bens/cc-port/internal/sync"
	"github.com/it-bens/cc-port/internal/ui"
)

var (
	pushAsName         string
	pushRemoteURL      string
	pushApply          bool
	pushForce          bool
	pushPassphraseEnv  string
	pushPassphraseFile string
	pushFromManifest   string
)

var pushCmd = &cobra.Command{
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
	RunE: runPushCmd,
}

// runPushCmd is the push subcommand body. The named return + deferred
// remote close pattern is load-bearing: remote.Sink commits the upload
// on Close, so a failed close must surface to the caller.
func runPushCmd(cmd *cobra.Command, args []string) (err error) {
	if pushAsName == "" {
		return &usageError{err: errors.New("--as <name> is required")}
	}
	if pushRemoteURL == "" {
		return &usageError{err: errors.New("--remote <url> is required")}
	}

	passphrase, err := resolvePassphrase(pushPassphraseEnv, pushPassphraseFile)
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

	categories, placeholders, err := resolvePushCategoriesAndPlaceholders(cmd, claudeHome, projectPath)
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	r, err := remote.New(ctx, pushRemoteURL)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := r.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("close remote: %w", cerr))
		}
	}()

	opts := syncc.PushOptions{
		ClaudeHome:   claudeHome,
		ProjectPath:  projectPath,
		Remote:       r,
		Name:         pushAsName,
		Categories:   categories,
		Placeholders: placeholders,
		Passphrase:   passphrase,
		Force:        pushForce,
	}

	plan, err := syncc.PlanPush(ctx, opts)
	if err != nil {
		return err
	}
	if err := plan.Render(cmd.OutOrStdout()); err != nil {
		return fmt.Errorf("render plan: %w", err)
	}

	if !pushApply {
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), "(no changes; pass --apply to commit)"); err != nil {
			return fmt.Errorf("write apply hint: %w", err)
		}
		return nil
	}

	if plan.CrossMachine && !pushForce {
		return fmt.Errorf(
			"%w: prior pushed by %s at %s",
			syncc.ErrCrossMachineConflict,
			plan.PriorPushedBy,
			plan.PriorPushedAt.Format("2006-01-02T15:04:05Z"),
		)
	}

	if err := syncc.ExecutePush(ctx, opts, plan); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Pushed: %s/%s\n", r.URL(), pushAsName); err != nil {
		return fmt.Errorf("write push confirmation: %w", err)
	}
	return nil
}

// resolvePushCategoriesAndPlaceholders mirrors cc-port export's branch:
// --from-manifest carries categories AND placeholders; without it, read
// categories from flags (or prompt via ui.SelectCategories when none
// are set) and run discoverAndPromptPlaceholders for placeholder
// confirmation.
func resolvePushCategoriesAndPlaceholders(
	cmd *cobra.Command, claudeHome *claude.Home, projectPath string,
) (manifest.CategorySet, []manifest.Placeholder, error) {
	if pushFromManifest != "" {
		metadata, err := manifest.ReadManifest(pushFromManifest)
		if err != nil {
			return manifest.CategorySet{}, nil, fmt.Errorf("read manifest: %w", err)
		}
		categories, err := manifest.ApplyCategoryEntries(metadata.Export.Categories)
		if err != nil {
			return manifest.CategorySet{}, nil, fmt.Errorf("categories from manifest: %w", err)
		}
		return categories, metadata.Placeholders, nil
	}

	categories, err := resolveCategoriesFromCmd(cmd)
	if err != nil {
		return manifest.CategorySet{}, nil, err
	}
	anySet := false
	for _, spec := range manifest.AllCategories {
		if spec.Value(&categories) {
			anySet = true
			break
		}
	}
	if !anySet {
		categories, err = ui.SelectCategories()
		if err != nil {
			return manifest.CategorySet{}, nil, err
		}
	}
	placeholders, err := discoverAndPromptPlaceholders(claudeHome, projectPath)
	if err != nil {
		return manifest.CategorySet{}, nil, err
	}
	return categories, placeholders, nil
}

func init() {
	pushCmd.Flags().StringVar(&pushAsName, "as", "",
		"stable name for the archive on the remote")
	pushCmd.Flags().StringVar(&pushRemoteURL, "remote", "",
		"remote URL (file://path or s3://bucket?region=...)")
	pushCmd.Flags().BoolVar(&pushApply, "apply", false,
		"commit the upload (default is dry-run)")
	pushCmd.Flags().BoolVar(&pushForce, "force", false,
		"override cross-machine conflict refusal")
	pushCmd.Flags().StringVar(&pushPassphraseEnv, "passphrase-env", "",
		"name of env var containing the encryption passphrase "+
			"(mutually exclusive with --passphrase-file)")
	pushCmd.Flags().StringVar(&pushPassphraseFile, "passphrase-file", "",
		"path to a file containing the encryption passphrase "+
			"(mutually exclusive with --passphrase-env)")
	pushCmd.Flags().StringVar(&pushFromManifest, "from-manifest", "",
		"path to a manifest XML with categories and placeholder declarations")
	pushCmd.MarkFlagsMutuallyExclusive("passphrase-env", "passphrase-file")
	registerCategoryFlags(pushCmd, "push")
	rootCmd.AddCommand(pushCmd)
}
