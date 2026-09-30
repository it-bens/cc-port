package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/lock"
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
)

// liveWriterWorkspace is a codexOnlyWorkspace whose witness reports a fixed
// set of live writers.
type liveWriterWorkspace struct {
	codexOnlyWorkspace
	writers []tool.ActiveWriter
}

func (workspace *liveWriterWorkspace) ActiveWriters() ([]tool.ActiveWriter, error) {
	return workspace.writers, nil
}

func TestIgnoreLiveSessionsWithoutApplyIsAUsageError(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{name: "move", args: []string{"move", "/Users/test/Projects/old", "/Users/test/Projects/new"}},
		{name: "import", args: []string{"import", "archive.zip", "/Users/test/Projects/imported"}},
		{name: "pull", args: []string{"pull", "myproj", "--to", "/Users/test/Projects/pulled", "--remote", "file:///tmp/x"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			rootCmd, _ := captureRootCmd(t, append(testCase.args, "--ignore-live-sessions"))

			err := rootCmd.Execute()

			usage, ok := errors.AsType[*usageError](err)
			require.True(t, ok, "err = %v, want *usageError", err)
			assert.EqualError(t, usage, "--ignore-live-sessions requires --apply")
		})
	}
}

func TestRenderIgnoredWritersNamesTheToolAndTheWriter(t *testing.T) {
	ignored := &lock.IgnoredWriters{}
	targets := []tool.Target{
		{Tool: claude.New(), Workspace: &liveWriterWorkspace{writers: []tool.ActiveWriter{
			{Pid: 4821, Cwd: "/Users/test/Projects/other"},
		}}},
		{Tool: &codexOnlyTool{}, Workspace: &liveWriterWorkspace{writers: []tool.ActiveWriter{
			{Detail: "busy database state_5.sqlite"},
		}}},
	}
	for _, target := range targets {
		_, err := lock.WitnessFor(target, ignored)()
		require.NoError(t, err)
	}
	var stderr bytes.Buffer

	require.NoError(t, renderIgnoredWriters(&stderr, targets, ignored))

	assert.Equal(t,
		"Warning: ignored live Claude Code writer: pid=4821 cwd=\"/Users/test/Projects/other\"\n"+
			"Warning: ignored live OpenAI Codex writer: busy database state_5.sqlite\n",
		stderr.String(),
	)
}

// writeLiveClaudeSession registers the test process as a live Claude Code
// session working in cwd, and returns the line the ignored-writer output
// prints for it.
func writeLiveClaudeSession(t *testing.T, home *claude.Home, cwd string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(home.SessionsDir(), 0o750))
	session, err := json.Marshal(claude.SessionFile{Cwd: cwd, Pid: os.Getpid()})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home.SessionsDir(), "live.json"), session, 0o600))
	return fmt.Sprintf("Warning: ignored live Claude Code writer: pid=%d cwd=%q\n", os.Getpid(), cwd)
}

// ignoredWriterFailer is a stderr whose writes of the ignored-writer lines
// fail and whose other writes pass through. It isolates a render failure from
// every other write a command makes.
type ignoredWriterFailer struct {
	buffer bytes.Buffer
}

func (failer *ignoredWriterFailer) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte("ignored live ")) {
		return 0, errors.New("stderr closed")
	}
	return failer.buffer.Write(p)
}

// executeCmdStreams runs the real root command with args, routing stderr
// through stderrWriter, and returns its stdout and error. Progress output goes
// to a temp file.
func executeCmdStreams(t *testing.T, stderrWriter io.Writer, args ...string) (string, error) {
	t.Helper()
	redirectProgressSink(t)
	rootCmd := newRootCmd(noopBanner{})
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(stderrWriter)
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return stdout.String(), err
}

// executeCmd runs the real root command with args and returns its stderr
// and error.
func executeCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var stderr bytes.Buffer
	_, err := executeCmdStreams(t, &stderr, args...)
	return stderr.String(), err
}
