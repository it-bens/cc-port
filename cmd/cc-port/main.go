// Package main implements the cc-port CLI.
package main

import (
	"errors"
	"fmt"
	"os"

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
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Printf("cc-port %s\n", version)
	},
}
