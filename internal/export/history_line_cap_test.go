package export_test

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/export"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/testutil"
)

// makeHistoryLine returns a single JSONL history line whose `cwd` field
// matches projectPath (so the inclusion rule accepts it) and whose
// `display` field carries n bytes of filler. The line ends with a newline.
func makeHistoryLine(n int, projectPath string) []byte {
	filler := bytes.Repeat([]byte("a"), n)
	line := []byte(`{"cwd":"`)
	line = append(line, []byte(projectPath)...)
	line = append(line, []byte(`","display":"`)...)
	line = append(line, filler...)
	line = append(line, []byte(`"}`)...)
	line = append(line, '\n')
	return line
}

func TestExport_AcceptsLargeHistoryLine(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long-line test in short mode")
	}
	claudeHome := testutil.SetupFixture(t)

	// 1 MiB of filler — comfortably under the 16 MiB scanner cap while
	// still exercising the large-line read path.
	line := makeHistoryLine(1<<20, fixtureProjectPath)
	require.NoError(t, os.WriteFile(claudeHome.HistoryFile(), line, 0o600))

	outputPath := filepath.Join(t.TempDir(), "export.zip")
	_, err := export.Run(t.Context(), claudeHome, &export.Options{
		ProjectPath: fixtureProjectPath,
		OutputPath:  outputPath,
		Categories:  manifest.CategorySet{History: true},
	})

	require.NoError(t, err)
}

func TestExport_RejectsHistoryLineOverLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping over-limit-line test in short mode")
	}
	claudeHome := testutil.SetupFixture(t)

	// 17 MiB — 1 MiB over the scanner cap. No JSON wrapper needed; the
	// scanner fails before any parsing.
	huge := bytes.Repeat([]byte("a"), 17<<20)
	require.NoError(t, os.WriteFile(claudeHome.HistoryFile(), huge, 0o600))

	outputPath := filepath.Join(t.TempDir(), "export.zip")
	_, err := export.Run(t.Context(), claudeHome, &export.Options{
		ProjectPath: fixtureProjectPath,
		OutputPath:  outputPath,
		Categories:  manifest.CategorySet{History: true},
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, bufio.ErrTooLong)
}
