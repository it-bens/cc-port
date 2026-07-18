package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/tool"
)

func TestExplicitCodexSelectionFailsWhenDefaultHomeIsAbsent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	toolSet := newToolSet()
	flags := newToolFlagsForTest()
	flags.selected = []string{"codex"}

	_, err := resolveTargets(toolSet, flags)

	require.ErrorIs(t, err, tool.ErrToolAbsent)
}

func TestDefaultSweepWithOnlyClaudeStateSkipsCodex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".claude"), 0o750))
	toolSet := newToolSet()
	flags := newToolFlagsForTest()

	targets, err := resolveTargets(toolSet, flags)

	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, "claude", targets[0].Tool.Name())
}

func TestSelectTools_ExplicitSelectionUsesRegistryOrder(t *testing.T) {
	selected, err := selectTools(newToolSet(), []string{"codex", "claude"})

	require.NoError(t, err)
	require.Len(t, selected, 2)
	assert.Equal(t, "claude", selected[0].Name())
	assert.Equal(t, "codex", selected[1].Name())
}
