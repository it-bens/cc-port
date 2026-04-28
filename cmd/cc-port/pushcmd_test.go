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
// discoverAndPromptPlaceholders. The fixture's session transcripts
// reference incidental absolute paths (e.g. "/remote-control" mentioned
// in a system message) that DiscoverPaths surfaces as non-Auto
// {{UNRESOLVED_N}} suggestions; without --from-manifest those would
// trip the no-TTY guard in ui.ResolvePlaceholder.
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

func TestPush_RejectsMissingAsFlag(t *testing.T) {
	resetPushFlags(t)
	rootCmd.SetArgs([]string{"push", "/tmp/x", "--remote", "mem://"})

	err := rootCmd.Execute()

	require.Error(t, err)
	var u *usageError
	if !errors.As(err, &u) {
		t.Fatalf("err = %v, want *usageError", err)
	}
}

func TestPush_RejectsMissingRemoteFlag(t *testing.T) {
	resetPushFlags(t)
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

	resetPushFlags(t)
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	t.Cleanup(func() { rootCmd.SetOut(nil) })
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

	resetPushFlags(t)
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

	resetPushFlags(t)
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

	resetPushFlags(t)
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

// resetPushFlags zeros every package-level cobra flag var the push
// command binds, plus the rootCmd persistent --claude-dir, and clears
// every category boolean registered through registerCategoryFlags.
// Cobra retains flag values across rootCmd.Execute calls when flags
// share package state, so a previous test's --claude-dir or --apply
// would leak into the next test without this reset.
func resetPushFlags(t *testing.T) {
	t.Helper()
	pushAsName = ""
	pushRemoteURL = ""
	pushApply = false
	pushForce = false
	pushPassphraseEnv = ""
	pushPassphraseFile = ""
	pushFromManifest = ""
	claudeDir = ""
	for _, spec := range manifest.AllCategories {
		_ = pushCmd.Flags().Set(spec.Name, "false")
	}
	_ = pushCmd.Flags().Set("all", "false")
}
