package archive_test

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/archive"
)

func TestResolveEntryBytes_SubstitutesEveryOccurrence(t *testing.T) {
	body := buildZip(t, map[string]string{
		"claude/note.txt": "path is {{PROJECT_PATH}} and again {{PROJECT_PATH}}",
	})
	reader, err := archive.OpenReader(bytes.NewReader(body), int64(len(body)), archive.DefaultCaps())
	require.NoError(t, err)
	entries, err := reader.RawEntries()
	require.NoError(t, err)

	result, err := archive.ResolveEntryBytes(entries[0].Entry, map[string]string{
		"{{PROJECT_PATH}}": "/home/user/myproject",
	})

	require.NoError(t, err)
	assert.Equal(t, []byte("path is /home/user/myproject and again /home/user/myproject"), result)
}

func TestResolveEntryBytes_LeavesUnresolvedKeyVerbatim(t *testing.T) {
	body := buildZip(t, map[string]string{
		"claude/note.txt": "known: {{KNOWN}} unknown: {{UNKNOWN}}",
	})
	reader, err := archive.OpenReader(bytes.NewReader(body), int64(len(body)), archive.DefaultCaps())
	require.NoError(t, err)
	entries, err := reader.RawEntries()
	require.NoError(t, err)

	result, err := archive.ResolveEntryBytes(entries[0].Entry, map[string]string{
		"{{KNOWN}}": "/home/user/known",
	})

	require.NoError(t, err)
	assert.Equal(t, []byte("known: /home/user/known unknown: {{UNKNOWN}}"), result)
}

// TestResolveEntryBytes_BoundsExpansion builds a tiny archive whose declared
// entry decodes well under a 1 MiB cap, but whose resolved output — a short
// token repeated 20,000 times, each expanding to 80 bytes — would exceed
// 1.5 MiB if fully substituted. It asserts the cap error fires, not that the
// process exhausts memory building the substituted body first: routing
// through ResolvePlaceholdersStream's countingWriter means the cap trips
// as the ~64 KiB internal write buffer flushes, long before the full
// expansion could be assembled.
func TestResolveEntryBytes_BoundsExpansion(t *testing.T) {
	const repeats = 20_000
	body := buildZip(t, map[string]string{
		"claude/note.txt": strings.Repeat("{{X}}", repeats),
	})
	reader, err := archive.OpenReader(bytes.NewReader(body), int64(len(body)), archive.Caps{
		MaxEntryBytes: 1 << 20, MaxAggregateBytes: 1 << 30,
	})
	require.NoError(t, err)
	entries, err := reader.RawEntries()
	require.NoError(t, err)

	_, err = archive.ResolveEntryBytes(entries[0].Entry, map[string]string{
		"{{X}}": strings.Repeat("Y", 80),
	})

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

// errCapCheckWriterFull is capCheckWriter's write-over-limit error.
var errCapCheckWriterFull = errors.New("capCheckWriter: over limit")

// capCheckWriter enforces a byte cap the way archive's internal
// countingWriter does: a Write that would push the running total over limit
// writes nothing and returns an error, so the underlying buffer can never
// exceed limit. FuzzResolvePlaceholdersStream uses it to confirm
// ResolvePlaceholdersStream always writes through the io.Writer it is given,
// rather than around it, and propagates a capping writer's error instead of
// swallowing it.
type capCheckWriter struct {
	inner *bytes.Buffer
	limit int
}

func (w *capCheckWriter) Write(p []byte) (int, error) {
	if w.inner.Len()+len(p) > w.limit {
		return 0, errCapCheckWriterFull
	}
	return w.inner.Write(p)
}

// FuzzResolvePlaceholdersStream exercises ResolvePlaceholdersStream — the
// primitive both the in-memory (ResolveEntryBytes) and streaming
// (StageSibling) resolution paths share — against arbitrary bodies and a
// single well-formed placeholder key. The manifest package's
// validatePlaceholderKeys gate guarantees every key ResolvePlaceholdersStream
// ever sees in production is "{{...}}"-shaped, so the fuzzed key is
// constructed in that shape rather than left arbitrary: an arbitrary key
// would exercise a domain the resolver's '{'-anchored matching can never
// reach, the same gap that let a malformed declared key pass through
// unsubstituted before the grammar gate existed.
//
// Reading through chunkReader (one byte per Read call) rather than
// bytes.NewReader forces every fuzzed body through the peek/discard path at
// many different read-boundary positions, not just whatever chunking
// bytes.Reader happens to produce for a given input length.
//
// Three invariants hold regardless of body or key/value content:
//   - nil resolutions is an identity transform (no key can ever match)
//   - a key absent from the body is an identity transform
//   - present-key output length equals len(data) + count*(len(value)-len(key)),
//     where count is the non-overlapping occurrence count — the same
//     length-accounting property FuzzApplyResolutions asserted before
//     ResolveEntryBytes was unified onto this primitive. A stronger
//     "the raw key never reappears in output" claim is deliberately not
//     asserted: a value abutting an unmatched literal '{' in the body can
//     coincidentally reconstruct the key's bytes at the boundary, which is
//     not a resolver bug.
//   - output never exceeds a bounding writer's cap, and any error the
//     bounding writer returns propagates rather than being swallowed.
//
// The seed corpus anchors adjacent placeholders ("{{A}}{{B}}"), a declared
// key juxtaposed with a similarly-named but undeclared token ("{{PROJECT}}"
// beside declared "{{PROJECT_PATH}}"), and a longer declared key.
func FuzzResolvePlaceholdersStream(f *testing.F) {
	f.Add([]byte("carries {{PROJECT_PATH}} in body"), "PROJECT_PATH", "/new/path")
	f.Add([]byte(""), "HOME", "/home/newuser")
	f.Add([]byte("no tokens here"), "NOPE", "/absent")
	f.Add([]byte("{{X}} and {{X}} twice"), "X", "Y")
	f.Add([]byte("{{A}}{{B}}"), "A", "1")
	f.Add([]byte("path is {{PROJECT_PATH}} and {{PROJECT}} too"), "PROJECT_PATH", "/resolved")
	f.Add([]byte("pre{{LONGER_DECLARED_KEY}}post"), "LONGER_DECLARED_KEY", "V")

	f.Fuzz(func(t *testing.T, data []byte, innerKey, value string) {
		if innerKey == "" || strings.Contains(value, "{{") {
			t.Skip()
		}
		key := "{{" + innerKey + "}}"

		var identityOut bytes.Buffer
		require.NoError(t, archive.ResolvePlaceholdersStream(
			&chunkReader{data: data, chunk: 1}, &identityOut, nil,
		))
		if !bytes.Equal(identityOut.Bytes(), data) {
			t.Fatalf("nil resolutions modified input: got %q", identityOut.Bytes())
		}

		var out bytes.Buffer
		const limit = 1 << 20
		bounded := &capCheckWriter{inner: &out, limit: limit}
		err := archive.ResolvePlaceholdersStream(
			&chunkReader{data: data, chunk: 1}, bounded, map[string]string{key: value},
		)
		if out.Len() > limit {
			t.Fatalf("capCheckWriter cap violated: wrote %d bytes, limit %d", out.Len(), limit)
		}
		if err != nil {
			require.ErrorIs(t, err, errCapCheckWriterFull, "the only acceptable error is the writer's own cap")
			return
		}

		if !bytes.Contains(data, []byte(key)) {
			if !bytes.Equal(out.Bytes(), data) {
				t.Fatalf("absent key mutated input: got %q want %q", out.Bytes(), data)
			}
			return
		}

		occurrenceCount := bytes.Count(data, []byte(key))
		expectedLength := len(data) + occurrenceCount*(len(value)-len(key))
		if out.Len() != expectedLength {
			t.Fatalf(
				"output length mismatch: got=%d want=%d (count=%d keyLen=%d valueLen=%d)",
				out.Len(), expectedLength,
				occurrenceCount, len(key), len(value),
			)
		}
	})
}
