package move_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/move"
	"github.com/it-bens/cc-port/internal/testutil"
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
	"github.com/it-bens/cc-port/internal/tool/codex"
)

// TestMove_MultiToolSymlinkAliasedProject_CodexThreadCwdSurvivesClaudeRemovingSource
// guards FIX-D end-to-end. move.Apply preflights every target's
// MoveSurfaces before any target applies, then applies targets one at a
// time in the given order (move.go's Apply). With Claude and Codex both
// selected, Claude's own apply physically removes the source project
// directory (applyProjectDirectoryMove's removeAll(req.OldPath)) before
// Codex's apply runs. Codex's thread row here is recorded through a
// symlink alias, so it must be rewritten from the canonical match set
// Codex's OWN preflight captured, before Claude's apply removed the
// directory, rather than by re-canonicalizing live at apply time, which
// would ENOENT against the now-missing source and silently leave the row
// stale.
func TestMove_MultiToolSymlinkAliasedProject_CodexThreadCwdSurvivesClaudeRemovingSource(t *testing.T) {
	tempRoot := t.TempDir()
	realProject := filepath.Join(tempRoot, "real", "project")
	require.NoError(t, os.MkdirAll(realProject, 0o750))
	require.NoError(t, os.Symlink(filepath.Join(tempRoot, "real"), filepath.Join(tempRoot, "link")))
	aliasedCWD := filepath.Join(tempRoot, "link", "project")
	newPath := filepath.Join(tempRoot, "real", "renamed-project")

	claudeHome := testutil.SetupFixture(t)
	setUpClaudeProjectFixture(t, claudeHome, realProject)

	codexHome := codex.SetupFixture(t)
	const threadID = "00000000-0000-4000-8000-0000000000ee"
	codex.InsertThreadRow(t, codexHome, threadID, aliasedCWD)

	targets := []tool.Target{
		{Tool: claude.New(), Workspace: claude.NewWorkspace(claudeHome)},
		{Tool: codex.New(), Workspace: quietCodexWorkspace(codexHome)},
	}

	result, err := move.Apply(t.Context(), targets, move.Options{OldPath: realProject, NewPath: newPath})

	require.NoError(t, err)
	require.Len(t, result.ByTool, 2)
	require.True(t, result.ByTool[0].Success, "claude apply: %v", result.ByTool[0].Err)
	require.True(t, result.ByTool[1].Success, "codex apply: %v", result.ByTool[1].Err)
	assert.NoDirExists(t, realProject, "sanity: Claude's apply must have physically removed the source directory")

	codexResult := result.ByTool[1]
	var stateDBCount int
	for _, surface := range codexResult.Surfaces {
		if surface.Name == "state-db" {
			stateDBCount = surface.Count
		}
	}
	assert.Positive(t, stateDBCount, "sanity: codex's state-db surface must have counted the symlink-aliased thread")
	assert.Equal(t, newPath, codex.ThreadCWD(t, codexHome, threadID),
		"Codex's own preflight-captured plan must still rewrite the symlink-aliased thread row after Claude removed the source")
}

// setUpClaudeProjectFixture adds a minimal project under claudeHome for
// projectPath: an encoded project directory holding one session transcript,
// plus a matching sessions/*.json witness, so Claude's move identity check
// (verifyProjectMoveIdentity) corroborates a fresh move instead of refusing
// it as uncorroborated. testutil.SetupFixture's own canned project is keyed
// to a fixed path unrelated to projectPath, so a cross-tool test needing a
// real, symlink-aliased project directory (for Codex's canonical
// thread-cwd matching) must build its own Claude project fixture rather
// than reuse the canned one.
func setUpClaudeProjectFixture(t *testing.T, claudeHome *claude.Home, projectPath string) {
	t.Helper()
	const sessionUUID = "eeeeeeee-0000-4000-8000-000000000010"
	// Exceeds the real PID ceiling so processLiveness never mistakes this
	// fixture session for a live writer, mirroring testutil.SetupFixture's
	// own dead-PID sanitization of its canned sessions.
	const deadPID = "2000000001"

	projectDir := claudeHome.ProjectDir(projectPath)
	require.NoError(t, os.MkdirAll(projectDir, 0o750))
	transcript := `{"type":"system","subtype":"bridge_status","content":"","isMeta":false,` +
		`"timestamp":"2026-01-15T10:00:00.000Z","uuid":"00000000-aaaa-bbbb-cccc-000000000101",` +
		`"userType":"external","entrypoint":"cli","cwd":"` + projectPath + `","sessionId":"` + sessionUUID +
		`","version":"2.1.0","gitBranch":"main","parentUuid":null,"isSidechain":false}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, sessionUUID+".jsonl"), []byte(transcript), 0o600))

	sessionWitness := `{"pid":` + deadPID + `,"sessionId":"` + sessionUUID + `","cwd":"` + projectPath +
		`","startedAt":1700200000000,"kind":"interactive","entrypoint":"cli"}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(claudeHome.SessionsDir(), "88888.json"), []byte(sessionWitness), 0o600))
}
