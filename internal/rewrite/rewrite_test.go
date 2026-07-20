package rewrite_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/fsutil"
	"github.com/it-bens/cc-port/internal/rewrite"
	"github.com/it-bens/cc-port/internal/tool"
)

func TestPromoteDir(t *testing.T) {
	t.Run("copy failure removes partial staging through restore", func(t *testing.T) {
		root := t.TempDir()
		source := filepath.Join(root, "source")
		destination := filepath.Join(root, "destination")
		restorer := tool.NewRestorer()
		require.NoError(t, os.Mkdir(source, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(source, "source.txt"), []byte("source"), 0o600))

		err := rewrite.PromoteDir(context.Background(), source, destination, restorer,
			func(_ context.Context, _, staging string, _ func()) error {
				// PromoteDir creates staging and writes the marker before calling
				// copyDir, so a crash mid-copy still strands a staging directory
				// an ownership check can positively attribute to this promotion.
				assert.FileExists(t, filepath.Join(staging, rewrite.MarkerFilename))
				require.NoError(t, os.WriteFile(filepath.Join(staging, "partial.txt"), []byte("partial"), 0o600))
				return assert.AnError
			})

		require.ErrorIs(t, err, assert.AnError)
		assert.NoDirExists(t, destination)
		assert.FileExists(t, filepath.Join(source, "source.txt"))
		require.NoError(t, restorer.Restore())
		assert.NoDirExists(t, destination+rewrite.StagingSuffix)
	})

	t.Run("successful promotion rolls back destination", func(t *testing.T) {
		root := t.TempDir()
		source := filepath.Join(root, "source")
		destination := filepath.Join(root, "destination")
		restorer := tool.NewRestorer()
		require.NoError(t, os.Mkdir(source, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(source, "source.txt"), []byte("source"), 0o600))

		err := rewrite.PromoteDir(context.Background(), source, destination, restorer,
			func(_ context.Context, from, to string, _ func()) error {
				data, readErr := os.ReadFile(filepath.Join(from, "source.txt")) //nolint:gosec // G304: t.TempDir() path
				require.NoError(t, readErr)
				return os.WriteFile(filepath.Join(to, "source.txt"), data, 0o600) //nolint:gosec // G304: t.TempDir() path
			})

		require.NoError(t, err)
		assert.FileExists(t, filepath.Join(destination, "source.txt"))
		assert.NoDirExists(t, destination+rewrite.StagingSuffix)
		assert.FileExists(t, filepath.Join(destination, rewrite.MarkerFilename))
		verified, verifyErr := rewrite.VerifyPromotedFrom(source, destination)
		require.NoError(t, verifyErr)
		assert.True(t, verified)
		require.NoError(t, restorer.Restore())
		assert.NoDirExists(t, destination)
		assert.NoFileExists(t, filepath.Join(destination, rewrite.MarkerFilename))
	})
}

func TestPromoteDir_InterruptedBeforeRenameLeavesMarkedStaging(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	staging := destination + rewrite.StagingSuffix
	require.NoError(t, os.Mkdir(source, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(source, "source.txt"), []byte("source"), 0o600))
	ctx, cancel := context.WithCancel(t.Context())

	err := rewrite.PromoteDir(ctx, source, destination, tool.NewRestorer(),
		func(_ context.Context, from, to string, _ func()) error {
			require.NoError(t, os.MkdirAll(to, 0o750))
			data, readErr := os.ReadFile(filepath.Join(from, "source.txt")) //nolint:gosec // G304: test-controlled path
			require.NoError(t, readErr)
			require.NoError(t, os.WriteFile(filepath.Join(to, "source.txt"), data, 0o600)) //nolint:gosec // G703: test-controlled staging path
			cancel()
			return nil
		})

	require.ErrorIs(t, err, context.Canceled)
	assert.DirExists(t, staging)
	assert.NoDirExists(t, destination)
	assert.FileExists(t, filepath.Join(staging, rewrite.MarkerFilename))

	// PromoteDir itself does not resume over a staging directory it already
	// marked: reconciling a stranded staging directory (verify ownership,
	// then remove it so the next PromoteDir call starts fresh) is the
	// caller's job — internal/tool/claude/move.go's removeStagingDir, which
	// always runs immediately before PromoteDir on the one production call
	// path. A direct re-invocation that skips that reconciliation step must
	// refuse rather than silently overwrite whatever the existing marker
	// protects; copyDir must never even run.
	err = rewrite.PromoteDir(t.Context(), source, destination, tool.NewRestorer(),
		func(context.Context, string, string, func()) error {
			t.Fatal("copyDir must not run when the marker write is refused")
			return nil
		})

	require.Error(t, err, "a direct re-invocation over an already-marked staging directory must refuse, not resume")
	assert.DirExists(t, staging, "the refusal must leave the stranded staging directory for the caller to reconcile")
}

// TestPromoteDir_MarkerDoesNotFollowSymlink uses a copyDir stub that fails
// the test if invoked at all: fsutil.CopyDir's own symlink creation would
// independently error on this same collision (symlink over an existing
// regular file, "file exists"), which would make this test pass even
// without the pre-copy check it is meant to prove — the stub rules that
// confound out, isolating refuseSourceMarkerCollision specifically.
func TestPromoteDir_MarkerDoesNotFollowSymlink(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	external := filepath.Join(root, "external.txt")
	externalContent := []byte("do not touch me")
	require.NoError(t, os.WriteFile(external, externalContent, 0o600))
	require.NoError(t, os.Mkdir(source, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(source, "real.txt"), []byte("real project file"), 0o600))
	require.NoError(t, os.Symlink(external, filepath.Join(source, rewrite.MarkerFilename)))

	err := rewrite.PromoteDir(context.Background(), source, destination, tool.NewRestorer(),
		func(context.Context, string, string, func()) error {
			t.Fatal("copyDir must not run when the source-side collision check refuses")
			return nil
		})

	require.Error(t, err, "a source carrying cc-port's own marker filename as a symlink must refuse promotion before any copy is attempted")
	assert.NoDirExists(t, destination, "no marker write must escape the staging tree onto a real destination")
	externalAfter, readErr := os.ReadFile(external) //nolint:gosec // G304: test-controlled path
	require.NoError(t, readErr)
	assert.Equal(t, externalContent, externalAfter, "the symlink target's content must be untouched")
	infoAfter, statErr := os.Stat(external)
	require.NoError(t, statErr)
	assert.EqualValues(t, len(externalContent), infoAfter.Size(), "the symlink target's size must be untouched")
}

// TestPromoteDir_MarkerCollidesWithRegularFileInSourceRefusesPromotion uses
// the real fsutil.CopyDir rather than a stub, so unlike its sibling cases it
// does not isolate one specific guard: the pre-copy source-side check
// refuses this before any copy runs, and if that check were ever absent the
// post-copy content comparison would independently refuse it too (the
// content here does not equal source). Kept as a real-CopyDir integration
// check on top of the isolated cases below.
func TestPromoteDir_MarkerCollidesWithRegularFileInSourceRefusesPromotion(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	require.NoError(t, os.Mkdir(source, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(source, "real.txt"), []byte("real project file"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(source, rewrite.MarkerFilename), []byte("attacker content"), 0o600))

	err := rewrite.PromoteDir(context.Background(), source, destination, tool.NewRestorer(), fsutil.CopyDir)

	require.Error(t, err, "a source carrying a regular file named cc-port's own marker must refuse promotion")
	assert.Contains(t, err.Error(), rewrite.MarkerFilename)
	assert.NoDirExists(t, destination, "the clobbered marker must never be promoted to the real destination")
}

// TestPromoteDir_MarkerCollidesWithMatchingContentRegularFileRefusesPromotion
// covers the case a content-only check misses: a colliding regular file
// whose bytes happen to equal source. Comparing the post-copy marker's
// bytes against source alone would pass this case, letting the promotion
// succeed over a collision it must refuse. The pre-copy source-side check
// (refuseSourceMarkerCollision) catches it regardless of content.
func TestPromoteDir_MarkerCollidesWithMatchingContentRegularFileRefusesPromotion(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	require.NoError(t, os.Mkdir(source, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(source, "real.txt"), []byte("real project file"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(source, rewrite.MarkerFilename), []byte(source), 0o600))

	err := rewrite.PromoteDir(context.Background(), source, destination, tool.NewRestorer(), fsutil.CopyDir)

	require.Error(t, err, "a colliding regular file must refuse promotion even when its bytes equal source")
	assert.Contains(t, err.Error(), rewrite.MarkerFilename)
	assert.NoDirExists(t, destination)
}

// TestPromoteDir_MarkerCollidesWithDirectoryInSourceRefusesPromotion confirms
// the source-side collision check is type-agnostic: a directory named
// MarkerFilename is refused exactly like a regular file or symlink would be.
// copyDir is a stub that fails the test if invoked at all: fsutil.CopyDir's
// own MkdirAll would independently error on this same collision ("mkdirat
// .cc-port-promoted-from: file exists"), which would make this test pass
// even without the pre-copy check it is meant to prove — the stub rules
// that confound out.
func TestPromoteDir_MarkerCollidesWithDirectoryInSourceRefusesPromotion(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	require.NoError(t, os.Mkdir(source, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(source, "real.txt"), []byte("real project file"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(source, rewrite.MarkerFilename), 0o750))

	err := rewrite.PromoteDir(context.Background(), source, destination, tool.NewRestorer(),
		func(context.Context, string, string, func()) error {
			t.Fatal("copyDir must not run when the source-side collision check refuses")
			return nil
		})

	require.Error(t, err, "a colliding directory must refuse promotion before any copy is attempted")
	assert.Contains(t, err.Error(), rewrite.MarkerFilename)
	assert.NoDirExists(t, destination)
}

// TestPromoteDir_PostCopyMarkerReplacementRefusesEvenWithMatchingContent
// exercises the post-copy verification's identity check in isolation, via a
// synthetic copyDir the source-side check cannot see (it inspects source's
// top level, not what an injected copyDir does to staging). The replacement
// marker's bytes still equal source — content matches — so only os.SameFile
// distinguishes "the file cc-port wrote" from "a different file with the
// same bytes."
func TestPromoteDir_PostCopyMarkerReplacementRefusesEvenWithMatchingContent(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	require.NoError(t, os.Mkdir(source, 0o750))

	err := rewrite.PromoteDir(context.Background(), source, destination, tool.NewRestorer(),
		func(_ context.Context, _, staging string, _ func()) error {
			markerPath := filepath.Join(staging, rewrite.MarkerFilename)
			data, readErr := os.ReadFile(markerPath) //nolint:gosec // G304: test-controlled path
			require.NoError(t, readErr)
			require.NoError(t, os.Remove(markerPath))
			return os.WriteFile(markerPath, data, 0o600) //nolint:gosec // G304: test-controlled path
		})

	require.Error(t, err, "a replaced marker must refuse promotion even when its content still equals source")
	assert.NoDirExists(t, destination)
}

// TestPromoteDir_PostCopyMarkerRewrittenInPlaceWithDifferentContentRefuses
// isolates the content half of the post-copy verification: a synthetic
// copyDir opens the existing marker and truncates it in place (the same
// open-existing-file-and-overwrite operation fsutil.CopyDir's own O_TRUNC
// write performs on a colliding regular file), so the underlying file — and
// os.SameFile identity — is unchanged. Only content differs from source, so
// only the content check, not identity, can catch this.
func TestPromoteDir_PostCopyMarkerRewrittenInPlaceWithDifferentContentRefuses(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	require.NoError(t, os.Mkdir(source, 0o750))

	err := rewrite.PromoteDir(context.Background(), source, destination, tool.NewRestorer(),
		func(_ context.Context, _, staging string, _ func()) error {
			markerPath := filepath.Join(staging, rewrite.MarkerFilename)
			file, openErr := os.OpenFile(markerPath, os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // G304: test-controlled path
			require.NoError(t, openErr)
			defer func() { require.NoError(t, file.Close()) }()
			_, writeErr := file.WriteString("attacker content")
			return writeErr
		})

	require.Error(t, err, "an in-place rewrite of the marker's content must refuse promotion even with identity unchanged")
	assert.NoDirExists(t, destination)
}

func TestPromoteDir_NonCollidingSourceStillPromotesWithMarker(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	require.NoError(t, os.Mkdir(source, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(source, "real.txt"), []byte("real project file"), 0o600))

	err := rewrite.PromoteDir(context.Background(), source, destination, tool.NewRestorer(), fsutil.CopyDir)

	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(destination, "real.txt"))
	verified, verifyErr := rewrite.VerifyPromotedFrom(source, destination)
	require.NoError(t, verifyErr)
	assert.True(t, verified, "the destination must carry a marker recording source")
}

func TestPromotionMarker(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "destination")
	require.NoError(t, os.Mkdir(destination, 0o750))

	verified, err := rewrite.VerifyPromotedFrom("/source", destination)
	require.NoError(t, err)
	assert.False(t, verified, "an absent marker must not verify a promotion")
	require.NoError(t, rewrite.RemoveMarker(destination), "removing an absent marker must be a no-op")

	markerPath := filepath.Join(destination, rewrite.MarkerFilename)
	require.NoError(t, os.WriteFile(markerPath, []byte("/source"), 0o600))
	verified, err = rewrite.VerifyPromotedFrom("/source", destination)
	require.NoError(t, err)
	assert.True(t, verified)

	verified, err = rewrite.VerifyPromotedFrom("/different-source", destination)
	require.NoError(t, err)
	assert.False(t, verified, "a marker for a different source must not verify")

	require.NoError(t, rewrite.RemoveMarker(destination))
	assert.NoFileExists(t, markerPath)
}

// TestPromotedFrom exercises all three outcomes of the tri-state reader
// directly: no marker, a marker recording one source, and a marker
// recording a different source. VerifyPromotedFrom collapses the latter two
// into a single bool when they disagree with a caller-supplied expectation;
// PromotedFrom reports the recorded value either way so a caller can tell
// "absent" from "present but names someone else" — the distinction
// resolveMoveIdentity needs to refuse a foreign promotion marker instead of
// misreporting it as an absent project.
func TestPromotedFrom(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "destination")
	require.NoError(t, os.Mkdir(destination, 0o750))

	source, present, err := rewrite.PromotedFrom(destination)
	require.NoError(t, err)
	assert.False(t, present, "an absent marker must report present=false")
	assert.Empty(t, source)

	markerPath := filepath.Join(destination, rewrite.MarkerFilename)
	require.NoError(t, os.WriteFile(markerPath, []byte("/source"), 0o600))
	source, present, err = rewrite.PromotedFrom(destination)
	require.NoError(t, err)
	assert.True(t, present)
	assert.Equal(t, "/source", source)

	require.NoError(t, os.WriteFile(markerPath, []byte("/different-source"), 0o600))
	source, present, err = rewrite.PromotedFrom(destination)
	require.NoError(t, err)
	assert.True(t, present, "a marker naming a different source is still present, just mismatched")
	assert.Equal(t, "/different-source", source, "PromotedFrom reports the recorded source verbatim, without comparing it")
}

// TestPromotedFrom_RefusesNonRegularMarker proves the marker path is
// Lstat'd, never followed: a dangling symlink must hard-fail rather than
// collapse to the same ENOENT a genuinely absent marker produces (silently
// degrading a malformed destination to "absent"), and a symlink to a file
// whose content happens to equal the expected source must hard-fail rather
// than pass as a genuine promotion proof. A directory at the marker path
// hard-fails too; asserting on the specific "not a regular file" message
// confirms the regular-file check itself refuses it, not an incidental
// read failure that would refuse it anyway.
func TestPromotedFrom_RefusesNonRegularMarker(t *testing.T) {
	tests := []struct {
		name    string
		arrange func(t *testing.T, root, markerPath string)
	}{
		{
			name: "dangling symlink",
			arrange: func(t *testing.T, root, markerPath string) {
				t.Helper()
				require.NoError(t, os.Symlink(filepath.Join(root, "does-not-exist"), markerPath))
			},
		},
		{
			name: "symlink to a file with matching content",
			arrange: func(t *testing.T, root, markerPath string) {
				t.Helper()
				target := filepath.Join(root, "target.txt")
				require.NoError(t, os.WriteFile(target, []byte("/old/project"), 0o600))
				require.NoError(t, os.Symlink(target, markerPath))
			},
		},
		{
			name: "directory",
			arrange: func(t *testing.T, _, markerPath string) {
				t.Helper()
				require.NoError(t, os.Mkdir(markerPath, 0o750))
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			destination := filepath.Join(root, "destination")
			require.NoError(t, os.Mkdir(destination, 0o750))
			markerPath := filepath.Join(destination, rewrite.MarkerFilename)
			test.arrange(t, root, markerPath)

			source, present, err := rewrite.PromotedFrom(destination)

			require.Error(t, err, "a non-regular entry at the marker path must hard-fail, not report absent")
			assert.False(t, present)
			assert.Empty(t, source)
			assert.Contains(t, err.Error(), "not a regular file")
		})
	}
}

func TestPromotionMarker_VerifiesArbitrarilyOldMarker(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "destination")
	markerPath := filepath.Join(destination, rewrite.MarkerFilename)
	source := "/source"
	now := time.Now()
	require.NoError(t, os.Mkdir(destination, 0o750))
	require.NoError(t, os.WriteFile(markerPath, []byte(source), 0o600))

	require.NoError(t, os.Chtimes(markerPath, now, now.AddDate(-100, 0, 0)))
	verified, err := rewrite.VerifyPromotedFrom(source, destination)
	require.NoError(t, err)
	assert.True(t, verified)
}

func TestReplacePathInBytes(t *testing.T) {
	t.Run("replaces full-component matches", func(t *testing.T) {
		input := []byte(`prefix /a/foo/bar /a/foo "/a/foo" /a/foo`)
		result, count := rewrite.ReplacePathInBytes(input, "/a/foo", "/x/qux")
		assert.Equal(t, 4, count)
		assert.NotContains(t, string(result), "/a/foo")
	})

	t.Run("does not corrupt prefix collisions on the right boundary", func(t *testing.T) {
		input := []byte(`/a/foo-extras /a/foo /a/foo2 /a/foo_bar /a/foo.txt`)
		result, count := rewrite.ReplacePathInBytes(input, "/a/foo", "/x/qux")
		assert.Equal(t, 1, count, "only the standalone /a/foo should match")
		assert.Contains(t, string(result), "/a/foo-extras")
		assert.Contains(t, string(result), "/a/foo2")
		assert.Contains(t, string(result), "/a/foo_bar")
		assert.Contains(t, string(result), "/a/foo.txt")
		assert.Contains(t, string(result), "/x/qux ")
	})

	t.Run("matches at end of buffer", func(t *testing.T) {
		input := []byte(`tail /a/foo`)
		result, count := rewrite.ReplacePathInBytes(input, "/a/foo", "/x/qux")
		assert.Equal(t, 1, count)
		assert.Equal(t, `tail /x/qux`, string(result))
	})

	t.Run("returns original on empty inputs", func(t *testing.T) {
		input := []byte(`some data`)
		result, count := rewrite.ReplacePathInBytes(input, "", "/x")
		assert.Equal(t, 0, count)
		assert.Equal(t, input, result)
	})
}

func TestIsBoundaryDescendant(t *testing.T) {
	tests := []struct {
		name      string
		parent    string
		candidate string
		want      bool
	}{
		{name: "equal", parent: "/a/proj", candidate: "/a/proj", want: true},
		{name: "nested", parent: "/a/proj", candidate: "/a/proj/sub", want: true},
		{name: "continuation byte", parent: "/a/proj", candidate: "/a/proj-backup", want: false},
		{name: "extension dot", parent: "/a/proj", candidate: "/a/proj.bak", want: false},
		{name: "no prefix", parent: "/a/proj", candidate: "/a/other", want: false},
		{name: "empty parent", parent: "", candidate: "/a/proj", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, rewrite.IsBoundaryDescendant(test.parent, test.candidate))
		})
	}
}

func TestIsBoundaryDescendant_RootParent(t *testing.T) {
	tests := []struct {
		name      string
		parent    string
		candidate string
		want      bool
	}{
		{name: "root parent, any child", parent: "/", candidate: "/x", want: true},
		{name: "separator-terminated parent, nested child", parent: "/a/", candidate: "/a/b", want: true},
		{
			name: "non-separator-terminated parent, continuation byte", parent: "/a", candidate: "/a-b", want: false,
			// Guards against widening the [A-Za-z0-9_-] boundary set: "/a-b"
			// must still be refused as a descendant of "/a" (README §Boundary
			// rules, internal/rewrite/AGENTS.md).
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, rewrite.IsBoundaryDescendant(test.parent, test.candidate))
		})
	}
}

func TestReplacePathInBytesWithJSONEscape_RewritesBothForms(t *testing.T) {
	input := []byte(`{"a":"/Users/me/foo","b":"\/Users\/me\/foo\/bar"}`)
	got, count := rewrite.ReplacePathInBytesWithJSONEscape(input, "/Users/me/foo", "/Users/me/bar")
	assert.Equal(t, 2, count)
	assert.Contains(t, string(got), `"a":"/Users/me/bar"`)
	assert.Contains(t, string(got), `"b":"\/Users\/me\/bar\/bar"`)
}

func TestReplacePathInBytesWithJSONEscape_NoFalseMatchOnBoundary(t *testing.T) {
	input := []byte(`["\/Users\/me\/foo","\/Users\/me\/foobar"]`)
	got, count := rewrite.ReplacePathInBytesWithJSONEscape(input, "/Users/me/foo", "/Users/me/bar")
	assert.Equal(t, 1, count)
	assert.Contains(t, string(got), `"\/Users\/me\/bar"`)
	assert.Contains(t, string(got), `"\/Users\/me\/foobar"`)
}

func TestReplacePathInBytesWithJSONEscape_NoEscapeIsByteIdentical(t *testing.T) {
	input := []byte(`["/Users/me/foo","not-a-path"]`)
	got, count := rewrite.ReplacePathInBytesWithJSONEscape(input, "/Users/me/foo", "/Users/me/bar")
	assert.Equal(t, 1, count)
	assert.Equal(t, `["/Users/me/bar","not-a-path"]`, string(got))
}

// TestReplacePathInBytesDotBoundary covers the two-byte lookahead that
// distinguishes a sentence-terminating '.' (prose) from an extension
// separator '.' (e.g. ".v2", ".txt"). Prose dots must not block the
// rewrite; extension dots must still block it.
func TestReplacePathInBytesDotBoundary(t *testing.T) {
	t.Run("rewrites path followed by sentence-terminating dot at end of buffer", func(t *testing.T) {
		input := []byte(`look at /a/foo.`)
		result, count := rewrite.ReplacePathInBytes(input, "/a/foo", "/x/qux")
		assert.Equal(t, 1, count)
		assert.Equal(t, `look at /x/qux.`, string(result))
	})

	t.Run("rewrites path followed by dot then whitespace", func(t *testing.T) {
		input := []byte(`look at /a/foo. Also see /a/foo`)
		result, count := rewrite.ReplacePathInBytes(input, "/a/foo", "/x/qux")
		assert.Equal(t, 2, count)
		assert.Equal(t, `look at /x/qux. Also see /x/qux`, string(result))
	})

	t.Run("rewrites path followed by dot then closing quote", func(t *testing.T) {
		input := []byte(`"see /a/foo."`)
		result, count := rewrite.ReplacePathInBytes(input, "/a/foo", "/x/qux")
		assert.Equal(t, 1, count)
		assert.Equal(t, `"see /x/qux."`, string(result))
	})

	t.Run("rewrites path followed by ellipsis", func(t *testing.T) {
		input := []byte(`see /a/foo... done`)
		result, count := rewrite.ReplacePathInBytes(input, "/a/foo", "/x/qux")
		assert.Equal(t, 1, count)
		assert.Equal(t, `see /x/qux... done`, string(result))
	})

	t.Run("still skips path followed by dot then extension letters", func(t *testing.T) {
		input := []byte(`/a/foo.v2 /a/foo.txt /a/foo.git`)
		_, count := rewrite.ReplacePathInBytes(input, "/a/foo", "/x/qux")
		assert.Equal(t, 0, count, "extension dots must still block replacement")
	})

	t.Run("still skips path followed by dot then digit", func(t *testing.T) {
		input := []byte(`/a/foo.2`)
		_, count := rewrite.ReplacePathInBytes(input, "/a/foo", "/x/qux")
		assert.Equal(t, 0, count)
	})

	t.Run("still skips path followed by dot then underscore or dash", func(t *testing.T) {
		input := []byte(`/a/foo._hidden /a/foo.-weird`)
		_, count := rewrite.ReplacePathInBytes(input, "/a/foo", "/x/qux")
		assert.Equal(t, 0, count)
	})
}

func TestRewriteSettingsJSON(t *testing.T) {
	t.Run("replaces project path strings in settings content", func(t *testing.T) {
		input := []byte(`{"allowedPaths":["/old/project","/old/project/subdir"],"other":"value"}`)
		result, count := rewrite.ReplacePathInBytes(input, "/old/project", "/new/project")
		assert.Equal(t, 2, count)
		assert.Contains(t, string(result), "/new/project")
		assert.NotContains(t, string(result), "/old/project")
	})

	t.Run("returns zero count when no matches in settings content", func(t *testing.T) {
		input := []byte(`{"allowedPaths":["/other/project"]}`)
		result, count := rewrite.ReplacePathInBytes(input, "/old/project", "/new/project")
		assert.Equal(t, 0, count)
		assert.Equal(t, input, result)
	})
}

func TestSafeWriteFile(t *testing.T) {
	t.Run("writes data to target path with correct content and permissions", func(t *testing.T) {
		temporaryDirectory := t.TempDir()
		targetPath := filepath.Join(temporaryDirectory, "output.json")
		data := []byte(`{"key": "value"}`)
		permissions := os.FileMode(0o600)

		err := rewrite.SafeWriteFile(targetPath, data, permissions)
		require.NoError(t, err)

		written, err := os.ReadFile(targetPath) //nolint:gosec // G304: path is constructed from t.TempDir()
		require.NoError(t, err)
		assert.Equal(t, data, written)

		info, err := os.Stat(targetPath)
		require.NoError(t, err)
		assert.Equal(t, permissions, info.Mode().Perm())
	})

	t.Run("overwrites existing file with new content", func(t *testing.T) {
		temporaryDirectory := t.TempDir()
		targetPath := filepath.Join(temporaryDirectory, "output.json")

		require.NoError(t, os.WriteFile(targetPath, []byte("old content"), 0o600))

		newData := []byte("new content")
		require.NoError(t, rewrite.SafeWriteFile(targetPath, newData, 0o600))

		written, err := os.ReadFile(targetPath) //nolint:gosec // G304
		require.NoError(t, err)
		assert.Equal(t, newData, written)
	})

	t.Run("returns error when directory does not exist", func(t *testing.T) {
		absentPath := filepath.Join(t.TempDir(), "absent", "file.json")
		err := rewrite.SafeWriteFile(absentPath, []byte("data"), 0o600)
		assert.Error(t, err)
	})
}

func TestSafeRenamePromoter_Files(t *testing.T) {
	t.Run("promotes a file onto a non-existent final", func(t *testing.T) {
		dir := t.TempDir()
		final := filepath.Join(dir, "final.txt")
		temp := filepath.Join(dir, "final.txt.tmp")
		require.NoError(t, os.WriteFile(temp, []byte("staged"), 0o600))

		promoter := rewrite.NewSafeRenamePromoter()
		promoter.StageFile(temp, final)
		require.NoError(t, promoter.Promote())

		data, err := os.ReadFile(final) //nolint:gosec // G304: t.TempDir() path
		require.NoError(t, err)
		assert.Equal(t, "staged", string(data))
		assert.NoFileExists(t, temp)
	})

	t.Run("promotes a file over an existing final", func(t *testing.T) {
		dir := t.TempDir()
		final := filepath.Join(dir, "final.txt")
		temp := filepath.Join(dir, "final.txt.tmp")
		require.NoError(t, os.WriteFile(final, []byte("old"), 0o600))
		require.NoError(t, os.WriteFile(temp, []byte("new"), 0o600))

		promoter := rewrite.NewSafeRenamePromoter()
		promoter.StageFile(temp, final)
		require.NoError(t, promoter.Promote())

		data, err := os.ReadFile(final) //nolint:gosec // G304: t.TempDir() path
		require.NoError(t, err)
		assert.Equal(t, "new", string(data))
	})

	t.Run("rollback restores the pre-promote contents of an existing final", assertRollbackRestoresFile)
	t.Run("rollback removes a promoted file that did not exist before", assertRollbackRemovesNewFile)
}

func TestSafeRenamePromoter_PreservesMtimeOnFileRename(t *testing.T) {
	tempDir := t.TempDir()

	// File-rename case: a temp file with a known mtime promoted via StageFile.
	fileMtime := time.Date(2024, 7, 15, 9, 0, 0, 0, time.UTC)
	fileTemp := filepath.Join(tempDir, "file.tmp")
	fileFinal := filepath.Join(tempDir, "file.final")
	require.NoError(t, os.WriteFile(fileTemp, []byte("body"), 0o600), "write file temp")
	require.NoError(t, os.Chtimes(fileTemp, fileMtime, fileMtime), "set file temp mtime")

	promoter := rewrite.NewSafeRenamePromoter()
	promoter.StageFile(fileTemp, fileFinal)
	require.NoError(t, promoter.Promote(), "promote staged file")

	fileStat, err := os.Stat(fileFinal)
	require.NoError(t, err, "stat promoted file")
	require.WithinDuration(t, fileMtime, fileStat.ModTime(), time.Second,
		"StageFile must preserve mtime on the renamed file")
}

func TestSafeRenamePromoter_PromotionErrorIncludesRollbackFailure(t *testing.T) {
	dir := t.TempDir()
	firstTemp := filepath.Join(dir, "first.tmp")
	secondTemp := filepath.Join(dir, "second.tmp")
	require.NoError(t, os.WriteFile(firstTemp, []byte("first"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(secondTemp, "nested"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(secondTemp, "nested", "body"), []byte("second"), 0o600))

	promoter := rewrite.NewSafeRenamePromoter()
	promoter.StageFile(firstTemp, filepath.Join(dir, "first"))
	promoter.StageFile(secondTemp, filepath.Join(dir, "second"))
	promoter.SetRenameFunc(failOnCallN(2))

	err := promoter.Promote()

	require.Error(t, err)
	require.ErrorContains(t, err, "simulated failure")
	assert.ErrorContains(t, err, "remove unpromoted temp")
}

func assertRollbackRestoresFile(t *testing.T) {
	dir := t.TempDir()
	finalA := filepath.Join(dir, "a.txt")
	tempA := filepath.Join(dir, "a.txt.tmp")
	finalB := filepath.Join(dir, "b.txt")
	tempB := filepath.Join(dir, "b.txt.tmp")

	require.NoError(t, os.WriteFile(finalA, []byte("A-old"), 0o600))
	require.NoError(t, os.WriteFile(finalB, []byte("B-old"), 0o600))
	require.NoError(t, os.WriteFile(tempA, []byte("A-new"), 0o600))
	require.NoError(t, os.WriteFile(tempB, []byte("B-new"), 0o600))

	promoter := rewrite.NewSafeRenamePromoter()
	promoter.StageFile(tempA, finalA)
	promoter.StageFile(tempB, finalB)
	promoter.SetRenameFunc(failOnCallN(2))

	err := promoter.Promote()
	require.Error(t, err)

	got, readErr := os.ReadFile(finalA) //nolint:gosec // G304: t.TempDir() path
	require.NoError(t, readErr)
	assert.Equal(t, "A-old", string(got))

	got, readErr = os.ReadFile(finalB) //nolint:gosec // G304: t.TempDir() path
	require.NoError(t, readErr)
	assert.Equal(t, "B-old", string(got))
}

func assertRollbackRemovesNewFile(t *testing.T) {
	dir := t.TempDir()
	finalA := filepath.Join(dir, "a.txt")
	tempA := filepath.Join(dir, "a.txt.tmp")
	finalB := filepath.Join(dir, "b.txt")
	tempB := filepath.Join(dir, "b.txt.tmp")
	require.NoError(t, os.WriteFile(tempA, []byte("A-new"), 0o600))
	require.NoError(t, os.WriteFile(tempB, []byte("B-new"), 0o600))

	promoter := rewrite.NewSafeRenamePromoter()
	promoter.StageFile(tempA, finalA)
	promoter.StageFile(tempB, finalB)
	promoter.SetRenameFunc(failOnCallN(2))

	err := promoter.Promote()
	require.Error(t, err)

	assert.NoFileExists(t, finalA)
	assert.NoFileExists(t, finalB)
}

// failOnCallN returns a rename hook that invokes os.Rename on every call
// except the nth, where it returns a simulated failure. Centralizes the
// "fail on call N" pattern shared by the rollback sub-tests.
func failOnCallN(n int) func(oldpath, newpath string) error {
	callCount := 0
	return func(oldpath, newpath string) error {
		callCount++
		if callCount == n {
			return errors.New("simulated failure")
		}
		return os.Rename(oldpath, newpath)
	}
}

func TestEscapeSJSONKey(t *testing.T) {
	t.Run("escapes dots so they are not read as nested keys", func(t *testing.T) {
		assert.Equal(t, `/Users/x/proj\.v2`, rewrite.EscapeSJSONKey("/Users/x/proj.v2"))
	})

	t.Run("escapes backslashes before dots", func(t *testing.T) {
		// Order matters: backslash escape must run before dot escape, otherwise
		// the backslash inserted by dot-escaping would be doubled a second time.
		assert.Equal(t, `a\\b\.c`, rewrite.EscapeSJSONKey(`a\b.c`))
	})

	t.Run("leaves keys without metacharacters untouched", func(t *testing.T) {
		assert.Equal(t, "/plain/key", rewrite.EscapeSJSONKey("/plain/key"))
	})

	t.Run("handles empty input", func(t *testing.T) {
		assert.Empty(t, rewrite.EscapeSJSONKey(""))
	})
}

func TestContainsBoundedPath(t *testing.T) {
	projectPath := "/Users/x/myproject"

	t.Run("detects standalone occurrence", func(t *testing.T) {
		data := []byte(`open /Users/x/myproject please`)
		assert.True(t, rewrite.ContainsBoundedPath(data, projectPath))
	})

	t.Run("detects occurrence followed by path separator", func(t *testing.T) {
		data := []byte(`see /Users/x/myproject/main.go for details`)
		assert.True(t, rewrite.ContainsBoundedPath(data, projectPath))
	})

	t.Run("detects occurrence at end of buffer", func(t *testing.T) {
		data := []byte(`cwd: /Users/x/myproject`)
		assert.True(t, rewrite.ContainsBoundedPath(data, projectPath))
	})

	t.Run("detects occurrence terminated by sentence punctuation", func(t *testing.T) {
		data := []byte(`project is /Users/x/myproject. please review`)
		assert.True(t, rewrite.ContainsBoundedPath(data, projectPath))
	})

	t.Run("does not detect prefix collision with sibling directory", func(t *testing.T) {
		data := []byte(`see /Users/x/myproject-extras/notes.md`)
		assert.False(t, rewrite.ContainsBoundedPath(data, projectPath),
			"myproject-extras is a distinct path and must not register as a match")
	})

	t.Run("does not detect prefix collision with extension", func(t *testing.T) {
		data := []byte(`backup at /Users/x/myproject.v2/old.log`)
		assert.False(t, rewrite.ContainsBoundedPath(data, projectPath),
			"myproject.v2 is a different path and must not register")
	})

	t.Run("returns false when path is absent", func(t *testing.T) {
		data := []byte(`no mention of the project here`)
		assert.False(t, rewrite.ContainsBoundedPath(data, projectPath))
	})

	t.Run("returns false for empty inputs", func(t *testing.T) {
		assert.False(t, rewrite.ContainsBoundedPath(nil, projectPath))
		assert.False(t, rewrite.ContainsBoundedPath([]byte(`something`), ""))
	})

	t.Run("matches inside JSON-escaped content", func(t *testing.T) {
		// History lines embed the path inside JSON strings; the bounding
		// byte after the match is typically `"` — still not a
		// path-continuation byte, so the match must register.
		data := []byte(`{"display":"open /Users/x/myproject/main.go"}`)
		assert.True(t, rewrite.ContainsBoundedPath(data, projectPath))
	})
}

func TestCountPathInBytes(t *testing.T) {
	projectPath := "/Users/x/myproject"

	t.Run("counts every bounded occurrence", func(t *testing.T) {
		data := []byte(`/Users/x/myproject /Users/x/myproject/main.go "/Users/x/myproject"`)
		assert.Equal(t, 3, rewrite.CountPathInBytes(data, projectPath))
	})

	t.Run("excludes prefix-sharing sibling names", func(t *testing.T) {
		data := []byte(`/Users/x/myproject-extras /Users/x/myproject2 /Users/x/myproject_bak /Users/x/myproject`)
		assert.Equal(t, 1, rewrite.CountPathInBytes(data, projectPath),
			"only the standalone path counts; -extras, 2, _bak share a prefix")
	})

	t.Run("excludes extension-dot continuation but counts sentence dot", func(t *testing.T) {
		data := []byte(`backup /Users/x/myproject.v2/old then /Users/x/myproject. done`)
		assert.Equal(t, 1, rewrite.CountPathInBytes(data, projectPath),
			".v2 is an extension separator and must not count; the prose dot must")
	})

	t.Run("counts a match at the end of the buffer", func(t *testing.T) {
		data := []byte(`cwd: /Users/x/myproject`)
		assert.Equal(t, 1, rewrite.CountPathInBytes(data, projectPath))
	})

	t.Run("returns zero for empty inputs", func(t *testing.T) {
		assert.Equal(t, 0, rewrite.CountPathInBytes(nil, projectPath))
		assert.Equal(t, 0, rewrite.CountPathInBytes([]byte(`anything`), ""))
	})
}

func TestCountPathInBytesWithJSONEscape(t *testing.T) {
	projectPath := "/Users/me/foo"

	t.Run("counts both raw and JSON-escaped forms", func(t *testing.T) {
		data := []byte(`{"a":"/Users/me/foo","b":"\/Users\/me\/foo\/bar","c":"\/Users\/me\/foo"}`)
		assert.Equal(t, 3, rewrite.CountPathInBytesWithJSONEscape(data, projectPath))
	})

	t.Run("excludes JSON-escaped prefix-sharing names", func(t *testing.T) {
		data := []byte(`["\/Users\/me\/foo","\/Users\/me\/foobar"]`)
		assert.Equal(t, 1, rewrite.CountPathInBytesWithJSONEscape(data, projectPath))
	})

	t.Run("matches CountPathInBytes when no escaped slash is present", func(t *testing.T) {
		data := []byte(`["/Users/me/foo","/Users/me/foo/sub"]`)
		assert.Equal(t, rewrite.CountPathInBytes(data, projectPath),
			rewrite.CountPathInBytesWithJSONEscape(data, projectPath))
	})
}

func TestRewrite_IsArtifactPath(t *testing.T) {
	tests := []struct {
		name string
		base string
		want bool
	}{
		{name: "staging suffix", base: "myproject.cc-port-staging.tmp", want: true},
		{name: "rollback suffix", base: "MEMORY.md.cc-port-rollback.tmp", want: true},
		{name: "import staging suffix", base: "config.toml.cc-port-import.tmp", want: true},
		{name: "safe-write temp prefix", base: ".tmp-123456789", want: true},
		{name: "bare suffix with no real name", base: ".cc-port-rollback.tmp", want: true},
		{name: "lookalike suffix with trailing byte is rejected", base: "foo.cc-port-rollback.tmpx", want: false},
		{name: "ordinary memory file", base: "MEMORY.md", want: false},
		{name: "ordinary directory", base: "myproject", want: false},
		{name: "empty base", base: "", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, rewrite.IsArtifactPath(test.base))
		})
	}
}

// TestCountPathAgreesWithReplace pins the count primitives to the replacers
// they mirror: each must report exactly the replacement count its Replace
// counterpart produces when rewriting the path onto itself. A drift in either
// scan loop fails here.
func TestCountPathAgreesWithReplace(t *testing.T) {
	projectPath := "/Users/x/myproject"
	cases := []struct {
		name string
		data []byte
	}{
		{"standalone and separator-bounded", []byte(`/Users/x/myproject see /Users/x/myproject/a.go`)},
		{"prefix-sharing siblings", []byte(`/Users/x/myproject-extras /Users/x/myproject.v2 /Users/x/myproject`)},
		{"sentence and ellipsis dots", []byte(`at /Users/x/myproject. and /Users/x/myproject... ok`)},
		{"json-escaped slashes", []byte(`{"p":"\/Users\/x\/myproject","q":"\/Users\/x\/myproject\/a"}`)},
		{"absent", []byte(`nothing here`)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, plainReplacements := rewrite.ReplacePathInBytes(testCase.data, projectPath, projectPath)
			assert.Equal(t, plainReplacements, rewrite.CountPathInBytes(testCase.data, projectPath),
				"CountPathInBytes must equal ReplacePathInBytes's count")

			_, escapeReplacements := rewrite.ReplacePathInBytesWithJSONEscape(testCase.data, projectPath, projectPath)
			assert.Equal(t, escapeReplacements, rewrite.CountPathInBytesWithJSONEscape(testCase.data, projectPath),
				"CountPathInBytesWithJSONEscape must equal ReplacePathInBytesWithJSONEscape's count")
		})
	}
}
