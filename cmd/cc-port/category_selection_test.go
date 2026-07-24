package main

import (
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/testutil"
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
)

func testTargets(t *testing.T) []tool.Target {
	t.Helper()
	home := testutil.SetupFixture(t)
	return []tool.Target{{Tool: claude.New(), Workspace: claude.NewWorkspace(home)}}
}

func TestApplyCategorySelection_FromManifestAloneAccepted(t *testing.T) {
	targets := testTargets(t)
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "m.xml")

	selected := make(map[string]bool)
	for _, category := range targets[0].Tool.Categories() {
		selected[category.Name] = true
	}
	metadata := &manifest.Metadata{
		Tools: []manifest.Tool{{
			Name:       "claude",
			Categories: manifest.BuildToolCategoryEntries(tool.CategoryNames(targets[0].Tool), selected),
		}},
	}
	require.NoError(t, manifest.WriteManifest(manifestPath, metadata))

	cmd := newTestCmdWithCategoryFlags(t)
	require.NoError(t, cmd.Flags().Set("from-manifest", manifestPath))

	gotSelection, gotPlaceholders, err := applyCategorySelection(cmd, targets, testutil.FixtureProjectPath(), noopBanner{})

	require.NoError(t, err)
	assert.True(t, gotSelection["claude"]["sessions"], "manifest with --all categories must select sessions")
	assert.Empty(t, gotPlaceholders["claude"], "manifest declared no placeholders")
}

func TestApplyCategorySelection_FromManifestRejectsAllFlag(t *testing.T) {
	targets := testTargets(t)
	cmd := newTestCmdWithCategoryFlags(t)
	require.NoError(t, cmd.Flags().Set("from-manifest", "/tmp/m.xml"))
	require.NoError(t, cmd.Flags().Set("all", "true"))

	_, _, err := applyCategorySelection(cmd, targets, testutil.FixtureProjectPath(), noopBanner{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--from-manifest is mutually exclusive")
	assert.Contains(t, err.Error(), "--all")
}

func TestApplyCategorySelection_FromManifestRejectsIncludeFlag(t *testing.T) {
	targets := testTargets(t)
	cmd := newTestCmdWithCategoryFlags(t)
	require.NoError(t, cmd.Flags().Set("from-manifest", "/tmp/m.xml"))
	require.NoError(t, cmd.Flags().Set("include", "claude/sessions"))

	_, _, err := applyCategorySelection(cmd, targets, testutil.FixtureProjectPath(), noopBanner{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--from-manifest is mutually exclusive")
	assert.Contains(t, err.Error(), "--include")
}

// newTestCmdWithCategoryFlags returns a cobra command carrying the
// flag surface applyCategorySelection consults: --from-manifest and the
// flags registerCategoryFlags registers.
func newTestCmdWithCategoryFlags(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().String("from-manifest", "", "")
	registerCategoryFlags(cmd, "test")
	return cmd
}
