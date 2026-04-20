package scan_test

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/scan"
)

func TestRules_FindsMatches(t *testing.T) {
	content := "# Rule\n\nApplies to /Users/test/Projects/myproject only.\n\nDo not touch /other/path.\n"
	dir := writeRulesDir(t, ruleFile{"test-rule.md", []byte(content)})

	warnings, err := scan.Rules(dir, "/Users/test/Projects/myproject")
	require.NoError(t, err)
	require.Len(t, warnings, 1)
	assert.Equal(t, "test-rule.md", warnings[0].File)
	assert.Equal(t, 3, warnings[0].Line)
	assert.Contains(t, warnings[0].Text, "/Users/test/Projects/myproject")
	assert.Equal(t, "/Users/test/Projects/myproject", warnings[0].Path)
}

func TestRules_MultiplePathsMultipleFiles(t *testing.T) {
	contentA := "# Rule A\n\nThis applies to /Users/alice/project.\nAlso references /Users/bob/project here.\n"
	contentB := "# Rule B\n\nNothing interesting here.\n"
	dir := writeRulesDir(t,
		ruleFile{"a.md", []byte(contentA)},
		ruleFile{"b.md", []byte(contentB)},
	)

	warnings, err := scan.Rules(dir, "/Users/alice/project", "/Users/bob/project")
	require.NoError(t, err)
	require.Len(t, warnings, 2)

	assert.Equal(t, "a.md", warnings[0].File)
	assert.Equal(t, 3, warnings[0].Line)
	assert.Equal(t, "/Users/alice/project", warnings[0].Path)

	assert.Equal(t, "a.md", warnings[1].File)
	assert.Equal(t, 4, warnings[1].Line)
	assert.Equal(t, "/Users/bob/project", warnings[1].Path)
}

func TestRules_OneWarningPerLineEvenIfMultiplePathsMatch(t *testing.T) {
	content := "# Rule\n\nThis line has /Users/alice/project and /Users/bob/project both.\n"
	dir := writeRulesDir(t, ruleFile{"rule.md", []byte(content)})

	warnings, err := scan.Rules(dir, "/Users/alice/project", "/Users/bob/project")
	require.NoError(t, err)
	require.Len(t, warnings, 1)
	assert.Equal(t, 3, warnings[0].Line)
}

func TestRules_NoMatches(t *testing.T) {
	content := "# Rule\n\nThis rule does not mention any project path.\n"
	dir := writeRulesDir(t, ruleFile{"rule.md", []byte(content)})

	warnings, err := scan.Rules(dir, "/Users/test/Projects/myproject")
	require.NoError(t, err)
	assert.Empty(t, warnings)
}

func TestRules_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	warnings, err := scan.Rules(dir, "/Users/test/Projects/myproject")
	require.NoError(t, err)
	assert.Empty(t, warnings)
}

func TestRules_DirNotExist(t *testing.T) {
	nonExistentDir := filepath.Join(t.TempDir(), "does-not-exist")

	warnings, err := scan.Rules(nonExistentDir, "/Users/test/Projects/myproject")
	require.NoError(t, err)
	assert.Nil(t, warnings)
}

func TestRules_IgnoresNonMdFiles(t *testing.T) {
	content := []byte("This file has /Users/test/Projects/myproject in it.\n")
	dir := writeRulesDir(t,
		ruleFile{"rule.txt", content},
		ruleFile{"rule.json", content},
	)

	warnings, err := scan.Rules(dir, "/Users/test/Projects/myproject")
	require.NoError(t, err)
	assert.Empty(t, warnings)
}

func TestRules_AcceptsLineUpTo16MiB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long-line test in short mode")
	}
	longLine := bytes.Repeat([]byte("a"), 1<<20)
	longLine = append(longLine, []byte("/target/path")...)
	rulesDir := writeRulesDir(t, ruleFile{"big.md", longLine})

	warnings, err := scan.Rules(rulesDir, "/target/path")
	require.NoError(t, err)
	assert.Len(t, warnings, 1)
}

func TestRules_RejectsLineOverLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping over-limit-line test in short mode")
	}
	huge := bytes.Repeat([]byte("a"), 17<<20)
	rulesDir := writeRulesDir(t, ruleFile{"huge.md", huge})

	_, err := scan.Rules(rulesDir, "/target/path")
	require.Error(t, err)
	assert.ErrorIs(t, err, bufio.ErrTooLong)
}

type ruleFile struct {
	name    string
	content []byte
}

func writeRulesDir(t *testing.T, files ...ruleFile) string {
	t.Helper()
	dir := t.TempDir()
	for _, file := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, file.name), file.content, 0o600))
	}
	return dir
}
