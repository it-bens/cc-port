package rewrite_test

import (
	"bytes"
	"testing"

	"github.com/it-bens/cc-port/internal/rewrite"
)

// FuzzReplacePathInBytes exercises ReplacePathInBytes across arbitrary inputs
// and asserts three invariants every call must satisfy:
//   - empty oldPath is a no-op returning count 0
//   - oldPath == newPath is a byte-for-byte identity rewrite
//   - output length equals len(input) + count*(len(newPath)-len(oldPath))
//
// The length equality catches off-by-one slice bugs that would otherwise
// silently truncate or duplicate bytes around a boundary match.
func FuzzReplacePathInBytes(f *testing.F) {
	f.Add([]byte("/a/myproject/foo and /a/myproject-extras/bar"), "/a/myproject", "/a/renamed")
	f.Add([]byte("see /a/foo.v2 and /a/foo. end"), "/a/foo", "/a/bar")
	f.Add([]byte(""), "", "")
	f.Add([]byte("no match in this string"), "/never", "/here")
	f.Add([]byte("overlapping abc abcabc"), "abc", "xy")
	f.Add([]byte(`{"a":"\/Users\/me\/foo\/bar","b":"\/Users\/me\/foobar"}`), "/Users/me/foo", "/Users/me/bar")
	f.Add([]byte(`"\/Users\/me\/foo"`), "/Users/me/foo", "/Users/me/bar")
	f.Add([]byte(`[{"p":"/Users/me/foo","q":"\/Users\/me\/foo"}]`), "/Users/me/foo", "/Users/me/bar")

	f.Fuzz(func(t *testing.T, data []byte, oldPath, newPath string) {
		output, count := rewrite.ReplacePathInBytes(data, oldPath, newPath)

		if count < 0 {
			t.Fatalf("negative replacement count: %d", count)
		}

		if oldPath == "" {
			if count != 0 {
				t.Fatalf("empty oldPath produced non-zero count %d", count)
			}
			if !bytes.Equal(output, data) {
				t.Fatalf("empty oldPath mutated data: input=%q output=%q", data, output)
			}
			return
		}

		if oldPath == newPath {
			if !bytes.Equal(output, data) {
				t.Fatalf("identity rewrite mutated bytes: input=%q output=%q", data, output)
			}
			return
		}

		expectedLength := len(data) + count*(len(newPath)-len(oldPath))
		if len(output) != expectedLength {
			t.Fatalf(
				"output length mismatch: got=%d want=%d (count=%d oldLen=%d newLen=%d)",
				len(output), expectedLength, count, len(oldPath), len(newPath),
			)
		}
	})
}
