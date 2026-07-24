package importer_test

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/archive"
	"github.com/it-bens/cc-port/internal/export"
	"github.com/it-bens/cc-port/internal/importer"
	"github.com/it-bens/cc-port/internal/lock"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/move"
	"github.com/it-bens/cc-port/internal/testutil"
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
	"github.com/it-bens/cc-port/internal/tool/codex"
)

func allSelected(t tool.Tool) map[string]bool {
	selected := make(map[string]bool)
	for _, category := range t.Categories() {
		selected[category.Name] = true
	}
	return selected
}

func buildArchive(t *testing.T) (body []byte, projectPath string) {
	t.Helper()
	home := testutil.SetupFixture(t)
	claudeTool := claude.New()
	targets := []tool.Target{{Tool: claudeTool, Workspace: claude.NewWorkspace(home)}}
	projectPath = testutil.FixtureProjectPath()

	var buf bytes.Buffer
	_, err := export.Run(context.Background(), targets, &export.Options{
		ProjectPath: projectPath,
		Output:      &buf,
		Selected:    map[string]map[string]bool{claudeTool.Name(): allSelected(claudeTool)},
	})
	require.NoError(t, err)
	return buf.Bytes(), projectPath
}

func blankHome(t *testing.T) *claude.Home {
	t.Helper()
	dir := t.TempDir()
	home := &claude.Home{Dir: filepath.Join(dir, "dotclaude"), ConfigFile: filepath.Join(dir, "dotclaude.json")}
	require.NoError(t, os.MkdirAll(home.Dir, 0o700))
	return home
}

func TestRun_RoundTripStagesSessionsIntoFreshHome(t *testing.T) {
	body, projectPath := buildArchive(t)
	home := blankHome(t)
	toolSet := tool.NewSet(claude.New())
	targets := []tool.Target{{Tool: toolSet.All()[0], Workspace: claude.NewWorkspace(home)}}

	_, err := importer.Run(context.Background(), toolSet, targets, &importer.Options{
		Source:     bytes.NewReader(body),
		Size:       int64(len(body)),
		TargetPath: projectPath,
		Caps:       archive.DefaultCaps(),
	})
	require.NoError(t, err)

	encodedDir := home.ProjectDir(projectPath)
	entries, err := os.ReadDir(encodedDir)
	require.NoError(t, err)
	assert.NotEmpty(t, entries, "encoded project directory must be populated after import")

	historyBytes, err := os.ReadFile(home.HistoryFile())
	require.NoError(t, err)
	assert.Contains(t, string(historyBytes), projectPath)
}

func TestRun_ReRunDoesNotDuplicateHistoryLines(t *testing.T) {
	body, projectPath := buildArchive(t)
	home := blankHome(t)
	toolSet := tool.NewSet(claude.New())
	targets := []tool.Target{{Tool: toolSet.All()[0], Workspace: claude.NewWorkspace(home)}}

	for i := range 2 {
		_, err := importer.Run(context.Background(), toolSet, targets, &importer.Options{
			Source:     bytes.NewReader(body),
			Size:       int64(len(body)),
			TargetPath: projectPath,
			Caps:       archive.DefaultCaps(),
		})
		require.NoError(t, err, "run %d", i)
	}

	historyBytes, err := os.ReadFile(home.HistoryFile())
	require.NoError(t, err)
	lines := bytes.Split(bytes.TrimRight(historyBytes, "\n"), []byte("\n"))

	seen := make(map[string]int)
	for _, line := range lines {
		seen[string(line)]++
	}
	for line, count := range seen {
		assert.Equalf(t, 1, count, "history line must not be duplicated by a re-import: %s", line)
	}
}

// TestRun_MultiToolArchiveImportsClaudeAndCodex exercises multi-tool mutation
// below the CLI. A real Codex CLI process is itself valid witness evidence, so
// cmd-level Codex import tests must refuse rather than weakening the witness.
func TestRun_MultiToolArchiveImportsClaudeAndCodex(t *testing.T) {
	sharedProject := codex.FixtureProjectPath()
	claudeSource := testutil.SetupFixture(t)
	claudeTool, codexTool := claude.New(), codex.New()

	moveResult, err := move.Apply(t.Context(), []tool.Target{{
		Tool: claudeTool, Workspace: claude.NewWorkspace(claudeSource),
	}}, move.Options{OldPath: testutil.FixtureProjectPath(), NewPath: sharedProject, RefsOnly: true})
	require.NoError(t, err)
	require.False(t, moveResult.Failed())

	codexSource := codex.SetupFixture(t)
	claudeSelection := map[string]bool{"sessions": true}
	codexSelection := map[string]bool{"sessions": true}
	claudeWorkspace := claude.NewWorkspace(claudeSource)
	codexWorkspace := quietCodexWorkspace(codexSource)
	claudePlaceholders, err := claudeWorkspace.Placeholders(sharedProject, claudeSelection)
	require.NoError(t, err)
	codexPlaceholders, err := codexWorkspace.Placeholders(sharedProject, codexSelection)
	require.NoError(t, err)

	var archiveBytes bytes.Buffer
	_, err = export.Run(t.Context(), []tool.Target{
		{Tool: claudeTool, Workspace: claudeWorkspace},
		{Tool: codexTool, Workspace: codexWorkspace},
	}, &export.Options{
		ProjectPath: sharedProject,
		Output:      &archiveBytes,
		Selected: map[string]map[string]bool{
			claudeTool.Name(): claudeSelection,
			codexTool.Name():  codexSelection,
		},
		Placeholders: map[string][]manifest.Placeholder{
			claudeTool.Name(): claudePlaceholders,
			codexTool.Name():  codexPlaceholders,
		},
	})
	require.NoError(t, err)

	claudeDestination := blankHome(t)
	codexDestinationDir := filepath.Join(t.TempDir(), "dotcodex")
	require.NoError(t, os.MkdirAll(codexDestinationDir, 0o750))
	config := []byte("# recipient config remains local\n[projects.\"/recipient/only\"]\ntrust_level = \"trusted\"\n")
	require.NoError(t, os.WriteFile(filepath.Join(codexDestinationDir, "config.toml"), config, 0o600))
	codexDestination := &codex.Home{Dir: codexDestinationDir, SQLiteDir: codexDestinationDir}

	registry := tool.NewSet(claudeTool, codexTool)
	reader := bytes.NewReader(archiveBytes.Bytes())
	result, err := importer.Run(t.Context(), registry, []tool.Target{
		{Tool: claudeTool, Workspace: claude.NewWorkspace(claudeDestination)},
		{Tool: codexTool, Workspace: quietCodexWorkspace(codexDestination)},
	}, &importer.Options{Source: reader, Size: int64(reader.Len()), TargetPath: sharedProject, Caps: archive.DefaultCaps()})
	require.NoError(t, err)

	require.FileExists(t, filepath.Join(
		claudeDestination.ProjectDir(sharedProject), "a1b2c3d4-0000-0000-0000-000000000001.jsonl",
	))
	require.FileExists(t, filepath.Join(
		codexDestination.Dir, "sessions", "2026", "07", "17",
		"rollout-2026-07-17T10-00-00-00000000-0000-4000-8000-000000000001.jsonl",
	))
	actualConfig, err := os.ReadFile(filepath.Join(codexDestination.Dir, "config.toml"))
	require.NoError(t, err)
	assert.Equal(t, config, actualConfig, "Codex config.toml must remain byte-identical")
	require.NotEmpty(t, result.Warnings[codexTool.Name()])
	assert.Contains(t, result.Warnings[codexTool.Name()][0], "threads sidecar row(s) could not be applied")
}

func quietCodexWorkspace(home *codex.Home) *codex.Workspace {
	return codex.NewWorkspace(
		home,
		func(string) string { return "" },
		func() ([]codex.ProcessInfo, error) { return nil, nil },
	)
}

func TestRun_RejectsIncomingHistoryLineAtScannerCapWithoutChangingTarget(t *testing.T) {
	body := buildClaudeArchive(t, map[string]string{
		"claude/history/history.jsonl": "first\n" + strings.Repeat("x", claude.MaxHistoryLine) + "\nlast",
	})
	home := blankHome(t)
	existing := []byte("existing history line\n")
	require.NoError(t, os.WriteFile(home.HistoryFile(), existing, 0o600))
	claudeTool := claude.New()
	toolSet := tool.NewSet(claudeTool)
	targets := []tool.Target{{Tool: claudeTool, Workspace: claude.NewWorkspace(home)}}

	_, err := importer.Run(t.Context(), toolSet, targets, &importer.Options{
		Source: bytes.NewReader(body), Size: int64(len(body)), TargetPath: "/Users/test/Projects/history-cap", Caps: archive.DefaultCaps(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "16777216")
	assert.Contains(t, err.Error(), "history/history.jsonl")
	actual, readErr := os.ReadFile(home.HistoryFile())
	require.NoError(t, readErr)
	assert.Equal(t, existing, actual)
}

func TestRun_ImportsIncomingHistoryLineBelowScannerCap(t *testing.T) {
	line := strings.Repeat("x", claude.MaxHistoryLine-1)
	body := buildClaudeArchive(t, map[string]string{
		"claude/history/history.jsonl": line,
	})
	home := blankHome(t)
	claudeTool := claude.New()
	toolSet := tool.NewSet(claudeTool)
	targets := []tool.Target{{Tool: claudeTool, Workspace: claude.NewWorkspace(home)}}

	_, err := importer.Run(t.Context(), toolSet, targets, &importer.Options{
		Source: bytes.NewReader(body), Size: int64(len(body)), TargetPath: "/Users/test/Projects/history-cap", Caps: archive.DefaultCaps(),
	})

	require.NoError(t, err)
	actual, readErr := os.ReadFile(home.HistoryFile())
	require.NoError(t, readErr)
	assert.Equal(t, line+"\n", string(actual))
}

// buildArchiveWithUnregisteredTool builds a minimal, well-formed archive
// whose manifest declares a tool this test's registry never registers.
func buildArchiveWithUnregisteredTool(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)

	entry, err := writer.Create("bogus/note.txt")
	require.NoError(t, err)
	_, err = entry.Write([]byte("hello"))
	require.NoError(t, err)

	_, err = archive.WriteMetadata(writer, &manifest.Metadata{
		Tools: []manifest.Tool{{Name: "bogus"}},
	})
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return buf.Bytes()
}

func TestRun_UnregisteredManifestToolFailsHard(t *testing.T) {
	body := buildArchiveWithUnregisteredTool(t)

	home := blankHome(t)
	toolSet := tool.NewSet(claude.New())
	targets := []tool.Target{{Tool: toolSet.All()[0], Workspace: claude.NewWorkspace(home)}}

	_, err := importer.Run(context.Background(), toolSet, targets, &importer.Options{
		Source:     bytes.NewReader(body),
		Size:       int64(len(body)),
		TargetPath: "/Users/test/Projects/demo",
		Caps:       archive.DefaultCaps(),
	})
	require.Error(t, err, "an archive naming an unregistered tool must fail hard, not silently skip")
}

func TestRun_StagingEnforcesAggregateCapWithoutPlaceholders(t *testing.T) {
	body := buildClaudeArchive(t, map[string]string{
		"claude/sessions/one.jsonl":   strings.Repeat("a", 1536),
		"claude/sessions/two.jsonl":   strings.Repeat("b", 1536),
		"claude/sessions/three.jsonl": strings.Repeat("c", 1536),
	})
	home := blankHome(t)
	toolSet := tool.NewSet(claude.New())
	targets := []tool.Target{{Tool: toolSet.All()[0], Workspace: claude.NewWorkspace(home)}}

	_, err := importer.Run(t.Context(), toolSet, targets, &importer.Options{
		Source:     bytes.NewReader(body),
		Size:       int64(len(body)),
		TargetPath: "/Users/test/Projects/capped",
		Caps:       archive.Caps{MaxEntryBytes: 4096, MaxAggregateBytes: 3072},
	})

	require.ErrorIs(t, err, archive.ErrAggregateCapExceeded)
	assert.Regexp(t, `claude/sessions/(one|two|three)\.jsonl`, err.Error())
}

func TestRun_StagingAggregateCapCountsShrinkingPlaceholderInput(t *testing.T) {
	body := buildClaudeArchiveWithPlaceholders(t, map[string]string{
		"claude/sessions/one.jsonl":   strings.Repeat("{{X}}", 12),
		"claude/sessions/two.jsonl":   strings.Repeat("{{X}}", 12),
		"claude/sessions/three.jsonl": strings.Repeat("{{X}}", 12),
	}, []manifest.Placeholder{{Key: "{{X}}", Resolve: "/"}})
	home := blankHome(t)
	claudeTool := claude.New()
	toolSet := tool.NewSet(claudeTool)
	targets := []tool.Target{{Tool: claudeTool, Workspace: claude.NewWorkspace(home)}}

	_, err := importer.Run(t.Context(), toolSet, targets, &importer.Options{
		Source:     bytes.NewReader(body),
		Size:       int64(len(body)),
		TargetPath: "/Users/test/Projects/capped",
		Caps:       archive.Caps{MaxEntryBytes: 64, MaxAggregateBytes: 100},
	})

	require.ErrorIs(t, err, archive.ErrAggregateCapExceeded)
	assert.Contains(t, err.Error(), "claude/sessions/three.jsonl")
	var capErr *archive.AggregateCapError
	require.ErrorAs(t, err, &capErr)
	assert.Equal(t, int64(101), capErr.Bytes)
}

func TestRun_StagingRejectsDotSegmentSessionPath(t *testing.T) {
	body := buildClaudeArchive(t, map[string]string{
		"claude/sessions/..": "payload",
	})
	home := blankHome(t)
	claudeTool := claude.New()
	toolSet := tool.NewSet(claudeTool)
	targets := []tool.Target{{Tool: claudeTool, Workspace: claude.NewWorkspace(home)}}

	_, err := importer.Run(t.Context(), toolSet, targets, &importer.Options{
		Source:     bytes.NewReader(body),
		Size:       int64(len(body)),
		TargetPath: "/Users/test/Projects/dot-segment",
		Caps:       archive.DefaultCaps(),
	})

	require.ErrorIs(t, err, archive.ErrZipSlip)
}

func TestRun_ClassificationEnforcesAggregateCapForUnresolvedPlaceholder(t *testing.T) {
	body := buildClaudeArchiveWithPlaceholders(t, map[string]string{
		"claude/sessions/one.jsonl": "{{ARCHIVE_PATH}}" + strings.Repeat("a", 1536),
		"claude/sessions/two.jsonl": strings.Repeat("b", 1536),
	}, []manifest.Placeholder{{Key: "{{ARCHIVE_PATH}}"}})
	home := blankHome(t)
	claudeTool := claude.New()
	toolSet := tool.NewSet(claudeTool)
	targets := []tool.Target{{Tool: claudeTool, Workspace: claude.NewWorkspace(home)}}

	_, err := importer.Run(t.Context(), toolSet, targets, &importer.Options{
		Source:     bytes.NewReader(body),
		Size:       int64(len(body)),
		TargetPath: "/Users/test/Projects/capped",
		Caps:       archive.Caps{MaxEntryBytes: 4096, MaxAggregateBytes: 3072},
	})

	require.ErrorIs(t, err, archive.ErrAggregateCapExceeded)
	assert.Contains(t, err.Error(), "claude/sessions/two.jsonl")
}

func TestRun_StagingFailureRemovesAllTemporaryFiles(t *testing.T) {
	assertStagingFailureRemovesAllTemporaryFiles(t, map[string]string{
		"claude/sessions/first.jsonl":       "small",
		"claude/sessions/zzz-failing.jsonl": strings.Repeat("x", 101),
	})
}

func TestRun_FileHistoryStagingFailureRemovesAllTemporaryFiles(t *testing.T) {
	assertStagingFailureRemovesAllTemporaryFiles(t, map[string]string{
		"claude/file-history/session/first":       "small",
		"claude/file-history/session/zzz-failing": strings.Repeat("x", 101),
	})
}

func TestRun_TodosStagingFailureRemovesAllTemporaryFiles(t *testing.T) {
	assertStagingFailureRemovesAllTemporaryFiles(t, map[string]string{
		"claude/todos/first.json":       "small",
		"claude/todos/zzz-failing.json": strings.Repeat("x", 101),
	})
}

func assertStagingFailureRemovesAllTemporaryFiles(t *testing.T, entries map[string]string) {
	t.Helper()
	body := buildClaudeArchive(t, entries)
	home := blankHome(t)
	toolSet := tool.NewSet(claude.New())
	targets := []tool.Target{{Tool: toolSet.All()[0], Workspace: claude.NewWorkspace(home)}}

	_, err := importer.Run(t.Context(), toolSet, targets, &importer.Options{
		Source:     bytes.NewReader(body),
		Size:       int64(len(body)),
		TargetPath: "/Users/test/Projects/cleanup",
		Caps:       archive.Caps{MaxEntryBytes: 100, MaxAggregateBytes: 4096},
	})

	require.ErrorIs(t, err, archive.ErrEntryCapExceeded)
	var temps []string
	walkErr := filepath.WalkDir(home.Dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".cc-port-import.tmp") {
			temps = append(temps, path)
		}
		return nil
	})
	require.NoError(t, walkErr)
	assert.Empty(t, temps)
}

func TestRun_AbortsWhenWitnessTurnsLiveBetweenLockAndPromotion(t *testing.T) {
	sessionID := "11111111-1111-4111-8111-111111111111"
	projectPath := "/Users/test/Projects/relock"
	body := buildClaudeArchive(t, map[string]string{
		"claude/sessions/" + sessionID + ".jsonl": "{}\n",
		"claude/config.json":                      `{"setting":"ported"}`,
	})
	home := blankHome(t)

	// Claude's witness reads sessions/<pid>.json and asks processLiveness per
	// PID, so this seam reports the writer dead at lock time and alive on the
	// pre-promotion re-check: a session launched mid-import.
	require.NoError(t, os.MkdirAll(home.SessionsDir(), 0o750))
	witness := []byte(`{"cwd":"/Users/test/Projects/relock","pid":4242}`)
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
			return livenessCalls >= 2
		})
	toolSet := tool.NewSet(claude.New())
	targets := []tool.Target{{Tool: toolSet.All()[0], Workspace: workspace}}

	_, err := importer.Run(t.Context(), toolSet, targets, &importer.Options{
		Source:     bytes.NewReader(body),
		Size:       int64(len(body)),
		TargetPath: projectPath,
		Caps:       archive.DefaultCaps(),
	})

	var liveErr *lock.LiveSessionsError
	require.ErrorAs(t, err, &liveErr, "the pre-promotion re-check must refuse with LiveSessionsError")
	assert.Equal(t, 2, livenessCalls, "the witness must run once at lock time and once before promotion")
	assert.NoFileExists(t, filepath.Join(home.ProjectDir(projectPath), sessionID+".jsonl"),
		"an import aborted by the re-check must promote nothing")
	assert.NoFileExists(t, home.ConfigFile,
		"an import aborted by the re-check must never reach the finalize config splice")
	var temps []string
	walkErr := filepath.WalkDir(home.Dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".cc-port-import.tmp") {
			temps = append(temps, path)
		}
		return nil
	})
	require.NoError(t, walkErr)
	assert.Empty(t, temps, "an import aborted by the re-check must clean up its staging temps")
}

func TestRun_RejectsMalformedConfigShapedEntryWithoutWriting(t *testing.T) {
	cases := []struct {
		name  string
		entry string
	}{
		{"config", "claude/config.json"},
		{"config-grants", "claude/config-grants.json"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			body := buildClaudeArchive(t, map[string]string{testCase.entry: `{"setting":`})
			home := blankHome(t)
			toolSet := tool.NewSet(claude.New())
			targets := []tool.Target{{Tool: toolSet.All()[0], Workspace: claude.NewWorkspace(home)}}

			_, err := importer.Run(t.Context(), toolSet, targets, &importer.Options{
				Source:     bytes.NewReader(body),
				Size:       int64(len(body)),
				TargetPath: "/Users/test/Projects/malformed",
				Caps:       archive.DefaultCaps(),
			})

			require.Error(t, err, "a malformed %s entry must refuse the import at staging", testCase.entry)
			assert.NoFileExists(t, home.ConfigFile,
				"an import refused at staging must never reach the finalize splices")
		})
	}
}

func buildClaudeArchive(t *testing.T, entries map[string]string) []byte {
	return buildClaudeArchiveWithPlaceholders(t, entries, nil)
}

func buildClaudeArchiveWithPlaceholders(t *testing.T, entries map[string]string, placeholders []manifest.Placeholder) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		content := entries[name]
		entryWriter, err := writer.Create(name)
		require.NoError(t, err)
		_, err = entryWriter.Write([]byte(content))
		require.NoError(t, err)
	}
	claudeTool := claude.New()
	categories := manifest.BuildToolCategoryEntries(tool.CategoryNames(claudeTool), nil)
	_, err := archive.WriteMetadata(writer, &manifest.Metadata{Tools: []manifest.Tool{{
		Name: claudeTool.Name(), Categories: categories, Placeholders: placeholders,
	}}})
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return buffer.Bytes()
}
