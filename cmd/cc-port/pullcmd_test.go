package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/manifest"
	syncc "github.com/it-bens/cc-port/internal/sync"
)

func TestPull_RejectsMissingTo(t *testing.T) {
	resetPullFlags(t)
	rootCmd.SetArgs([]string{"pull", "myproj", "--remote", "file:///tmp/x"})

	err := rootCmd.Execute()

	require.Error(t, err)
	var u *usageError
	if !errors.As(err, &u) {
		t.Fatalf("err = %v, want *usageError", err)
	}
}

func TestPull_RejectsMissingRemote(t *testing.T) {
	resetPullFlags(t)
	rootCmd.SetArgs([]string{"pull", "myproj", "--to", "/tmp/x"})

	err := rootCmd.Execute()

	require.Error(t, err)
	var u *usageError
	if !errors.As(err, &u) {
		t.Fatalf("err = %v, want *usageError", err)
	}
}

func TestPull_DryRunDoesNotImport(t *testing.T) {
	tmpHome, _ := setupCmdFixture(t)
	claudeFixtureDir := filepath.Join(tmpHome, "dotclaude")
	url := "file://" + t.TempDir()
	injectArchiveWithPusherAtURL(t, url, "myproj", "host-user")
	targetPath := filepath.Join(t.TempDir(), "pulled-project")

	resetPullFlags(t)
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	t.Cleanup(func() { rootCmd.SetOut(nil) })
	rootCmd.SetArgs([]string{
		"pull", "myproj",
		"--claude-dir", claudeFixtureDir,
		"--to", targetPath,
		"--remote", url,
		"--resolution", "{{HOME}}=/Users/me",
	})

	err := rootCmd.Execute()

	require.NoError(t, err)
	if !strings.Contains(buf.String(), "[dry-run]") {
		t.Fatalf("expected dry-run header in output:\n%s", buf.String())
	}
	resolved, err := claude.ResolveProjectPath(targetPath)
	require.NoError(t, err)
	encodedDir := filepath.Join(claudeFixtureDir, "projects", claude.EncodePath(resolved))
	if _, statErr := os.Stat(encodedDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected encoded project dir to be absent after dry-run, stat err = %v", statErr)
	}
}

func TestPull_ApplyImportsToTarget(t *testing.T) {
	tmpHome, _ := setupCmdFixture(t)
	claudeFixtureDir := filepath.Join(tmpHome, "dotclaude")
	url := "file://" + t.TempDir()
	injectArchiveWithPusherAtURL(t, url, "myproj", "host-user")
	targetPath := filepath.Join(t.TempDir(), "pulled-project")

	resetPullFlags(t)
	rootCmd.SetArgs([]string{
		"pull", "myproj",
		"--claude-dir", claudeFixtureDir,
		"--to", targetPath,
		"--remote", url,
		"--resolution", "{{HOME}}=/Users/me",
		"--apply",
	})

	err := rootCmd.Execute()

	require.NoError(t, err)
	resolved, err := claude.ResolveProjectPath(targetPath)
	require.NoError(t, err)
	encodedDir := filepath.Join(claudeFixtureDir, "projects", claude.EncodePath(resolved))
	if _, statErr := os.Stat(encodedDir); statErr != nil {
		t.Fatalf("expected encoded project dir to exist after apply, stat err = %v", statErr)
	}
}

func TestPull_ApplyWithUnresolvedPlaceholdersRefuses(t *testing.T) {
	tmpHome, _ := setupCmdFixture(t)
	claudeFixtureDir := filepath.Join(tmpHome, "dotclaude")
	url := "file://" + t.TempDir()
	injectArchiveWithDeclaredPlaceholderAtURL(t, url, "myproj", "{{SECRET}}", "/Users/sender/secret", "host-user")
	targetPath := filepath.Join(t.TempDir(), "pulled-project")
	emptyManifestPath := filepath.Join(t.TempDir(), "empty-manifest.xml")
	require.NoError(t, manifest.WriteManifest(emptyManifestPath, &manifest.Metadata{}))

	resetPullFlags(t)
	rootCmd.SetArgs([]string{
		"pull", "myproj",
		"--claude-dir", claudeFixtureDir,
		"--to", targetPath,
		"--remote", url,
		"--from-manifest", emptyManifestPath,
		"--apply",
	})

	err := rootCmd.Execute()

	if !errors.Is(err, syncc.ErrUnresolvedPlaceholder) {
		t.Fatalf("err = %v, want ErrUnresolvedPlaceholder", err)
	}
}

// resetPullFlags zeros every package-level cobra flag var the pull
// command binds, plus the rootCmd persistent --claude-dir. Cobra retains
// flag values across rootCmd.Execute calls when flags share package
// state, so a previous test's --to or --apply would leak into the next
// test without this reset.
func resetPullFlags(t *testing.T) {
	t.Helper()
	pullToPath = ""
	pullRemoteURL = ""
	pullApply = false
	pullPassphraseEnv = ""
	pullPassphraseFile = ""
	pullResolutionKV = nil
	pullFromManifest = ""
	claudeDir = ""
}
