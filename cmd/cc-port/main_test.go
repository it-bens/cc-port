package main

import (
	"bytes"
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureRootCmd builds a fresh rootCmd via newRootCmd(), redirects its
// output and error streams to a shared buffer, and sets the arg vector.
// Each test owns its rootCmd, so flag state cannot leak across tests.
func captureRootCmd(t *testing.T, args []string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	var buffer bytes.Buffer
	rootCmd := newRootCmd()
	rootCmd.SetOut(&buffer)
	rootCmd.SetErr(&buffer)
	rootCmd.SetArgs(args)
	return rootCmd, &buffer
}

func TestRootCommand_SilenceUsageOnRuntimeError(t *testing.T) {
	rootCmd := newRootCmd()
	require.True(t, rootCmd.SilenceUsage, "rootCmd must silence usage on runtime errors")
	require.True(t, rootCmd.SilenceErrors, "rootCmd must silence error print (main owns it)")
}

func TestRootCommand_VersionFlagPresent(t *testing.T) {
	rootCmd := newRootCmd()
	assert.NotEmpty(t, rootCmd.Version, "rootCmd.Version must be set so --version registers")
}

func TestUsageError_IsIdentifiable(t *testing.T) {
	underlying := errors.New("bad flag")
	wrapped := &usageError{err: underlying}

	var target *usageError
	require.ErrorAs(t, wrapped, &target)

	assert.Equal(t, underlying, target.Unwrap())
}

func TestRootCommandVersionFlagPrintsVersion(t *testing.T) {
	rootCmd, buffer := captureRootCmd(t, []string{"--version"})

	require.NoError(t, rootCmd.Execute())

	output := buffer.String()
	assert.Contains(t, output, "cc-port")
	assert.Contains(t, output, version)
}

func TestRootCommandHelpFlagPrintsUsageAndCommands(t *testing.T) {
	rootCmd, buffer := captureRootCmd(t, []string{"--help"})

	require.NoError(t, rootCmd.Execute())

	output := buffer.String()
	assert.Contains(t, output, "Move, export, and import")
	assert.Contains(t, output, "Usage:")
	assert.Contains(t, output, "Available Commands:")
	assert.Contains(t, output, "version")
	assert.Contains(t, output, "export")
	assert.Contains(t, output, "import")
	assert.Contains(t, output, "move")
}

func TestVersionSubcommandPrintsVersion(t *testing.T) {
	rootCmd, buffer := captureRootCmd(t, []string{"version"})

	require.NoError(t, rootCmd.Execute())

	output := buffer.String()
	assert.Contains(t, output, "cc-port")
	assert.Contains(t, output, version)
}
