// Package main implements the cc-port CLI.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/it-bens/cc-port/internal/logo"
)

// Set by goreleaser ldflags.
var version = "dev"

// usageError marks an invocation-level error (bad flag, wrong arg count).
// main converts it to exit code 2. Runtime errors that lack this wrapper
// exit 1. Cobra's own flag errors are wrapped in SetFlagErrorFunc below.
type usageError struct{ err error }

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		var usage *usageError
		if errors.As(err, &usage) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

var claudeDir string

var rootCmd = &cobra.Command{
	Use:   "cc-port",
	Short: "Claude Code project portability tool",
	Long:  "Move, export, and import Claude Code project configuration and history.",
}

func init() {
	rootCmd.PersistentFlags().StringVar(&claudeDir, "claude-dir", "", "override ~/.claude location")
	rootCmd.AddCommand(versionCmd)
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.Version = version
	rootCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &usageError{err: err}
	})

	defaultHelp := rootCmd.HelpFunc()
	rootCmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		// Capture cobra's help output into a buffer by swapping the
		// command's out writer, then render the logo beside it. The
		// defer restores the original writer so subsequent help calls
		// are unaffected.
		originalOut := cmd.OutOrStdout()
		var helpBuffer bytes.Buffer
		cmd.SetOut(&helpBuffer)
		defer cmd.SetOut(originalOut)
		defaultHelp(cmd, args)
		_ = logo.RenderBeside(originalOut, helpBuffer.String())
	})

	// Cobra writes the version template to stdout for the --version flag.
	// The template func cannot receive a writer, so BesideString gates
	// on os.Stdout directly to match what cobra will write to.
	cobra.AddTemplateFunc("ccPortVersionBanner", func() string {
		return logo.BesideString(fmt.Sprintf("cc-port %s\n", rootCmd.Version))
	})
	rootCmd.SetVersionTemplate("{{ccPortVersionBanner}}")
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, _ []string) {
		_ = logo.RenderBeside(cmd.OutOrStdout(), fmt.Sprintf("cc-port %s\n", version))
	},
}
