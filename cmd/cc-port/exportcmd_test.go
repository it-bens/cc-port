package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExportManifestCmd_HasOutputFlag(t *testing.T) {
	var claudeDir string
	cmd := newExportManifestCmd(&claudeDir)
	flag := cmd.Flags().Lookup("output")
	require.NotNil(t, flag, "export manifest --output must be registered")
	short := cmd.Flags().ShorthandLookup("o")
	require.NotNil(t, short, "export manifest -o must be registered")
	assert.Equal(t, "manifest.xml", flag.DefValue)
}

func TestExportManifestCmd_OverwriteGuard(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "pre-existing.xml")
	require.NoError(t, os.WriteFile(outPath, []byte("x"), 0o600))
	var claudeDir string
	cmd := newExportManifestCmd(&claudeDir)
	require.NoError(t, cmd.Flags().Set("output", outPath))

	err := runExportManifest(cmd, []string{"/Users/test/Projects/myproject"}, claudeDir)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestExportCmd_PassphraseFlagsRegistered(t *testing.T) {
	var claudeDir string
	cmd := newExportCmd(&claudeDir)
	require.NotNil(t, cmd.Flags().Lookup("passphrase-env"),
		"--passphrase-env should be registered on export")
	require.NotNil(t, cmd.Flags().Lookup("passphrase-file"),
		"--passphrase-file should be registered on export")
}
