package importer_test

import (
	"bytes"
	"testing"

	"github.com/it-bens/cc-port/internal/importer"
)

// FuzzResolvePlaceholders exercises ResolvePlaceholders against arbitrary
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
func FuzzResolvePlaceholders(f *testing.F) {
	f.Add([]byte("carries {{PROJECT_PATH}} in body"), "{{PROJECT_PATH}}", "/new/path")
	f.Add([]byte(""), "{{HOME}}", "/home/newuser")
	f.Add([]byte("no tokens here"), "{{NOPE}}", "/absent")
	f.Add([]byte("{{X}} and {{X}} twice"), "{{X}}", "Y")

	f.Fuzz(func(t *testing.T, data []byte, key, value string) {
		if key == "" {
			t.Skip()
		}

		emptyResolutionOutput := importer.ResolvePlaceholders(data, map[string]string{})
		if !bytes.Equal(emptyResolutionOutput, data) {
			t.Fatalf("empty resolutions modified input: got %q", emptyResolutionOutput)
		}

		if !bytes.Contains(data, []byte(key)) {
			absentKeyOutput := importer.ResolvePlaceholders(
				data, map[string]string{key: value},
			)
			if !bytes.Equal(absentKeyOutput, data) {
				t.Fatalf("absent key mutated input: got %q", absentKeyOutput)
			}
			return
		}

		occurrenceCount := bytes.Count(data, []byte(key))
		presentKeyOutput := importer.ResolvePlaceholders(
			data, map[string]string{key: value},
		)
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
