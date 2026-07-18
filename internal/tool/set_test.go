package tool_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/tool"
)

// fakeTool is a minimal tool.Tool double for registry-validation tests.
// It never opens a real Workspace; NewSet only needs Name/Categories/
// ImplicitAnchorKeys at construction time.
type fakeTool struct {
	name       string
	categories []tool.Category
	anchorKeys []string
	detected   bool
	detectErr  error
}

func (f *fakeTool) Name() string                 { return f.name }
func (f *fakeTool) DisplayName() string          { return f.name }
func (f *fakeTool) Categories() []tool.Category  { return f.categories }
func (f *fakeTool) ImplicitAnchorKeys() []string { return f.anchorKeys }
func (f *fakeTool) Detect() (bool, error)        { return f.detected, f.detectErr }
func (f *fakeTool) Open(string) (tool.Workspace, error) {
	return nil, nil //nolint:nilnil // Open is not exercised by these registry-validation tests
}

func TestNewSet_AcceptsDistinctTools(t *testing.T) {
	a := &fakeTool{name: "claude", categories: []tool.Category{{Name: "sessions"}}, anchorKeys: []string{"{{A}}"}}
	b := &fakeTool{name: "codex", categories: []tool.Category{{Name: "sessions"}}, anchorKeys: []string{"{{B}}"}}

	set := tool.NewSet(a, b)
	assert.Len(t, set.All(), 2)
}

func TestNewSet_PanicsOnDuplicateName(t *testing.T) {
	a := &fakeTool{name: "claude"}
	b := &fakeTool{name: "claude"}

	assert.PanicsWithValue(t, `tool.NewSet: duplicate tool name "claude"`, func() { tool.NewSet(a, b) })
}

func TestNewSet_PanicsOnDuplicateQualifiedCategoryWithinOneTool(t *testing.T) {
	a := &fakeTool{name: "claude", categories: []tool.Category{{Name: "sessions"}, {Name: "sessions"}}}

	assert.PanicsWithValue(t, "tool.NewSet: duplicate qualified category claude/sessions", func() { tool.NewSet(a) })
}

func TestNewSet_PanicsOnDuplicatePlaceholderKeyAcrossTools(t *testing.T) {
	a := &fakeTool{name: "claude", anchorKeys: []string{"{{HOME}}"}}
	b := &fakeTool{name: "codex", anchorKeys: []string{"{{HOME}}"}}

	assert.PanicsWithValue(t, `tool.NewSet: placeholder key {{HOME}} claimed by both "claude" and "codex"`, func() { tool.NewSet(a, b) })
}

func TestNewSet_PanicsOnEmptyName(t *testing.T) {
	assert.PanicsWithValue(t, "tool.NewSet: a tool registered with an empty Name()", func() {
		tool.NewSet(&fakeTool{})
	})
}

func TestNewSet_PanicsOnEmptyRegistry(t *testing.T) {
	assert.PanicsWithValue(t, "tool.NewSet: no tools registered", func() { tool.NewSet() })
}

func TestSet_ByName(t *testing.T) {
	a := &fakeTool{name: "claude"}
	set := tool.NewSet(a)

	found, ok := set.ByName("claude")
	require.True(t, ok)
	assert.Equal(t, a, found)

	_, ok = set.ByName("nonexistent")
	assert.False(t, ok)
}

func TestSet_Detected(t *testing.T) {
	present := &fakeTool{name: "claude", detected: true}
	absent := &fakeTool{name: "codex", detected: false}
	set := tool.NewSet(present, absent)

	detected, err := set.Detected()
	require.NoError(t, err)
	require.Len(t, detected, 1)
	assert.Equal(t, "claude", detected[0].Name())
}

func TestParseQualified_ValidInput(t *testing.T) {
	qualified, err := tool.ParseQualified("claude/sessions")
	require.NoError(t, err)
	assert.Equal(t, tool.Qualified{Tool: "claude", Category: "sessions"}, qualified)
}

func TestParseQualified_RejectsBareCategoryName(t *testing.T) {
	_, err := tool.ParseQualified("sessions")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sessions")
}

func TestParseQualified_RejectsEmptySegments(t *testing.T) {
	for _, raw := range []string{"/sessions", "claude/", "/"} {
		_, err := tool.ParseQualified(raw)
		assert.Errorf(t, err, "expected error for %q", raw)
	}
}
