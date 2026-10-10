package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/export"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
	"github.com/it-bens/cc-port/internal/tool/claude/claudetest"
)

// writeFixtureArchive runs an in-process export against the fixture project
// and returns the resulting archive path under t.TempDir(). Tests that need
// to drive an import or pull cmd against a known-good archive call this
// rather than constructing one inline.
func writeFixtureArchive(t *testing.T) string {
	t.Helper()
	home := claudetest.SetupFixture(t)
	archivePath := filepath.Join(t.TempDir(), "fixture.zip")

	file, err := os.Create(archivePath) //nolint:gosec // path under t.TempDir
	if err != nil {
		t.Fatalf("create archive: %v", err)
	}
	defer func() { _ = file.Close() }()

	claudeTool := claude.New()
	workspace := claude.NewWorkspace(home)
	targets := []tool.Target{{Tool: claudeTool, Workspace: workspace}}

	options := export.Options{
		ProjectPath: claudetest.FixtureProjectPath(),
		Output:      file,
		Selected:    allCategoriesCmdSelection(),
	}
	if _, err := export.Run(context.Background(), targets, &options); err != nil {
		t.Fatalf("export fixture: %v", err)
	}
	return archivePath
}

func TestWriteFixtureArchive_ProducesValidArchive(t *testing.T) {
	path := writeFixtureArchive(t)
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Positive(t, info.Size())

	// Manifest read must succeed; archive is a valid cc-port export.
	file, err := os.Open(path) //nolint:gosec // G304: path returned from helper rooted in t.TempDir
	require.NoError(t, err)
	defer func() { _ = file.Close() }()
	metadata, err := manifest.ReadManifestFromZip(file, info.Size())
	require.NoError(t, err)
	block, ok := metadata.ToolBlock("claude")
	require.True(t, ok, "manifest must carry a claude tool block")
	assert.NotEmpty(t, block.Categories)
}
