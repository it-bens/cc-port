package tool_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/it-bens/cc-port/internal/tool"
)

// EscapeControl's contract spans three byte classes a render-level test never
// exercises together: the C1 range, DEL, and bytes that are not valid UTF-8.
// Pinning each class here keeps a later edit to the byte-vs-rune branching
// from silently regressing one of them. C1 inputs are spelled as their UTF-8
// byte pairs so every byte in this file stays printable ASCII.
func TestEscapeControlRevealsEveryControlClass(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"clean ASCII renders verbatim", "npx -y server", "npx -y server"},
		{"printable multi-byte runes render verbatim", "café 日本語", "café 日本語"},
		{"empty string stays empty", "", ""},
		{"ESC reveals as its hex escape", "a\x1b[2Kb", "a\\x1b[2Kb"},
		{"CR and LF reveal as their short escapes", "a\r\nb", "a\\r\\nb"},
		{"tab reveals as its short escape", "a\tb", "a\\tb"},
		{"DEL reveals as its hex escape", "a\x7fb", "a\\x7fb"},
		{"C1 NEL reveals as its unicode escape", "a\xc2\x85b", "a\\u0085b"},
		{"C1 CSI reveals as its unicode escape", "a\xc2\x9bb", "a\\u009bb"},
		{"a bare invalid byte reveals as its hex escape", "a\x9bb", "a\\x9bb"},
		{"a truncated multi-byte sequence reveals per byte", "a\xc2", "a\\xc2"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, tool.EscapeControl(testCase.input))
		})
	}
}

// An empty argument would otherwise render as nothing, leaving
// args:["a","","b"] and args:["a","b"] distinguishable only by counting
// spaces at the consent surface.
func TestLaunchLineQuotesAnEmptyArgument(t *testing.T) {
	server := tool.MCPServer{Command: "run", Args: []string{"a", "", "b"}}

	assert.Equal(t, `run a "" b`, server.LaunchLine())
}
