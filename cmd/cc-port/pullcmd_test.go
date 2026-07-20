package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/credentials"
	"github.com/it-bens/cc-port/internal/encrypt"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/progress"
	"github.com/it-bens/cc-port/internal/progress/progresstest"
	"github.com/it-bens/cc-port/internal/remote"
	syncc "github.com/it-bens/cc-port/internal/sync"
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
)

func TestPull_RejectsMissingTo(t *testing.T) {
	rootCmd := newRootCmd(noopBanner{})
	rootCmd.SetArgs([]string{"pull", "myproj", "--remote", "file:///tmp/x"})

	err := rootCmd.Execute()

	require.Error(t, err)
	var u *usageError
	if !errors.As(err, &u) {
		t.Fatalf("err = %v, want *usageError", err)
	}
}

func TestPull_RejectsMissingRemote(t *testing.T) {
	rootCmd := newRootCmd(noopBanner{})
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

	rootCmd := newRootCmd(noopBanner{})
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetArgs([]string{
		"pull", "myproj",
		"--claude-home", claudeFixtureDir,
		"--to", targetPath,
		"--remote", url,
	})

	err := rootCmd.Execute()

	require.NoError(t, err)
	if !strings.Contains(buf.String(), "[dry-run]") {
		t.Fatalf("expected dry-run header in output:\n%s", buf.String())
	}
	resolved, err := tool.ResolveProjectPath(targetPath)
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

	rootCmd := newRootCmd(noopBanner{})
	rootCmd.SetArgs([]string{
		"pull", "myproj",
		"--claude-home", claudeFixtureDir,
		"--to", targetPath,
		"--remote", url,
		"--apply",
	})

	err := rootCmd.Execute()

	require.NoError(t, err)
	resolved, err := tool.ResolveProjectPath(targetPath)
	require.NoError(t, err)
	encodedDir := filepath.Join(claudeFixtureDir, "projects", claude.EncodePath(resolved))
	if _, statErr := os.Stat(encodedDir); statErr != nil {
		t.Fatalf("expected encoded project dir to exist after apply, stat err = %v", statErr)
	}
}

func TestPull_ApplyRendersToolWarnings(t *testing.T) {
	tmpHome, _ := setupCmdFixture(t)
	claudeFixtureDir := filepath.Join(tmpHome, "dotclaude")
	url := "file://" + t.TempDir()
	injectArchiveWithPusherAtURL(t, url, "myproj", "host-user")
	targetPath := filepath.Join(t.TempDir(), "pulled-project")
	resolved, err := tool.ResolveProjectPath(targetPath)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(claudeFixtureDir, "rules"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(claudeFixtureDir, "rules", "pull-rule.md"),
		[]byte("Applies to "+resolved+" only.\n"),
		0o600,
	))

	rootCmd := newRootCmd(noopBanner{})
	var stderr bytes.Buffer
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{
		"pull", "myproj",
		"--claude-home", claudeFixtureDir,
		"--to", targetPath,
		"--remote", url,
		"--apply",
	})

	require.NoError(t, rootCmd.Execute())

	assert.Contains(t, stderr.String(), "Warning: rules file pull-rule.md (line 1) references this project")
}

func TestPull_ApplyWithUnresolvedPlaceholdersRefuses(t *testing.T) {
	tmpHome, projectPath := setupCmdFixture(t)
	claudeFixtureDir := filepath.Join(tmpHome, "dotclaude")
	url := "file://" + t.TempDir()
	// Original must be a path the fixture's own archived bodies actually
	// contain, so the placeholder is embedded and genuinely referenced.
	// Under the corrected classifier (finding FE3), a declared-but-never-
	// referenced placeholder no longer blocks --apply — see the sibling
	// TestPull_ApplyWithDeclaredUnusedPlaceholderAccepts below.
	injectArchiveWithDeclaredPlaceholderAtURL(t, url, "myproj", "{{SECRET}}", projectPath, "host-user")
	targetPath := filepath.Join(t.TempDir(), "pulled-project")
	emptyManifestPath := filepath.Join(t.TempDir(), "empty-manifest.xml")
	require.NoError(t, manifest.WriteManifest(emptyManifestPath, &manifest.Metadata{}))

	rootCmd := newRootCmd(noopBanner{})
	rootCmd.SetArgs([]string{
		"pull", "myproj",
		"--claude-home", claudeFixtureDir,
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

// TestPull_ApplyWithDeclaredUnusedPlaceholderAccepts is the accept-side
// sibling of TestPull_ApplyWithUnresolvedPlaceholdersRefuses: a declared
// placeholder whose Original never appears in any archived body must not
// block --apply on either command, and pull must agree with plain import
// on the exact same archive bytes (finding FE3 — pull used to refuse
// archives plain import accepted).
func TestPull_ApplyWithDeclaredUnusedPlaceholderAccepts(t *testing.T) {
	tmpHome, _ := setupCmdFixture(t)
	claudeFixtureDir := filepath.Join(tmpHome, "dotclaude")
	placeholders := map[string][]manifest.Placeholder{
		"claude": {{Key: "{{SECRET}}", Original: "/Users/sender/never-referenced"}},
	}
	body := buildCmdArchiveBytes(t, "host-user", "", placeholders)

	url := "file://" + t.TempDir()
	writeAtURL(t, url, "myproj", body)
	pullTargetPath := filepath.Join(t.TempDir(), "pulled-project")

	pullCmd := newRootCmd(noopBanner{})
	pullCmd.SetArgs([]string{
		"pull", "myproj",
		"--claude-home", claudeFixtureDir,
		"--to", pullTargetPath,
		"--remote", url,
		"--apply",
	})
	require.NoError(t, pullCmd.Execute(), "pull --apply must accept a declared-but-unused placeholder")

	archivePath := filepath.Join(t.TempDir(), "same-archive.zip")
	require.NoError(t, os.WriteFile(archivePath, body, 0o600))
	importTargetPath := filepath.Join(t.TempDir(), "imported-project")
	importHomeDir := filepath.Join(t.TempDir(), "import-claude-home")
	require.NoError(t, os.MkdirAll(importHomeDir, 0o750))

	importCmd := newRootCmd(noopBanner{})
	importCmd.SetArgs([]string{
		"import", archivePath, importTargetPath,
		"--claude-home", importHomeDir,
	})
	require.NoError(t, importCmd.Execute(), "import must accept the same declared-but-unused placeholder")
}

func TestPull_AcceptsValidCredentialsFile(t *testing.T) {
	tmpHome, _ := setupCmdFixture(t)
	claudeFixtureDir := filepath.Join(tmpHome, "dotclaude")
	url := "file://" + t.TempDir()
	injectArchiveWithPusherAtURL(t, url, "myproj", "host-user")
	targetPath := filepath.Join(t.TempDir(), "pulled-project")
	credentialsPath := writeTestCredentialsFile(t, 0o600)

	rootCmd := newRootCmd(noopBanner{})
	rootCmd.SetArgs([]string{
		"pull", "myproj",
		"--claude-home", claudeFixtureDir,
		"--to", targetPath,
		"--remote", url,
		"--credentials-file", credentialsPath,
	})

	err := rootCmd.Execute()

	require.NoError(t, err)
}

func TestPull_AcceptsNoPromptFlag(t *testing.T) {
	tmpHome, _ := setupCmdFixture(t)
	claudeFixtureDir := filepath.Join(tmpHome, "dotclaude")
	url := "file://" + t.TempDir()
	injectArchiveWithPusherAtURL(t, url, "myproj", "host-user")
	targetPath := filepath.Join(t.TempDir(), "pulled-project")

	rootCmd := newRootCmd(noopBanner{})
	rootCmd.SetArgs([]string{
		"pull", "myproj",
		"--claude-home", claudeFixtureDir,
		"--to", targetPath,
		"--remote", url,
		"--no-prompt",
	})

	err := rootCmd.Execute()

	require.NoError(t, err)
}

// TestPull_RejectsTooPermissiveCredentialsFile pins the wiring between
// the --credentials-file flag and credentials.Resolve on the pull side.
// A 0644-mode file is rejected by the resolver before any parse work;
// the test passes only when ErrFilePermissionsTooPermissive surfaces.
func TestPull_RejectsTooPermissiveCredentialsFile(t *testing.T) {
	tmpHome, _ := setupCmdFixture(t)
	claudeFixtureDir := filepath.Join(tmpHome, "dotclaude")
	url := "file://" + t.TempDir()
	injectArchiveWithPusherAtURL(t, url, "myproj", "host-user")
	targetPath := filepath.Join(t.TempDir(), "pulled-project")
	credentialsPath := writeTestCredentialsFile(t, 0o644)

	rootCmd := newRootCmd(noopBanner{})
	rootCmd.SetArgs([]string{
		"pull", "myproj",
		"--claude-home", claudeFixtureDir,
		"--to", targetPath,
		"--remote", url,
		"--credentials-file", credentialsPath,
	})

	err := rootCmd.Execute()

	require.ErrorIs(t, err, credentials.ErrFilePermissionsTooPermissive)
}

func TestPull_RejectsMalformedCredentialsFile(t *testing.T) {
	tmpHome, _ := setupCmdFixture(t)
	claudeFixtureDir := filepath.Join(tmpHome, "dotclaude")
	url := "file://" + t.TempDir()
	injectArchiveWithPusherAtURL(t, url, "myproj", "host-user")
	targetPath := filepath.Join(t.TempDir(), "pulled-project")
	malformedCredentialsPath := filepath.Join(t.TempDir(), "malformed.env")
	require.NoError(t, os.WriteFile(malformedCredentialsPath, []byte("BROKEN_NO_EQUALS\n"), 0o600))

	rootCmd := newRootCmd(noopBanner{})
	rootCmd.SetArgs([]string{
		"pull", "myproj",
		"--claude-home", claudeFixtureDir,
		"--to", targetPath,
		"--remote", url,
		"--credentials-file", malformedCredentialsPath,
	})

	err := rootCmd.Execute()

	var parseErr *credentials.FileParseError
	require.ErrorAs(t, err, &parseErr)
}

func TestOpenArchiveSource_MissingObjectReturnsErrRemoteNotFound(t *testing.T) {
	url := "file://" + t.TempDir()
	r, err := remote.New(context.Background(), url, remote.Deps{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	recorder := progresstest.NewRecorder()

	_, err = openArchiveSource(context.Background(), r, "missing", "", recorder.Reporter(progress.LevelInfo))

	if !errors.Is(err, syncc.ErrRemoteNotFound) {
		t.Fatalf("err = %v, want syncc.ErrRemoteNotFound", err)
	}
}

func TestOpenArchiveSource_EncryptedNoPassphraseReturnsErrPassphraseRequired(t *testing.T) {
	url := "file://" + t.TempDir()
	injectEncryptedArchiveAtURL(t, url, "k", "correct horse battery staple", "host-user")
	r, err := remote.New(context.Background(), url, remote.Deps{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	recorder := progresstest.NewRecorder()

	_, err = openArchiveSource(context.Background(), r, "k", "", recorder.Reporter(progress.LevelInfo))

	if !errors.Is(err, syncc.ErrPassphraseRequired) {
		t.Fatalf("err = %v, want syncc.ErrPassphraseRequired", err)
	}
}

func TestOpenArchiveSource_PlaintextWithPassphraseReturnsErrUnencryptedInput(t *testing.T) {
	url := "file://" + t.TempDir()
	injectArchiveWithPusherAtURL(t, url, "k", "host-user")
	r, err := remote.New(context.Background(), url, remote.Deps{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	recorder := progresstest.NewRecorder()

	_, err = openArchiveSource(context.Background(), r, "k", "any-pass", recorder.Reporter(progress.LevelInfo))

	if !errors.Is(err, encrypt.ErrUnencryptedInput) {
		t.Fatalf("err = %v, want encrypt.ErrUnencryptedInput", err)
	}
}

func TestOpenArchiveSource_PlaintextNoPassphraseSucceeds(t *testing.T) {
	url := "file://" + t.TempDir()
	injectArchiveWithPusherAtURL(t, url, "k", "host-user")
	r, err := remote.New(context.Background(), url, remote.Deps{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	recorder := progresstest.NewRecorder()

	src, err := openArchiveSource(context.Background(), r, "k", "", recorder.Reporter(progress.LevelInfo))
	require.NoError(t, err)
	t.Cleanup(func() { _ = src.Close() })
	require.Positive(t, src.Size, "src.Size must be positive")

	// The download phase must be determinate: PhaseStart.Total == remote blob
	// size, and the cumulative PhaseAdvance.Done reaches exactly Total. For the
	// plaintext (no-passphrase) case the encrypt stage is a passthrough, so the
	// remote blob size equals the materialized tempfile size (src.Size).
	starts := progresstest.OfType[progress.PhaseStart](recorder.Events())
	require.Len(t, starts, 1, "expected exactly one download PhaseStart")
	assert.Equal(t, []string{"download"}, starts[0].Path)
	assert.Equal(t, src.Size, starts[0].Total, "download phase total must equal remote blob size")

	advances := progresstest.OfType[progress.PhaseAdvance](recorder.Events())
	require.NotEmpty(t, advances, "expected at least one PhaseAdvance")
	last := advances[len(advances)-1]
	assert.Equal(t, starts[0].Total, last.Done, "cumulative done must equal total (no overshoot)")

	ends := progresstest.OfType[progress.PhaseEnd](recorder.Events())
	assert.Len(t, ends, 1, "expected exactly one PhaseEnd")
}
