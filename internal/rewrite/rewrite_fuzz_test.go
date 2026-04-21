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

		if len(oldPath) == 0 {
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

// FuzzFindPlaceholderTokens exercises the narrow placeholder scanner against
// arbitrary input and asserts every returned token:
//   - is distinct within the result slice
//   - matches the documented grammar `{{[A-Z0-9_]{1,64}}}`
//   - appears as a literal substring of the input
func FuzzFindPlaceholderTokens(f *testing.F) {
	f.Add([]byte(""))
	f.Add([]byte("{{PROJECT_PATH}} and {{HOME}}"))
	f.Add([]byte("{{ lowercase }} not matched"))
	f.Add([]byte("{{unterminated"))
	f.Add([]byte("{{NESTED{{INNER}}}}"))
	f.Add([]byte("{{A}}{{B}}{{A}}"))

	f.Fuzz(func(t *testing.T, data []byte) {
		tokens := rewrite.FindPlaceholderTokens(data)

		seen := make(map[string]struct{}, len(tokens))
		for _, token := range tokens {
			if _, duplicate := seen[token]; duplicate {
				t.Fatalf("duplicate token %q returned", token)
			}
			seen[token] = struct{}{}

			if !isValidPlaceholderToken(token) {
				t.Fatalf("token %q violates documented grammar", token)
			}
			if !bytes.Contains(data, []byte(token)) {
				t.Fatalf("token %q not present in input", token)
			}
		}
	})
}

// isValidPlaceholderToken restates the grammar described in
// rewrite.FindPlaceholderTokens independently of the scanner implementation,
// so a scanner bug that manufactures an ill-formed token cannot pass the fuzz
// check just because the assertion was derived from the same code.
func isValidPlaceholderToken(token string) bool {
	const openLen, closeLen = 2, 2
	if len(token) < openLen+1+closeLen {
		return false
	}
	if token[0] != '{' || token[1] != '{' {
		return false
	}
	if token[len(token)-1] != '}' || token[len(token)-2] != '}' {
		return false
	}
	key := token[openLen : len(token)-closeLen]
	if len(key) == 0 || len(key) > 64 {
		return false
	}
	for index := range len(key) {
		keyByte := key[index]
		switch {
		case keyByte >= 'A' && keyByte <= 'Z':
		case keyByte >= '0' && keyByte <= '9':
		case keyByte == '_':
		default:
			return false
		}
	}
	return true
}
