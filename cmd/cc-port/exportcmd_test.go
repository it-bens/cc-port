package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/manifest"
)

func TestExportManifestCmd_HasOutputFlag(t *testing.T) {
	toolSet := newToolSet()
	flags := registerToolFlags(&cobra.Command{}, toolSet)
	cmd := newExportManifestCmd(toolSet, flags, noopBanner{})
	flag := cmd.Flags().Lookup("output")
	require.NotNil(t, flag, "export manifest --output must be registered")
	short := cmd.Flags().ShorthandLookup("o")
	require.NotNil(t, short, "export manifest -o must be registered")
	assert.Equal(t, "manifest.xml", flag.DefValue)
}

func TestExportManifestCmd_OverwriteGuard(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "pre-existing.xml")
	require.NoError(t, os.WriteFile(outPath, []byte("x"), 0o600))

	toolSet := newToolSet()
	flags := registerToolFlags(&cobra.Command{}, toolSet)
	*flags.homeOverrides["claude"] = t.TempDir()
	cmd := newExportManifestCmd(toolSet, flags, noopBanner{})
	require.NoError(t, cmd.Flags().Set("output", outPath))

	err := runExportManifest(cmd, []string{"/Users/test/Projects/myproject"}, toolSet, flags, noopBanner{})

	require.ErrorIs(t, err, errOutputExists)
}

func TestExportManifestCmd_ProjectAbsentWritesEmptyToolBlock(t *testing.T) {
	toolSet := newToolSet()
	flags := registerToolFlags(&cobra.Command{}, toolSet)
	flags.selected = []string{"claude"}
	*flags.homeOverrides["claude"] = filepath.Join(t.TempDir(), "empty-claude-home")
	cmd := newExportManifestCmd(toolSet, flags, noopBanner{})
	output := filepath.Join(t.TempDir(), "manifest.xml")
	require.NoError(t, cmd.Flags().Set("output", output))
	require.NoError(t, cmd.Flags().Set("all", "true"))

	require.NoError(t, runExportManifest(cmd, []string{t.TempDir()}, toolSet, flags, noopBanner{}))

	metadata, err := manifest.ReadManifest(output)
	require.NoError(t, err)
	block, ok := metadata.ToolBlock("claude")
	require.True(t, ok)
	assert.Empty(t, block.Placeholders)
	for _, category := range block.Categories {
		assert.False(t, category.Included, "absent tool category %s must be excluded", category.Name)
	}
}

func TestExportCmd_PassphraseFlagsRegistered(t *testing.T) {
	toolSet := newToolSet()
	flags := registerToolFlags(&cobra.Command{}, toolSet)
	cmd := newExportCmd(toolSet, flags, noopBanner{})
	require.NotNil(t, cmd.Flags().Lookup("passphrase-env"),
		"--passphrase-env should be registered on export")
	require.NotNil(t, cmd.Flags().Lookup("passphrase-file"),
		"--passphrase-file should be registered on export")
}
