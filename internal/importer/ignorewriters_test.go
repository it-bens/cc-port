package importer_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/archive"
	"github.com/it-bens/cc-port/internal/importer"
	"github.com/it-bens/cc-port/internal/lock"
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
	"github.com/it-bens/cc-port/internal/tool/codex"
)

// The lock-order fakes stage nothing, so these tests run a real Claude
// workspace whose witness reads a session file from the fixture home.

func TestRun_IgnoredWriterReportedAtLockAndRecheckStillPromotes(t *testing.T) {
	sessionID := "11111111-1111-4111-8111-111111111111"
	projectPath := "/Users/test/Projects/ignored-writer"
	body := buildClaudeArchive(t, map[string]string{
		"claude/sessions/" + sessionID + ".jsonl": "{}\n",
		"claude/config.json":                      `{"setting":"ported"}`,
	})
	home := blankHome(t)
	require.NoError(t, os.MkdirAll(home.SessionsDir(), 0o750))
	witness := []byte(`{"cwd":"/Users/test/Projects/other","pid":4242}`)
	require.NoError(t, os.WriteFile(filepath.Join(home.SessionsDir(), "4242.json"), witness, 0o600))
	fakeUserHome := t.TempDir()
	livenessCalls := 0
	workspace := claude.NewWorkspaceForTest(home,
		func(key string) string {
			if key == "HOME" {
				return fakeUserHome
			}
			return ""
		},
		func(int) bool {
			livenessCalls++
			return true
		},
		func(int) (time.Time, error) {
			t.Fatal("the witness session file carries no procStart, so its start time must not be read")
			return time.Time{}, nil
		})
	toolSet := tool.NewSet(claude.New())
	targets := []tool.Target{{Tool: toolSet.All()[0], Workspace: workspace}}
	ignored := &lock.IgnoredWriters{}

	_, err := importer.Run(t.Context(), toolSet, targets, &importer.Options{
		Source:         bytes.NewReader(body),
		Size:           int64(len(body)),
		TargetPath:     projectPath,
		Caps:           archive.DefaultCaps(),
		IgnoredWriters: ignored,
	})

	require.NoError(t, err)
	assert.Equal(t, 2, livenessCalls, "the writer must be reported at lock time and again before promotion")
	assert.FileExists(t, filepath.Join(home.ProjectDir(projectPath), sessionID+".jsonl"))
	assert.FileExists(t, home.ConfigFile)
	assert.Equal(t, []lock.IgnoredWriter{
		{Tool: "claude", Writer: tool.ActiveWriter{Pid: 4242, Cwd: "/Users/test/Projects/other"}},
	}, ignored.List())
}

// withAllLocks creates the second target's witness inside its own recursion,
// so a collector dropped there would let the second target's live writer
// refuse the import even under --ignore-live-sessions. Targets are locked in
// registry order, so Codex is witnessed second.
func TestRun_IgnoredWriterOnTheSecondTargetStillPromotesBothTools(t *testing.T) {
	claudeTool, codexTool := claude.New(), codex.New()
	body, sharedProject := buildMultiToolArchive(t)

	claudeDestination := blankHome(t)
	codexDestinationDir := filepath.Join(t.TempDir(), "dotcodex")
	require.NoError(t, os.MkdirAll(codexDestinationDir, 0o750))
	codexDestination := &codex.Home{Dir: codexDestinationDir, SQLiteDir: codexDestinationDir}
	witnessCalls := 0
	codexWorkspace := codex.NewWorkspace(
		codexDestination,
		func(string) string { return "" },
		func() ([]codex.ProcessInfo, error) {
			witnessCalls++
			return []codex.ProcessInfo{{PID: 4242, Name: "codex"}}, nil
		},
	)
	registry := tool.NewSet(claudeTool, codexTool)
	ignored := &lock.IgnoredWriters{}

	_, err := importer.Run(t.Context(), registry, []tool.Target{
		{Tool: claudeTool, Workspace: claude.NewWorkspace(claudeDestination)},
		{Tool: codexTool, Workspace: codexWorkspace},
	}, &importer.Options{
		Source:         bytes.NewReader(body),
		Size:           int64(len(body)),
		TargetPath:     sharedProject,
		Caps:           archive.DefaultCaps(),
		IgnoredWriters: ignored,
	})

	require.NoError(t, err)
	assert.Equal(t, 2, witnessCalls, "the Codex writer must be reported at lock time and again before promotion")
	require.FileExists(t, filepath.Join(
		claudeDestination.ProjectDir(sharedProject), "a1b2c3d4-0000-0000-0000-000000000001.jsonl",
	))
	require.FileExists(t, filepath.Join(
		codexDestination.Dir, "sessions", "2026", "07", "17",
		"rollout-2026-07-17T10-00-00-00000000-0000-4000-8000-000000000001.jsonl",
	))
	assert.Equal(t, []lock.IgnoredWriter{
		{Tool: "codex", Writer: tool.ActiveWriter{Pid: 4242}},
	}, ignored.List())
}

func TestRun_WitnessErrorRefusesWithIgnoredWritersAndPromotesNothing(t *testing.T) {
	sessionID := "11111111-1111-4111-8111-111111111111"
	projectPath := "/Users/test/Projects/unreadable-witness"
	body := buildClaudeArchive(t, map[string]string{
		"claude/sessions/" + sessionID + ".jsonl": "{}\n",
		"claude/config.json":                      `{"setting":"ported"}`,
	})
	home := blankHome(t)
	require.NoError(t, os.MkdirAll(home.SessionsDir(), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(home.SessionsDir(), "4242.json"), []byte(`{"pid":`), 0o600))
	toolSet := tool.NewSet(claude.New())
	targets := []tool.Target{{Tool: toolSet.All()[0], Workspace: claude.NewWorkspace(home)}}
	ignored := &lock.IgnoredWriters{}

	_, err := importer.Run(t.Context(), toolSet, targets, &importer.Options{
		Source:         bytes.NewReader(body),
		Size:           int64(len(body)),
		TargetPath:     projectPath,
		Caps:           archive.DefaultCaps(),
		IgnoredWriters: ignored,
	})

	require.ErrorIs(t, err, tool.ErrNoWitness)
	assert.NoFileExists(t, filepath.Join(home.ProjectDir(projectPath), sessionID+".jsonl"))
	assert.NoFileExists(t, home.ConfigFile)
	assert.Empty(t, ignored.List())
}
