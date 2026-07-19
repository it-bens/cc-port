// Package main implements the cc-port CLI.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"

	"github.com/spf13/cobra"
)

// Set by goreleaser ldflags.
var version = "dev"

// usageError marks an invocation-level error (bad flag, wrong arg count).
// main converts it to exit code 2. Runtime errors that lack this wrapper
// exit 1. Cobra's own flag errors are wrapped in SetFlagErrorFunc below.
type usageError struct{ err error }

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

// newRootCmd returns a fully-wired root cobra.Command. main() and tests
// each call it once. toolSet is the composition root's registry; toolFlags
// carries the generated --tool / --<name>-home persistent flag locals so
// every subcommand's RunE can call resolveTargets at call time, after
// cobra's flag parse has populated them.
func newRootCmd(banner Banner) *cobra.Command {
	toolSet := newToolSet()
	rootCmd := &cobra.Command{
		Use:   "cc-port",
		Short: "Coding-tool project portability tool",
		Long:  "Move, export, and import coding-tool project configuration and history.",
	}
	toolFlags := registerToolFlags(rootCmd, toolSet)
	// -v is reserved for --version because rootCmd.Version is set, so --verbose
	// takes no shorthand.
	rootCmd.PersistentFlags().BoolP("quiet", "q", false, "suppress progress output, show only errors")
	rootCmd.PersistentFlags().Bool("verbose", false, "show verbose progress detail")
	rootCmd.PersistentFlags().Bool("debug", false, "show debug-level progress detail")
	rootCmd.PersistentFlags().Bool("json", false, "emit machine-readable JSON instead of human-readable output")
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.Version = version
	rootCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &usageError{err: err}
	})

	defaultHelp := rootCmd.HelpFunc()
	rootCmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		// Capture cobra's help output into a buffer by swapping the
		// command's out writer, then render the banner beside it. The
		// defer restores the original writer so subsequent help calls
		// are unaffected.
		originalOut := cmd.OutOrStdout()
		var helpBuffer bytes.Buffer
		cmd.SetOut(&helpBuffer)
		defer cmd.SetOut(originalOut)
		defaultHelp(cmd, args)
		// Cosmetic banner+help write: SetHelpFunc has no error return,
		// and a failure to write here cannot be retried meaningfully
		// (the original help text is already in helpBuffer, not on the
		// terminal). Swallow rather than log.
		_ = banner.RenderBeside(originalOut, helpBuffer.String())
	})

	// Cobra writes the version template to stdout for the --version flag.
	// The template func cannot receive a writer; closing over the cobra
	// command lets it read OutOrStdout at template-eval time and pass
	// it explicitly to BesideString.
	cobra.AddTemplateFunc("ccPortVersionBanner", func() string {
		return banner.BesideString(rootCmd.OutOrStdout(), fmt.Sprintf("cc-port %s\n", rootCmd.Version))
	})
	rootCmd.SetVersionTemplate("{{ccPortVersionBanner}}")

	rootCmd.AddCommand(newVersionCmd(banner))
	rootCmd.AddCommand(newMoveCmd(toolSet, toolFlags))
	rootCmd.AddCommand(newExportCmd(toolSet, toolFlags, banner))
	rootCmd.AddCommand(newImportCmd(toolSet, toolFlags))
	rootCmd.AddCommand(newPushCmd(toolSet, toolFlags, banner))
	rootCmd.AddCommand(newPullCmd(toolSet, toolFlags))
	rootCmd.AddCommand(newStatsCmd(toolSet, toolFlags))

	return rootCmd
}

func newVersionCmd(banner Banner) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, _ []string) {
			// Cosmetic version-line write: cobra's Run has no error
			// return, and a failed write to the version surface has no
			// recovery path the caller could take. Swallow rather than
			// log.
			_ = banner.RenderBeside(cmd.OutOrStdout(), fmt.Sprintf("cc-port %s\n", version))
		},
	}
}

func main() {
	os.Exit(run())
}

// run wires and executes the root command under a SIGINT-cancellable context,
// returning the process exit code. main defers to it so the deferred
// signal-handler teardown runs before the process exits, which a direct
// os.Exit in main would skip.
func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	rootCmd := newRootCmd(bannerImpl)
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		var usage *usageError
		if errors.As(err, &usage) {
			return 2
		}
		return 1
	}
	return 0
}
