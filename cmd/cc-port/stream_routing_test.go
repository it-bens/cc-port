package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/testutil"
)

// TestStreamRouting_ExportManifestSuccessLineRoutedToCobraStdout asserts that
// the export-manifest command's success line ("Manifest written to ...")
// reaches a buffer set via cmd.SetOut, not os.Stdout. A bare fmt.Printf
// would bypass the buffer and the assertion would fail.
func TestStreamRouting_ExportManifestSuccessLineRoutedToCobraStdout(t *testing.T) {
	_, stdout, manifestPath := driveExportManifest(t, testutil.FixtureProjectPath())

	assert.Contains(t, stdout, "Manifest written to "+manifestPath,
		"success line did not reach cmd.OutOrStdout buffer; bare fmt.Printf regression?")
}

// TestStreamRouting_ImportManifestSuccessLineRoutedToCobraStdout pins the
// same property for the import-manifest cmd.
func TestStreamRouting_ImportManifestSuccessLineRoutedToCobraStdout(t *testing.T) {
	archivePath := testutil.WriteFixtureArchive(t)

	var stdout, stderr bytes.Buffer
	cmd := newImportManifestCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	output := filepath.Join(t.TempDir(), "m.xml")
	require.NoError(t, cmd.Flags().Set("output", output))
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{archivePath})

	require.NoError(t, cmd.Execute())
	assert.Contains(t, stdout.String(), "Manifest written to "+output)
	assert.Contains(t, stdout.String(), "Edit the resolve attributes")
}

// stageMinimalHome stages a Claude home under t.TempDir() with only an empty
// project directory keyed to projectPath. The empty content means discovery
// surfaces no {{PROJECT_PATH}}/{{HOME}} suggestions; the deterministic
// {{PROJECT_DIR}} placeholder is still declared but matches nothing, and the
// cmd reaches its success line.
func stageMinimalHome(t *testing.T, projectPath string) *claude.Home {
	t.Helper()

	temporaryDir := t.TempDir()
	claudeDir := filepath.Join(temporaryDir, "dotclaude")
	configFilePath := filepath.Join(temporaryDir, "dotclaude.json")
	require.NoError(t, os.MkdirAll(filepath.Join(claudeDir, "projects", claude.EncodePath(projectPath)), 0o700))
	require.NoError(t, os.WriteFile(configFilePath, []byte("{}"), 0o600))

	return &claude.Home{
		Dir:        claudeDir,
		ConfigFile: configFilePath,
	}
}

// driveExportManifest stages a minimal home for projectPath, runs the
// export-manifest command with --all to a temp manifest, and returns the home,
// the command's stdout, and the manifest path.
//
// It stages a minimal home rather than testutil.SetupFixture: the canonical
// fixture's session and memory bodies reference sibling project paths (e.g.
// /Users/test/Projects/myproject-extras) that the placeholder discoverer
// surfaces as {{UNRESOLVED_N}} prompts, blocking a no-TTY drive of the cmd.
func driveExportManifest(t *testing.T, projectPath string) (home *claude.Home, stdout, manifestPath string) {
	t.Helper()
	home = stageMinimalHome(t, projectPath)

	var out, errBuf bytes.Buffer
	claudeDir := home.Dir
	cmd := newExportManifestCmd(&claudeDir, noopBanner{})
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)

	manifestPath = filepath.Join(t.TempDir(), "m.xml")
	require.NoError(t, cmd.Flags().Set("output", manifestPath))
	require.NoError(t, cmd.Flags().Set("all", "true"))
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{projectPath})
	require.NoError(t, cmd.Execute())

	return home, out.String(), manifestPath
}
