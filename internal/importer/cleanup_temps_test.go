package importer

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/progress"
)

// TestBuildImportPlan_CleanupReclaimsFileHistoryTempOnCapFailure drives a
// file-history entry that exceeds the per-entry cap through buildImportPlan +
// cleanupTemps directly. streamVerbatimToTemp creates the staging temp before
// openCappedZipEntry's cap check, so a temp is on disk when the error
// propagates. The test asserts the temp exists once buildImportPlan returns
// the cap error, then asserts cleanupTemps reclaims it. Run cannot exercise
// this path: pass-1 classifyPresentDeclaredKeys rejects the same over-cap
// entry before any temp is staged.
func TestBuildImportPlan_CleanupReclaimsFileHistoryTempOnCapFailure(t *testing.T) {
	SetMaxEntryBytes(t, 16)

	claudeHome := newStagingClaudeHome(t)
	archiveBytes := buildSingleEntryArchive(t, "file-history/abc/snapshot@v1", bytes.Repeat([]byte("x"), 64))

	plan, err := planFromArchive(t, claudeHome, archiveBytes)
	require.ErrorIs(t, err, ErrEntryCapExceeded)
	require.NotNil(t, plan)

	require.True(t, importTempExists(t, claudeHome.FileHistoryDir()),
		"streamVerbatimToTemp creates the temp before the cap check")

	require.NoError(t, plan.cleanupTemps())
	assertNoImportTemps(t, claudeHome.FileHistoryDir())
}

// TestBuildImportPlan_CleanupReclaimsSessionKeyedTempOnCapFailure is the
// session-keyed counterpart: an over-cap todos/ entry fails inside
// streamResolveToTemp after the temp is created.
func TestBuildImportPlan_CleanupReclaimsSessionKeyedTempOnCapFailure(t *testing.T) {
	SetMaxEntryBytes(t, 16)

	claudeHome := newStagingClaudeHome(t)
	archiveBytes := buildSingleEntryArchive(t, "todos/session-abc.json", bytes.Repeat([]byte("x"), 64))

	plan, err := planFromArchive(t, claudeHome, archiveBytes)
	require.ErrorIs(t, err, ErrEntryCapExceeded)
	require.NotNil(t, plan)

	require.True(t, importTempExists(t, claudeHome.TodosDir()),
		"streamResolveToTemp creates the temp before the cap check")

	require.NoError(t, plan.cleanupTemps())
	assertNoImportTemps(t, claudeHome.TodosDir())
}

// newStagingClaudeHome returns a claude.Home rooted under t.TempDir with the
// projects directory present, enough for buildImportPlan to stage entries.
func newStagingClaudeHome(t *testing.T) *claude.Home {
	t.Helper()
	tempDir := t.TempDir()
	claudeDir := filepath.Join(tempDir, "dotclaude")
	require.NoError(t, os.MkdirAll(filepath.Join(claudeDir, "projects"), dirPerm))
	configFile := filepath.Join(tempDir, "dotclaude.json")
	require.NoError(t, os.WriteFile(configFile, []byte(`{"projects":{}}`), filePerm))
	return &claude.Home{Dir: claudeDir, ConfigFile: configFile}
}

// buildSingleEntryArchive returns a ZIP byte slice with one entry named
// entryName carrying body verbatim. No metadata.xml: buildImportPlan does not
// read it, and forEachNonMetadataEntry skips only by the literal name.
func buildSingleEntryArchive(t *testing.T, entryName string, body []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	zipWriter := zip.NewWriter(&buffer)
	entry, err := zipWriter.Create(entryName)
	require.NoError(t, err, "create zip entry %q", entryName)
	_, err = entry.Write(body)
	require.NoError(t, err, "write zip entry %q", entryName)
	require.NoError(t, zipWriter.Close(), "close zip writer")
	return buffer.Bytes()
}

// planFromArchive runs buildImportPlan against an in-memory archive and
// returns the plan and its error, bypassing Run's pass-1 cap rejection so the
// pass-2 staging temps are exercised.
func planFromArchive(t *testing.T, claudeHome *claude.Home, archiveBytes []byte) (*importPlan, error) {
	t.Helper()
	source := bytes.NewReader(archiveBytes)
	targetPath := filepath.Join(t.TempDir(), "project")
	encodedProjectDir := claudeHome.ProjectDir(targetPath)
	extractPhase := progress.Noop().Phase("extract", 1, progress.UnitEntries)
	return buildImportPlan(
		t.Context(), claudeHome, source, int64(len(archiveBytes)),
		targetPath, encodedProjectDir, map[string]string{}, extractPhase,
	)
}

// importTempExists reports whether any staging temp remains anywhere under
// root.
func importTempExists(t *testing.T, root string) bool {
	t.Helper()
	found := false
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if strings.HasSuffix(path, stagingSuffix) {
			found = true
		}
		return nil
	})
	return found
}

// assertNoImportTemps walks root and fails if any staging temp remains.
func assertNoImportTemps(t *testing.T, root string) {
	t.Helper()
	if importTempExists(t, root) {
		t.Errorf("staging temp (%s) must not remain under %q after cleanupTemps", stagingSuffix, root)
	}
}
