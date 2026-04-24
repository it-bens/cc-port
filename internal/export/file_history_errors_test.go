package export_test

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/export"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/testutil"
)

func TestExport_FileHistoryFailsWhenSnapshotUnreadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based fault injection not supported on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("chmod bits bypassed when running as root")
	}

	claudeHome := testutil.SetupFixture(t)

	locations, err := claude.LocateProject(claudeHome, fixtureProjectPath)
	require.NoError(t, err)
	require.NotEmpty(t, locations.FileHistoryDirs)
	sort.Strings(locations.FileHistoryDirs)
	firstDir := locations.FileHistoryDirs[0]

	dirEntries, err := os.ReadDir(firstDir)
	require.NoError(t, err)
	require.NotEmpty(t, dirEntries)
	sort.Slice(dirEntries, func(i, j int) bool { return dirEntries[i].Name() < dirEntries[j].Name() })
	snapshotPath := filepath.Join(firstDir, dirEntries[0].Name())

	chmodScoped(t, snapshotPath, 0)

	outputPath := filepath.Join(t.TempDir(), "export.zip")
	_, err = export.Run(t.Context(), claudeHome, &export.Options{
		ProjectPath:  fixtureProjectPath,
		OutputPath:   outputPath,
		Categories:   manifest.CategorySet{FileHistory: true},
		Placeholders: defaultPlaceholders(),
	})

	require.Error(t, err)
	require.ErrorContains(t, err, "open ")
	require.ErrorContains(t, err, snapshotPath)
}

func TestExport_FileHistoryFailsWhenDirUnreadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based fault injection not supported on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("chmod bits bypassed when running as root")
	}

	claudeHome := testutil.SetupFixture(t)

	locations, err := claude.LocateProject(claudeHome, fixtureProjectPath)
	require.NoError(t, err)
	require.NotEmpty(t, locations.FileHistoryDirs)
	sort.Strings(locations.FileHistoryDirs)
	firstDir := locations.FileHistoryDirs[0]

	chmodScoped(t, firstDir, 0)

	outputPath := filepath.Join(t.TempDir(), "export.zip")
	_, err = export.Run(t.Context(), claudeHome, &export.Options{
		ProjectPath:  fixtureProjectPath,
		OutputPath:   outputPath,
		Categories:   manifest.CategorySet{FileHistory: true},
		Placeholders: defaultPlaceholders(),
	})

	require.Error(t, err)
	require.ErrorContains(t, err, "read directory")
}

// chmodScoped sets path's mode and registers a Cleanup to restore the
// captured pre-chmod mode. Restoration runs before t.TempDir's rm-rf so
// fixture cleanup succeeds even if chmod 000 made the entry unreadable.
func chmodScoped(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err, "stat before chmod %s", path)
	t.Cleanup(func() { _ = os.Chmod(path, info.Mode()) })
	require.NoError(t, os.Chmod(path, mode), "chmod %s to %#o", path, mode)
}
