package main

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureRootCmd redirects rootCmd's output and error streams to a
// shared buffer and resets the command's arg vector on test completion.
// Package-level rootCmd state is shared across tests; the cleanup keeps
// a test's configuration from leaking into its neighbors.
func captureRootCmd(t *testing.T, args []string) *bytes.Buffer {
	t.Helper()
	var buffer bytes.Buffer
	rootCmd.SetOut(&buffer)
	rootCmd.SetErr(&buffer)
	rootCmd.SetArgs(args)
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	return &buffer
}

func TestRootCommand_SilenceUsageOnRuntimeError(t *testing.T) {
	require.True(t, rootCmd.SilenceUsage, "rootCmd must silence usage on runtime errors")
	require.True(t, rootCmd.SilenceErrors, "rootCmd must silence error print (main owns it)")
}

func TestRootCommand_VersionFlagPresent(t *testing.T) {
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
	buffer := captureRootCmd(t, []string{"--version"})

	require.NoError(t, rootCmd.Execute())

	output := buffer.String()
	assert.Contains(t, output, "cc-port")
	assert.Contains(t, output, version)
}

func TestRootCommandHelpFlagPrintsUsageAndCommands(t *testing.T) {
	buffer := captureRootCmd(t, []string{"--help"})

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
	buffer := captureRootCmd(t, []string{"version"})

	require.NoError(t, rootCmd.Execute())

	output := buffer.String()
	assert.Contains(t, output, "cc-port")
	assert.Contains(t, output, version)
}
