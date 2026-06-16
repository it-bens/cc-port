package export_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/export"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/progress"
	"github.com/it-bens/cc-port/internal/progress/progresstest"
	"github.com/it-bens/cc-port/internal/testutil"
)

// allCategoriesSet builds the CategorySet with every manifest category selected
// from the registry, so a new category without a writer-and-sub-phase fails the
// drift-guard test rather than silently passing.
func allCategoriesSet() manifest.CategorySet {
	var set manifest.CategorySet
	for _, spec := range manifest.AllCategories {
		spec.Apply(&set, true)
	}
	return set
}

// topLevelPhaseNames returns the names of every top-level phase opened on the
// recorder, in emission order. A top-level phase is a PhaseStart whose Path has
// exactly one segment.
func topLevelPhaseNames(events []progress.Event) []string {
	var names []string
	for _, start := range progresstest.OfType[progress.PhaseStart](events) {
		if len(start.Path) == 1 {
			names = append(names, start.Path[0])
		}
	}
	return names
}

// archiveSubPhaseNames returns the set of sub-phase names opened directly under
// the archive phase.
func archiveSubPhaseNames(events []progress.Event) map[string]struct{} {
	names := make(map[string]struct{})
	for _, start := range progresstest.OfType[progress.PhaseStart](events) {
		if len(start.Path) == 2 && start.Path[0] == "archive" {
			names[start.Path[1]] = struct{}{}
		}
	}
	return names
}

func TestRun_EmitsLocateThenArchive(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	archivePath := filepath.Join(t.TempDir(), "export.zip")

	recorder := progresstest.NewRecorder()
	file := createArchiveFile(t, archivePath)
	_, err := export.Run(t.Context(), claudeHome, &export.Options{
		ProjectPath:  fixtureProjectPath,
		Output:       file,
		Placeholders: defaultPlaceholders(),
		Categories:   allCategoriesSet(),
		Reporter:     recorder.Reporter(progress.LevelInfo),
	})
	require.NoError(t, err)

	assert.Equal(t,
		[]string{"locate", "archive"},
		topLevelPhaseNames(recorder.Events()),
		"export emits the locate phase before the archive phase",
	)
}

// TestRun_ArchiveSubPhasesAreManifestCategories is the export progress
// drift-guard: every archive sub-phase name is a manifest.AllCategories name
// (named by category, never group), and when every category is selected every
// category emits exactly one sub-phase. Expected names derive from the registry
// so a new category that forgets its writer-and-sub-phase fails this test.
func TestRun_ArchiveSubPhasesAreManifestCategories(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	archivePath := filepath.Join(t.TempDir(), "export.zip")

	recorder := progresstest.NewRecorder()
	file := createArchiveFile(t, archivePath)
	_, err := export.Run(t.Context(), claudeHome, &export.Options{
		ProjectPath:  fixtureProjectPath,
		Output:       file,
		Placeholders: defaultPlaceholders(),
		Categories:   allCategoriesSet(),
		Reporter:     recorder.Reporter(progress.LevelInfo),
	})
	require.NoError(t, err)

	subPhaseNames := archiveSubPhaseNames(recorder.Events())

	categoryNames := make(map[string]struct{}, len(manifest.AllCategories))
	for _, spec := range manifest.AllCategories {
		categoryNames[spec.Name] = struct{}{}
	}

	for name := range subPhaseNames {
		assert.Contains(t, categoryNames, name,
			"archive sub-phase %q must be a manifest category name, not a group name", name)
	}
	for _, spec := range manifest.AllCategories {
		assert.Contains(t, subPhaseNames, spec.Name,
			"category %q must open an archive sub-phase when selected", spec.Name)
	}
}

// TestRun_ExcludedCategoryOpensNoSubPhase pins that an unselected category
// produces no sub-phase: only sessions is selected, so no other category name
// appears under archive.
func TestRun_ExcludedCategoryOpensNoSubPhase(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	archivePath := filepath.Join(t.TempDir(), "export.zip")

	recorder := progresstest.NewRecorder()
	file := createArchiveFile(t, archivePath)
	_, err := export.Run(t.Context(), claudeHome, &export.Options{
		ProjectPath:  fixtureProjectPath,
		Output:       file,
		Placeholders: defaultPlaceholders(),
		Categories:   manifest.CategorySet{Sessions: true},
		Reporter:     recorder.Reporter(progress.LevelInfo),
	})
	require.NoError(t, err)

	subPhaseNames := archiveSubPhaseNames(recorder.Events())
	assert.Contains(t, subPhaseNames, "sessions",
		"the selected sessions category opens a sub-phase")
	assert.NotContains(t, subPhaseNames, "memory",
		"an unselected category must open no sub-phase")
	assert.NotContains(t, subPhaseNames, "config",
		"an unselected category must open no sub-phase")
}
