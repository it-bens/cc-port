package stats

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/claude"
)

// TestCancellationStopsCountAndDiskScans verifies the per-line and per-file
// ctx.Err() checks inside countReferences and computeDisk. The public entry
// points guard the context before reaching either scan, so this loop-level
// cancellation is only observable from inside the package.
func TestCancellationStopsCountAndDiskScans(t *testing.T) {
	dir := t.TempDir()
	home := &claude.Home{Dir: filepath.Join(dir, "dotclaude"), ConfigFile: filepath.Join(dir, "dotclaude.json")}
	const (
		projectPath = "/Users/test/Projects/demo"
		sessionUUID = "aaaaaaaa-0000-0000-0000-000000000001"
	)
	encodedDir := home.ProjectDir(projectPath)

	writeFile(t, filepath.Join(encodedDir, sessionUUID+".jsonl"), "{}\n")
	writeFile(t, filepath.Join(home.SessionsDir(), sessionUUID+".json"),
		fmt.Sprintf(`{"sessionId":%q,"cwd":%q,"pid":2000000001}`, sessionUUID, projectPath))
	writeFile(t, home.HistoryFile(), `{"project":"/Users/test/Projects/demo"}`+"\n")

	locations, err := claude.LocateProject(home, projectPath)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = countReferences(ctx, home, locations)
	require.ErrorIs(t, err, context.Canceled, "countReferences must abort its scans on a canceled context")

	_, err = computeDisk(ctx, locations)
	assert.ErrorIs(t, err, context.Canceled, "computeDisk must abort its scans on a canceled context")
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}
