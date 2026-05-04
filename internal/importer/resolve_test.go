package importer_test

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/importer"
	"github.com/it-bens/cc-port/internal/manifest"
)

func TestResolvePlaceholdersStream_PassesThroughWhenNoResolutions(t *testing.T) {
	src := bytes.NewReader([]byte("hello {{UNKNOWN}} world"))
	var dst bytes.Buffer

	err := importer.ResolvePlaceholdersStream(src, &dst, nil)

	require.NoError(t, err)
	assert.Equal(t, "hello {{UNKNOWN}} world", dst.String())
}

func TestResolvePlaceholdersStream_ReplacesTokenAtStart(t *testing.T) {
	src := bytes.NewReader([]byte("{{HOME}}/projects/myproject"))
	var dst bytes.Buffer

	err := importer.ResolvePlaceholdersStream(src, &dst, map[string]string{
		"{{HOME}}": "/Users/dest",
	})

	require.NoError(t, err)
	assert.Equal(t, "/Users/dest/projects/myproject", dst.String())
}

func TestResolvePlaceholdersStream_ReplacesTokenInMiddle(t *testing.T) {
	src := bytes.NewReader([]byte("prefix {{KEY}} suffix"))
	var dst bytes.Buffer

	err := importer.ResolvePlaceholdersStream(src, &dst, map[string]string{
		"{{KEY}}": "value",
	})

	require.NoError(t, err)
	assert.Equal(t, "prefix value suffix", dst.String())
}

func TestResolvePlaceholdersStream_ReplacesTokenAtEnd(t *testing.T) {
	src := bytes.NewReader([]byte("prefix {{KEY}}"))
	var dst bytes.Buffer

	err := importer.ResolvePlaceholdersStream(src, &dst, map[string]string{
		"{{KEY}}": "value",
	})

	require.NoError(t, err)
	assert.Equal(t, "prefix value", dst.String())
}

func TestResolvePlaceholdersStream_PrefersLongestMatchingKey(t *testing.T) {
	// One key is a strict prefix of the other. Longest-first ordering
	// must resolve `{{PROJECT_PATH}}` as a whole rather than consuming
	// `{{PROJECT}}` and leaving `_PATH}}` behind.
	src := bytes.NewReader([]byte("{{PROJECT_PATH}} and {{PROJECT}}"))
	var dst bytes.Buffer

	err := importer.ResolvePlaceholdersStream(src, &dst, map[string]string{
		"{{PROJECT}}":      "P",
		"{{PROJECT_PATH}}": "PP",
	})

	require.NoError(t, err)
	assert.Equal(t, "PP and P", dst.String())
}

func TestResolvePlaceholdersStream_LoneOpenBraceSurvives(t *testing.T) {
	src := bytes.NewReader([]byte("single { brace"))
	var dst bytes.Buffer

	err := importer.ResolvePlaceholdersStream(src, &dst, map[string]string{
		"{{KEY}}": "value",
	})

	require.NoError(t, err)
	assert.Equal(t, "single { brace", dst.String())
}

// chunkReader yields n bytes per Read call, emulating a network-style
// reader whose boundaries don't align with token boundaries. Used to
// verify tokens straddling a buffered-reader fill produce the same
// output as a whole-body transform.
type chunkReader struct {
	data  []byte
	chunk int
	pos   int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := r.chunk
	if n > len(p) {
		n = len(p)
	}
	remaining := len(r.data) - r.pos
	if n > remaining {
		n = remaining
	}
	copy(p, r.data[r.pos:r.pos+n])
	r.pos += n
	return n, nil
}

func TestResolvePlaceholdersStream_HandlesTokenStraddlingReadBoundary(t *testing.T) {
	// A body with a placeholder token interleaved with surrounding bytes.
	// The chunkReader hands data to the stream one byte per Read, so the
	// token's first byte and the rest of the token cross many fill
	// cycles of the internal bufio.Reader.
	body := []byte("abc{{KEY}}xyz and tail {{KEY}} end")
	resolutions := map[string]string{"{{KEY}}": "RESOLVED"}

	var streamed bytes.Buffer
	require.NoError(t, importer.ResolvePlaceholdersStream(
		&chunkReader{data: body, chunk: 1}, &streamed, resolutions,
	))

	assert.Equal(t, "abcRESOLVEDxyz and tail RESOLVED end", streamed.String(),
		"streaming output must equal whole-body substitution output")
}

func TestValidateResolutions(t *testing.T) {
	tests := []struct {
		name        string
		resolutions map[string]string
		wantErr     bool
	}{
		{
			name: "valid absolute paths",
			resolutions: map[string]string{
				"{{PLACEHOLDER}}": "/home/user/project",
			},
			wantErr: false,
		},
		{
			name: "empty value is always rejected",
			resolutions: map[string]string{
				"{{PLACEHOLDER}}": "",
			},
			wantErr: true,
		},
		{
			name: "relative path is rejected",
			resolutions: map[string]string{
				"{{PLACEHOLDER}}": "relative/path",
			},
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			err := importer.ValidateResolutions(testCase.resolutions)
			if testCase.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestClassifyPlaceholders(t *testing.T) {
	for _, testCase := range classifyPlaceholderCases() {
		t.Run(testCase.name, func(t *testing.T) {
			missing, undeclared := importer.ClassifyPlaceholders(
				testCase.bodies, testCase.declared, testCase.resolutions,
			)
			assert.Equal(t, testCase.wantMissing, missing)
			assert.Equal(t, testCase.wantUndeclared, undeclared)
		})
	}
}

// classifyPlaceholderCases returns the table feeding TestClassifyPlaceholders.
// Kept as a helper so the test body stays under funlen while all cases live
// in one place.
func classifyPlaceholderCases() []classifyCase {
	cases := classifyUpperSnakeCases()
	cases = append(cases, classifyExoticShapeCases()...)
	return cases
}

func classifyUpperSnakeCases() []classifyCase {
	return []classifyCase{
		{
			name:   "all present keys resolved",
			bodies: [][]byte{[]byte(`cwd={{PROJECT_PATH}}, home={{HOME}}`)},
			declared: []manifest.Placeholder{
				{Key: "{{PROJECT_PATH}}"},
				{Key: "{{HOME}}"},
			},
			resolutions: map[string]string{
				"{{PROJECT_PATH}}": "/dest/project",
				"{{HOME}}":         "/dest",
			},
		},
		{
			name:        "declared + present + not resolved is missing",
			bodies:      [][]byte{[]byte(`extra={{EXTRA}}`)},
			declared:    []manifest.Placeholder{{Key: "{{EXTRA}}"}},
			wantMissing: []string{"{{EXTRA}}"},
		},
		{
			name:           "present but not declared is undeclared",
			bodies:         [][]byte{[]byte(`leaked={{SECRET}}`)},
			declared:       []manifest.Placeholder{{Key: "{{PROJECT_PATH}}"}},
			wantUndeclared: []string{"{{SECRET}}"},
		},
		{
			name:   "both missing and undeclared populated and sorted",
			bodies: [][]byte{[]byte(`{{ZETA}} {{ALPHA}} {{UNDECLARED_B}} {{UNDECLARED_A}}`)},
			declared: []manifest.Placeholder{
				{Key: "{{ZETA}}"},
				{Key: "{{ALPHA}}"},
			},
			wantMissing:    []string{"{{ALPHA}}", "{{ZETA}}"},
			wantUndeclared: []string{"{{UNDECLARED_A}}", "{{UNDECLARED_B}}"},
		},
		{
			name:     "declared but absent from bodies is clean",
			bodies:   [][]byte{[]byte("no placeholder tokens here")},
			declared: []manifest.Placeholder{{Key: "{{UNUSED}}"}},
		},
		{
			// importer.Run pre-fills PROJECT_PATH unconditionally.
			name:     "PROJECT_PATH resolved implicitly even without explicit resolution",
			bodies:   [][]byte{[]byte(`cwd={{PROJECT_PATH}}`)},
			declared: []manifest.Placeholder{{Key: "{{PROJECT_PATH}}"}},
		},
	}
}

// classifyExoticShapeCases exercises manifest-driven resolution against key
// shapes the upper-snake scanner cannot see (punctuation, lowercase).
// Resolution is now driven by literal substring search over declared keys,
// so grammar does not affect classification.
func classifyExoticShapeCases() []classifyCase {
	return []classifyCase{
		{
			name:        "exotic-shape declared key missing a resolution is flagged",
			bodies:      [][]byte{[]byte(`path={{my-weird.key}}`)},
			declared:    []manifest.Placeholder{{Key: "{{my-weird.key}}"}},
			wantMissing: []string{"{{my-weird.key}}"},
		},
		{
			name:   "exotic-shape declared key with a resolution is clean",
			bodies: [][]byte{[]byte(`path={{my-weird.key}}`)},
			declared: []manifest.Placeholder{
				{Key: "{{my-weird.key}}"},
			},
			resolutions: map[string]string{"{{my-weird.key}}": "/dest/weird"},
		},
	}
}

// classifyCase captures one expected outcome of ClassifyPlaceholders for the
// table-driven TestClassifyPlaceholders.
type classifyCase struct {
	name           string
	bodies         [][]byte
	declared       []manifest.Placeholder
	resolutions    map[string]string
	wantMissing    []string
	wantUndeclared []string
}

func TestIsImplicitKey_RecognizesHome(t *testing.T) {
	assert.True(t, importer.IsImplicitKey("{{HOME}}"),
		"{{HOME}} must be implicit so the orchestrator filters it from prompts and importer.Run injects it")
	assert.True(t, importer.IsImplicitKey("{{PROJECT_PATH}}"),
		"{{PROJECT_PATH}} stays implicit (regression guard)")
	assert.False(t, importer.IsImplicitKey("{{ARBITRARY_KEY}}"))
}

func TestCheckConflict(t *testing.T) {
	t.Run("no conflict when directory does not exist", func(t *testing.T) {
		nonExistentDir := filepath.Join(t.TempDir(), "does-not-exist")

		err := importer.CheckConflict(nonExistentDir)

		assert.NoError(t, err)
	})

	t.Run("conflict when directory exists", func(t *testing.T) {
		existingDir := t.TempDir()
		require.DirExists(t, existingDir)

		err := importer.CheckConflict(existingDir)

		require.ErrorIs(t, err, importer.ErrEncodedDirCollision)
	})

	t.Run("wraps stat error when existence cannot be determined", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX search-permission semantics do not apply on Windows")
		}
		if os.Geteuid() == 0 {
			t.Skip("root bypasses POSIX search-permission checks")
		}

		tempDir := t.TempDir()
		lockedParent := filepath.Join(tempDir, "locked")
		require.NoError(t, os.Mkdir(lockedParent, 0o700))
		// Drop search permission so Stat on a child cannot traverse the parent.
		require.NoError(t, os.Chmod(lockedParent, 0o000))
		// Restore the execute bit before t.TempDir cleanup runs (LIFO order).
		t.Cleanup(func() {
			_ = os.Chmod(lockedParent, 0o700) //nolint:gosec // G302: restore dir traversal
		})

		target := filepath.Join(lockedParent, "target")

		err := importer.CheckConflict(target)

		require.Error(t, err)
		require.NotErrorIs(t, err, importer.ErrEncodedDirCollision,
			"must not silently treat a stat error as 'no conflict'")
		require.ErrorIs(t, err, importer.ErrStatProjectDirectory)
		require.ErrorIs(t, err, fs.ErrPermission)
	})
}
