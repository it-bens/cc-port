package codex

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func rolloutFixturePath(home *Home, relative string) string {
	return filepath.Join(home.Dir, filepath.FromSlash(relative))
}

const (
	eraCPath = "sessions/2026/07/17/rollout-2026-07-17T10-00-00-00000000-0000-4000-8000-000000000001.jsonl"
	eraBPath = "sessions/2026/07/16/rollout-2026-07-16T08-00-00-00000000-0000-4000-8000-000000000003.jsonl"
	eraAPath = "sessions/2026/07/15/rollout-2026-07-15T07-00-00-00000000-0000-4000-8000-000000000004.jsonl"
)

func TestDiscoverRolloutFilesFindsBothRoots(t *testing.T) {
	home := SetupFixture(t)

	files, err := discoverRolloutFiles(home)

	require.NoError(t, err)
	assert.Contains(t, files, rolloutFixturePath(home, eraCPath))
	assert.Contains(t, files, rolloutFixturePath(home, "archived_sessions/rollout-2026-07-10T09-00-00-00000000-0000-4000-8000-000000000002.jsonl"))
}

func TestPlanRolloutFileEraCCountsStructuredAndProseUnderDeep(t *testing.T) {
	home := SetupFixture(t)
	path := rolloutFixturePath(home, eraCPath)

	shallow, eraA, err := planRolloutFile(path, FixtureProjectPath(), false)
	require.NoError(t, err)
	assert.False(t, eraA)
	assert.Equal(t, 3, shallow, "session_meta.cwd, turn_context.cwd, and turn_context.workspace_roots[0], not the prose response_item")

	deep, eraA, err := planRolloutFile(path, FixtureProjectPath(), true)
	require.NoError(t, err)
	assert.False(t, eraA)
	assert.Equal(t, 5, deep, "structured fields, structured free text, and the prose response_item under --deep")
}

func TestPlanRolloutFileEraBHasNoTurnContext(t *testing.T) {
	home := SetupFixture(t)
	path := rolloutFixturePath(home, eraBPath)

	shallow, eraA, err := planRolloutFile(path, FixtureProjectPath(), false)
	require.NoError(t, err)
	assert.False(t, eraA)
	assert.Equal(t, 1, shallow, "only session_meta.cwd; era-B predates turn_context")
}

func TestPlanRolloutFileEraAIsSkippedEvenUnderDeep(t *testing.T) {
	home := SetupFixture(t)
	path := rolloutFixturePath(home, eraAPath)

	for _, deep := range []bool{false, true} {
		count, eraA, err := planRolloutFile(path, FixtureProjectPath(), deep)
		require.NoError(t, err)
		assert.True(t, eraA, "no session_meta or turn_context line: era-A")
		assert.Equal(t, 0, count)
	}
}

func TestApplyRolloutFileRewritesStructuredFieldsAlways(t *testing.T) {
	home := SetupFixture(t)
	path := rolloutFixturePath(home, eraCPath)
	newPath := "/Users/fixture/renamed-project"

	changed, eraA, err := applyRolloutFile(context.Background(), path, FixtureProjectPath(), newPath, false)

	require.NoError(t, err)
	assert.False(t, eraA)
	assert.Equal(t, 3, changed)

	lines, _, err := readRolloutLines(path)
	require.NoError(t, err)
	require.Len(t, lines, 3)
	assert.Contains(t, string(lines[0]), newPath, "session_meta.cwd rewritten")
	assert.Contains(t, string(lines[1]), newPath, "turn_context.cwd and workspace_roots rewritten")
	assert.Contains(t, string(lines[2]), FixtureProjectPath(), "prose left untouched without --deep")
}

func TestApplyRolloutFileRewritesProseUnderDeep(t *testing.T) {
	home := SetupFixture(t)
	path := rolloutFixturePath(home, eraCPath)
	newPath := "/Users/fixture/renamed-project"

	changed, eraA, err := applyRolloutFile(context.Background(), path, FixtureProjectPath(), newPath, true)

	require.NoError(t, err)
	assert.False(t, eraA)
	assert.Equal(t, 5, changed)

	lines, _, err := readRolloutLines(path)
	require.NoError(t, err)
	for _, line := range lines {
		assert.NotContains(t, string(line), FixtureProjectPath())
	}
}

func TestRewriteRolloutLineScopesShallowModeToStructuredFields(t *testing.T) {
	line := []byte(`{"type":"session_meta","payload":{"cwd":"/Users/fixture/codexproject","note":"keep /Users/fixture/codexproject verbatim"}}`)

	shallow, shallowCount := rewriteRolloutLine(line, FixtureProjectPath(), "/Users/fixture/renamed-project", false)
	deep, deepCount := rewriteRolloutLine(line, FixtureProjectPath(), "/Users/fixture/renamed-project", true)

	assert.Equal(t, 1, shallowCount)
	assert.Contains(t, string(shallow), `"note":"keep /Users/fixture/codexproject verbatim"`)
	assert.Equal(t, 2, deepCount)
	assert.NotContains(t, string(deep), FixtureProjectPath())
}

func TestApplyRolloutFileLeavesEraAFileByteIdentical(t *testing.T) {
	home := SetupFixture(t)
	path := rolloutFixturePath(home, eraAPath)
	before, _, err := readRolloutLines(path)
	require.NoError(t, err)

	_, eraA, err := applyRolloutFile(context.Background(), path, FixtureProjectPath(), "/Users/fixture/renamed-project", true)
	require.NoError(t, err)
	require.True(t, eraA)

	after, _, err := readRolloutLines(path)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}
