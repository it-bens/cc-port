package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestExportCmd_PassphraseFlagsRegistered(t *testing.T) {
	toolSet := newToolSet()
	flags := registerToolFlags(&cobra.Command{}, toolSet)
	cmd := newExportCmd(toolSet, flags, noopBanner{})
	require.NotNil(t, cmd.Flags().Lookup("passphrase-env"),
		"--passphrase-env should be registered on export")
	require.NotNil(t, cmd.Flags().Lookup("passphrase-file"),
		"--passphrase-file should be registered on export")
}
