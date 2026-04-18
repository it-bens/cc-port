// Package main implements the cc-port CLI.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Set by goreleaser ldflags.
var version = "dev"

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
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
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Printf("cc-port %s\n", version)
	},
}
