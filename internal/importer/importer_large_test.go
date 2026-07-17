//go:build large

package importer_test

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/archive"
	"github.com/it-bens/cc-port/internal/importer"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
)

// TestRun_RejectsEntryOverProductionCap builds an archive whose single
// claude/sessions/ entry decodes past the production 512 MiB per-entry
// cap and asserts importer.Run refuses it, exercising the real production
// threshold rather than a test-lowered one. The entry's decompressed bytes
// are a repeating pattern, so archive/zip's Deflate writer keeps the
// on-disk archive tiny even though the decoded size is production-scale —
// the same shape a genuine zip-bomb payload takes.
//
// Tagged `large`: exercised only on demand (see root AGENTS.md), not part
// of the default `go test ./...` run, since decoding ~512 MiB is slow for
// routine CI.
func TestRun_RejectsEntryOverProductionCap(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "oversized.zip")
	archiveFile, err := os.Create(archivePath) //nolint:gosec // G304: test-controlled tempdir path
	require.NoError(t, err)

	writer := zip.NewWriter(archiveFile)
	entryWriter, err := writer.Create("claude/sessions/oversized.jsonl")
	require.NoError(t, err)

	// One byte past the production per-entry cap (512 MiB), written in
	// chunks of repeated bytes so the compressed archive stays small.
	const chunkSize = 1 << 20 // 1 MiB
	chunk := bytes.Repeat([]byte("x"), chunkSize)
	totalChunks := (512 << 20 / chunkSize) + 1
	for range totalChunks {
		_, err := entryWriter.Write(chunk)
		require.NoError(t, err)
	}

	claudeTool := claude.New()
	selected := make(map[string]bool, len(claudeTool.Categories()))
	for _, category := range claudeTool.Categories() {
		selected[category.Name] = true
	}
	categoryEntries := make([]manifest.Category, 0, len(claudeTool.Categories()))
	for _, category := range claudeTool.Categories() {
		categoryEntries = append(categoryEntries, manifest.Category{Name: category.Name, Included: selected[category.Name]})
	}

	_, err = archive.WriteMetadata(writer, &manifest.Metadata{
		Tools: []manifest.Tool{{Name: "claude", Categories: categoryEntries}},
	})
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	require.NoError(t, archiveFile.Close())

	archiveInfo, err := os.Stat(archivePath)
	require.NoError(t, err)

	archiveFile, err = os.Open(archivePath) //nolint:gosec // G304: test-controlled tempdir path
	require.NoError(t, err)
	defer func() { _ = archiveFile.Close() }()

	home := blankHome(t)
	toolSet := tool.NewSet(claude.New())
	targets := []tool.Target{{Tool: toolSet.All()[0], Workspace: claude.NewWorkspace(home)}}

	_, err = importer.Run(context.Background(), toolSet, targets, &importer.Options{
		Source:     archiveFile,
		Size:       archiveInfo.Size(),
		TargetPath: "/Users/test/Projects/oversized",
	})
	require.Error(t, err, "an entry over the production per-entry cap must be refused")
	require.ErrorIs(t, err, archive.ErrEntryCapExceeded)
}
