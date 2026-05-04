package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/remote"
	syncc "github.com/it-bens/cc-port/internal/sync"

	// memblob registers the "mem" scheme so TestPush_DryRunDoesNotUpload
	// can target an in-memory bucket. Each blob.OpenBucket("mem://") call
	// returns a fresh bucket, so the post-dry-run verification by opening
	// a second mem:// remote does not share storage with the cobra
	// command's bucket. The dry-run header and apply hint are the
	// load-bearing assertions; the ErrNotFound check is structural.
	_ "gocloud.dev/blob/memblob"
)

// pushTestManifestPath writes a manifest XML enabling every category
// and declaring no placeholders, then returns its path. Tests pass
// --from-manifest <path> instead of --all + per-category flags so
// runPushCmd skips both ui.SelectCategories and
// discoverAndPromptPlaceholders. --from-manifest bypasses the discovery
// pipeline entirely and loads the placeholder set straight from XML; the
// test uses it so the assertion is keyed to a known placeholder list
// rather than whatever export.DiscoverPlaceholders happens to surface
// from the fixture's transcripts.
func pushTestManifestPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "push-manifest.xml")
	categories := allCategoriesCmdSet()
	metadata := &manifest.Metadata{
		Export: manifest.Info{
			Categories: manifest.BuildCategoryEntries(&categories),
		},
	}
	require.NoError(t, manifest.WriteManifest(path, metadata))
	return path
}

func TestPush_RejectsFromManifestWithCategoryFlag(t *testing.T) {
	rootCmd := newRootCmd()
	rootCmd.SetArgs([]string{
		"push", "/Users/test/Projects/myproject",
		"--as", "myproj",
		"--remote", "mem://",
		"--from-manifest", "/tmp/m.xml",
		"--sessions",
	})

	err := rootCmd.Execute()

	require.Error(t, err)
	if !strings.Contains(err.Error(), "--from-manifest is mutually exclusive") {
		t.Fatalf("err = %v, want exclusivity error", err)
	}
}

func TestPush_RejectsMissingAsFlag(t *testing.T) {
	rootCmd := newRootCmd()
	rootCmd.SetArgs([]string{"push", "/tmp/x", "--remote", "mem://"})

	err := rootCmd.Execute()

	require.Error(t, err)
	var u *usageError
	if !errors.As(err, &u) {
		t.Fatalf("err = %v, want *usageError", err)
	}
}

func TestPush_RejectsMissingRemoteFlag(t *testing.T) {
	rootCmd := newRootCmd()
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

	rootCmd := newRootCmd()
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetArgs([]string{
		"push", projectPath,
		"--claude-dir", claudeFixtureDir,
		"--as", "myproj",
		"--remote", "mem://",
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
	r, err := remote.New(context.Background(), "mem://")
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	if _, openErr := r.Open(context.Background(), "myproj"); !errors.Is(openErr, remote.ErrNotFound) {
		t.Fatalf("expected ErrNotFound on mem:// after dry-run, got %v", openErr)
	}
}

func TestPush_ApplyCommitsToRemote(t *testing.T) {
	tmpHome, projectPath := setupCmdFixture(t)
	claudeFixtureDir := filepath.Join(tmpHome, "dotclaude")
	manifestPath := pushTestManifestPath(t)
	url := "file://" + t.TempDir()

	rootCmd := newRootCmd()
	rootCmd.SetArgs([]string{
		"push", projectPath,
		"--claude-dir", claudeFixtureDir,
		"--as", "myproj",
		"--remote", url,
		"--from-manifest", manifestPath,
		"--apply",
	})

	err := rootCmd.Execute()

	require.NoError(t, err)
	r, err := remote.New(context.Background(), url)
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

	rootCmd := newRootCmd()
	rootCmd.SetArgs([]string{
		"push", projectPath,
		"--claude-dir", claudeFixtureDir,
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

	rootCmd := newRootCmd()
	rootCmd.SetArgs([]string{
		"push", projectPath,
		"--claude-dir", claudeFixtureDir,
		"--as", "myproj",
		"--remote", url,
		"--from-manifest", manifestPath,
		"--apply",
		"--force",
	})

	err := rootCmd.Execute()

	require.NoError(t, err)
}

func TestOpenPriorRead_NoObjectReturnsNil(t *testing.T) {
	url := "file://" + t.TempDir()
	r, err := remote.New(context.Background(), url)
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
	r, err := remote.New(context.Background(), url)
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
	r, err := remote.New(context.Background(), url)
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
	r, err := remote.New(context.Background(), url)
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
