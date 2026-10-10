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
	"github.com/it-bens/cc-port/internal/testutil"
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
)

// writeFixtureArchive runs an in-process export against the fixture project
// and returns the resulting archive path under t.TempDir(). Tests that need
// to drive an import or pull cmd against a known-good archive call this
// rather than constructing one inline.
func writeFixtureArchive(t *testing.T) string {
	t.Helper()
	home := testutil.SetupFixture(t)
	archivePath := filepath.Join(t.TempDir(), "fixture.zip")

	file, err := os.Create(archivePath) //nolint:gosec // path under t.TempDir
	if err != nil {
		t.Fatalf("create archive: %v", err)
	}
	defer func() { _ = file.Close() }()

	claudeTool := claude.New()
	workspace := claude.NewWorkspace(home)
	targets := []tool.Target{{Tool: claudeTool, Workspace: workspace}}

	selected := make(map[string]bool)
	for _, category := range claudeTool.Categories() {
		selected[category.Name] = true
	}

	options := export.Options{
		ProjectPath: testutil.FixtureProjectPath(),
		Output:      file,
		Selected:    map[string]map[string]bool{claudeTool.Name(): selected},
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
