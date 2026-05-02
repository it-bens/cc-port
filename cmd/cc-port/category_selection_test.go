package main

import (
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/manifest"
)

func TestApplyCategorySelection_FromManifestAloneAccepted(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "m.xml")
	categories := allCategoriesCmdSet()
	metadata := &manifest.Metadata{
		Export: manifest.Info{
			Categories: manifest.BuildCategoryEntries(&categories),
		},
	}
	require.NoError(t, manifest.WriteManifest(manifestPath, metadata))

	cmd := newTestCmdWithCategoryFlags(t)
	require.NoError(t, cmd.Flags().Set("from-manifest", manifestPath))

	gotCategories, gotPlaceholders, err := applyCategorySelection(cmd, nil, "/Users/test/Projects/myproject")

	require.NoError(t, err)
	assert.True(t, gotCategories.Sessions, "manifest with --all categories must populate Sessions")
	assert.Empty(t, gotPlaceholders, "manifest declared no placeholders")
}

func TestApplyCategorySelection_FromManifestRejectsAllFlag(t *testing.T) {
	cmd := newTestCmdWithCategoryFlags(t)
	require.NoError(t, cmd.Flags().Set("from-manifest", "/tmp/m.xml"))
	require.NoError(t, cmd.Flags().Set("all", "true"))

	_, _, err := applyCategorySelection(cmd, nil, "/Users/test/Projects/myproject")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--from-manifest is mutually exclusive")
	assert.Contains(t, err.Error(), "--all")
}

func TestApplyCategorySelection_FromManifestRejectsEachPerCategoryFlag(t *testing.T) {
	for _, spec := range manifest.AllCategories {
		t.Run(spec.Name, func(t *testing.T) {
			cmd := newTestCmdWithCategoryFlags(t)
			require.NoError(t, cmd.Flags().Set("from-manifest", "/tmp/m.xml"))
			require.NoError(t, cmd.Flags().Set(spec.Name, "true"))

			_, _, err := applyCategorySelection(cmd, nil, "/Users/test/Projects/myproject")

			require.Error(t, err)
			assert.Contains(t, err.Error(), "--from-manifest is mutually exclusive")
			assert.Contains(t, err.Error(), "--"+spec.Name)
		})
	}
}

// newTestCmdWithCategoryFlags returns a cobra command carrying the
// flag surface applyCategorySelection consults: --from-manifest and
// every category flag registered by registerCategoryFlags.
func newTestCmdWithCategoryFlags(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().String("from-manifest", "", "")
	registerCategoryFlags(cmd, "test")
	return cmd
}
