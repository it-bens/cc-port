//go:build large

// Scale-validation siblings for the CI-runnable cap-rejection tests in
// importer_test.go. Materialize production-scale archives (600 MiB per-entry,
// multi-GiB aggregate). Run with `go test -tags large ./internal/importer/...`.
// Not gated on CI: a cap regression here would consume gigabytes of runner
// disk before failing.
package importer_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/importer"
)

func TestReadZipFile_RejectsOversizedEntry(t *testing.T) {
	// 600 MiB entry, well above the 512 MiB cap; zero-fill deflates to ~600 KiB.
	destClaudeHome := buildEmptyDestClaudeHome(t)
	archivePath := filepath.Join(t.TempDir(), "bomb.zip")
	buildArchiveWithSingleEntry(t, archivePath, "sessions/bomb.json", 600<<20)

	source, size := openArchive(t, archivePath)
	_, err := importer.Run(t.Context(), destClaudeHome, importer.Options{
		Source:     source,
		Size:       size,
		TargetPath: filepath.Join(t.TempDir(), "project"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds")
}

// TestRun_RefusesArchiveExceedingAggregateUncompressedCap builds an archive
// whose total decompressed payload exceeds the production aggregate cap and
// asserts that Run rejects it before staging. The archive consists of many
// 500 MiB entries so no individual entry trips the per-entry cap; only the
// aggregate guard can fire.
func TestRun_RefusesArchiveExceedingAggregateUncompressedCap(t *testing.T) {
	archivePath := buildArchiveWithAggregateSize(t, importer.MaxArchiveBytes()+1, 500<<20)
	destClaudeHome := buildEmptyDestClaudeHome(t)

	source, size := openArchive(t, archivePath)
	_, err := importer.Run(t.Context(), destClaudeHome, importer.Options{
		Source:      source,
		Size:        size,
		TargetPath:  filepath.Join(t.TempDir(), "project"),
		Resolutions: map[string]string{},
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "archive decompressed size exceeds")
}
