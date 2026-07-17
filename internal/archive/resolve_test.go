package archive_test

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/archive"
)

func TestApplyResolutions_SubstitutesEveryOccurrence(t *testing.T) {
	content := []byte("path is __PROJECT_PATH__ and again __PROJECT_PATH__")
	resolutions := map[string]string{
		"__PROJECT_PATH__": "/home/user/myproject",
	}

	result, err := archive.ApplyResolutions(content, resolutions)

	require.NoError(t, err)
	assert.Equal(t, []byte("path is /home/user/myproject and again /home/user/myproject"), result)
}

func TestApplyResolutions_UnresolvedLeft(t *testing.T) {
	content := []byte("known: __KNOWN__ unknown: __UNKNOWN__")
	resolutions := map[string]string{
		"__KNOWN__": "/home/user/known",
	}

	result, err := archive.ApplyResolutions(content, resolutions)

	require.NoError(t, err)
	assert.Equal(t, []byte("known: /home/user/known unknown: __UNKNOWN__"), result)
}

// FuzzApplyResolutions exercises archive.ApplyResolutions against arbitrary
// byte bodies and single-key resolution maps, asserting three invariants:
//   - empty resolutions is an identity transform
//   - a key absent from data is an identity transform
//   - the output length equals len(data) + count*(len(value)-len(key)), where
//     count is the non-overlapping occurrence count bytes.ReplaceAll sees
//
// The length-accounting property catches off-by-one bugs in any future
// implementation change (e.g. swapping the single-pass bytes.ReplaceAll for a
// looped rewrite) without asserting full key disappearance — that stronger
// claim only holds under the production `{{UPPER_SNAKE}}` grammar, where
// values are resolved paths that cannot reconstruct a key at a boundary.
//
// The function must never panic — a crash on unexpected bytes would abort the
// import stage-and-swap after the archive had been extracted to disk.
func FuzzApplyResolutions(f *testing.F) {
	f.Add([]byte("carries {{PROJECT_PATH}} in body"), "{{PROJECT_PATH}}", "/new/path")
	f.Add([]byte(""), "{{HOME}}", "/home/newuser")
	f.Add([]byte("no tokens here"), "{{NOPE}}", "/absent")
	f.Add([]byte("{{X}} and {{X}} twice"), "{{X}}", "Y")

	f.Fuzz(func(t *testing.T, data []byte, key, value string) {
		if key == "" {
			t.Skip()
		}

		emptyResolutionOutput, err := archive.ApplyResolutions(data, map[string]string{})
		require.NoError(t, err)
		if !bytes.Equal(emptyResolutionOutput, data) {
			t.Fatalf("empty resolutions modified input: got %q", emptyResolutionOutput)
		}

		if !bytes.Contains(data, []byte(key)) {
			absentKeyOutput, err := archive.ApplyResolutions(data, map[string]string{key: value})
			require.NoError(t, err)
			if !bytes.Equal(absentKeyOutput, data) {
				t.Fatalf("absent key mutated input: got %q", absentKeyOutput)
			}
			return
		}

		occurrenceCount := bytes.Count(data, []byte(key))
		presentKeyOutput, err := archive.ApplyResolutions(data, map[string]string{key: value})
		require.NoError(t, err)
		expectedLength := len(data) + occurrenceCount*(len(value)-len(key))
		if len(presentKeyOutput) != expectedLength {
			t.Fatalf(
				"output length mismatch: got=%d want=%d (count=%d keyLen=%d valueLen=%d)",
				len(presentKeyOutput), expectedLength,
				occurrenceCount, len(key), len(value),
			)
		}
	})
}

func TestApplyResolutions_RejectsExpandedEntryOverCap(t *testing.T) {
	restore := archive.SetCaps(archive.Caps{MaxEntryBytes: 8, MaxAggregateBytes: 64})
	t.Cleanup(restore)

	_, err := archive.ApplyResolutions([]byte("{{X}}"), map[string]string{"{{X}}": "/expanded/"})

	require.ErrorIs(t, err, archive.ErrEntryCapExceeded)
}

func TestResolvePlaceholdersStream_PassesThroughWhenNoResolutions(t *testing.T) {
	src := bytes.NewReader([]byte("hello {{UNKNOWN}} world"))
	var dst bytes.Buffer

	err := archive.ResolvePlaceholdersStream(src, &dst, nil)

	require.NoError(t, err)
	assert.Equal(t, "hello {{UNKNOWN}} world", dst.String())
}

func TestResolvePlaceholdersStream_ReplacesTokenAtStart(t *testing.T) {
	src := bytes.NewReader([]byte("{{HOME}}/projects/myproject"))
	var dst bytes.Buffer

	err := archive.ResolvePlaceholdersStream(src, &dst, map[string]string{
		"{{HOME}}": "/Users/dest",
	})

	require.NoError(t, err)
	assert.Equal(t, "/Users/dest/projects/myproject", dst.String())
}

func TestResolvePlaceholdersStream_ReplacesTokenInMiddle(t *testing.T) {
	src := bytes.NewReader([]byte("prefix {{KEY}} suffix"))
	var dst bytes.Buffer

	err := archive.ResolvePlaceholdersStream(src, &dst, map[string]string{
		"{{KEY}}": "value",
	})

	require.NoError(t, err)
	assert.Equal(t, "prefix value suffix", dst.String())
}

func TestResolvePlaceholdersStream_ReplacesTokenAtEnd(t *testing.T) {
	src := bytes.NewReader([]byte("prefix {{KEY}}"))
	var dst bytes.Buffer

	err := archive.ResolvePlaceholdersStream(src, &dst, map[string]string{
		"{{KEY}}": "value",
	})

	require.NoError(t, err)
	assert.Equal(t, "prefix value", dst.String())
}

func TestResolvePlaceholdersStream_LoneOpenBraceSurvives(t *testing.T) {
	src := bytes.NewReader([]byte("single { brace"))
	var dst bytes.Buffer

	err := archive.ResolvePlaceholdersStream(src, &dst, map[string]string{
		"{{KEY}}": "value",
	})

	require.NoError(t, err)
	assert.Equal(t, "single { brace", dst.String())
}

// chunkReader yields chunk bytes per Read call, emulating a network-style
// reader whose boundaries don't align with token boundaries. Used to verify
// tokens straddling a buffered-reader fill produce the same output as a
// whole-body transform.
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
	// token's first byte and the rest of the token cross many fill cycles
	// of the internal bufio.Reader.
	body := []byte("abc{{KEY}}xyz and tail {{KEY}} end")
	resolutions := map[string]string{"{{KEY}}": "RESOLVED"}

	var streamed bytes.Buffer
	require.NoError(t, archive.ResolvePlaceholdersStream(
		&chunkReader{data: body, chunk: 1}, &streamed, resolutions,
	))

	assert.Equal(t, "abcRESOLVEDxyz and tail RESOLVED end", streamed.String(),
		"streaming output must equal whole-body substitution output")
}
