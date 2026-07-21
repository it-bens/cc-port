package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/credentials"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/remote"
	syncc "github.com/it-bens/cc-port/internal/sync"
	"github.com/it-bens/cc-port/internal/tool/claude"
)

// pushTestManifestPath writes a manifest XML enabling every category for
// the claude tool and declaring no placeholders, then returns its path.
// Tests pass --from-manifest <path> instead of --all + --include so
// runPushCmd skips both ui.SelectCategories and placeholder discovery.
func pushTestManifestPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "push-manifest.xml")
	claudeTool := claude.New()
	selected := make(map[string]bool)
	for _, category := range claudeTool.Categories() {
		selected[category.Name] = true
	}
	metadata := &manifest.Metadata{
		Tools: []manifest.Tool{{
			Name:       claudeTool.Name(),
			Categories: manifest.BuildToolCategoryEntries(categoryNames(claudeTool), selected),
		}},
	}
	require.NoError(t, manifest.WriteManifest(path, metadata))
	return path
}

func TestPush_RejectsFromManifestWithCategoryFlag(t *testing.T) {
	remoteURL := "file://" + filepath.ToSlash(t.TempDir())
	rootCmd := newRootCmd(noopBanner{})
	rootCmd.SetArgs([]string{
		"push", "/Users/test/Projects/myproject",
		"--as", "myproj",
		"--remote", remoteURL,
		"--from-manifest", "/tmp/m.xml",
		"--include", "claude/sessions",
	})

	err := rootCmd.Execute()

	require.Error(t, err)
	if !strings.Contains(err.Error(), "--from-manifest is mutually exclusive") {
		t.Fatalf("err = %v, want exclusivity error", err)
	}
}

func TestPush_RejectsMissingAsFlag(t *testing.T) {
	remoteURL := "file://" + filepath.ToSlash(t.TempDir())
	rootCmd := newRootCmd(noopBanner{})
	rootCmd.SetArgs([]string{"push", "/tmp/x", "--remote", remoteURL})

	err := rootCmd.Execute()

	require.Error(t, err)
	var u *usageError
	if !errors.As(err, &u) {
		t.Fatalf("err = %v, want *usageError", err)
	}
}

func TestPush_RejectsMissingRemoteFlag(t *testing.T) {
	rootCmd := newRootCmd(noopBanner{})
	rootCmd.SetArgs([]string{"push", "/tmp/x", "--as", "name"})

	err := rootCmd.Execute()

	require.Error(t, err)
	var u *usageError
	if !errors.As(err, &u) {
		t.Fatalf("err = %v, want *usageError", err)
	}
}

func TestPush_DryRunDoesNotUpload(t *testing.T) {
	tmpHome, projectPath := setupCmdFixture(t)
	claudeFixtureDir := filepath.Join(tmpHome, "dotclaude")
	manifestPath := pushTestManifestPath(t)
	remoteURL := "file://" + filepath.ToSlash(t.TempDir())

	rootCmd := newRootCmd(noopBanner{})
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetArgs([]string{
		"push", projectPath,
		"--claude-home", claudeFixtureDir,
		"--as", "myproj",
		"--remote", remoteURL,
		"--from-manifest", manifestPath,
	})

	err := rootCmd.Execute()

	require.NoError(t, err)
	if !strings.Contains(buf.String(), "[dry-run]") {
		t.Fatalf("expected dry-run header in output:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "(no changes; pass --apply to commit)") {
		t.Fatalf("expected apply hint:\n%s", buf.String())
	}
	r, err := remote.New(context.Background(), remoteURL, remote.Deps{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	if _, openErr := r.Open(context.Background(), "myproj"); !errors.Is(openErr, remote.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after dry-run, got %v", openErr)
	}
}

func TestPush_ApplyCommitsToRemote(t *testing.T) {
	tmpHome, projectPath := setupCmdFixture(t)
	claudeFixtureDir := filepath.Join(tmpHome, "dotclaude")
	manifestPath := pushTestManifestPath(t)
	url := "file://" + t.TempDir()

	rootCmd := newRootCmd(noopBanner{})
	rootCmd.SetArgs([]string{
		"push", projectPath,
		"--claude-home", claudeFixtureDir,
		"--as", "myproj",
		"--remote", url,
		"--from-manifest", manifestPath,
		"--apply",
	})

	err := rootCmd.Execute()

	require.NoError(t, err)
	r, err := remote.New(context.Background(), url, remote.Deps{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	if _, statErr := r.Stat(context.Background(), "myproj"); statErr != nil {
		t.Fatalf("Stat after apply: %v", statErr)
	}
}

func TestPush_CrossMachineRefusesWithoutForce(t *testing.T) {
	tmpHome, projectPath := setupCmdFixture(t)
	claudeFixtureDir := filepath.Join(tmpHome, "dotclaude")
	manifestPath := pushTestManifestPath(t)
	url := "file://" + t.TempDir()
	injectArchiveWithPusherAtURL(t, url, "myproj", "other-host-other-user")

	rootCmd := newRootCmd(noopBanner{})
	rootCmd.SetArgs([]string{
		"push", projectPath,
		"--claude-home", claudeFixtureDir,
		"--as", "myproj",
		"--remote", url,
		"--from-manifest", manifestPath,
		"--apply",
	})

	err := rootCmd.Execute()

	if !errors.Is(err, syncc.ErrCrossMachineConflict) {
		t.Fatalf("err = %v, want ErrCrossMachineConflict", err)
	}
}

func TestPush_ForceOverridesCrossMachineRefusal(t *testing.T) {
	tmpHome, projectPath := setupCmdFixture(t)
	claudeFixtureDir := filepath.Join(tmpHome, "dotclaude")
	manifestPath := pushTestManifestPath(t)
	url := "file://" + t.TempDir()
	injectArchiveWithPusherAtURL(t, url, "myproj", "other-host-other-user")

	rootCmd := newRootCmd(noopBanner{})
	rootCmd.SetArgs([]string{
		"push", projectPath,
		"--claude-home", claudeFixtureDir,
		"--as", "myproj",
		"--remote", url,
		"--from-manifest", manifestPath,
		"--apply",
		"--force",
	})

	err := rootCmd.Execute()

	require.NoError(t, err)
}

// writeTestCredentialsFile writes a .env-style credentials file holding
// the two required AWS_* keys at the requested mode and returns its
// path. The credentials are dummy because file:// remotes ignore them;
// tests use this helper to exercise the credentials.Resolve path.
func writeTestCredentialsFile(t *testing.T, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "credentials.env")
	contents := "AWS_ACCESS_KEY_ID=AKIATEST\nAWS_SECRET_ACCESS_KEY=secrettest\n"
	require.NoError(t, os.WriteFile(path, []byte(contents), mode))
	return path
}

func TestPush_AcceptsValidCredentialsFile(t *testing.T) {
	tmpHome, projectPath := setupCmdFixture(t)
	claudeFixtureDir := filepath.Join(tmpHome, "dotclaude")
	manifestPath := pushTestManifestPath(t)
	remoteURL := "file://" + filepath.ToSlash(t.TempDir())
	credentialsPath := writeTestCredentialsFile(t, 0o600)

	rootCmd := newRootCmd(noopBanner{})
	rootCmd.SetArgs([]string{
		"push", projectPath,
		"--claude-home", claudeFixtureDir,
		"--as", "myproj",
		"--remote", remoteURL,
		"--from-manifest", manifestPath,
		"--credentials-file", credentialsPath,
	})

	err := rootCmd.Execute()

	require.NoError(t, err)
}

func TestPush_AcceptsNoPromptFlag(t *testing.T) {
	tmpHome, projectPath := setupCmdFixture(t)
	claudeFixtureDir := filepath.Join(tmpHome, "dotclaude")
	manifestPath := pushTestManifestPath(t)
	remoteURL := "file://" + filepath.ToSlash(t.TempDir())

	rootCmd := newRootCmd(noopBanner{})
	rootCmd.SetArgs([]string{
		"push", projectPath,
		"--claude-home", claudeFixtureDir,
		"--as", "myproj",
		"--remote", remoteURL,
		"--from-manifest", manifestPath,
		"--no-prompt",
	})

	err := rootCmd.Execute()

	require.NoError(t, err)
}

// TestPush_RejectsTooPermissiveCredentialsFile pins the wiring between
// the --credentials-file flag and credentials.Resolve.
func TestPush_RejectsTooPermissiveCredentialsFile(t *testing.T) {
	tmpHome, projectPath := setupCmdFixture(t)
	claudeFixtureDir := filepath.Join(tmpHome, "dotclaude")
	manifestPath := pushTestManifestPath(t)
	remoteURL := "file://" + filepath.ToSlash(t.TempDir())
	credentialsPath := writeTestCredentialsFile(t, 0o644)

	rootCmd := newRootCmd(noopBanner{})
	rootCmd.SetArgs([]string{
		"push", projectPath,
		"--claude-home", claudeFixtureDir,
		"--as", "myproj",
		"--remote", remoteURL,
		"--from-manifest", manifestPath,
		"--credentials-file", credentialsPath,
	})

	err := rootCmd.Execute()

	require.ErrorIs(t, err, credentials.ErrFilePermissionsTooPermissive)
}

func TestPush_RejectsMalformedCredentialsFile(t *testing.T) {
	tmpHome, projectPath := setupCmdFixture(t)
	claudeFixtureDir := filepath.Join(tmpHome, "dotclaude")
	manifestPath := pushTestManifestPath(t)
	remoteURL := "file://" + filepath.ToSlash(t.TempDir())
	malformedCredentialsPath := filepath.Join(t.TempDir(), "malformed.env")
	require.NoError(t, os.WriteFile(malformedCredentialsPath, []byte("BROKEN_NO_EQUALS\n"), 0o600))

	rootCmd := newRootCmd(noopBanner{})
	rootCmd.SetArgs([]string{
		"push", projectPath,
		"--claude-home", claudeFixtureDir,
		"--as", "myproj",
		"--remote", remoteURL,
		"--from-manifest", manifestPath,
		"--credentials-file", malformedCredentialsPath,
	})

	err := rootCmd.Execute()

	var parseErr *credentials.FileParseError
	require.ErrorAs(t, err, &parseErr)
}

func TestOpenPriorRead_NoObjectReturnsNil(t *testing.T) {
	url := "file://" + t.TempDir()
	r, err := remote.New(context.Background(), url, remote.Deps{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	prior, err := openPriorRead(context.Background(), r, "missing", "", false)

	require.NoError(t, err)
	if prior != nil {
		t.Fatalf("prior = %v, want nil for missing object", prior)
	}
}

func TestOpenPriorRead_EncryptedNoPassphraseNoForceReturnsErrPassphraseRequired(t *testing.T) {
	url := "file://" + t.TempDir()
	injectEncryptedArchiveAtURL(t, url, "k", "correct horse battery staple", "host-user")
	r, err := remote.New(context.Background(), url, remote.Deps{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	_, err = openPriorRead(context.Background(), r, "k", "", false)

	if !errors.Is(err, syncc.ErrPassphraseRequired) {
		t.Fatalf("err = %v, want syncc.ErrPassphraseRequired", err)
	}
}

func TestOpenPriorRead_EncryptedNoPassphraseWithForceReturnsNil(t *testing.T) {
	url := "file://" + t.TempDir()
	injectEncryptedArchiveAtURL(t, url, "k", "correct horse battery staple", "host-user")
	r, err := remote.New(context.Background(), url, remote.Deps{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	prior, err := openPriorRead(context.Background(), r, "k", "", true)

	require.NoError(t, err)
	if prior != nil {
		t.Fatalf("prior = %v, want nil under --force suppression", prior)
	}
}

func TestOpenPriorRead_PlaintextReturnsPriorReadWithoutEncryptedFlag(t *testing.T) {
	url := "file://" + t.TempDir()
	injectArchiveWithPusherAtURL(t, url, "k", "host-user")
	r, err := remote.New(context.Background(), url, remote.Deps{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	prior, err := openPriorRead(context.Background(), r, "k", "", false)

	require.NoError(t, err)
	if prior == nil {
		t.Fatal("prior = nil, want non-nil for readable plaintext archive")
	}
	t.Cleanup(func() { _ = prior.Source.Close() })
	if prior.WasEncrypted {
		t.Fatal("WasEncrypted = true, want false for plaintext archive")
	}
}
