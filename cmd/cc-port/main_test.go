package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/tool/claude/claudetest"
	"github.com/it-bens/cc-port/internal/tool/codex/codextest"
)

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

// runTests defers each cleanup as soon as its resource exists, so every exit
// path removes what setup created.
func runTests(m *testing.M) (exitCode int) {
	templateCleanup, err := codextest.InstallTemplate()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() {
		if err := templateCleanup(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			exitCode = 1
		}
	}()

	restore, err := claudetest.IsolateHome()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() {
		if err := restore(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			exitCode = 1
		}
	}()

	return m.Run()
}

// stubBanner is the Banner the command tests pass to newRootCmd and the
// command constructors regardless of build tags: Render writes nothing,
// RenderBeside writes the supplied text, and BesideString returns it unchanged.
type stubBanner struct{}

func (stubBanner) Render(io.Writer) error { return nil }

func (stubBanner) RenderBeside(out io.Writer, text string) error {
	_, err := io.WriteString(out, text)
	return err
}

func (stubBanner) BesideString(_ io.Writer, text string) string {
	return text
}

// captureRootCmd builds a fresh rootCmd via newRootCmd(stubBanner{}),
// redirects its output and error streams to a shared buffer, and sets
// the arg vector. Each test owns its rootCmd, so flag state cannot
// leak across tests.
func captureRootCmd(t *testing.T, args []string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	var buffer bytes.Buffer
	rootCmd := newRootCmd(stubBanner{})
	rootCmd.SetOut(&buffer)
	rootCmd.SetErr(&buffer)
	rootCmd.SetArgs(args)
	return rootCmd, &buffer
}

func TestRootCommand_SilenceUsageOnRuntimeError(t *testing.T) {
	rootCmd := newRootCmd(stubBanner{})
	require.True(t, rootCmd.SilenceUsage, "rootCmd must silence usage on runtime errors")
	require.True(t, rootCmd.SilenceErrors, "rootCmd must silence error print (main owns it)")
}

func TestRootCommand_VersionFlagPresent(t *testing.T) {
	rootCmd := newRootCmd(stubBanner{})
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
