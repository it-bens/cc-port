package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseExportOptions_FromManifestWithCategoryFlagErrors(t *testing.T) {
	cmd := newExportCmdForTest(t)
	require.NoError(t, cmd.Flags().Set("from-manifest", "/tmp/m.xml"))
	require.NoError(t, cmd.Flags().Set("sessions", "true"))

	_, _, err := parseExportOptions(cmd, []string{"/Users/test/Projects/myproject"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--from-manifest is mutually exclusive")
	assert.Contains(t, err.Error(), "--sessions")
}

func TestParseExportOptions_CategoryFlagsAloneAccepted(t *testing.T) {
	cmd := newExportCmdForTest(t)
	require.NoError(t, cmd.Flags().Set("output", "/tmp/o.zip"))
	require.NoError(t, cmd.Flags().Set("sessions", "true"))

	opts, outputPath, err := parseExportOptions(cmd, []string{"/Users/test/Projects/myproject"})

	require.NoError(t, err)
	assert.True(t, opts.Categories.Sessions)
	assert.Equal(t, "/tmp/o.zip", outputPath)
}

func TestParseExportOptions_FromManifestAloneAccepted(t *testing.T) {
	cmd := newExportCmdForTest(t)
	require.NoError(t, cmd.Flags().Set("output", "/tmp/o.zip"))
	require.NoError(t, cmd.Flags().Set("from-manifest", "/tmp/m.xml"))

	opts, outputPath, err := parseExportOptions(cmd, []string{"/Users/test/Projects/myproject"})

	require.NoError(t, err)
	assert.Equal(t, "/tmp/m.xml", opts.FromManifest)
	assert.Equal(t, "/tmp/o.zip", outputPath)
}

func TestExportManifestCmd_HasOutputFlag(t *testing.T) {
	flag := exportManifestCmd.Flags().Lookup("output")
	require.NotNil(t, flag, "export manifest --output must be registered")
	short := exportManifestCmd.Flags().ShorthandLookup("o")
	require.NotNil(t, short, "export manifest -o must be registered")
	assert.Equal(t, "manifest.xml", flag.DefValue)
}

func TestExportManifestCmd_OverwriteGuard(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "pre-existing.xml")
	require.NoError(t, os.WriteFile(outPath, []byte("x"), 0o600))
	require.NoError(t, exportManifestCmd.Flags().Set("output", outPath))
	t.Cleanup(func() { _ = exportManifestCmd.Flags().Set("output", "manifest.xml") })

	err := runExportManifest(exportManifestCmd, []string{"/Users/test/Projects/myproject"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

// newExportCmdForTest returns a cobra command with every export flag
// registered. Keeps flag names in one place so the tests track the real
// command surface.
func newExportCmdForTest(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.Flags().String("output", "", "")
	cmd.Flags().String("from-manifest", "", "")
	cmd.Flags().Bool("all", false, "")
	cmd.Flags().Bool("sessions", false, "")
	cmd.Flags().Bool("memory", false, "")
	cmd.Flags().Bool("history", false, "")
	cmd.Flags().Bool("file-history", false, "")
	cmd.Flags().Bool("config", false, "")
	cmd.Flags().Bool("todos", false, "")
	cmd.Flags().Bool("usage-data", false, "")
	cmd.Flags().Bool("plugins-data", false, "")
	cmd.Flags().Bool("tasks", false, "")
	return cmd
}
